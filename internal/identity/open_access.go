package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/platform"
	"golang.org/x/crypto/bcrypt"
)

// 固定引导身份（仅 IC_OPEN_ACCESS=true 时使用）。密码随机且不对外文档化。
const (
	OpenUserID      = "u_open_local"
	OpenUserEmail   = "open@local"
	OpenUserName    = "Open"
	OpenWorkspaceID = "ws_open_default"
	OpenProjectID   = "pj_open_default"
	openSessionTTL  = 10 * 365 * 24 * time.Hour // 长寿命会话，避免频繁弹登录
)

// EnsureOpenAccessBootstrap 幂等创建引导用户 + 默认工作区 + 默认项目。
func (s *Service) EnsureOpenAccessBootstrap(ctx context.Context) error {
	if !s.cfg.OpenAccess {
		return nil
	}
	now := s.clock.Now().UTC()

	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ?`, OpenUserID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		pw := randomUnusedPassword()
		hash, herr := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if herr != nil {
			return platform.AsError(herr)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO users (id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
			OpenUserID, OpenUserEmail, OpenUserName, string(hash), now); err != nil {
			if !isUnique(err) {
				return platform.AsError(err)
			}
		}
	} else if err != nil {
		return platform.AsError(err)
	}

	var wsID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE id = ?`, OpenWorkspaceID).Scan(&wsID)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO workspaces (id, name, slug, owner_id, plan, settings, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			OpenWorkspaceID, "Open 工作区", "open", OpenUserID, "free", "{}", now); err != nil {
			if !isUnique(err) {
				return platform.AsError(err)
			}
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
			OpenWorkspaceID, OpenUserID, "owner", now); err != nil {
			if !isUnique(err) {
				return platform.AsError(err)
			}
		}
	} else if err != nil {
		return platform.AsError(err)
	} else {
		// 确保成员关系存在（幂等）。
		_, _ = s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
			OpenWorkspaceID, OpenUserID, "owner", now)
	}

	var pjID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE id = ?`, OpenProjectID).Scan(&pjID)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO projects (id, workspace_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			OpenProjectID, OpenWorkspaceID, "默认项目", "", now, now); err != nil {
			if !isUnique(err) {
				return platform.AsError(err)
			}
		}
	} else if err != nil {
		return platform.AsError(err)
	}
	return nil
}

// OpenAccess 为引导用户签发长寿命会话；仅 OpenAccess 开启时可用。
func (s *Service) OpenAccess(ctx context.Context) (*api.SessionDTO, error) {
	if !s.cfg.OpenAccess {
		return nil, platform.NewError(404, platform.CodeNotFound, "open access is disabled")
	}
	if err := s.EnsureOpenAccessBootstrap(ctx); err != nil {
		return nil, err
	}
	var id, email, name string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, name FROM users WHERE id = ?`, OpenUserID).Scan(&id, &email, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platform.NewError(500, platform.CodeInternal, "open access bootstrap missing")
	}
	if err != nil {
		return nil, platform.AsError(err)
	}
	return s.issueSessionTTL(ctx, id, email, name, openSessionTTL)
}

func (s *Service) issueSessionTTL(ctx context.Context, userID, email, name string, ttl time.Duration) (*api.SessionDTO, error) {
	tok, err := platform.RandomToken(32)
	if err != nil {
		return nil, platform.AsError(err)
	}
	now := s.clock.Now().UTC()
	exp := now.Add(ttl)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, scopes, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		hashToken(tok), userID, "user", now, exp); err != nil {
		return nil, platform.AsError(err)
	}
	return s.buildSessionFor(ctx, tok, userID, email, name, exp)
}

func randomUnusedPassword() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "unused-" + hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return "unused-" + hex.EncodeToString(b)
}
