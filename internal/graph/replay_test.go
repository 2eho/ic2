package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"
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
