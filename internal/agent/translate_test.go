package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// 翻译层是纯函数，因此可以穷举。这一组用例的价值在于：
// 它们全部是**上游客户端的真实入参形状**，而错误都被在翻译期拦住了 ——
// 若放进 op 层，用户看到的会是「spec 不合法」这类与入参对不上的错误。
//
// 前三条覆盖三个最容易出的错：
//   1. metadata 塞进 spec（会被 ValidateSpec 拒绝）
//    2. 坐标与偏移同时给（无法判断哪个生效）
//    3. 相对位移被当成绝对坐标（拖 10px 变成跳到 (10,10)）

func seqIDs() idGen {
	n := 0
	return func(prefix string) string {
		n++
		return prefix + "_" + string(rune('a'+n-1))
	}
}

func TestTranslateCreateNodePutsMetadataInMetaNotSpec(t *testing.T) {
	args := json.RawMessage(`{"nodeType":"text","title":"T","metadata":{"myKey":1}}`)
	res, err := translateCreateNode(args, seqIDs())
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	var op struct {
		Node map[string]any `json:"node"`
	}
	if err := json.Unmarshal(res.ops[0], &op); err != nil {
		t.Fatalf("op 不是合法 JSON: %v", err)
	}
	if _, ok := op.Node["meta"]; !ok {
		t.Fatal("metadata 应当落进 meta（spec 是判别联合，塞进去会被 ValidateSpec 拒绝）")
	}
	spec, _ := op.Node["spec"].(map[string]any)
	if _, leaked := spec["myKey"]; leaked {
		t.Fatal("metadata 不该泄漏进 spec")
	}
}

func TestTranslateCreateNodeRejectsUnknownType(t *testing.T) {
	_, err := translateCreateNode(json.RawMessage(`{"nodeType":"nope"}`), seqIDs())
	if err == nil || !strings.Contains(err.Error(), "nodeType") {
		t.Fatalf("未知类型应当报出字段名，实际 %v", err)
	}
}

func TestTranslateCreateNodeRejectsInvalidSize(t *testing.T) {
	for _, args := range []string{
		`{"nodeType":"text","width":0,"height":100}`,
		`{"nodeType":"text","width":100,"height":-5}`,
		`{"nodeType":"text","width":999999,"height":100}`,
	} {
		if _, err := translateCreateNode(json.RawMessage(args), seqIDs()); err == nil {
			t.Fatalf("非法尺寸应当被拒绝: %s（0 宽高会让节点变成一个不可点击的点）", args)
		}
	}
}

func TestTranslateCreateTextNodesLaysOutWithoutOverlap(t *testing.T) {
	args := json.RawMessage(`{"items":[{"text":"a"},{"text":"b"},{"text":"c"}],"x":100,"y":50}`)
	res, err := translateCreateTextNodes(args, seqIDs())
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	if len(res.ops) != 3 {
		t.Fatalf("应当产出 3 个 op，实际 %d", len(res.ops))
	}
	seen := map[[2]float64]bool{}
	for _, raw := range res.ops {
		var op struct {
			Node map[string]any `json:"node"`
		}
		_ = json.Unmarshal(raw, &op)
		rect := op.Node["rect"].(map[string]any)
		key := [2]float64{rect["x"].(float64), rect["y"].(float64)}
		if seen[key] {
			t.Fatalf("节点坐标重叠 (%v)：用户会以为「只创建了一个」", key)
		}
		seen[key] = true
	}
}

func TestTranslateCreateTextNodesRespectsDirection(t *testing.T) {
	row, err := translateCreateTextNodes(
		json.RawMessage(`{"items":[{"text":"a"},{"text":"b"}],"direction":"row"}`), seqIDs())
	if err != nil {
		t.Fatalf("row 布局失败: %v", err)
	}
	col, err := translateCreateTextNodes(
		json.RawMessage(`{"items":[{"text":"a"},{"text":"b"}],"direction":"column"}`), seqIDs())
	if err != nil {
		t.Fatalf("column 布局失败: %v", err)
	}
	xs := func(res translateResult) []float64 {
		var out []float64
		for _, raw := range res.ops {
			var op struct {
				Node map[string]any `json:"node"`
			}
			_ = json.Unmarshal(raw, &op)
			rect := op.Node["rect"].(map[string]any)
			out = append(out, rect["x"].(float64))
		}
		return out
	}
	if xs(row)[0] == xs(row)[1] {
		t.Fatal("row 布局下 x 应当不同")
	}
	if xs(col)[0] != xs(col)[1] {
		t.Fatal("column 布局下 x 应当相同")
	}
}

func TestTranslateCreateTextNodesRejectsEmptyItems(t *testing.T) {
	if _, err := translateCreateTextNodes(json.RawMessage(`{"items":[]}`), seqIDs()); err == nil {
		t.Fatal("空 items 应当被拒绝")
	}
	if _, err := translateCreateTextNodes(json.RawMessage(`{"items":[{"text":""}]}`), seqIDs()); err == nil {
		t.Fatal("空 text 应当被拒绝并指出下标")
	}
}

func TestTranslateMoveNodesRejectsMixedAbsoluteAndRelative(t *testing.T) {
	args := json.RawMessage(`{"items":[{"id":"n1","x":10,"dx":5}]}`)
	_, err := translateMoveNodes(args)
	if err == nil {
		t.Fatal("同时给坐标与偏移应当被拒绝（否则无法判断哪个生效）")
	}
	if !strings.Contains(err.Error(), "items[0]") {
		t.Fatalf("错误应当指出是第几项，实际 %v", err)
	}
}

func TestTranslateMoveNodesUsesDeltaFlagForRelative(t *testing.T) {
	res, err := translateMoveNodes(json.RawMessage(`{"items":[{"id":"n1","dx":5,"dy":-3}]}`))
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	var op map[string]any
	_ = json.Unmarshal(res.ops[0], &op)
	if op["delta"] != true {
		t.Fatal("相对位移必须带 delta:true，否则「拖 5px」会变成「跳到 (5,-3)」")
	}
	if op["x"] != float64(5) || op["y"] != float64(-3) {
		t.Fatalf("偏移量应当写入 x/y: %#v", op)
	}
}

func TestTranslateMoveNodesAbsoluteHasNoDelta(t *testing.T) {
	res, err := translateMoveNodes(json.RawMessage(`{"items":[{"id":"n1","x":10,"y":20}]}`))
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	var op map[string]any
	_ = json.Unmarshal(res.ops[0], &op)
	if _, has := op["delta"]; has {
		t.Fatal("绝对坐标不应带 delta")
	}
}

func TestTranslateUpdateNodeRequiresAtLeastOneChange(t *testing.T) {
	if _, err := translateUpdateNode(json.RawMessage(`{"id":"n1"}`)); err == nil {
		t.Fatal("没有任何修改时应当被拒绝，而不是产出一个空 op 列表")
	}
}

func TestTranslateUpdateNodeTextRequiresNonEmptyText(t *testing.T) {
	if _, err := translateUpdateNodeText(json.RawMessage(`{"id":"n1","text":""}`)); err == nil {
		t.Fatal("空文本应当被拒绝（清空是另一个语义，需要显式表达）")
	}
}

func TestTranslateResizeRejectsOutOfRange(t *testing.T) {
	if _, err := translateResizeNode(json.RawMessage(`{"id":"n1","width":100,"height":1}`)); err == nil {
		t.Fatal("超范围尺寸应当被拒绝")
	}
}

func TestTranslateDeleteNodesExpandsToPerNodeOps(t *testing.T) {
	res, err := translateDeleteNodes(json.RawMessage(`{"ids":["n1","n2"]}`))
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	if len(res.ops) != 2 {
		t.Fatalf("remove_node 的 payload 只有 id 与 cascade，必须逐个展开，实际 %d 个 op", len(res.ops))
	}
	var op map[string]any
	_ = json.Unmarshal(res.ops[0], &op)
	if op["kind"] != "remove_node" || op["cascade"] != true {
		t.Fatalf("op 形状错误: %#v", op)
	}
}

func TestTranslateConnectNodesRejectsSelfLoop(t *testing.T) {
	args := json.RawMessage(`{"connections":[{"fromNodeId":"n1","toNodeId":"n1"}]}`)
	if _, err := translateConnectNodes(args); err == nil {
		t.Fatal("自连应当被拒绝")
	}
}

func TestTranslateConnectNodesRejectsEmptyConnectionList(t *testing.T) {
	if _, err := translateConnectNodes(json.RawMessage(`{"connections":[]}`)); err == nil {
		t.Fatal("空 connections 应当被拒绝")
	}
}

func TestDefaultSpecForPassesValidation(t *testing.T) {
	// defaultSpecFor 必须返回**通过校验的最小 spec**：
	// 否则失败会推迟到 op 应用阶段，错误信息与创建入参对不上。
	for _, tool := range []string{"text", "image", "video", "audio", "config", "group"} {
		res, err := translateCreateNode(
			json.RawMessage(`{"nodeType":"`+tool+`"}`), seqIDs())
		if err != nil {
			t.Fatalf("%s 的最小 spec 翻译失败: %v", tool, err)
		}
		if len(res.ops) != 1 {
			t.Fatalf("%s 应当产出 1 个 op", tool)
		}
	}
}
