package asset

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/platform"
)

// 上传边界（真源见 internal/graph/limits.go 与 api.PublicLimits）。
const (
	MaxUploadBytes = 2 << 30 // 2GB
	MaxImageEdge   = 8192
	ThumbMaxEdge   = 512
)

// Service 实现 api.AssetService。
type Service struct {
	db    *sql.DB
	blobs BlobStore
	clock platform.Clock
	ids   platform.IDGen
}

// New 构造资产服务。
func New(db *sql.DB, blobs BlobStore, clock platform.Clock, ids platform.IDGen) *Service {
	if clock == nil {
		clock = platform.SystemClock()
	}
	if ids == nil {
		ids = platform.DefaultIDGen()
	}
	return &Service{db: db, blobs: blobs, clock: clock, ids: ids}
}

// Upload 见 api.AssetService：流式接收 → 算 hash → 命中复用 → 落元数据。
func (s *Service) Upload(ctx context.Context, wsID, name, mime string, r io.Reader, size int64) (*api.AssetDTO, error) {
	if wsID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	if size > MaxUploadBytes {
		return nil, platform.NewError(413, platform.CodePayloadTooLarge, "file too large").
			WithDetail("limitBytes", MaxUploadBytes)
	}
	// 先缓冲后落盘：需要 hash 才能决定路径（内容寻址）。
	// 上限内缓冲是可接受的；超过上限的流式直传在分片上传中实现（M4）。
	buf, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return nil, platform.AsError(err)
	}
	if int64(len(buf)) > MaxUploadBytes {
		return nil, platform.NewError(413, platform.CodePayloadTooLarge, "file too large").
			WithDetail("limitBytes", MaxUploadBytes)
	}
	hash, n, err := HashReader(bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	mime = normalizeMIME(mime, buf, name)
	kind := KindOf(mime, name)

	// 内容寻址去重：同工作区同 hash 已有记录则直接复用（ATK-16 前提）。
	var existing string
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM assets WHERE workspace_id = ? AND hash = ? AND deleted_at IS NULL`, wsID, hash).Scan(&existing)
	if err == nil {
		return s.Get(ctx, wsID, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, platform.AsError(err)
	}

	if err := s.blobs.Put(ctx, hash, bytes.NewReader(buf), n, mime); err != nil {
		return nil, err
	}

	id := s.ids.NewID("as")
	now := s.clock.Now().UTC()
	meta := ExtractMeta(buf, mime)
	metaJSON, _ := json.Marshal(meta)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO assets (id, workspace_id, kind, hash, size, mime, name, meta, origin, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, wsID, kind, hash, n, mime, sanitizeName(name), string(metaJSON), "upload", now); err != nil {
		// 唯一索引冲突 → 并发上传同文件，回退到已有记录（单飞语义）
		if isUnique(err) {
			var again string
			if qerr := s.db.QueryRowContext(ctx,
				`SELECT id FROM assets WHERE workspace_id = ? AND hash = ?`, wsID, hash).Scan(&again); qerr == nil {
				return s.Get(ctx, wsID, again)
			}
		}
		return nil, platform.AsError(err)
	}
	return s.Get(ctx, wsID, id)
}

// Get 见 api.AssetService。
func (s *Service) Get(ctx context.Context, wsID, id string) (*api.AssetDTO, error) {
	// 注意：此处不过滤 deleted_at。软删只表示「不再出现在素材列表」，
	// 仍被画布引用的资产必须可读，否则打开旧画布会全图破图（见 11 §2.8）。
	q := `SELECT id, workspace_id, kind, hash, size, mime, name, meta, origin, source_run_id, created_at
	      FROM assets WHERE id = ?`
	args := []any{id}
	if wsID != "" {
		// 强制按 workspace 归属校验，杜绝 IDOR（ATK-08 / INV-10）
		q += ` AND workspace_id = ?`
		args = append(args, wsID)
	}
	var d api.AssetDTO
	var metaRaw string
	var runID sql.NullString
	var created string
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&d.ID, &d.Workspace, &d.Kind, &d.Hash, &d.Size, &d.MIME, &d.Name, &metaRaw, &d.Origin, &runID, &created,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platform.ErrNotFound("asset")
		}
		return nil, platform.AsError(err)
	}
	_ = json.Unmarshal([]byte(metaRaw), &d.Meta)
	d.CreatedAt = parseTime(created)
	if runID.Valid && runID.String != "" {
		d.Source = map[string]string{"runId": runID.String}
	}
	return &d, nil
}

// Open 见 api.AssetService。
func (s *Service) Open(ctx context.Context, wsID, id string) (io.ReadSeekCloser, int64, string, error) {
	d, err := s.Get(ctx, wsID, id)
	if err != nil {
		return nil, 0, "", err
	}
	rc, size, err := s.blobs.Open(ctx, d.Hash)
	if err != nil {
		return nil, 0, "", err
	}
	return rc, size, d.MIME, nil
}

// List 见 api.AssetService（游标分页，按创建时间倒序）。
func (s *Service) List(ctx context.Context, wsID, kind string, limit int, cursor string) ([]api.AssetDTO, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := `SELECT id, workspace_id, kind, hash, size, mime, name, meta, origin, created_at
	      FROM assets WHERE workspace_id = ? AND deleted_at IS NULL`
	args := []any{wsID}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	if cursor != "" {
		// 游标为 created_at|id，保证同毫秒内也稳定
		parts := strings.SplitN(cursor, "|", 2)
		if len(parts) == 2 {
			q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
			args = append(args, parts[0], parts[0], parts[1])
		}
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	defer rows.Close()
	out := []api.AssetDTO{}
	for rows.Next() {
		var d api.AssetDTO
		var metaRaw, created string
		if err := rows.Scan(&d.ID, &d.Workspace, &d.Kind, &d.Hash, &d.Size, &d.MIME, &d.Name, &metaRaw, &d.Origin, &created); err != nil {
			return nil, "", platform.AsError(err)
		}
		_ = json.Unmarshal([]byte(metaRaw), &d.Meta)
		d.CreatedAt = parseTime(created)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", platform.AsError(err)
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
		out = out[:limit]
	}
	return out, next, nil
}

// Delete 见 api.AssetService：软删记录，Blob 由 GC 在引用归零后回收。
func (s *Service) Delete(ctx context.Context, wsID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET deleted_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
		s.clock.Now().UTC(), id, wsID)
	if err != nil {
		return platform.AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return platform.ErrNotFound("asset")
	}
	return nil
}

// Thumb 见 api.AssetService：优先返回缓存的缩略图，未命中则按需生成并落 Blob。
func (s *Service) Thumb(ctx context.Context, wsID, id string, w int) ([]byte, string, error) {
	d, err := s.Get(ctx, wsID, id)
	if err != nil {
		return nil, "", err
	}
	if d.Kind != "image" {
		return nil, "", platform.NewError(415, platform.CodeInvalidRequest, "thumbnail is only available for images")
	}
	thumbHash := d.Hash + "-thumb" + itoa(w)
	if ok, _ := s.blobs.Exists(ctx, thumbHash); ok {
		rc, _, err := s.blobs.Open(ctx, thumbHash)
		if err == nil {
			defer rc.Close()
			b, rerr := io.ReadAll(rc)
			if rerr == nil {
				return b, "image/png", nil
			}
		}
	}
	rc, _, err := s.blobs.Open(ctx, d.Hash)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	src, _, err := image.Decode(rc)
	if err != nil {
		return nil, "", platform.NewError(500, platform.CodeInternal, "image decode failed").WithCause(err)
	}
	dst := Downscale(src, ThumbMaxEdge)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, "", platform.AsError(err)
	}
	_ = s.blobs.Put(ctx, thumbHash, bytes.NewReader(buf.Bytes()), int64(buf.Len()), "image/png")
	return buf.Bytes(), "image/png", nil
}

// Downscale 等比缩放到最长边不超过 maxEdge（纯函数，可单测）。
func Downscale(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return src
	}
	ratio := float64(maxEdge) / float64(w)
	if h > w {
		ratio = float64(maxEdge) / float64(h)
	}
	nw := int(float64(w) * ratio)
	nh := int(float64(h) * ratio)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return resizeNearest(src, nw, nh)
}

// resizeNearest 用最近邻实现（标准库无缩放；质量要求不高的缩略图足够）。
func resizeNearest(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	for y := 0; y < h; y++ {
		sy := b.Min.Y + y*b.Dy()/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*b.Dx()/w
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// KindOf 由 MIME 与文件名推断资产类型。
func KindOf(mime, name string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "text/"):
		return "text"
	case mime == "application/json":
		return "json"
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"),
		strings.HasSuffix(lower, ".webp"), strings.HasSuffix(lower, ".gif"), strings.HasSuffix(lower, ".avif"):
		return "image"
	case strings.HasSuffix(lower, ".mp4"), strings.HasSuffix(lower, ".webm"), strings.HasSuffix(lower, ".mov"):
		return "video"
	case strings.HasSuffix(lower, ".mp3"), strings.HasSuffix(lower, ".wav"), strings.HasSuffix(lower, ".opus"),
		strings.HasSuffix(lower, ".m4a"):
		return "audio"
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".md"):
		return "text"
	case strings.HasSuffix(lower, ".json"):
		return "json"
	}
	return "file"
}

// ExtractMeta 提取图片元数据（宽高）。不在原图域做额外解码能力，只读必要信息（见 11 §2.6）。
func ExtractMeta(buf []byte, mime string) map[string]any {
	meta := map[string]any{}
	if !strings.HasPrefix(mime, "image/") {
		return meta
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(buf)); err == nil {
		meta["width"] = cfg.Width
		meta["height"] = cfg.Height
	}
	return meta
}

// normalizeMIME 判定资产的真实类型。
//
// 判定原则（ATK-19）：**不采信客户端声明**。
// 旧实现的写法是「声明不是 octet-stream 就直接返回声明」，于是上传者只要把
// Content-Type 写成 image/svg+xml，就能让服务端把一段可执行 XML 存成 svg 并在
// 同源下原样下发——可执行类型的选择权完全交给了上传者。
//
// 现在的顺序：字节嗅探 → 文本标记语言按扩展名+内容双重确认 → 声明兜底 → octet-stream。
// 声明只在「嗅探得不出结论」时作为弱依据使用，且不会用来判定可执行类型。
func normalizeMIME(mime string, buf []byte, name string) string {
	declared := strings.TrimSpace(strings.Split(mime, ";")[0])
	if byContent := platform.SniffContentType(buf, declared, name); byContent != "" {
		return byContent
	}
	// 嗅探无结论：才退回到声明，且必须剔除可执行类型——
	// 无法判定内容的资产，不允许被贴上「浏览器会执行它」的类型。
	if declared != "" && declared != "application/octet-stream" && !platform.IsExecutableType(declared) {
		return declared
	}
	return "application/octet-stream"
}

// sanitizeName 移除路径分隔符与控制字符，避免文件名进入任何路径计算（ATK-05）。
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

func isUnique(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate")
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// EncodeJPEG 供导出路径使用（统一质量，避免各处硬编码）。
func EncodeJPEG(w io.Writer, img image.Image, quality int) error {
	if quality <= 0 || quality > 100 {
		quality = 85
	}
	return jpeg.Encode(w, img, &jpeg.Options{Quality: quality})
}

// UpdateMeta 更新资产的显示元数据（标题、标签、来源、备注）。
//
// 为什么要这个能力：资产一旦生成就带着一个自动名（如 "result-1"），
// 用户需要把它改成有意义的名字才能用起来——没有改名能力的素材库等于一个只读列表。
//
// 有意不允许修改的字段：hash / size / mime / kind。
// 它们是**内容寻址的事实**，改了会让「同一 hash 只存一份」这条不变量失效
// （DB 说这条记录是 PNG，Blob 里其实是 JPEG，缩略图与下载都会错乱）。
func (s *Service) UpdateMeta(ctx context.Context, wsID, id string, patch AssetMetaPatch) (*api.AssetDTO, error) {
	if wsID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	if !validID(id) {
		return nil, platform.ErrNotFound("asset")
	}
	// 先校验归属：跨工作区改名必须 404（不泄露存在性，INV-10）
	current, err := s.Get(ctx, wsID, id)
	if err != nil {
		return nil, err
	}
	name := current.Name
	if patch.Name != nil {
		name = sanitizeName(*patch.Name)
		if len(name) > MaxNameLen {
			name = name[:MaxNameLen]
		}
	}
	meta := current.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	for k, v := range patch.Meta {
		meta[k] = v
	}
	// 只允许覆盖已知键，其余透传：meta 是留给上层的扩展位，
	// 但删除用显式 null（避免「传空对象就清空」这种隐式语义）。
	for _, k := range patch.MetaDelete {
		delete(meta, k)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, platform.AsError(err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET name = ?, meta = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
		name, string(raw), id, wsID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, platform.ErrNotFound("asset")
	}
	return s.Get(ctx, wsID, id)
}

// AssetMetaPatch 是资产元数据补丁。用指针表示「是否提供该字段」，
// 从而区分「不修改」与「改成空串」。
type AssetMetaPatch struct {
	Name       *string
	Meta       map[string]any
	MetaDelete []string
}

// MaxNameLen 资产显示名上限（与前端 sanitizeFileName 的截断长度一致）。
const MaxNameLen = 120

// validID 校验资产 id 形状（与 internal/graph 的 ID 规则一致）。
// 刻意不依赖 graph 包：asset 不该为了一个字符串校验而依赖图领域。
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}
