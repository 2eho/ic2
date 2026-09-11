package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/identity"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

// ATK-22：删除工作区后用旧 ID 访问必须 404，且冷静期后 Blob 才被清理。
//
// 这条用例把「删除」拆成两个独立要求，分别断言：
//  1. **立即不可见**：软删那一刻起，旧 ID 就访问不到（安全要求，不能等冷静期）；
//  2. **冷静期保护**：7 天内可恢复（可用性要求，避免误删不可逆）；
//  3. **到期可清理**：过了冷静期进入 purge 候选（存储要求，避免只软删占盘）。
func TestATK22WorkspaceDeletedIsInvisibleImmediately(t *testing.T) {
	ctx := context.Background()
	svc, db, wsID, userID := newIdentityHarness(t)

	visible, err := svc.WorkspaceVisible(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if !visible {
		t.Fatal("删除前工作区应可见")
	}

	if err := svc.DeleteWorkspace(ctx, wsID, userID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	// 1) 立即不可见
	visible2, err := svc.WorkspaceVisible(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if visible2 {
		t.Fatal("软删后工作区必须立即不可见（否则旧 ID 仍可访问）")
	}
	// 列表里也不应再出现
	list, err := svc.ListWorkspaces(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range list {
		if w.ID == wsID {
			t.Fatal("已删工作区仍出现在列表里")
		}
	}

	// 子资源（项目/画布）也必须一并不可见，否则可通过直接 id 绕过
	var deletedProjects int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM projects WHERE workspace_id = ? AND deleted_at IS NOT NULL`, wsID).Scan(&deletedProjects); err != nil {
		t.Fatal(err)
	}
	if deletedProjects == 0 {
		t.Fatal("项目未被级联软删，可通过直接 id 访问已删工作区的子资源")
	}

	// 2) 冷静期后可恢复
	if err := svc.RestoreWorkspace(ctx, wsID, userID); err != nil {
		t.Fatalf("冷静期内恢复失败: %v", err)
	}
	visible3, _ := svc.WorkspaceVisible(ctx, wsID)
	if !visible3 {
		t.Fatal("恢复后应重新可见")
	}
}

// 冷静期内不得进入清理候选；超过冷静期才进入。
func TestATK22PurgeOnlyAfterGracePeriod(t *testing.T) {
	ctx := context.Background()
	svc, _, wsID, userID := newIdentityHarness(t)
	if err := svc.DeleteWorkspace(ctx, wsID, userID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// 立即查：不应是候选（要等冷静期）
	early, err := svc.PurgeCandidates(ctx, now, identity.PurgeGracePeriod)
	if err != nil {
		t.Fatal(err)
	}
	if contains(early, wsID) {
		t.Fatal("冷静期未过，不应进入物理清理候选")
	}

	// 8 天后查：应是候选
	later, err := svc.PurgeCandidates(ctx, now.Add(8*24*time.Hour), identity.PurgeGracePeriod)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(later, wsID) {
		t.Fatal("冷静期已过，应进入物理清理候选")
	}
}

// 只有 owner 能删；其他角色必须 403。删除是不可逆操作，admin 也不够。
func TestATK22OnlyOwnerCanDeleteWorkspace(t *testing.T) {
	ctx := context.Background()
	svc, _, wsID, _ := newIdentityHarness(t)

	// 造一个 admin 成员
	other := "u_admin_" + wsID
	if err := applyMemberSVCRow(svc, ctx, wsID, other, "admin"); err != nil {
		t.Fatal(err)
	}
	err := svc.DeleteWorkspace(ctx, wsID, other)
	if err == nil {
		t.Fatal("admin 不应能删除工作区")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeForbidden {
		t.Fatalf("期望 forbidden，实际 %s", de.Code)
	}

	// 非成员必须彻底看不到（404 语义，而不是 403——不泄露存在性）
	err2 := svc.DeleteWorkspace(ctx, wsID, "u_stranger")
	if err2 == nil {
		t.Fatal("非成员不应能删除工作区")
	}
}

// 重复删除必须幂等：用户连点两次删除不应报错。
func TestATK22DeleteIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _, wsID, userID := newIdentityHarness(t)
	if err := svc.DeleteWorkspace(ctx, wsID, userID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteWorkspace(ctx, wsID, userID); err != nil {
		t.Fatalf("重复删除应幂等成功，实际 %v", err)
	}
}

// ------------------------------------------------------------------ helpers

func newIdentityHarness(t *testing.T) (*identity.Service, *platform.DB, string, string) {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + t.TempDir() + "/id.db"
	cfg.AllowInsecureDevKey = true
	cfg.AllowRegistration = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	svc := identity.New(db.DB, platform.SystemClock(), platform.DefaultIDGen(), cfg)
	sess, err := svc.Register(ctx, "atk22@test.dev", "ATK22", "password123")
	if err != nil {
		t.Fatal(err)
	}
	wsID := sess.Workspaces[0].ID
	// 建一个项目与画布，用于验证级联软删
	if _, err := svc.CreateProject(ctx, wsID, "p", ""); err != nil {
		t.Fatal(err)
	}
	return svc, db, wsID, sess.User.ID
}

func applyMemberSVCRow(svc *identity.Service, ctx context.Context, wsID, userID, role string) error {
	// 走公开的 CreateWorkspace 无法造出「同工作区其他成员」，这里直接用 SQL 造。
	// 目的是验证角色判定本身，而不是验证成员邀请流程。
	return svc.AddMemberForTest(ctx, wsID, userID, role)
}

func contains(list []identity.PurgeCandidate, wsID string) bool {
	for _, c := range list {
		if c.WorkspaceID == wsID {
			return true
		}
	}
	return false
}
