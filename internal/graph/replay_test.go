package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// ATK-21：画布 op 日志重放与快照比对，必须完全一致（INV-1）。
func TestATK21ReplayEqualsSnapshot(t *testing.T) {
	doc := baseDoc()
	opSets := [][]json.RawMessage{
		{addPromptOp(t, "p_1", 0, 0), addPromptOp(t, "p_2", 400, 0)},
		{mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "g_1", "type": "generation",
				"rect": map[string]any{"x": 800, "y": 0, "w": 340, "h": 260},
				"spec": map[string]any{"capability": "image.generate", "model": "gpt-image-1"},
			},
		})},
		{mustJSON(t, map[string]any{
			"kind": "add_edge",
			"edge": map[string]any{
				"id":   "e_1",
				"from": map[string]any{"nodeId": "p_1", "portId": "out"},
				"to":   map[string]any{"nodeId": "g_1", "portId": "prompt"},
			},
		})},
		{mustJSON(t, map[string]any{"kind": "move_node", "id": "g_1", "x": 900, "y": 40})},
		{mustJSON(t, map[string]any{"kind": "resize_node", "id": "g_1", "w": 380, "h": 300})},
		{mustJSON(t, map[string]any{"kind": "set_title", "id": "g_1", "title": "主图生成"})},
		{mustJSON(t, map[string]any{"kind": "set_spec", "id": "g_1", "patch": map[string]any{"outputCount": 4}})},
		{mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "grp_1", "type": "group",
				"rect": map[string]any{"x": -50, "y": -50, "w": 600, "h": 400},
				"spec": map[string]any{},
			},
		})},
		{mustJSON(t, map[string]any{"kind": "group", "nodeIds": []string{"p_1", "p_2"}, "groupId": "grp_1"})},
		{mustJSON(t, map[string]any{"kind": "set_viewport", "viewport": map[string]any{"x": 120, "y": 80, "k": 0.8}})},
		{mustJSON(t, map[string]any{"kind": "set_settings", "settings": map[string]any{"background": "lines"}})},
		{mustJSON(t, map[string]any{"kind": "remove_edge", "id": "e_1"})},
		{mustJSON(t, map[string]any{"kind": "remove_node", "id": "p_2"})},
		{mustJSON(t, map[string]any{
			"kind": "add_edge",
			"edge": map[string]any{
				"id":   "e_2",
				"from": map[string]any{"nodeId": "p_1", "portId": "out"},
				"to":   map[string]any{"nodeId": "g_1", "portId": "prompt"},
			},
		})},
	}

	log := [][]byte{}
	cur := doc
	now := time.Unix(1700000000, 0).UTC()
	for i, ops := range opSets {
		raws := make([]json.RawMessage, 0, len(ops))
		for _, o := range ops {
			log = append(log, o)
			raws = append(raws, o)
		}
		var err error
		_, cur, err = Apply(cur, raws, "u_1", now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("op set %d: %v", i, err)
		}
	}

	replayed, err := Replay(doc, log, now)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	// 版本号在重放中会累积，比对前对齐。
	replayed.Version = cur.Version
	replayed.UpdatedAt = cur.UpdatedAt
	if diff := DiffDocuments(cur, replayed); diff != "" {
		t.Fatalf("INV-1 违反：%s", diff)
	}
}

// 通过 Service 走完整的落库 + 重放校验路径。
func TestServiceReplayInvariant(t *testing.T) {
	store := NewMemoryStore()
	bus := NewMemoryBus()
	svc := NewService(store, bus, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	ctx := context.Background()
	meta, err := svc.Create(ctx, "pj_1", "画布 1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := svc.Get(ctx, meta.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, _, err := svc.AppendOps(ctx, meta.ID, doc.Version, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, "u_1"); err != nil {
		t.Fatalf("append: %v", err)
	}
	cur, _ := svc.Get(ctx, meta.ID)
	if _, _, err := svc.AppendOps(ctx, meta.ID, cur.Version, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "move_node", "id": "p_1", "x": 40, "y": 40}),
	}, "u_1"); err != nil {
		t.Fatalf("append2: %v", err)
	}
	if err := svc.OpenReplay(ctx, meta.ID); err != nil {
		t.Fatalf("INV-1 校验失败: %v", err)
	}
}

func TestVersionConflictDetected(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	ctx := context.Background()
	meta, _ := svc.Create(ctx, "pj_1", "c")
	doc, _ := svc.Get(ctx, meta.ID)
	if _, _, err := svc.AppendOps(ctx, meta.ID, doc.Version, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, "u_1"); err != nil {
		t.Fatal(err)
	}
	// 同一 baseVersion 再次提交且目标节点不同 → 允许 rebase
	if _, _, err := svc.AppendOps(ctx, meta.ID, doc.Version, []json.RawMessage{addPromptOp(t, "p_2", 500, 0)}, "u_2"); err != nil {
		t.Fatalf("不同节点的并发提交应可 rebase，实际: %v", err)
	}
	// 同一 baseVersion 且改同一节点 spec → 409
	cur, _ := svc.Get(ctx, meta.ID)
	if _, _, err := svc.AppendOps(ctx, meta.ID, doc.Version, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "conflict"}}),
	}, "u_3"); err == nil {
		t.Fatal("同一字段并发修改应返回 409")
	}
	_ = cur
}

// ATK-11 回归：baseVersion=0 不得被解释成「以服务端当前版本为准」。
//
// 真实缺陷：旧实现 `if base == 0 { base = current.Version }`，于是任何漏传
// baseVersion 的客户端都会**跳过冲突检测**并静默覆盖他人改动。
// 实测表现：客户端在版本 1 用 baseVersion=0 提交，期望 409，实得 200。
func TestATK11ZeroBaseVersionIsNotABypass(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	ctx := context.Background()
	meta, _ := svc.Create(ctx, "pj_1", "c")

	// 客户端 A 在版本 0（刚建）提交 → 合法，版本推进到 1
	if _, _, err := svc.AppendOps(ctx, meta.ID, 0, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, "u_a"); err != nil {
		t.Fatalf("版本 0 的首次写入应被接受: %v", err)
	}

	// 客户端 B 仍以为自己处于版本 0（漏传 / 传 0），提交对**同一节点同一字段**的
	// 修改 → 必须 409，而不是静默覆盖 A 的改动。
	//
	// 用同一节点同一字段是为了让冲突**不可自动 rebase**：若换成新建另一个节点，
	// 按设计（docs/design/03 §2.1 策略 1）是允许 rebase 的，那就测不到本缺陷。
	// 旧实现会把 base 悄悄改成服务端当前版本 → 跳过比对 → 直接应用 → 返回 200。
	_, _, err := svc.AppendOps(ctx, meta.ID, 0, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "overwritten-by-stale-client"}}),
	}, "u_b")
	if err == nil {
		t.Fatal("baseVersion=0 在版本已推进后必须冲突（否则等于静默覆盖他人改动）")
	}
	var pe *platform.DomainError
	if !errors.As(err, &pe) || pe.Status != 409 {
		t.Fatalf("期望 409 conflict，实际: %v", err)
	}

	// 且 A 的改动必须原样保留（「返回 409」与「没写进去」必须同时成立）
	doc, _ := svc.Get(ctx, meta.ID)
	node, ok := doc.Nodes["p_1"]
	if !ok {
		t.Fatal("A 的节点消失了")
	}
	if got := node.Spec["text"]; got == "overwritten-by-stale-client" {
		t.Fatalf("过期客户端的写入被静默应用（值是 %v）", got)
	}

	// 另一侧：baseVersion 正确时同一字段的修改仍应被允许（不能把冲突检查做成恒拒）
	cur, _ := svc.Get(ctx, meta.ID)
	if _, _, err := svc.AppendOps(ctx, meta.ID, cur.Version, []json.RawMessage{
		mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "legit-update"}}),
	}, "u_c"); err != nil {
		t.Fatalf("持有正确版本的写入不应被拒: %v", err)
	}
}

// baseVersion 超前于服务端（时钟/状态错乱）必须冲突，不能盲目应用。
func TestATK11FutureBaseVersionConflicts(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	ctx := context.Background()
	meta, _ := svc.Create(ctx, "pj_1", "c")
	_, _, err := svc.AppendOps(ctx, meta.ID, 99, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, "u_a")
	if err == nil {
		t.Fatal("baseVersion 超前必须冲突")
	}
	var pe *platform.DomainError
	if !errors.As(err, &pe) || pe.Status != 409 {
		t.Fatalf("期望 409，实际: %v", err)
	}
}

func TestRebaseableAllowsDisjointMoves(t *testing.T) {
	_ = mustDoc(t, addPromptOp(t, "p_1", 0, 0), addPromptOp(t, "p_2", 500, 0))
	inflight := []Record{{Op: mustJSON(t, map[string]any{"kind": "move_node", "id": "p_1", "x": 10, "y": 10})}}
	incoming := []json.RawMessage{mustJSON(t, map[string]any{"kind": "move_node", "id": "p_2", "x": 20, "y": 20})}
	ok, kinds := rebaseable(incoming, inflight)
	if !ok || len(kinds) != 1 {
		t.Fatalf("不同节点的移动应可 rebase: ok=%v kinds=%v", ok, kinds)
	}
}

func TestRebaseableRejectsSameField(t *testing.T) {
	inflight := []Record{{Op: mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "a"}})}}
	incoming := []json.RawMessage{mustJSON(t, map[string]any{"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "b"}})}
	if ok, _ := rebaseable(incoming, inflight); ok {
		t.Fatal("同一节点 spec 的并发修改不可自动 rebase")
	}
}

func TestRebaseableRejectsGlobalOps(t *testing.T) {
	inflight := []Record{{Op: mustJSON(t, map[string]any{"kind": "set_viewport", "viewport": map[string]any{"x": 1, "y": 1, "k": 1}})}}
	incoming := []json.RawMessage{mustJSON(t, map[string]any{"kind": "move_node", "id": "p_1", "x": 1, "y": 1})}
	if ok, _ := rebaseable(incoming, inflight); ok {
		t.Fatal("全局 op 在途时不可自动 rebase")
	}
}

func TestEventBroadcast(t *testing.T) {
	store := NewMemoryStore()
	bus := NewMemoryBus()
	svc := NewService(store, bus, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	ctx := context.Background()
	meta, _ := svc.Create(ctx, "pj_1", "c")
	ch, cancel := svc.Subscribe(meta.ID)
	defer cancel()
	doc, _ := svc.Get(ctx, meta.ID)
	if _, _, err := svc.AppendOps(ctx, meta.ID, doc.Version, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, "u_1"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Type != "canvas.op" || ev.Seq != 1 || ev.ActorID != "u_1" {
			t.Fatalf("事件内容不符: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到事件")
	}
}

func TestActorRequired(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, &fixedClock{t: time.Unix(1700000000, 0).UTC()}, nil)
	ctx := context.Background()
	meta, _ := svc.Create(ctx, "pj_1", "c")
	doc, _ := svc.Get(ctx, meta.ID)
	// INV-8：写操作必须可归属。
	if _, _, err := svc.AppendOps(ctx, meta.ID, doc.Version, []json.RawMessage{addPromptOp(t, "p_1", 0, 0)}, ""); err == nil {
		t.Fatal("无 actor 的写操作应被拒绝")
	}
}

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time                  { return c.t }
func (c *fixedClock) Since(t time.Time) time.Duration { return c.t.Sub(t) }
