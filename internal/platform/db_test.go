package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/context-flow/ic/migrations"
)

func TestMigrateEmbedded(t *testing.T) {
	cfg := Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file::memory:?cache=shared"
	cfg.AllowInsecureDevKey = true
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 幂等：再跑一次不出错
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='canvas_ops'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("canvas_ops 不存在")
	}
}

// 迁移路径必须跳过 `*.down.sql`：把回滚脚本当上行迁移执行会让 schema_migrations
// 被中途删除，之后所有迁移记录静默丢失（曾真实发生，故固化为用例）。
func TestMigrateSkipsDownScripts(t *testing.T) {
	cfg := Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + t.TempDir() + "/ic.db"
	cfg.AllowInsecureDevKey = true
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	applied, err := db.AppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("迁移记录为空，说明 down 脚本被当成上行迁移执行了")
	}
	for _, v := range applied {
		if strings.HasSuffix(v, ".down.sql") {
			t.Fatalf("回滚脚本 %s 被当作上行迁移执行", v)
		}
	}
	// 幂等：再跑一次不应新增
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	again, _ := db.AppliedMigrations(ctx)
	if len(again) != len(applied) {
		t.Fatalf("迁移不幂等：%d → %d", len(applied), len(again))
	}
}

// 回滚必须能回到干净状态，且 applied 记录同步清除（备份恢复演练的前置能力）。
func TestMigrateDownRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + t.TempDir() + "/ic.db"
	cfg.AllowInsecureDevKey = true
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	rolled, err := db.MigrateDown(ctx, migrations.FS, ".", 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(rolled) != 1 {
		t.Fatalf("应回滚 1 个迁移，实际 %d", len(rolled))
	}
	applied, err := db.AppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("回滚后仍有迁移记录: %v", applied)
	}
	// 回滚后可重新迁移（说明 down 脚本真的把表清干净了）
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatalf("回滚后重新迁移失败: %v", err)
	}
}
