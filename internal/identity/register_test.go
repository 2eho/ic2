package identity

import (
	"context"
	"testing"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file::memory:?cache=shared"
	cfg.AllowInsecureDevKey = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	return New(db.DB, platform.SystemClock(), platform.DefaultIDGen(), cfg)
}

func TestRegisterLoginSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sess, err := svc.Register(ctx, "a@b.com", "Zeho", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if sess == nil || sess.Token == "" {
		t.Fatalf("sess=%+v", sess)
	}
	if len(sess.Workspaces) != 1 {
		t.Fatalf("自动创建工作区失败: %+v", sess.Workspaces)
	}
	p, err := svc.Authenticate(ctx, sess.Token)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if p.UserID != sess.User.ID || p.Email != "a@b.com" {
		t.Fatalf("principal=%+v", p)
	}
	if _, err := svc.Login(ctx, "a@b.com", "wrong"); err == nil {
		t.Fatal("错误密码应被拒绝")
	}
	if _, err := svc.Login(ctx, "a@b.com", "password123"); err != nil {
		t.Fatalf("旧密码登录失败: %v", err)
	}
	if _, err := svc.Register(ctx, "a@b.com", "x", "password123"); err == nil {
		t.Fatal("重复邮箱应冲突")
	}
	if _, err := svc.Register(ctx, "bad-email", "x", "password123"); err == nil {
		t.Fatal("非法邮箱应被拒绝")
	}
	if _, err := svc.Register(ctx, "c@d.com", "x", "short"); err == nil {
		t.Fatal("弱密码应被拒绝")
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sess, err := svc.Register(ctx, "a@b.com", "Zeho", "password123")
	if err != nil {
		t.Fatal(err)
	}
	wsID := sess.Workspaces[0].ID
	key, err := svc.IssueAPIKey(ctx, wsID, sess.User.ID, "cli")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(key) < 32 {
		t.Fatalf("api key 太短: %q", key)
	}
	p, err := svc.Authenticate(ctx, key)
	if err != nil {
		t.Fatalf("api key auth: %v", err)
	}
	if p.WorkspaceID != wsID {
		t.Fatalf("principal=%+v", p)
	}
	if err := svc.RevokeAPIKey(ctx, wsID, key); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.Authenticate(ctx, key); err == nil {
		t.Fatal("已吊销的 key 应拒绝")
	}
}

func TestWorkspaceRoleIsolation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	a, _ := svc.Register(ctx, "a@b.com", "A", "password123")
	b, _ := svc.Register(ctx, "b@b.com", "B", "password123")
	wsA := a.Workspaces[0].ID
	// B 不是 A 工作区的成员
	role, err := svc.WorkspaceRole(ctx, wsA, b.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if role != "" {
		t.Fatalf("跨工作区成员判定失败: role=%q", role)
	}
	// B 的工作区列表不含 A 的工作区（INV-10）
	list, _ := svc.ListWorkspaces(ctx, b.User.ID)
	for _, w := range list {
		if w.ID == wsA {
			t.Fatal("工作区越权可见")
		}
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sess, _ := svc.Register(ctx, "a@b.com", "A", "password123")
	if err := svc.Logout(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, sess.Token); err == nil {
		t.Fatal("登出后 token 应失效")
	}
}
