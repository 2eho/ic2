// Package identity 负责用户、工作区、成员、会话与 API Key。
package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/platform"
	"golang.org/x/crypto/bcrypt"
)

// Service 实现 api.AuthService。
type Service struct {
	db    *sql.DB
	clock platform.Clock
	ids   platform.IDGen
	cfg   platform.Config
}

// New 构造身份服务。
func New(db *sql.DB, clock platform.Clock, ids platform.IDGen, cfg platform.Config) *Service {
	if clock == nil {
		clock = platform.SystemClock()
	}
	if ids == nil {
		ids = platform.DefaultIDGen()
	}
	return &Service{db: db, clock: clock, ids: ids, cfg: cfg}
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Login 见 api.AuthService。
func (s *Service) Login(ctx context.Context, email, password string) (*api.SessionDTO, error) {
	var id, hash, name string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, password_hash, name FROM users WHERE email = ?`, strings.ToLower(email)).
		Scan(&id, &hash, &name)
	if errors.Is(err, sql.ErrNoRows) {
		// 统一错误信息，避免用户枚举（见 11 §2.6）。
		return nil, platform.NewError(401, platform.CodeUnauthorized, "invalid credentials")
	}
	if err != nil {
		return nil, platform.AsError(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, platform.NewError(401, platform.CodeUnauthorized, "invalid credentials")
	}
	return s.issueSession(ctx, id, strings.ToLower(email), name)
}

// Register 见 api.AuthService（首个用户自动成为 owner 并获得默认工作区）。
func (s *Service) Register(ctx context.Context, email, name, password string) (*api.SessionDTO, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRe.MatchString(email) {
		return nil, platform.ErrInvalid("email format is invalid")
	}
	if len(password) < 8 {
		return nil, platform.ErrInvalid("password must be at least 8 characters")
	}
	if len(name) == 0 {
		name = strings.SplitN(email, "@", 2)[0]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, platform.AsError(err)
	}
	id := s.ids.NewID("u")
	now := s.clock.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, email, name, string(hash), now); err != nil {
		if isUnique(err) {
			return nil, platform.NewError(409, platform.CodeConflict, "email already registered")
		}
		return nil, platform.AsError(err)
	}
	// 自动创建个人工作区。
	if _, err := s.CreateWorkspace(ctx, id, name+" 的工作区"); err != nil {
		return nil, err
	}
	sess, err := s.issueSession(ctx, id, email, name)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Service) issueSession(ctx context.Context, userID, email, name string) (*api.SessionDTO, error) {
	tok, err := platform.RandomToken(32)
	if err != nil {
		return nil, platform.AsError(err)
	}
	now := s.clock.Now().UTC()
	exp := now.Add(30 * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, scopes, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		hashToken(tok), userID, "user", now, exp); err != nil {
		return nil, platform.AsError(err)
	}
	return s.buildSessionFor(ctx, tok, userID, email, name, exp)
}

// buildSessionFor 读取成员工作区并组装会话结果。
func (s *Service) buildSessionFor(ctx context.Context, tok, userID, email, name string, exp time.Time) (*api.SessionDTO, error) {
	wsList, _ := s.ListWorkspaces(ctx, userID)
	sess := &api.SessionDTO{}
	sess.Token = tok
	sess.ExpiresAt = exp
	sess.User = api.UserDTO{ID: userID, Email: email, Name: name}
	if wsList != nil {
		sess.Workspaces = wsList
	} else {
		sess.Workspaces = []api.WorkspaceDTO{}
	}
	return sess, nil
}

// Logout 见 api.AuthService。
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, hashToken(token))
	return platform.AsError(err)
}

// Authenticate 见 api.AuthService。
func (s *Service) Authenticate(ctx context.Context, token string) (*api.Principal, error) {
	if token == "" {
		return nil, platform.NewError(401, platform.CodeUnauthorized, "missing credentials")
	}
	// Session 或 API Key 二选一。
	var userID, email, name string
	var exp string
	err := s.db.QueryRowContext(ctx,
		`SELECT s.user_id, u.email, u.name, s.expires_at FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = ?`, hashToken(token)).Scan(&userID, &email, &name, &exp)
	if err == nil {
		if t, perr := time.Parse(time.RFC3339Nano, exp); perr == nil && s.clock.Now().UTC().After(t) {
			return nil, platform.NewError(401, platform.CodeUnauthorized, "session expired")
		}
		return &api.Principal{UserID: userID, Email: email, Name: name, Role: "owner"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, platform.AsError(err)
	}
	// API Key
	var keyID, wsID, scopes string
	var revoked sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, scopes, revoked_at FROM api_keys WHERE hash = ?`, hashToken(token)).
		Scan(&keyID, &wsID, &scopes, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platform.NewError(401, platform.CodeUnauthorized, "invalid credentials")
	}
	if err != nil {
		return nil, platform.AsError(err)
	}
	if revoked.Valid && revoked.String != "" {
		return nil, platform.NewError(401, platform.CodeUnauthorized, "api key revoked")
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, s.clock.Now().UTC(), keyID)
	return &api.Principal{WorkspaceID: wsID, Role: "editor", Scopes: splitCSV(scopes)}, nil
}

// ListWorkspaces 见 api.AuthService。
func (s *Service) ListWorkspaces(ctx context.Context, userID string) ([]api.WorkspaceDTO, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.name, w.slug, m.role, w.plan FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE m.user_id = ? AND w.deleted_at IS NULL ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []api.WorkspaceDTO{}
	for rows.Next() {
		var w api.WorkspaceDTO
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Role, &w.Plan); err != nil {
			return nil, platform.AsError(err)
		}
		out = append(out, w)
	}
	return out, platform.AsError(rows.Err())
}

// CreateWorkspace 见 api.AuthService。
func (s *Service) CreateWorkspace(ctx context.Context, userID, name string) (*api.WorkspaceDTO, error) {
	if strings.TrimSpace(name) == "" {
		name = "我的工作区"
	}
	id := s.ids.NewID("ws")
	slug := id
	now := s.clock.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (id, name, slug, owner_id, plan, settings, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, slug, userID, "free", "{}", now); err != nil {
		return nil, platform.AsError(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
		id, userID, "owner", now); err != nil {
		return nil, platform.AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, platform.AsError(err)
	}
	return &api.WorkspaceDTO{ID: id, Name: name, Slug: slug, Role: "owner", Plan: "free"}, nil
}

// ListProjects 见 api.AuthService。
func (s *Service) ListProjects(ctx context.Context, wsID string) ([]api.ProjectDTO, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.workspace_id, p.name, p.description, p.created_at, p.updated_at,
		        (SELECT COUNT(1) FROM canvases c WHERE c.project_id = p.id AND c.deleted_at IS NULL)
		 FROM projects p WHERE p.workspace_id = ? AND p.deleted_at IS NULL ORDER BY p.updated_at DESC`, wsID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []api.ProjectDTO{}
	for rows.Next() {
		var p api.ProjectDTO
		var created, updated string
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Description, &created, &updated, &p.CanvasCount); err != nil {
			return nil, platform.AsError(err)
		}
		p.CreatedAt = parseTime(created)
		p.UpdatedAt = parseTime(updated)
		out = append(out, p)
	}
	return out, platform.AsError(rows.Err())
}

// CreateProject 见 api.AuthService。
func (s *Service) CreateProject(ctx context.Context, wsID, name, description string) (*api.ProjectDTO, error) {
	if strings.TrimSpace(name) == "" {
		name = "未命名项目"
	}
	id := s.ids.NewID("pj")
	now := s.clock.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, workspace_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, wsID, name, description, now, now); err != nil {
		return nil, platform.AsError(err)
	}
	return &api.ProjectDTO{ID: id, WorkspaceID: wsID, Name: name, Description: description, CreatedAt: now, UpdatedAt: now}, nil
}

// IssueAPIKey 见 api.AuthService：明文只返回一次，库里存 HMAC。
func (s *Service) IssueAPIKey(ctx context.Context, wsID, userID, name string) (string, error) {
	raw, err := platform.RandomToken(24)
	if err != nil {
		return "", platform.AsError(err)
	}
	token := "ic_" + raw
	id := s.ids.NewID("ak")
	now := s.clock.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, workspace_id, user_id, name, hash, scopes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, wsID, userID, name, hashToken(token), "canvas:read,canvas:write,assets:read,assets:write", now); err != nil {
		return "", platform.AsError(err)
	}
	return token, nil
}

// RevokeAPIKey 见 api.AuthService。
func (s *Service) RevokeAPIKey(ctx context.Context, wsID, key string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE workspace_id = ? AND hash = ?`, s.clock.Now().UTC(), wsID, hashToken(key))
	return platform.AsError(err)
}

// WorkspaceRole 返回成员在某工作区的角色；非成员返回空。
func (s *Service) WorkspaceRole(ctx context.Context, wsID, userID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, wsID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", platform.AsError(err)
	}
	return role, nil
}

// newSession 组装会话 DTO。返回的 Workspaces 为空切片（非 nil），
// 避免前端拿到 null 需要额外判空。
func newSession(tok, userID, email, name string, exp time.Time) *api.SessionDTO {
	// 说明：函数体刻意逐字段赋值。结构体字面量返回在本工具链组合下出现过
	// 生成代码返回 nil 的问题（详见 docs/upstream/sync-log.md 记录），
	// 逐字段赋值是可验证可靠的写法。
	return buildSessionFields(tok, userID, email, name, exp)
}

func buildSessionFields(tok, userID, email, name string, exp time.Time) *api.SessionDTO {
	user := api.UserDTO{}
	user.ID = userID
	user.Email = email
	user.Name = name
	sess := &api.SessionDTO{}
	sess.Token = tok
	sess.ExpiresAt = exp
	sess.User = user
	sess.Workspaces = []api.WorkspaceDTO{}
	return sess
}

// hashToken 用 HMAC-SHA256 + 固定盐散列 token，避免明文落库。
func hashToken(t string) string {
	key := []byte("ic-token-hash-v1")
	m := hmac.New(sha256.New, key)
	m.Write([]byte(t))
	return hex.EncodeToString(m.Sum(nil))
}

// RandID 给出短随机 ID（供上层需要时使用）。
func RandID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func isUnique(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate")
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	// SQLite 可能以纳秒整型存储。
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(0, n).UTC()
	}
	return time.Time{}
}
