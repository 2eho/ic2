package legacy

import (
	"encoding/json"
	"testing"

	"github.com/context-flow/ic/internal/graph"
)

func str(s string) *string   { return &s }
func num(f float64) *float64 { return &f }
func i64(n int64) *int64     { return &n }
func iptr(n int) *int        { return &n }
func bl(b bool) *bool        { return &b }

// legacy-mapping：遍历旧 metadata 的每个 key，断言存在映射或显式 dropped。
// 这条用例的意义：漏一个字段就红（docs/design/10 §2.13 的验收要求）。
func TestLegacyMappingCoversEveryKey(t *testing.T) {
	keys := KnownMetadataKeys()
	if len(keys) < 30 {
		t.Fatalf("旧 metadata 字段数异常少: %d", len(keys))
	}
	for _, k := range keys {
		_, mapped := MappingTable[k]
		_, dropped := DroppedMetadataKeys[k]
		if !mapped && !dropped {
			t.Fatalf("字段 %q 没有映射也没有显式标注 dropped", k)
		}
	}
	// 反向：映射表里不应有旧结构里不存在的字段
	for k := range MappingTable {
		found := false
		for _, real := range keys {
			if real == k {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("映射表里的 %q 在旧结构中不存在（可能是笔误）", k)
		}
	}
}

func TestMigrateBasicProject(t *testing.T) {
	p := LegacyProject{
		ID:   "old_1",
		Name: "旧画布",
		Nodes: []LegacyNode{
			{
				ID: "n1", Type: "text", Title: "提示词",
				Position: struct {
					X float64 `json:"x"`
					Y float64 `json:"y"`
				}{X: 10, Y: 20},
				Width: 300, Height: 200,
				Metadata: LegacyMetadata{Content: str("一只猫")},
			},
			{
				ID: "n2", Type: "image",
				Position: struct {
					X float64 `json:"x"`
					Y float64 `json:"y"`
				}{X: 400, Y: 20},
				Metadata: LegacyMetadata{
					StorageKey: str("image:abc"), MimeType: str("image/png"),
					NaturalWidth: num(1024), NaturalHeight: num(768), Bytes: i64(2048),
				},
			},
		},
		Connections: []LegacyConnection{{ID: "c1", FromNodeID: "n1", ToNodeID: "n2"}},
	}

	resolve := func(key, _ string) (string, bool) {
		if key == "image:abc" {
			return "as_1", true
		}
		return "", false
	}
	doc, res, err := Migrate(p, resolve, "cv_new")
	if err != nil {
		t.Fatal(err)
	}
	if res.NodeCount != 2 {
		t.Fatalf("节点数异常: %+v", res)
	}
	// text → image 在旧项目里没有合法端口（image 的输入是 image），
	// 因此必须被跳过并给出警告，而不是伪造一条类型不匹配的连线。
	if res.EdgeCount != 0 {
		t.Fatalf("类型不匹配的连线不应被迁移: %+v", res)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("跳过连线必须给出警告")
	}
	if doc.Nodes["n1"].Spec["text"] != "一只猫" {
		t.Fatalf("文本未迁移: %+v", doc.Nodes["n1"].Spec)
	}
	if doc.Nodes["n2"].Spec["assetId"] != "as_1" {
		t.Fatalf("资产未迁移: %+v", doc.Nodes["n2"].Spec)
	}
	if doc.Nodes["n1"].Rect.X != 10 || doc.Nodes["n1"].Rect.Y != 20 {
		t.Fatalf("位置未迁移: %+v", doc.Nodes["n1"].Rect)
	}
	if res.ContentHash == "" {
		t.Fatal("缺少内容 hash（导入幂等依赖它）")
	}
}

// 内容 hash 必须稳定（顺序无关），否则重复导入会建重复画布。
func TestContentHashStableRegardlessOfOrder(t *testing.T) {
	a := LegacyProject{ID: "p", Nodes: []LegacyNode{
		{ID: "n1", Type: "text"}, {ID: "n2", Type: "image"},
	}}
	b := LegacyProject{ID: "p", Nodes: []LegacyNode{
		{ID: "n2", Type: "image"}, {ID: "n1", Type: "text"},
	}}
	if hashProject(a) != hashProject(b) {
		t.Fatal("hash 应对顺序不敏感（否则导入不幂等）")
	}
	c := LegacyProject{ID: "p", Nodes: []LegacyNode{{ID: "n1", Type: "text"}}}
	if hashProject(a) == hashProject(c) {
		t.Fatal("不同内容的 hash 必须不同")
	}
}

// ATK 语义：单个 Blob 丢失不阻断整体导入。
func TestMissingAssetDoesNotAbort(t *testing.T) {
	p := LegacyProject{
		ID: "p", Name: "x",
		Nodes: []LegacyNode{
			{ID: "n1", Type: "text", Metadata: LegacyMetadata{Content: str("hi")}},
			{ID: "n2", Type: "image", Metadata: LegacyMetadata{StorageKey: str("image:missing")}},
		},
	}
	doc, res, err := Migrate(p, func(string, string) (string, bool) { return "", false }, "cv")
	if err != nil {
		t.Fatal("单个资源缺失不应报错")
	}
	if res.NodeCount != 2 {
		t.Fatalf("节点数应为 2，实际 %d（缺资源不应丢节点）", res.NodeCount)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("缺失必须产生警告")
	}
	if _, ok := doc.Nodes["n2"]; !ok {
		t.Fatal("媒体节点应保留（只是没有 assetId）")
	}
}

// status=loading → failed + interrupted（不能永远转圈）。
func TestLoadingBecomesInterrupted(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "text",
		Metadata: LegacyMetadata{Content: str("x"), Status: str("loading")},
	}}}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	n := doc.Nodes["n1"]
	if n.State != graph.NodeFailed {
		t.Fatalf("loading 应映射为 failed，实际 %s", n.State)
	}
	if n.Error == nil || n.Error.Code == "" {
		t.Fatal("必须带错误码，便于前端显示「已中断」")
	}
	if n.Spec["importedState"] != "interrupted" {
		t.Fatalf("应记录 importedState: %+v", n.Spec)
	}
}

func TestMultiImageResultsAndPrimary(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "image",
		Metadata: LegacyMetadata{
			Images: []LegacyImage{
				{ID: "i1", Status: "success", Content: "blob:1"},
				{ID: "i2", Status: "success", Content: "blob:2"},
				{ID: "i3", Status: "error", Content: "", ErrorDetails: str("failed")},
			},
			PrimaryImageID: str("i2"),
		},
	}}}
	resolve := func(_ string, inline string) (string, bool) {
		switch inline {
		case "blob:1":
			return "as_1", true
		case "blob:2":
			return "as_2", true
		}
		return "", false
	}
	doc, _, err := Migrate(p, resolve, "cv")
	if err != nil {
		t.Fatal(err)
	}
	r := doc.Nodes["n1"].Result
	if r == nil || len(r.Variants) != 3 {
		t.Fatalf("多图结果未迁移: %+v", r)
	}
	if r.Variants[0].AssetID != "as_1" || r.Variants[1].AssetID != "as_2" {
		t.Fatalf("素材引用错误: %+v", r.Variants)
	}
	if r.Primary != 1 {
		t.Fatalf("主图索引应为 1，实际 %d", r.Primary)
	}
	if r.Variants[2].Status != graph.NodeFailed || r.Variants[2].Error == "" {
		t.Fatalf("失败项的 status/error 未迁移: %+v", r.Variants[2])
	}
}

func TestMultiTextResultsAndPrimary(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "text",
		Metadata: LegacyMetadata{
			Content:       str("原始"),
			Texts:         []LegacyText{{ID: "t1", Status: "success", Content: "A"}, {ID: "t2", Status: "success", Content: "B"}},
			PrimaryTextID: str("t1"),
		},
	}}}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	r := doc.Nodes["n1"].Result
	if r == nil || len(r.Variants) != 2 || r.Primary != 0 {
		t.Fatalf("多文本未迁移: %+v", r)
	}
	if doc.Nodes["n1"].Spec["text"] != "原始" {
		t.Fatal("主文本内容不应被结果覆盖")
	}
}

func TestGroupIdBecomesParentId(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{
		{ID: "g1", Type: "group"},
		{ID: "n1", Type: "text", Metadata: LegacyMetadata{Content: str("x"), GroupID: str("g1")}},
	}}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Nodes["n1"].ParentID != "g1" {
		t.Fatalf("groupId 未映射到 ParentID: %+v", doc.Nodes["n1"])
	}
}

func TestMissingGroupProducesWarningNotError(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{
		{ID: "n1", Type: "text", Metadata: LegacyMetadata{Content: str("x"), GroupID: str("gone")}},
	}}
	doc, res, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Nodes["n1"].ParentID != "" {
		t.Fatal("不存在的分组不应被设置")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("应产生警告")
	}
}

func TestReferencesBecomeEdges(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{
		{ID: "n1", Type: "image", Metadata: LegacyMetadata{StorageKey: str("image:a")}},
		{ID: "n2", Type: "config", Metadata: LegacyMetadata{
			GenerationMode: str("image"), GenerationType: str("edit"),
			References: []string{"image:a"},
		}},
	}}
	resolve := func(k, _ string) (string, bool) {
		if k == "image:a" {
			return "as_a", true
		}
		return "", false
	}
	doc, _, err := Migrate(p, resolve, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Edges) != 1 {
		t.Fatalf("references 应还原为连线，实际 %d 条", len(doc.Edges))
	}
	var edge graph.Edge
	for _, e := range doc.Edges {
		edge = e
	}
	if edge.From.NodeID != "n1" || edge.To.NodeID != "n2" || edge.Kind != graph.KindImage {
		t.Fatalf("连线内容错误: %+v", edge)
	}
	if doc.Nodes["n2"].Spec["capability"] != "image.edit" {
		t.Fatalf("generationType=edit 应映射为 image.edit: %+v", doc.Nodes["n2"].Spec)
	}
}

func TestGenerationParamsMigrate(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "config",
		Metadata: LegacyMetadata{
			GenerationMode: str("video"), Model: str("sora-2"),
			Seconds: str("8"), Vquality: str("1080p"), Watermark: str("true"),
			Count: iptr(4), ReasoningEffort: str("high"),
			VideoTaskID: str("task_1"), VideoTaskProvider: str("openai"),
		},
	}}}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	s := doc.Nodes["n1"].Spec
	if s["capability"] != "video.generate" || s["model"] != "sora-2" || s["outputCount"] != 4 {
		t.Fatalf("生成参数未迁移: %+v", s)
	}
	params, _ := s["params"].(map[string]any)
	if params["seconds"] != "8" || params["vquality"] != "1080p" || params["watermark"] != "true" {
		t.Fatalf("params 未迁移: %+v", params)
	}
	if s["legacyTaskId"] != "task_1" {
		t.Fatal("视频任务信息应保留以便续查")
	}
}

func TestCountClampedToRange(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "config",
		Metadata: LegacyMetadata{GenerationMode: str("image"), Count: iptr(999)},
	}}}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Nodes["n1"].Spec["outputCount"] != 15 {
		t.Fatalf("张数应被夹到 15: %+v", doc.Nodes["n1"].Spec)
	}
}

func TestUnknownNodeTypeIsSkippedWithWarning(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{
		{ID: "n1", Type: "text", Metadata: LegacyMetadata{Content: str("ok")}},
		{ID: "n2", Type: "totally-unknown"},
	}}
	doc, res, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("未知类型应跳过，实际节点数 %d", len(doc.Nodes))
	}
	if len(res.SkippedNodes) != 1 || res.SkippedNodes[0] != "n2" {
		t.Fatalf("应记录被跳过的节点: %+v", res.SkippedNodes)
	}
}

func TestPluginNodeDegradedToGeneration(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "com.example.demo:viewer",
		Metadata: LegacyMetadata{Content: str("x"), Interactive: bl(true)},
	}}}
	doc, res, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	n, ok := doc.Nodes["n1"]
	if !ok {
		t.Fatal("插件节点应保留（降级）")
	}
	if n.Type != graph.NodeTypeGeneration {
		t.Fatalf("旧插件节点应降级为 generation: %s", n.Type)
	}
	if n.Spec["interactive"] != true {
		t.Fatalf("interactive 未迁移: %+v", n.Spec)
	}
	_ = res
}

// 旧数据里的异常几何必须被修正而不是丢弃。
func TestInvalidGeometryIsCorrected(t *testing.T) {
	p := LegacyProject{ID: "p", Nodes: []LegacyNode{{
		ID: "n1", Type: "text",
		Position: struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}{X: 1e15, Y: 0},
		Width: 0, Height: -5,
		Metadata: LegacyMetadata{Content: str("x")},
	}}}
	doc, res, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	n, ok := doc.Nodes["n1"]
	if !ok {
		t.Fatal("几何异常不应丢节点")
	}
	if n.Rect.W < 16 || n.Rect.H < 16 {
		t.Fatalf("尺寸应被修正: %+v", n.Rect)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("应产生修正警告")
	}
	// 修正后必须能通过服务端校验（这是"修正"有效性的证明）
	ops := []json.RawMessage{mustJSON(map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "probe", "type": "prompt",
			"rect": map[string]any{"x": n.Rect.X, "y": n.Rect.Y, "w": n.Rect.W, "h": n.Rect.H},
			"spec": map[string]any{"text": "x"},
		},
	})}
	if _, _, err := graph.Apply(graph.NewDocument("c", "p"), ops, "m", graph.Now()); err != nil {
		t.Fatalf("修正后的几何仍被服务端拒绝: %v", err)
	}
}

func TestViewportAndSettingsMigrate(t *testing.T) {
	vp := &struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		K float64 `json:"k"`
	}{X: 100, Y: 200, K: 1.5}
	p := LegacyProject{ID: "p", Viewport: vp, BackgroundMode: str("lines"), ShowImageInfo: bl(false)}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Viewport.X != 100 || doc.Viewport.K != 1.5 {
		t.Fatalf("视口未迁移: %+v", doc.Viewport)
	}
	if doc.Settings.Background != "lines" || doc.Settings.ImageInfo {
		t.Fatalf("画布设置未迁移: %+v", doc.Settings)
	}
}

func TestInvalidViewportFallsBack(t *testing.T) {
	vp := &struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		K float64 `json:"k"`
	}{X: 0, Y: 0, K: 1e9}
	p := LegacyProject{ID: "p", Viewport: vp}
	doc, res, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Viewport.K != 1 {
		t.Fatalf("非法视口应回退默认: %+v", doc.Viewport)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("应产生警告（不能静默回退）")
	}
}

func TestMissingProjectIDRejected(t *testing.T) {
	if _, _, err := Migrate(LegacyProject{}, nil, "cv"); err == nil {
		t.Fatal("缺项目 ID 应被拒绝")
	}
}

// 迁移结果必须能通过服务端校验后落库（端到端有效性证明）。
func TestMigratedDocumentPassesServerValidation(t *testing.T) {
	p := LegacyProject{
		ID: "p", Name: "x",
		Nodes: []LegacyNode{
			{ID: "n1", Type: "text", Metadata: LegacyMetadata{Content: str("一只猫")}},
			{ID: "n2", Type: "config", Metadata: LegacyMetadata{GenerationMode: str("image")}},
		},
		Connections: []LegacyConnection{{ID: "c1", FromNodeID: "n1", ToNodeID: "n2"}},
	}
	doc, _, err := Migrate(p, nil, "cv")
	if err != nil {
		t.Fatal(err)
	}
	// 把文档展开为 op 并逐条校验
	ops := []json.RawMessage{}
	for _, id := range doc.NodeIDs() {
		n := doc.Nodes[id]
		ops = append(ops, mustJSON(map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": n.ID, "type": n.Type, "title": n.Title,
				"rect": map[string]any{"x": n.Rect.X, "y": n.Rect.Y, "w": n.Rect.W, "h": n.Rect.H},
				"spec": n.Spec,
			},
		}))
	}
	for _, id := range doc.EdgeIDs() {
		e := doc.Edges[id]
		ops = append(ops, mustJSON(map[string]any{
			"kind": "add_edge",
			"edge": map[string]any{
				"id":   e.ID,
				"from": map[string]any{"nodeId": e.From.NodeID, "portId": e.From.PortID},
				"to":   map[string]any{"nodeId": e.To.NodeID, "portId": e.To.PortID},
				"kind": string(e.Kind),
			},
		}))
	}
	base := graph.NewDocument("cv", "pj")
	result, applied, err := graph.Apply(base, ops, "import", graph.Now())
	if err != nil {
		t.Fatalf("迁移结果被服务端拒绝: %v", err)
	}
	if result.Applied != len(ops) || len(applied.Nodes) != len(doc.Nodes) || len(applied.Edges) != len(doc.Edges) {
		t.Fatalf("应用结果与迁移结果不一致: applied=%d nodes=%d/%d edges=%d/%d",
			result.Applied, len(applied.Nodes), len(doc.Nodes), len(applied.Edges), len(doc.Edges))
	}
}
