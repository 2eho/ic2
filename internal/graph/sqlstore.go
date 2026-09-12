package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// SQLStore 是 Store 的 SQL 实现（SQLite / Postgres 通用 SQL 子集）。
type SQLStore struct {
	db *sql.DB
	// dialect: sqlite | postgres
	dialect string
}

// NewSQLStore 创建 SQL 存储。
func NewSQLStore(db *sql.DB, dialect string) *SQLStore {
	return &SQLStore{db: db, dialect: dialect}
}

func (s *SQLStore) rebind(q string) string {
	if s.dialect != "postgres" {
		return q
	}
	// 将 ? 依次替换为 $1..$n。
	out := make([]byte, 0, len(q)+8)
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			out = append(out, '$')
			out = append(out, []byte(itoa64(n))...)
			continue
		}
		out = append(out, q[i])
	}
	return string(out)
}

func itoa64(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// GetDocument 见 Store。
func (s *SQLStore) GetDocument(ctx context.Context, canvasID string) (*CanvasDocument, error) {
	var raw []byte
	var version int64
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT version, doc FROM canvases WHERE id = ? AND deleted_at IS NULL`), canvasID).
		Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platform.ErrNotFound("canvas")
	}
	if err != nil {
		return nil, platform.AsError(err)
	}
	var doc CanvasDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, platform.NewError(500, platform.CodeInternal, "canvas document is corrupted").WithCause(err)
	}
	doc.Version = version
	if doc.Nodes == nil {
		doc.Nodes = map[string]Node{}
	}
	if doc.Edges == nil {
		doc.Edges = map[string]Edge{}
	}
	doc.ID = canvasID
	return &doc, nil
}

// CreateDocument 见 Store。
func (s *SQLStore) CreateDocument(ctx context.Context, doc *CanvasDocument) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return platform.AsError(err)
	}
	_, err = s.db.ExecContext(ctx, s.rebind(
		`INSERT INTO canvases (id, project_id, name, version, settings, doc, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		doc.ID, doc.ProjectID, "", doc.Version, mustJSONString(doc.Settings), string(body), doc.UpdatedAt, doc.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return platform.NewError(409, CodeConflict, "canvas already exists")
		}
		return platform.AsError(err)
	}
	return nil
}

// UpdateMeta 见 Store。
func (s *SQLStore) UpdateMeta(ctx context.Context, canvasID, name string, settings CanvasSettings) error {
	_, err := s.db.ExecContext(ctx, s.rebind(
		`UPDATE canvases SET name = ?, settings = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`),
		name, mustJSONString(settings), time.Now().UTC(), canvasID)
	return platform.AsError(err)
}

// DeleteDocument 软删（保留冷静期，见 11 §2.8）。
func (s *SQLStore) DeleteDocument(ctx context.Context, canvasID string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(
		`UPDATE canvases SET deleted_at = ? WHERE id = ?`), time.Now().UTC(), canvasID)
	return platform.AsError(err)
}

// AppendOps 单事务写 op 日志 + 快照，保证 INV-1（事件与状态同事务）。
func (s *SQLStore) AppendOps(ctx context.Context, canvasID string, baseVersion int64,
	doc *CanvasDocument, ops []json.RawMessage, actor string, now time.Time) (int64, []Record, error) {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, platform.AsError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var curVersion int64
	if err := tx.QueryRowContext(ctx, s.rebind(`SELECT version FROM canvases WHERE id = ? AND deleted_at IS NULL`), canvasID).
		Scan(&curVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, platform.ErrNotFound("canvas")
		}
		return 0, nil, platform.AsError(err)
	}
	if curVersion != baseVersion {
		return 0, nil, platform.NewError(409, CodeConflict, "version conflict").
			WithDetail("baseVersion", baseVersion).WithDetail("serverVersion", curVersion)
	}

	var maxSeq int64
	if err := tx.QueryRowContext(ctx, s.rebind(`SELECT COALESCE(MAX(seq), 0) FROM canvas_ops WHERE canvas_id = ?`), canvasID).
		Scan(&maxSeq); err != nil {
		return 0, nil, platform.AsError(err)
	}

	newVersion := curVersion + 1
	records := make([]Record, 0, len(ops))
	for _, raw := range ops {
		maxSeq++
		records = append(records, Record{Seq: maxSeq, CanvasID: canvasID, ActorID: actor,
			Op: append(json.RawMessage(nil), raw...), Version: newVersion, CreatedAt: now})
		if _, err := tx.ExecContext(ctx, s.rebind(
			`INSERT INTO canvas_ops (canvas_id, seq, version, actor_id, op, created_at) VALUES (?, ?, ?, ?, ?, ?)`),
			canvasID, maxSeq, newVersion, actor, string(raw), now); err != nil {
			return 0, nil, platform.AsError(err)
		}
	}

	nextDoc := doc.Clone()
	nextDoc.Version = newVersion
	nextDoc.UpdatedAt = now
	body, err := json.Marshal(nextDoc)
	if err != nil {
		return 0, nil, platform.AsError(err)
	}
	if _, err := tx.ExecContext(ctx, s.rebind(
		`UPDATE canvases SET version = ?, doc = ?, updated_at = ? WHERE id = ? AND version = ?`),
		newVersion, string(body), now, canvasID, curVersion); err != nil {
		return 0, nil, platform.AsError(err)
	}
	if _, err := tx.ExecContext(ctx, s.rebind(
		`INSERT INTO canvas_docs (canvas_id, version, doc, created_at) VALUES (?, ?, ?, ?)`),
		canvasID, newVersion, string(body), now); err != nil {
		return 0, nil, platform.AsError(err)
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			return 0, nil, platform.NewError(409, CodeConflict, "concurrent version conflict")
		}
		return 0, nil, platform.AsError(err)
	}
	return newVersion, records, nil
}

// ListOps 见 Store。
func (s *SQLStore) ListOps(ctx context.Context, canvasID string, since int64, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT seq, actor_id, op, version, created_at FROM canvas_ops
		 WHERE canvas_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`), canvasID, since, limit)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanOpRecord(rows, canvasID)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, platform.AsError(rows.Err())
}

// ListOpsSinceVersion 见 Store：按版本定位增量 op。
func (s *SQLStore) ListOpsSinceVersion(ctx context.Context, canvasID string, sinceVersion int64, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT seq, actor_id, op, version, created_at FROM canvas_ops
		 WHERE canvas_id = ? AND version > ? ORDER BY seq ASC LIMIT ?`), canvasID, sinceVersion, limit)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanOpRecord(rows, canvasID)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, platform.AsError(rows.Err())
}

// ListCanvases 见 Store。
func (s *SQLStore) ListCanvases(ctx context.Context, projectID string) ([]CanvasMeta, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT id, project_id, name, version, settings, doc, updated_at FROM canvases
		 WHERE project_id = ? AND deleted_at IS NULL ORDER BY updated_at DESC`), projectID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []CanvasMeta{}
	for rows.Next() {
		var m CanvasMeta
		var settingsRaw, docRaw string
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Version, &settingsRaw, &docRaw, &m.UpdatedAt); err != nil {
			return nil, platform.AsError(err)
		}
		_ = json.Unmarshal([]byte(settingsRaw), &m.Settings)
		var doc struct {
			Nodes map[string]json.RawMessage `json:"nodes"`
		}
		_ = json.Unmarshal([]byte(docRaw), &doc)
		m.Nodes = len(doc.Nodes)
		out = append(out, m)
	}
	return out, platform.AsError(rows.Err())
}

func mustJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsFold(msg, "unique") || containsFold(msg, "duplicate") || containsFold(msg, "constraint failed")
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// WorkspaceOf 反查画布所属工作区（canvas → project → workspace）。
//
// 授权必须基于服务端解析出的归属，而不是客户端传入的 workspaceId：
// 否则攻击者只要在请求里换一个「自己有权的工作区 id」就能绕过校验（INV-10）。
func (s *SQLStore) WorkspaceOf(ctx context.Context, canvasID string) (string, error) {
	var wsID string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.workspace_id
		FROM canvases c JOIN projects p ON p.id = c.project_id
		WHERE c.id = ? AND c.deleted_at IS NULL`, canvasID).Scan(&wsID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", platform.ErrNotFound("canvas")
		}
		return "", platform.AsError(err)
	}
	return wsID, nil
}

// scanOpRecord 读取一行 canvas_ops。
//
// 为什么需要独立函数：`created_at` 在 SQLite 里是 TEXT（迁移脚本如此定义），
// 在 Postgres 里是 timestamptz。直接 Scan 到 time.Time 时 SQLite 会报
// 「unsupported Scan, storing driver.Value type string into type *time.Time」——
// 而报错发生在**冲突 rebase 路径**上，于是「并发冲突自动合并」这个功能在
// SQLite（也就是默认自托管形态）下从未真正工作过，只是没人走到那条分支。
// 因此统一按字符串读入再解析，两种驱动行为一致。
func scanOpRecord(rows *sql.Rows, canvasID string) (Record, error) {
	var r Record
	var raw string
	var created string
	if err := rows.Scan(&r.Seq, &r.ActorID, &raw, &r.Version, &created); err != nil {
		return Record{}, platform.AsError(err)
	}
	r.CanvasID = canvasID
	r.Op = json.RawMessage(raw)
	r.CreatedAt = parseSQLTime(created)
	return r, nil
}

// parseSQLTime 容忍多种时间表示（SQLite TEXT / Postgres timestamptz / 驱动差异）。
// 无法解析时返回零值而不是报错：时间戳坏了不该让整个画布不可用。
func parseSQLTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999Z07:00",
		// Go time.Time 的默认 String() 形态（modernc sqlite 驱动会以它写入）。
		// 实测写入值是 "2023-11-14 22:13:20 +0000 UTC"，不覆盖这一种就会
		// 全部落回零值——比直接报错更隐蔽（时间看起来"有"，只是永远不对）。
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
