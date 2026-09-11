package platform

import (
	"context"
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
