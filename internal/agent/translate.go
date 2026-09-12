package agent

import (
	"encoding/json"
	"fmt"

	"github.com/context-flow/ic/internal/graph"
)

// 上游工具调用 → 本仓 op 的翻译层。
//
// 这一层刻意做成**纯函数**（输入是参数、输出是 op 列表），因为它要能被穷举测试。
// 直接在执行函数里边校验边拼 op，会让「哪些参数组合会失败」无法被枚举，
// 而用户碰到的正是这些组合。
//
// 三条纪律：
//
//  1. 所有 op 都走标准 kind（add_node / set_spec / ...），不新增 op 类型。
//     新增 op 类型意味着校验、rebase、重放、契约四处都要改。
//  2. ID 由调用方（Service）注入，翻译层不自造 ID ——
//     否则同一份输入两次翻译会得到不同结果，重放就不成立了。
//  3. 参数校验在这里做完。翻译失败的错误必须能指出**哪个字段**，
//     因为下游的 op 校验只会说「spec 不合法」。

// idGen 是翻译层需要的 ID 生成能力（注入以便测试可确定）。
type idGen func(prefix string) string

// translateResult 是翻译产物。
type translateResult struct {
	ops []json.RawMessage
	// result 是给模型看的返回值（例如新建节点 ID）。
	result map[string]any
}

// translateCreateNode 翻译 canvas_create_node。
//
// 上游允许带任意 metadata；本仓的 spec 是**判别联合 + 未知字段拒绝**。
// 因此 metadata 不能直接塞进 spec（会被 ValidateSpec 拒绝），
// 而是落进 Node.Meta（一个显式的、不参与执行的扩展位）。
// 这个区分很重要：metadata 里的内容在重写后不会被执行引擎读取，
// 如果它看起来「生效了」，用户会以为自定义字段能影响生成。
func translateCreateNode(args json.RawMessage, newID idGen) (translateResult, error) {
	var in struct {
		NodeType string         `json:"nodeType"`
		Title    string         `json:"title"`
		X        *float64       `json:"x"`
		Y        *float64       `json:"y"`
		Width    *float64       `json:"width"`
		Height   *float64       `json:"height"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	t, ok := kindFromNodeType(in.NodeType)
	if !ok {
		return translateResult{}, fmt.Errorf("nodeType 不支持 %q，可用 text/image/config/video/audio/group", in.NodeType)
	}
	schema, ok := graph.BuiltinSchema(t)
	if !ok {
		return translateResult{}, fmt.Errorf("节点类型 %s 没有可用 schema", t)
	}
	w, h := schema.DefaultSize[0], schema.DefaultSize[1]
	if in.Width != nil {
		w = *in.Width
	}
	if in.Height != nil {
		h = *in.Height
	}
	// 尺寸必须落在 limits 内，且不能是 0/负数：0 宽高会让节点在画布上
	// 变成一个不可点击的点，用户以为节点没创建成功。
	if err := validateSize(w, h); err != nil {
		return translateResult{}, err
	}
	title := in.Title
	if title == "" {
		title = schema.DefaultTitle
	}
	spec := defaultSpecFor(t)
	node := map[string]any{
		"id":    newID(nodeIDPrefix(t)),
		"type":  string(t),
		"title": title,
		"rect":  map[string]any{"x": deref(in.X), "y": deref(in.Y), "w": w, "h": h},
		"spec":  spec,
	}
	if len(in.Metadata) > 0 {
		// metadata 落进 Node.Meta（显式扩展位，不参与执行）。
		// **不能**塞进 spec：spec 是判别联合且拒绝未知字段，
		// 塞进去会被 ValidateSpec 拒绝，而用户看到的是「节点创建失败」。
		node["meta"] = in.Metadata
	}
	op, _ := json.Marshal(map[string]any{"kind": "add_node", "node": node})
	return translateResult{
		ops:    []json.RawMessage{op},
		result: map[string]any{"nodeId": node["id"], "type": string(t)},
	}, nil
}

// translateCreateTextNodes 翻译 canvas_create_text_nodes（批量 + 自动排布）。
//
// 排布必须由翻译层算，而不是让每个节点用相同坐标：上游的 items 里
// x/y 是可选的，全部省略时节点会叠在一起 —— 用户看到的是「只创建了一个」。
func translateCreateTextNodes(args json.RawMessage, newID idGen) (translateResult, error) {
	var in struct {
		Items []struct {
			Text   string   `json:"text"`
			Title  string   `json:"title"`
			X      *float64 `json:"x"`
			Y      *float64 `json:"y"`
			Width  *float64 `json:"width"`
			Height *float64 `json:"height"`
		} `json:"items"`
		X         *float64 `json:"x"`
		Y         *float64 `json:"y"`
		Gap       *float64 `json:"gap"`
		Direction string   `json:"direction"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if len(in.Items) == 0 {
		return translateResult{}, fmt.Errorf("items 不能为空")
	}
	if len(in.Items) > 20 {
		return translateResult{}, fmt.Errorf("items 最多 20 条，实际 %d", len(in.Items))
	}
	dir := in.Direction
	if dir == "" {
		dir = "column"
	}
	if dir != "row" && dir != "column" {
		return translateResult{}, fmt.Errorf("direction 只能是 row 或 column")
	}
	gap := 40.0
	if in.Gap != nil {
		gap = *in.Gap
	}
	baseX, baseY := deref(in.X), deref(in.Y)
	schema, _ := graph.BuiltinSchema(graph.NodeTypePrompt)
	w, h := schema.DefaultSize[0], schema.DefaultSize[1]

	ops := make([]json.RawMessage, 0, len(in.Items))
	ids := make([]string, 0, len(in.Items))
	for i, item := range in.Items {
		if item.Text == "" {
			return translateResult{}, fmt.Errorf("items[%d].text 不能为空", i)
		}
		x, y := baseX, baseY
		if dir == "row" {
			x += float64(i) * (w + gap)
		} else {
			y += float64(i) * (h + gap)
		}
		if item.X != nil {
			x = *item.X
		}
		if item.Y != nil {
			y = *item.Y
		}
		iw, ih := w, h
		if item.Width != nil {
			iw = *item.Width
		}
		if item.Height != nil {
			ih = *item.Height
		}
		if err := validateSize(iw, ih); err != nil {
			return translateResult{}, fmt.Errorf("items[%d]: %w", i, err)
		}
		title := item.Title
		if title == "" {
			title = schema.DefaultTitle
		}
		id := newID("n")
		ids = append(ids, id)
		op, _ := json.Marshal(map[string]any{"kind": "add_node", "node": map[string]any{
			"id": id, "type": string(graph.NodeTypePrompt), "title": title,
			"rect": map[string]any{"x": x, "y": y, "w": iw, "h": ih},
			"spec": map[string]any{"text": item.Text},
		}})
		ops = append(ops, op)
	}
	return translateResult{ops: ops, result: map[string]any{"nodeIds": ids}}, nil
}

// translateUpdateNode 翻译 canvas_update_node。
//
// 上游的 patch 是一个自由对象（直接改节点字段）。本仓只允许
// **三类明确的修改**：标题 / spec / meta。其余（id、type、rect）
// 分别有各自的语义化工具（move/resize），混在一起会让「改了什么」
// 无法从 op 日志里看出来。
func translateUpdateNode(args json.RawMessage) (translateResult, error) {
	var in struct {
		ID       string         `json:"id"`
		Title    *string        `json:"title"`
		Patch    map[string]any `json:"patch"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if in.ID == "" {
		return translateResult{}, fmt.Errorf("id 必填")
	}
	var ops []json.RawMessage
	if in.Title != nil {
		op, _ := json.Marshal(map[string]any{"kind": "set_title", "id": in.ID, "title": *in.Title})
		ops = append(ops, op)
	}
	if len(in.Patch) > 0 {
		op, _ := json.Marshal(map[string]any{"kind": "set_spec", "id": in.ID, "patch": in.Patch})
		ops = append(ops, op)
	}
	if len(in.Metadata) > 0 {
		// Node.Meta 不在 op 集合里单独设（那会新增一个 op 类型，
		// 而 op 类型是契约面，新增要动校验/rebase/重放/契约四处）。
		// 这里的做法是**走 set_spec 的 meta 通道**——见 applySetSpec 的 meta 支持。
		op, _ := json.Marshal(map[string]any{"kind": "set_spec", "id": in.ID, "meta": in.Metadata})
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return translateResult{}, fmt.Errorf("至少要提供 title / patch / metadata 之一")
	}
	return translateResult{ops: ops, result: map[string]any{"nodeId": in.ID}}, nil
}

// translateUpdateNodeText 翻译 canvas_update_node_text。
func translateUpdateNodeText(args json.RawMessage) (translateResult, error) {
	var in struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Title string `json:"title"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if in.ID == "" {
		return translateResult{}, fmt.Errorf("id 必填")
	}
	if in.Text == "" {
		return translateResult{}, fmt.Errorf("text 必填（清空文本请显式传空串并说明意图）")
	}
	spec, _ := json.Marshal(map[string]any{"text": in.Text})
	ops := []json.RawMessage{mustJSONOp(map[string]any{
		"kind": "set_spec", "id": in.ID, "patch": json.RawMessage(spec),
	})}
	if in.Title != "" {
		ops = append(ops, mustJSONOp(map[string]any{"kind": "set_title", "id": in.ID, "title": in.Title}))
	}
	return translateResult{ops: ops, result: map[string]any{"nodeId": in.ID}}, nil
}

// translateMoveNodes 翻译 canvas_move_nodes。
//
// 支持绝对坐标与 dx/dy 两种写法。**两者不能同时给**：
// 同时给会让人无法判断哪个生效，而「看起来生效了」的错误最难查。
func translateMoveNodes(args json.RawMessage) (translateResult, error) {
	var in struct {
		Items []struct {
			ID string   `json:"id"`
			X  *float64 `json:"x"`
			Y  *float64 `json:"y"`
			DX *float64 `json:"dx"`
			DY *float64 `json:"dy"`
		} `json:"items"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if len(in.Items) == 0 {
		return translateResult{}, fmt.Errorf("items 不能为空")
	}
	if len(in.Items) > 200 {
		return translateResult{}, fmt.Errorf("items 最多 200 条，实际 %d", len(in.Items))
	}
	ops := make([]json.RawMessage, 0, len(in.Items))
	for i, it := range in.Items {
		if it.ID == "" {
			return translateResult{}, fmt.Errorf("items[%d].id 不能为空", i)
		}
		hasAbs := it.X != nil || it.Y != nil
		hasRel := it.DX != nil || it.DY != nil
		if hasAbs && hasRel {
			return translateResult{}, fmt.Errorf("items[%d] 不能同时使用坐标与偏移（x/y 与 dx/dy 二选一）", i)
		}
		// 相对位移用 `delta: true` 表达（op 的既有语义），
		// 而不是把 dx/dy 当成 x/y —— 后者会让「拖了 10px」变成
		// 「跳到坐标 (10,10)」，是静默的语义错误。
		if hasRel {
			ops = append(ops, mustJSONOp(map[string]any{
				"kind": "move_node", "id": it.ID, "delta": true,
				"x": deref(it.DX), "y": deref(it.DY),
			}))
			continue
		}
		ops = append(ops, mustJSONOp(map[string]any{
			"kind": "move_node", "id": it.ID, "x": deref(it.X), "y": deref(it.Y),
		}))
	}
	return translateResult{ops: ops}, nil
}

// translateResizeNode 翻译 canvas_resize_node。
func translateResizeNode(args json.RawMessage) (translateResult, error) {
	var in struct {
		ID     string  `json:"id"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if in.ID == "" {
		return translateResult{}, fmt.Errorf("id 必填")
	}
	if err := validateSize(in.Width, in.Height); err != nil {
		return translateResult{}, err
	}
	return translateResult{ops: []json.RawMessage{mustJSONOp(map[string]any{
		"kind": "resize_node", "id": in.ID, "w": in.Width, "h": in.Height,
	})}}, nil
}

// translateDeleteNodes 翻译 canvas_delete_nodes。
func translateDeleteNodes(args json.RawMessage) (translateResult, error) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if len(in.IDs) == 0 {
		return translateResult{}, fmt.Errorf("ids 不能为空")
	}
	for i, id := range in.IDs {
		if id == "" {
			return translateResult{}, fmt.Errorf("ids[%d] 是空字符串", i)
		}
	}
	// 一个节点一个 op：`remove_node` 的 payload 只有 id 与 cascade。
	// 想当然地传 `ids` 会被 op 解析拒绝（未知字段），
	// 而错误信息会指向「op 不合法」——所以这里逐个展开。
	ops := make([]json.RawMessage, 0, len(in.IDs))
	for _, id := range in.IDs {
		ops = append(ops, mustJSONOp(map[string]any{"kind": "remove_node", "id": id, "cascade": true}))
	}
	return translateResult{ops: ops, result: map[string]any{"removed": len(in.IDs)}}, nil
}

// translateConnectNodes 翻译 canvas_connect_nodes。
//
// **端口由服务端解析**（按类型匹配），不让客户端指定 portId：
// 端口是内部实现细节，上游的工具签名里也没有它。
// 若让客户端指定，就会出现「上游版本升级后端口改名 → 连线静默失败」。
// 解析发生在执行期（那里能读到文档），因此这里只做形状校验。
func translateConnectNodes(args json.RawMessage) (translateResult, error) {
	var in struct {
		Connections []struct {
			FromNodeID string `json:"fromNodeId"`
			ToNodeID   string `json:"toNodeId"`
		} `json:"connections"`
	}
	if err := strictDecode(args, &in); err != nil {
		return translateResult{}, err
	}
	if len(in.Connections) == 0 {
		return translateResult{}, fmt.Errorf("connections 不能为空")
	}
	for i, c := range in.Connections {
		if c.FromNodeID == "" || c.ToNodeID == "" {
			return translateResult{}, fmt.Errorf("connections[%d] 的 fromNodeId/toNodeId 都必填", i)
		}
		if c.FromNodeID == c.ToNodeID {
			return translateResult{}, fmt.Errorf("connections[%d] 是自连（from 与 to 相同），已拒绝", i)
		}
	}
	// 端口解析留给执行期，因此这里返回一个「待解析」的描述。
	// 用 result 承载而不是 op：op 必须能独立重放，而「解析端口」需要文档。
	return translateResult{result: map[string]any{"connections": in.Connections}}, nil
}

// ---------------------------------------------------------------- 小工具

func mustJSONOp(v map[string]any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// nodeIDPrefix 按节点类型给出可读的 ID 前缀（日志与画布上都能看出类型）。
func nodeIDPrefix(t graph.NodeTypeID) string {
	switch t {
	case graph.NodeTypeGroup:
		return "grp"
	case graph.NodeTypeGeneration:
		return "gen"
	}
	return "n"
}

// defaultSpecFor 给出新节点的最小合法 spec。
//
// 必须返回**通过 ValidateSpec 的最小值**：如果这里给了一个不合法 spec，
// 失败会发生在 op 应用阶段（错误信息是「spec 不合法」），
// 而不是在创建阶段（错误信息能指出缺哪个字段）。
func defaultSpecFor(t graph.NodeTypeID) map[string]any {
	switch t {
	case graph.NodeTypePrompt:
		return map[string]any{"text": ""}
	case graph.NodeTypeGeneration:
		return map[string]any{"capability": "image.generate", "outputCount": 1}
	case graph.NodeTypeGroup:
		return map[string]any{"collapsed": false}
	}
	return map[string]any{}
}

func validateSize(w, h float64) error {
	if w < graph.SizeMin || w > graph.SizeMax {
		return fmt.Errorf("宽度必须在 %g–%g 之间，实际 %g", graph.SizeMin, graph.SizeMax, w)
	}
	if h < graph.SizeMin || h > graph.SizeMax {
		return fmt.Errorf("高度必须在 %g–%g 之间，实际 %g", graph.SizeMin, graph.SizeMax, h)
	}
	return nil
}
