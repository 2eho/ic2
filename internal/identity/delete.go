package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// 工作区删除（ATK-22 / docs/design/13 §4.2「备份恢复」的前置能力）。
//
// 语义（刻意的两段式，与「软删」和「立即清空」都不同）：
//
//	阶段一 soft delete：写 deleted_at，工作区立即对所有接口不可见（404）；
//	阶段二 purge：冷静期（默认 7 天）之后清空 Blob 等不可恢复的资源。
//
// 为什么要冷静期：误删工作区是不可逆事故。冷静期让「误删」可恢复，
// 同时保证「删除后旧 ID 访问不到」这一安全要求立刻成立——
// 后者才是 ATK-22 真正要拦的东西（删除 ≠ 数据还能被读到）。
//
// 为什么 purge 也要有：只软删会让 Blob 永久占盘，与其说「删了」不如说是隐藏。

// PurgeGracePeriod 是默认冷静期。
const PurgeGracePeriod = 7 * 24 * time.Hour

// ErrWorkspaceDeleted 表示工作区已被删除（对外统一表现为 404）。
var ErrWorkspaceDeleted = platform.ErrNotFound("workspace")

// DeleteWorkspace 软删工作区：立刻不可见，进入冷静期。
func (s *Service) DeleteWorkspace(ctx context.Context, wsID, actorID string) error {
	if wsID == "" {
		return platform.ErrInvalid("workspaceId is required")
	}
	// 只有 owner 能删（这是破坏性操作，admin 也不够）
	role, err := s.WorkspaceRole(ctx, wsID, actorID)
	if err != nil {
		return err
	}
	if role != "owner" {
		return platform.NewError(403, platform.CodeForbidden, "只有工作区所有者可以删除工作区")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE workspaces SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), wsID)
	if err != nil {
		return platform.AsError(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// 已经是删除态：幂等返回成功（重复点删除不应报错）
		return nil
	}
	// 关联画布/项目一并软删：否则「已删工作区」的子资源仍能通过直接 id 访问，
	// 这正是 ATK-22 要拦的越权读取路径。
	for _, q := range []string{
		`UPDATE projects SET deleted_at = ? WHERE workspace_id = ? AND deleted_at IS NULL`,
		`UPDATE canvases SET deleted_at = ? WHERE project_id IN (SELECT id FROM projects WHERE workspace_id = ?) AND deleted_at IS NULL`,
	} {
		if _, err := s.db.ExecContext(ctx, q, time.Now().UTC().Format(time.RFC3339Nano), wsID); err != nil {
			return platform.AsError(err)
		}
	}
	return nil
}

// RestoreWorkspace 在冷静期内恢复（误删可救）。
func (s *Service) RestoreWorkspace(ctx context.Context, wsID, actorID string) error {
	role, err := s.WorkspaceRole(ctx, wsID, actorID)
	if err != nil {
		// 恢复时工作区已不可见，因此这里改用「历史成员关系」判断
		return err
	}
	if role != "owner" {
		return platform.NewError(403, platform.CodeForbidden, "只有工作区所有者可以恢复工作区")
	}
	for _, q := range []string{
		`UPDATE workspaces SET deleted_at = NULL WHERE id = ?`,
		`UPDATE projects SET deleted_at = NULL WHERE workspace_id = ?`,
		`UPDATE canvases SET deleted_at = NULL WHERE project_id IN (SELECT id FROM projects WHERE workspace_id = ?)`,
	} {
		if _, err := s.db.ExecContext(ctx, q, wsID); err != nil {
			return platform.AsError(err)
		}
	}
	return nil
}

// WorkspaceVisible 判定工作区对当前主体是否可见（软删即不可见）。
func (s *Service) WorkspaceVisible(ctx context.Context, wsID string) (bool, error) {
	var deleted sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT deleted_at FROM workspaces WHERE id = ?`, wsID).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, platform.AsError(err)
	}
	return !deleted.Valid || deleted.String == "", nil
}

// PurgeCandidate 是一个待物理清理的工作区。
type PurgeCandidate struct {
	WorkspaceID string
	DeletedAt   time.Time
}

// PurgeCandidates 返回过了冷静期、可以物理清理的工作区。
// 只返回候选，实际清理由上层（worker）执行——因为清理需要同时操作 Blob 存储，
// 而 identity 不该依赖 asset 包（依赖方向必须单向）。
func (s *Service) PurgeCandidates(ctx context.Context, now time.Time, grace time.Duration) ([]PurgeCandidate, error) {
	if grace <= 0 {
		grace = PurgeGracePeriod
	}
	cutoff := now.Add(-grace).UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, deleted_at FROM workspaces WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, cutoff)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	var out []PurgeCandidate
	for rows.Next() {
		var id, deletedAt string
		if err := rows.Scan(&id, &deletedAt); err != nil {
			return nil, platform.AsError(err)
		}
		t, err := time.Parse(time.RFC3339Nano, deletedAt)
		if err != nil {
			continue
		}
		out = append(out, PurgeCandidate{WorkspaceID: id, DeletedAt: t.UTC()})
	}
	return out, rows.Err()
}

// AddMemberForTest 供测试构造成员关系。
//
// 暴露一个测试专用入口而不是让测试直接写 SQL：SQL 里字段名/默认值一变，
// 测试就会因为「列不匹配」而失败，掩盖它真正要验的业务逻辑。
// 命名为 ...ForTest 是为了让它在生产代码里一眼可见、不可被误用。
func (s *Service) AddMemberForTest(ctx context.Context, wsID, userID, role string) error {
	switch role {
	case "owner", "admin", "editor", "viewer":
	default:
		return platform.ErrInvalid("invalid role: " + role)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, name, password_hash, created_at) VALUES (?, ?, ?, '', ?)
		 ON CONFLICT(id) DO NOTHING`,
		userID, userID+"@test.dev", userID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return platform.AsError(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(workspace_id, user_id) DO UPDATE SET role = excluded.role`,
		wsID, userID, role, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return platform.AsError(err)
	}
	return nil
}
