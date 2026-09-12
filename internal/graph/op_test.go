package graph

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func baseDoc() *CanvasDocument {
	d := NewDocument("cv_1", "pj_1")
	d.Settings = DefaultSettings()
	return d
}

func addPromptOp(t *testing.T, id string, x, y float64) json.RawMessage {
	t.Helper()
	return mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": id, "type": "prompt", "title": "提示词",
			"rect": map[string]any{"x": x, "y": y, "w": 320, "h": 220},
			"spec": map[string]any{"text": "hello"},
		},
	})
}

// ATK-01：提交 x: NaN 的 add_node → 422，code=invalid_geometry。
func TestATK01NaNGeometryRejected(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		raw, err := json.Marshal(map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "n_1", "type": "prompt",
				"rect": map[string]any{"x": bad, "y": 0, "w": 320, "h": 220},
				"spec": map[string]any{"text": "x"},
			},
		})
		if err != nil {
			// encoding/json 拒绝 NaN/Inf 本身也是可接受的防线
			continue
		}
		_, _, aerr := Apply(baseDoc(), []json.RawMessage{raw}, "u_1", time.Now())
		if aerr == nil {
			t.Fatalf("x=%v 未被拒绝", bad)
		}
		if de := platform.AsDomainError(aerr); de.Code != CodeInvalidGeometry && de.Code != CodeUnknownField {
			t.Fatalf("x=%v code=%s", bad, de.Code)
		}
	}
	// 直接构造非法 float（绕过 JSON 编码）
	doc := baseDoc()
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "move_node", "id": "nope", "x": 0.0, "y": 0.0,
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("移动不存在的节点应报错")
	}
	if de := platform.AsDomainError(err); de.Code != CodeNotFound {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestCoordinateRangeRejected(t *testing.T) {
	_, _, err := Apply(baseDoc(), []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "n_1", "type": "prompt",
			"rect": map[string]any{"x": 1e15, "y": 0, "w": 320, "h": 220},
			"spec": map[string]any{"text": "x"},
		},
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("x=1e15 应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != CodeInvalidGeometry {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestSizeRangeRejected(t *testing.T) {
	for _, w := range []float64{0, 1, 1e9} {
		_, _, err := Apply(baseDoc(), []json.RawMessage{mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "n_1", "type": "prompt",
				"rect": map[string]any{"x": 0, "y": 0, "w": w, "h": 220},
				"spec": map[string]any{"text": "x"},
			},
		})}, "u_1", time.Now())
		if err == nil {
			t.Fatalf("w=%v 应被拒绝", w)
		}
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	err := ValidateSpec(NodeTypePrompt, NodeSpec{"text": "a", "bogus": 1})
	if err == nil {
		t.Fatal("未知 spec 字段应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != CodeInvalidSpec {
		t.Fatalf("code=%s", de.Code)
	}
	_, _, aerr := Apply(baseDoc(), []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "n_1", "type": "prompt", "bogusField": 1,
			"rect": map[string]any{"x": 0, "y": 0, "w": 320, "h": 220},
			"spec": map[string]any{"text": "x"},
		},
	})}, "u_1", time.Now())
	if aerr == nil {
		t.Fatal("未知 node 字段应被拒绝（严格模式）")
	}
}

func TestNullSpecValueRejected(t *testing.T) {
	err := ValidateSpec(NodeTypePrompt, NodeSpec{"text": nil})
	if err == nil {
		t.Fatal("null 字段应被拒绝，必须用 unset 显式删除")
	}
}

func TestInvalidIDRejected(t *testing.T) {
	for _, id := range []string{"", "  ", "../../etc/passwd", "a/b", "id with space"} {
		_, _, err := Apply(baseDoc(), []json.RawMessage{mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": id, "type": "prompt",
				"rect": map[string]any{"x": 0, "y": 0, "w": 320, "h": 220},
				"spec": map[string]any{"text": "x"},
			},
		})}, "u_1", time.Now())
		if err == nil {
			t.Fatalf("id=%q 应被拒绝", id)
		}
	}
}

func TestInvalidNodeTypeRejected(t *testing.T) {
	for _, ty := range []string{"../../etc/passwd", "Image", "-bad", "a:b:c", ""} {
		if ValidNodeTypeID(ty) && ty != "" {
			continue
		}
		_, _, err := Apply(baseDoc(), []json.RawMessage{mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "n_1", "type": ty,
				"rect": map[string]any{"x": 0, "y": 0, "w": 320, "h": 220},
				"spec": map[string]any{},
			},
		})}, "u_1", time.Now())
		if err == nil {
			t.Fatalf("type=%q 应被拒绝", ty)
		}
	}
}

func TestDuplicateNodeConflict(t *testing.T) {
	doc := baseDoc()
	next, err := applyBatch(t, doc, addPromptOp(t, "n_1", 0, 0))
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	_, _, err = Apply(next, []json.RawMessage{addPromptOp(t, "n_1", 10, 10)}, "u_1", time.Now())
	if err == nil {
		t.Fatal("重复 ID 应返回冲突")
	}
	if de := platform.AsDomainError(err); de.Code != CodeConflict {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestPortKindMismatchRejected(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0), addPromptOp(t, "p_2", 0, 300))
	// prompt 只有 text 输出，没有 image 输出 → 端口不存在
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "add_edge",
		"edge": map[string]any{
			"id":   "e_1",
			"from": map[string]any{"nodeId": "p_1", "portId": "out"},
			"to":   map[string]any{"nodeId": "p_2", "portId": "in"},
		},
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("prompt→prompt 输入端口不存在，应拒绝")
	}
}

func TestGroupNodeCannotBeConnected(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0))
	doc2 := doc.Clone()
	doc2.Nodes["g_1"] = Node{ID: "g_1", Type: NodeTypeGroup, Rect: Rect{0, 0, 480, 320}, Ports: Ports{}, Spec: NodeSpec{}}
	_, _, err := Apply(doc2, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "add_edge",
		"edge": map[string]any{
			"id":   "e_1",
			"from": map[string]any{"nodeId": "p_1", "portId": "out"},
			"to":   map[string]any{"nodeId": "g_1", "portId": "in"},
		},
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("分组节点禁止连线")
	}
}

func TestCycleRejected(t *testing.T) {
	genA := mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "g_1", "type": "generation",
			"rect": map[string]any{"x": 0, "y": 0, "w": 340, "h": 260},
			"spec": map[string]any{"capability": "image.generate"},
		},
	})
	genB := mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "g_2", "type": "generation",
			"rect": map[string]any{"x": 0, "y": 300, "w": 340, "h": 260},
			"spec": map[string]any{"capability": "image.generate"},
		},
	})
	doc := mustDoc(t, genA, genB)
	doc = mustDoc2(t, doc, mustJSON(t, map[string]any{
		"kind": "add_edge",
		"edge": map[string]any{
			"id":   "e_1",
			"from": map[string]any{"nodeId": "g_1", "portId": "out"},
			"to":   map[string]any{"nodeId": "g_2", "portId": "ref"},
		},
	}))
	// g_2 输出 → g_1 参考图：成环
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "add_edge",
		"edge": map[string]any{
			"id":   "e_2",
			"from": map[string]any{"nodeId": "g_2", "portId": "out"},
			"to":   map[string]any{"nodeId": "g_1", "portId": "ref"},
		},
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("成环连线应被拒绝")
	}
}

func TestSelfConnectionRejected(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0))
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "add_edge",
		"edge": map[string]any{
			"id":   "e_1",
			"from": map[string]any{"nodeId": "p_1", "portId": "out"},
			"to":   map[string]any{"nodeId": "p_1", "portId": "in"},
		},
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("自连接应被拒绝")
	}
}

func TestOpBatchSizeLimit(t *testing.T) {
	ops := make([]json.RawMessage, 0, MaxOpBatch+1)
	for i := 0; i <= MaxOpBatch; i++ {
		ops = append(ops, addPromptOp(t, "n_"+itoa(i), float64(i*10), 0))
	}
	_, _, err := Apply(baseDoc(), ops, "u_1", time.Now())
	if err == nil {
		t.Fatal("超批量上限应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != CodePayloadTooLarge {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestUnknownOpKindRejected(t *testing.T) {
	_, _, err := Apply(baseDoc(), []json.RawMessage{json.RawMessage(`{"kind":"explode_canvas"}`)}, "u_1", time.Now())
	if err == nil {
		t.Fatal("未知 op 应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != CodeUnknownField {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestEmptyOpBatchIsNoop(t *testing.T) {
	doc := baseDoc()
	res, next, err := Apply(doc, nil, "u_1", time.Now())
	if err != nil {
		t.Fatalf("空 op 不应报错: %v", err)
	}
	if res.Version != doc.Version || next.Version != doc.Version {
		t.Fatal("空 op 不应递增版本")
	}
}

func TestSpecUnsetSemantics(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0))
	next := mustDoc2(t, doc, mustJSON(t, map[string]any{
		"kind": "set_spec", "id": "p_1", "unset": []string{"text"},
	}))
	if _, ok := next.Nodes["p_1"].Spec["text"]; ok {
		t.Fatal("unset 未生效")
	}
	// inverse 应能恢复
	inv := mustJSON(t, map[string]any{
		"kind": "set_spec", "id": "p_1", "patch": map[string]any{"text": "recovered"},
	})
	back := mustDoc2(t, next, inv)
	if back.Nodes["p_1"].Spec["text"] != "recovered" {
		t.Fatal("反向 op 未恢复字段")
	}
}

func TestSetSpecUnknownFieldRejected(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0))
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "set_spec", "id": "p_1", "patch": map[string]any{"hack": 1},
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("未知字段应被拒绝")
	}
}

func TestGenerationOutputCountRange(t *testing.T) {
	for _, n := range []int{0, -1, 16, 100} {
		err := ValidateSpec(NodeTypeGeneration, NodeSpec{"capability": "image.generate", "outputCount": n})
		if err == nil {
			t.Fatalf("outputCount=%d 应被拒绝", n)
		}
	}
	if err := ValidateSpec(NodeTypeGeneration, NodeSpec{"capability": "image.generate", "outputCount": 4}); err != nil {
		t.Fatalf("outputCount=4 应通过: %v", err)
	}
}

func TestGenerationCapabilityMustBeKnown(t *testing.T) {
	err := ValidateSpec(NodeTypeGeneration, NodeSpec{"capability": "image.hack"})
	if err == nil {
		t.Fatal("未知 capability 应被拒绝")
	}
}

func TestGroupDepthLimit(t *testing.T) {
	doc := baseDoc()
	next := doc
	for i := 0; i < MaxGroupDepth+3; i++ {
		op := mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "g_" + itoa(i), "type": "group",
				"rect": map[string]any{"x": 0, "y": 0, "w": 480, "h": 320},
				"spec": map[string]any{},
			},
		})
		var err error
		next, err = applyBatch(t, next, op)
		if err != nil {
			t.Fatalf("add group %d: %v", i, err)
		}
	}
	// 逐层嵌套：允许的层数 = MaxGroupDepth，再深一层必须被拒绝。
	applied := 0
	for i := 0; i < MaxGroupDepth+3; i++ {
		op := mustJSON(t, map[string]any{
			"kind": "set_parent", "id": "g_" + itoa(i), "parentId": "g_" + itoa(i+1),
		})
		var err error
		var candidate *CanvasDocument
		candidate, err = applyBatch(t, next, op)
		if err != nil {
			if applied < MaxGroupDepth {
				t.Fatalf("在第 %d 层被拒绝，过早（期望至少 %d 层可嵌套）", applied, MaxGroupDepth)
			}
			return
		}
		next = candidate
		applied++
	}
	t.Fatal("超深嵌套应被拒绝")
}

func TestGroupCycleDetected(t *testing.T) {
	doc := baseDoc()
	for i := 0; i < 3; i++ {
		op := mustJSON(t, map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": "g_" + itoa(i), "type": "group",
				"rect": map[string]any{"x": 0, "y": 0, "w": 480, "h": 320},
				"spec": map[string]any{},
			},
		})
		var err error
		doc, err = applyBatch(t, doc, op)
		if err != nil {
			t.Fatal(err)
		}
	}
	doc = mustDoc2(t, doc, mustJSON(t, map[string]any{"kind": "set_parent", "id": "g_0", "parentId": "g_1"}))
	doc = mustDoc2(t, doc, mustJSON(t, map[string]any{"kind": "set_parent", "id": "g_1", "parentId": "g_2"}))
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "set_parent", "id": "g_2", "parentId": "g_0",
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("分组环应被拒绝")
	}
}

func TestViewportRange(t *testing.T) {
	doc := baseDoc()
	for _, k := range []float64{0, -1, 1e6, ZoomMin / 2, ZoomMax * 2} {
		_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
			"kind": "set_viewport", "viewport": map[string]any{"x": 0, "y": 0, "k": k},
		})}, "u_1", time.Now())
		if err == nil {
			t.Fatalf("k=%v 应被拒绝", k)
		}
	}
	if _, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "set_viewport", "viewport": map[string]any{"x": 10, "y": -20, "k": 1.5},
	})}, "u_1", time.Now()); err != nil {
		t.Fatalf("合法视口被拒绝: %v", err)
	}
}

func TestSettingsValidation(t *testing.T) {
	doc := baseDoc()
	if _, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "set_settings", "settings": map[string]any{"background": "rainbow"},
	})}, "u_1", time.Now()); err == nil {
		t.Fatal("非法背景应被拒绝")
	}
	if _, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "set_settings", "settings": map[string]any{"unknown": true},
	})}, "u_1", time.Now()); err == nil {
		t.Fatal("未知设置项应被拒绝")
	}
	next := mustDoc2(t, doc, mustJSON(t, map[string]any{
		"kind": "set_settings", "settings": map[string]any{"background": "blank", "imageInfo": false},
	}))
	if next.Settings.Background != "blank" || next.Settings.ImageInfo {
		t.Fatal("设置未生效")
	}
}

func TestRemoveNonExistentNode(t *testing.T) {
	_, _, err := Apply(baseDoc(), []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "remove_node", "id": "nope",
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("删除不存在节点应报 404")
	}
	if de := platform.AsDomainError(err); de.Code != CodeNotFound {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestRemoveNodeCascadeFalseWithEdges(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0))
	gen := mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "g_1", "type": "generation",
			"rect": map[string]any{"x": 400, "y": 0, "w": 340, "h": 260},
			"spec": map[string]any{"capability": "image.generate"},
		},
	})
	doc = mustDoc2(t, doc, gen)
	edge := mustJSON(t, map[string]any{
		"kind": "add_edge",
		"edge": map[string]any{
			"id":   "e_1",
			"from": map[string]any{"nodeId": "p_1", "portId": "out"},
			"to":   map[string]any{"nodeId": "g_1", "portId": "prompt"},
		},
	})
	doc = mustDoc2(t, doc, edge)
	_, _, err := Apply(doc, []json.RawMessage{mustJSON(t, map[string]any{
		"kind": "remove_node", "id": "p_1", "cascade": false,
	})}, "u_1", time.Now())
	if err == nil {
		t.Fatal("有连线且 cascade=false 应返回冲突")
	}
}

func TestSingleInputPortReplacement(t *testing.T) {
	doc := baseDoc()
	// video 节点 in 端口是 multiple=true，这里改用 generation 的 prompt 多入；
	// 用 plugin 语义的 image in（multiple=true）不便于测试，改测「单入端口」用 image 的 out→generation prompt
	// 构造两个 prompt 接同一个 generation 的 prompt 端口（multiple=true，都保留）
	for _, id := range []string{"p_1", "p_2"} {
		var err error
		doc, err = applyBatch(t, doc, addPromptOp(t, id, float64(len(id))*100, 0))
		if err != nil {
			t.Fatal(err)
		}
	}
	doc = mustDoc2(t, doc, mustJSON(t, map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "g_1", "type": "generation",
			"rect": map[string]any{"x": 400, "y": 0, "w": 340, "h": 260},
			"spec": map[string]any{"capability": "image.generate"},
		},
	}))
	for i, p := range []string{"p_1", "p_2"} {
		doc = mustDoc2(t, doc, mustJSON(t, map[string]any{
			"kind": "add_edge",
			"edge": map[string]any{
				"id":   "e_" + itoa(i),
				"from": map[string]any{"nodeId": p, "portId": "out"},
				"to":   map[string]any{"nodeId": "g_1", "portId": "prompt"},
			},
		}))
	}
	if len(doc.Edges) != 2 {
		t.Fatalf("多入端口应保留两条边，实际 %d", len(doc.Edges))
	}
}

func TestApplyIsAtomicOnFailure(t *testing.T) {
	doc := mustDoc(t, addPromptOp(t, "p_1", 0, 0))
	before := doc.Clone()
	ops := []json.RawMessage{
		addPromptOp(t, "p_2", 100, 0),
		mustJSON(t, map[string]any{"kind": "move_node", "id": "missing", "x": 1, "y": 1}),
	}
	_, _, err := Apply(doc, ops, "u_1", time.Now())
	if err == nil {
		t.Fatal("应失败")
	}
	if DiffDocuments(before, doc) != "" {
		t.Fatal("失败时不应污染原文档")
	}
}

func TestStrictOpPayloadRejectsUnknownKeys(t *testing.T) {
	// add_node payload 中的未知键必须拒绝
	_, _, err := Apply(baseDoc(), []json.RawMessage{json.RawMessage(`{"kind":"add_node","bogus":1,"node":{"id":"n_1","type":"prompt","rect":{"x":0,"y":0,"w":320,"h":220},"spec":{"text":"a"}}}`)}, "u_1", time.Now())
	if err == nil {
		t.Fatal("op 未知键应被拒绝")
	}
}

func TestDomainErrorShape(t *testing.T) {
	err := platform.NewError(422, CodeInvalidGeometry, "bad").WithDetail("k", 1)
	var de *platform.DomainError
	if !errors.As(error(err), &de) {
		t.Fatal("errors.As 失败")
	}
	if de.Code != "invalid_geometry" || de.Status != 422 {
		t.Fatalf("%+v", de)
	}
}

// helpers

func applyBatch(t *testing.T, doc *CanvasDocument, ops ...json.RawMessage) (*CanvasDocument, error) {
	t.Helper()
	_, next, err := Apply(doc, ops, "u_1", time.Unix(1700000000, 0).UTC())
	return next, err
}

func mustDoc(t *testing.T, ops ...json.RawMessage) *CanvasDocument {
	t.Helper()
	next, err := applyBatch(t, baseDoc(), ops...)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return next
}

func mustDoc2(t *testing.T, doc *CanvasDocument, ops ...json.RawMessage) *CanvasDocument {
	t.Helper()
	next, err := applyBatch(t, doc, ops...)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return next
}

func itoa(n int) string {
	return json.Number(itoaRaw(n)).String()
}

func itoaRaw(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ---------------------------------------------------------------- meta 通道
//
// meta 是「明确不参与执行」的扩展位（02-domain-model）。它不做字段白名单
//（那会限制用途），但必须做原型链与规模检查 —— 因为 meta 会被序列化、
// 传到前端、由 JSON.parse 变成真实对象，那时才是污染生效的时刻。

func TestApplySetMetaWritesUnderMetaNotSpec(t *testing.T) {
	doc := NewDocument("cv_1", "p_1")
	doc.Nodes["n1"] = Node{ID: "n1", Type: NodeTypePrompt, Spec: NodeSpec{"text": "t"}}

	inverse, err := applySetSpec(doc, &SetSpecPayload{ID: "n1", Meta: map[string]any{"k": "v"}})
	if err != nil {
		t.Fatalf("只改 meta 应当成功: %v", err)
	}
	if doc.Nodes["n1"].Meta["k"] != "v" {
		t.Fatalf("meta 未写入: %#v", doc.Nodes["n1"].Meta)
	}
	if _, leaked := doc.Nodes["n1"].Spec["k"]; leaked {
		t.Fatal("meta 内容不该进 spec")
	}
	if len(inverse) == 0 {
		t.Fatal("必须返回 inverse（否则这个修改无法撤销）")
	}
}

// ATK-25：meta 里的原型链键必须在服务端就被拒绝。
func TestApplySetMetaRejectsProtoKeys(t *testing.T) {
	for _, key := range []string{"__proto__", "constructor", "prototype"} {
		doc := NewDocument("cv_1", "p_1")
		doc.Nodes["n1"] = Node{ID: "n1", Type: NodeTypePrompt}
		_, err := applySetSpec(doc, &SetSpecPayload{ID: "n1", Meta: map[string]any{key: 1}})
		if err == nil {
			t.Fatalf("meta 键 %s 应当被拒绝（它会在前端 JSON.parse 后变成真实污染）", key)
		}
	}
}

func TestApplySetMetaRejectsNullValue(t *testing.T) {
	doc := NewDocument("cv_1", "p_1")
	doc.Nodes["n1"] = Node{ID: "n1", Type: NodeTypePrompt}
	_, err := applySetSpec(doc, &SetSpecPayload{ID: "n1", Meta: map[string]any{"k": nil}})
	if err == nil {
		t.Fatal("null 值应当被拒绝：显式删除走 metaUnset（与 spec 的 unset 同一条纪律）")
	}
}

func TestApplySetMetaEmptyObjectDoesNotClear(t *testing.T) {
	// 传空对象**不表示清空**：隐式清空是「一次误传就毁掉配置」的典型形态。
	doc := NewDocument("cv_1", "p_1")
	doc.Nodes["n1"] = Node{ID: "n1", Type: NodeTypePrompt, Meta: map[string]any{"keep": 1}}
	_, err := applySetSpec(doc, &SetSpecPayload{ID: "n1", Meta: map[string]any{}})
	if err == nil {
		t.Fatal("空 meta 且无其它改动应当被拒绝（没有可执行的动作）")
	}
	if doc.Nodes["n1"].Meta["keep"] != 1 {
		t.Fatal("被拒绝的调用不应改动 meta")
	}
}

func TestApplySetMetaUnsetRemovesKey(t *testing.T) {
	doc := NewDocument("cv_1", "p_1")
	doc.Nodes["n1"] = Node{ID: "n1", Type: NodeTypePrompt, Meta: map[string]any{"a": 1, "b": 2}}
	if _, err := applySetSpec(doc, &SetSpecPayload{ID: "n1", MetaUnset: []string{"a"}}); err != nil {
		t.Fatalf("显式删除应当成功: %v", err)
	}
	if _, ok := doc.Nodes["n1"].Meta["a"]; ok {
		t.Fatal("a 未被删除")
	}
	if doc.Nodes["n1"].Meta["b"] != 2 {
		t.Fatal("b 不该被影响")
	}
}

func TestApplySetMetaKeyLimit(t *testing.T) {
	doc := NewDocument("cv_1", "p_1")
	doc.Nodes["n1"] = Node{ID: "n1", Type: NodeTypePrompt}
	meta := map[string]any{}
	for i := 0; i < MaxMetaKeys+5; i++ {
		meta["k"+itoa(i)] = i
	}
	if _, err := applySetSpec(doc, &SetSpecPayload{ID: "n1", Meta: meta}); err == nil {
		t.Fatal("超过键数上限应当被拒绝（无上限的 meta 会变成隐形数据库）")
	}
}
