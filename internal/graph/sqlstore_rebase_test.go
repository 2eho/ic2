package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

// newSQLService 起一个真实的 SQLite 存储（不是内存替身）。
//
// 这里刻意**不复用**内存实现：本文件覆盖的缺陷只在 SQL 路径上存在，
// 用内存替身跑永远不会红——那正是它长期没被发现的原因。
func newSQLService(t *testing.T) (*Service, *SQLStore) {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	// 每个用例独立库名，避免共享 cache 互相污染
	cfg.DBDSN = "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db.DB, db.Dialect)
	svc := NewService(store, nil, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	return svc, store
}

// 回归：SQLite 下 ListOpsSinceVersion 必须能读出来。
//
// 真实缺陷：`created_at` 在 SQLite 是 TEXT，直接 Scan 到 time.Time 报
// "unsupported Scan, storing driver.Value type string into type *time.Time"。
// 该函数只被**冲突 rebase 路径**调用，因此症状是：
//   - 单客户端顺序写入一切正常；
//   - 一旦出现并发（需要 rebase），接口直接 500，而不是「合并」或「409」。
//
// 实测在 SQLite 上并发提交第二个 op 就 500（ATK-11 e2e 抓到的）。
func TestATK11SQLListOpsSinceVersionReadsRows(t *testing.T) {
	svc, store := newSQLService(t)
	ctx := context.Background()
	meta, err := svc.Create(ctx, "pj_1", "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AppendOps(ctx, meta.ID, 0, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, "u_1"); err != nil {
		t.Fatal(err)
	}

	recs, err := store.ListOpsSinceVersion(ctx, meta.ID, 0, 32)
	if err != nil {
		t.Fatalf("ListOpsSinceVersion 读取失败（时间列扫描问题会在此暴露）: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("期望 1 条 op，实际 %d", len(recs))
	}
	if recs[0].CreatedAt.IsZero() {
		t.Fatal("created_at 未能解析（时间戳全为零值会让审计与排序失真）")
	}
	if recs[0].ActorID != "u_1" {
		t.Fatalf("actor 读错: %q", recs[0].ActorID)
	}
	if recs[0].Version == 0 {
		t.Fatal("version 读错")
	}
}

// 回归：SQLite 上「可 rebase 的并发提交」必须真的合并成功，
// 「不可 rebase 的并发提交」必须返回 409（不是 500）。
//
// 这两个断言把「冲突路径在 SQL 存储上从未工作」这件事钉住：
// 旧实现下两者都会得到 500 internal。
func TestATK11SQLRebasePathWorks(t *testing.T) {
	svc, _ := newSQLService(t)
	ctx := context.Background()
	meta, err := svc.Create(ctx, "pj_1", "c")
	if err != nil {
		t.Fatal(err)
	}
	// 版本 0 → 1：两个节点
	if _, _, err := svc.AppendOps(ctx, meta.ID, 0, []json.RawMessage{
		addPromptOp(t, "p_1", 0, 0), addPromptOp(t, "p_2", 500, 0),
	}, "u_1"); err != nil {
		t.Fatal(err)
	}
	base, _ := svc.Get(ctx, meta.ID)

	// 在途：移动 p_1（版本推进到 2）
	if _, _, err := svc.AppendOps(ctx, meta.ID, base.Version, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "move_node", "id": "p_1", "x": 10, "y": 10}),
	}, "u_2"); err != nil {
		t.Fatal(err)
	}

	// 过期客户端移动 p_2：与在途移动「不同节点」→ 应自动 rebase 成功
	res, _, err := svc.AppendOps(ctx, meta.ID, base.Version, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "move_node", "id": "p_2", "x": 20, "y": 20}),
	}, "u_3")
	if err != nil {
		t.Fatalf("可交换的并发提交应自动 rebase，实际报错: %v", err)
	}
	if !res.Rebased {
		t.Fatal("期望标记 rebased=true（否则用户不知道发生过合并）")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("自动合并必须给出 warning（静默合并与静默覆盖一样危险）")
	}

	// 过期客户端改同一节点 spec：不可 rebase → 必须 409
	// 注意在途 op 必须**也落在同一节点**，否则按设计就是可合并的（测不到冲突）。
	inflight := []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "a"}}),
	}
	if _, _, err := svc.AppendOps(ctx, meta.ID, base.Version, inflight, "u_2b"); err != nil {
		t.Fatalf("首次 spec 修改应成功: %v", err)
	}
	_, _, err = svc.AppendOps(ctx, meta.ID, base.Version, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "x"}}),
	}, "u_4")
	if err == nil {
		t.Fatal("同一字段并发修改必须冲突")
	}
	var de *platform.DomainError
	if !errors.As(err, &de) || de.Status != 409 {
		t.Fatalf("期望 409，实际: %v（500 说明冲突路径本身坏了）", err)
	}
}
