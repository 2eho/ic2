package exec

import (
	"strings"
	"testing"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

func newCompiler() *Compiler {
	return NewCompiler(func(provider.Capability, string, string) (provider.Credential, bool) {
		return provider.Credential{ID: "cred_1"}, true
	})
}

func addPrompt(doc *graph.CanvasDocument, id, text string) {
	doc.Nodes[id] = graph.Node{
		ID: id, Type: graph.NodeTypePrompt, Title: id,
		Rect: graph.Rect{X: 0, Y: 0, W: 320, H: 220},
		Spec: graph.NodeSpec{"text": text},
	}
}

func addGeneration(doc *graph.CanvasDocument, id string, spec graph.NodeSpec) {
	s, _ := graph.SchemaFor(graph.NodeTypeGeneration)
	doc.Nodes[id] = graph.Node{
		ID: id, Type: graph.NodeTypeGeneration, Title: id,
		Rect: graph.Rect{X: 400, Y: 0, W: 340, H: 260}, Ports: s.Ports, Spec: spec,
	}
}

func connect(doc *graph.CanvasDocument, id, from, fromPort, to, toPort string, kind graph.ResourceKind) {
	doc.Edges[id] = graph.Edge{ID: id,
		From: graph.Endpoint{NodeID: from, PortID: fromPort},
		To:   graph.Endpoint{NodeID: to, PortID: toPort}, Kind: kind}
}

func TestCompileRejectsEmptyTargets(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	if _, err := newCompiler().Compile(doc, nil); err == nil {
		t.Fatal("空目标应被拒绝")
	}
}

func TestCompileRejectsUnknownNode(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	_, err := newCompiler().Compile(doc, []string{"missing"})
	if err == nil {
		t.Fatal("未知节点应被拒绝")
	}
	ce, ok := err.(*CompileError)
	if !ok || ce.Code != platform.CodeNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestCompileRejectsNonExecutableTarget(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addPrompt(doc, "p_1", "hello")
	_, err := newCompiler().Compile(doc, []string{"p_1"})
	if err == nil {
		t.Fatal("目标不含执行节点应报错（不能静默成功）")
	}
	if ce, ok := err.(*CompileError); !ok || ce.Code != platform.CodeInvalidRequest {
		t.Fatalf("err=%v", err)
	}
}

func TestCompileResolvesInputsWithStableOrdering(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addPrompt(doc, "p_1", "第一段")
	addPrompt(doc, "p_2", "第二段")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	// 故意用非字典序的 ID，验证排序依据是端口 order 而不是 ID
	connect(doc, "e_b", "p_2", "out", "g_1", "prompt", graph.KindText)
	connect(doc, "e_a", "p_1", "out", "g_1", "prompt", graph.KindText)

	plan, err := newCompiler().Compile(doc, []string{"g_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps=%d", len(plan.Steps))
	}
	inputs := plan.Steps[0].Inputs
	if len(inputs) != 2 {
		t.Fatalf("inputs=%d", len(inputs))
	}
	if inputs[0].Value != "第一段" || inputs[1].Value != "第二段" {
		t.Fatalf("顺序不稳定: %+v", inputs)
	}
	if inputs[0].Label != "文本1" || inputs[1].Label != "文本2" {
		t.Fatalf("编号错误: %q %q", inputs[0].Label, inputs[1].Label)
	}
	if inputs[0].Origin.NodeID != "p_1" || inputs[0].Origin.PortID != "out" {
		t.Fatalf("origin 未记录: %+v", inputs[0].Origin)
	}
}

func TestCompileSnapshotsInputs(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addPrompt(doc, "p_1", "运行时的文案")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	connect(doc, "e_1", "p_1", "out", "g_1", "prompt", graph.KindText)

	plan, err := newCompiler().Compile(doc, []string{"g_1"})
	if err != nil {
		t.Fatal(err)
	}
	// 改动上游节点后，已编译的输入快照不应变化（保证重放）
	doc.Nodes["p_1"] = graph.Node{ID: "p_1", Type: graph.NodeTypePrompt,
		Rect: graph.Rect{X: 0, Y: 0, W: 320, H: 220}, Spec: graph.NodeSpec{"text": "改过了"}}
	if plan.Steps[0].Inputs[0].Value != "运行时的文案" {
		t.Fatalf("输入未快照，重放会漂移: %q", plan.Steps[0].Inputs[0].Value)
	}
}

func TestCompileChainsExecutableNodes(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addPrompt(doc, "p_1", "a")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	addGeneration(doc, "g_2", graph.NodeSpec{"capability": "image.edit"})
	connect(doc, "e_1", "p_1", "out", "g_1", "prompt", graph.KindText)
	connect(doc, "e_2", "g_1", "out", "g_2", "ref", graph.KindImage)

	// g_1 尚无结果：上游执行节点未产出时必须显式报错
	_, err := newCompiler().Compile(doc, []string{"g_2"})
	if err == nil {
		t.Fatal("上游执行节点无结果时应报错")
	}

	// 给 g_1 一个结果后应可编译，且顺序为 g_1 → g_2
	doc.Nodes["g_1"] = graph.Node{
		ID: "g_1", Type: graph.NodeTypeGeneration, Rect: graph.Rect{X: 400, Y: 0, W: 340, H: 260},
		Spec: graph.NodeSpec{"capability": "image.generate"},
		Result: &graph.NodeResult{Variants: []graph.ResultVariant{
			{AssetID: "as_1", Kind: graph.KindImage, Status: graph.NodeSucceeded},
		}},
	}
	plan, err := newCompiler().Compile(doc, []string{"g_2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("steps=%d", len(plan.Steps))
	}
	if plan.Steps[0].NodeID != "g_1" || plan.Steps[1].NodeID != "g_2" {
		t.Fatalf("拓扑序错误: %s, %s", plan.Steps[0].NodeID, plan.Steps[1].NodeID)
	}
	if plan.Steps[1].Inputs[0].AssetID != "as_1" {
		t.Fatalf("上游结果未作为输入: %+v", plan.Steps[1].Inputs)
	}
}

func TestCompileExpandsGroup(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	doc.Nodes["grp"] = graph.Node{ID: "grp", Type: graph.NodeTypeGroup, Rect: graph.Rect{X: 0, Y: 0, W: 500, H: 500}}
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	doc.Nodes["g_1"] = func() graph.Node { n := doc.Nodes["g_1"]; n.ParentID = "grp"; return n }()

	plan, err := newCompiler().Compile(doc, []string{"grp"})
	if err != nil {
		t.Fatalf("组应展开为子节点: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].NodeID != "g_1" {
		t.Fatalf("展开失败: %+v", plan.Order)
	}
}

func TestCompileRejectsMissingCapability(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addGeneration(doc, "g_1", graph.NodeSpec{})
	_, err := newCompiler().Compile(doc, []string{"g_1"})
	if err == nil {
		t.Fatal("缺少 capability 应报错")
	}
	if ce, ok := err.(*CompileError); !ok || ce.Code != platform.CodeInvalidSpec {
		t.Fatalf("err=%v", err)
	}
}

func TestCompileRejectsUnknownCapability(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.hack"})
	if _, err := newCompiler().Compile(doc, []string{"g_1"}); err == nil {
		t.Fatal("未知能力应报错")
	}
}

func TestCompileRejectsNoCredential(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	c := NewCompiler(func(provider.Capability, string, string) (provider.Credential, bool) {
		return provider.Credential{}, false
	})
	_, err := c.Compile(doc, []string{"g_1"})
	if err == nil {
		t.Fatal("无可用凭据应报错（不能提交后才失败）")
	}
}

func TestCompileRejectsEmptyPromptNode(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addPrompt(doc, "p_1", "")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	connect(doc, "e_1", "p_1", "out", "g_1", "prompt", graph.KindText)
	_, err := newCompiler().Compile(doc, []string{"g_1"})
	if err == nil {
		t.Fatal("空提示词节点作为输入应报错")
	}
}

// 环在生产路径已被 graph 层拒绝；这里直接构造环验证编译器也能检出（防御性）。
func TestCompileDetectsCycleInPlan(t *testing.T) {
	steps := []PlanStep{
		{ID: "a", NodeID: "a", DependsOn: []string{"b"}},
		{ID: "b", NodeID: "b", DependsOn: []string{"a"}},
	}
	if _, err := topoSort(steps); err == nil {
		t.Fatal("环应被检出")
	}
}

func TestCompileStepCountLimit(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	targets := []string{}
	for i := 0; i < MaxDAGNodes+5; i++ {
		id := "g_" + itoa(i)
		addGeneration(doc, id, graph.NodeSpec{"capability": "image.generate"})
		targets = append(targets, id)
	}
	_, err := newCompiler().Compile(doc, targets)
	if err == nil {
		t.Fatal("超步骤上限应被拒绝")
	}
	if !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("err=%v", err)
	}
}

func TestCompileWriteBackTarget(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addGeneration(doc, "g_1", graph.NodeSpec{"capability": "image.generate"})
	plan, err := newCompiler().Compile(doc, []string{"g_1"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].WriteBack.NodeID != "g_1" || plan.Steps[0].WriteBack.PortID != "out" {
		t.Fatalf("回写目标错误: %+v", plan.Steps[0].WriteBack)
	}
}

func TestIntFromSpec(t *testing.T) {
	if got := IntFromSpec(graph.NodeSpec{"outputCount": 4}, "outputCount", 1); got != 4 {
		t.Fatalf("got=%d", got)
	}
	if got := IntFromSpec(graph.NodeSpec{"outputCount": 4.5}, "outputCount", 1); got != 1 {
		t.Fatalf("非整数应回退默认值，got=%d", got)
	}
	if got := IntFromSpec(graph.NodeSpec{}, "outputCount", 3); got != 3 {
		t.Fatalf("got=%d", got)
	}
}

func TestProviderForSpecCarriesParams(t *testing.T) {
	doc := graph.NewDocument("cv_1", "pj_1")
	addGeneration(doc, "g_1", graph.NodeSpec{
		"capability": "video.generate", "model": "sora-2",
		"providerId": "openai", "outputCount": 2,
		"params": map[string]any{"seconds": "8"},
	})
	plan, err := newCompiler().Compile(doc, []string{"g_1"})
	if err != nil {
		t.Fatal(err)
	}
	s := plan.Steps[0]
	if s.Capability != provider.CapVideoGenerate || s.Model != "sora-2" || s.Count != 2 {
		t.Fatalf("步骤参数错误: %+v", s)
	}
	if s.Params["seconds"] != "8" {
		t.Fatalf("params 未传递: %+v", s.Params)
	}
}

// 直通生成的参考图必须成为 Steps 的 Inputs（否则「图生图」会退化成文生图）。
//
// 这条对应一个真实的静默错误：参考图传进来了但没进请求体，
// 上游按纯文本生成，用户看到「结果和参考图没关系」却查不出原因。
func TestAdHocReferencesBecomeInputs(t *testing.T) {
	plan, err := CompileAdHoc(Run{
		ID: "run_1",
		Params: map[string]any{
			"capability": "image.edit",
			"prompt":     "改成夜景",
			"references": []any{"as_aaa", "as_bbb"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("步骤数不符: %d", len(plan.Steps))
	}
	step := plan.Steps[0]
	if len(step.Inputs) != 2 {
		t.Fatalf("参考图未成为输入: %+v", step.Inputs)
	}
	// Label 必须与前端角标编号一致（"图片1" / "图片2"）
	if step.Inputs[0].Label != "图片1" || step.Inputs[1].Label != "图片2" {
		t.Fatalf("引用标签与 UI 编号不一致: %q %q", step.Inputs[0].Label, step.Inputs[1].Label)
	}
	if step.Inputs[0].AssetID != "as_aaa" {
		t.Fatalf("顺序错位: %+v", step.Inputs)
	}
}

// 参考图数量超限必须在提交前拒绝（否则上游会报一个看不懂的 400）。
func TestAdHocRejectsTooManyReferences(t *testing.T) {
	refs := make([]any, 0, 8)
	for i := 0; i < 8; i++ {
		refs = append(refs, "as_"+string(rune('a'+i)))
	}
	_, err := CompileAdHoc(Run{ID: "r", Params: map[string]any{
		"capability": "image.edit", "prompt": "x", "references": refs,
	}})
	if err == nil {
		t.Fatal("超过 7 张参考图应被拒绝")
	}
}

// 非法的 assetId 同样在提交前拒绝。
func TestAdHocRejectsInvalidAssetID(t *testing.T) {
	_, err := CompileAdHoc(Run{ID: "r", Params: map[string]any{
		"capability": "image.edit", "prompt": "x", "references": []any{"../../etc/passwd"},
	}})
	if err == nil {
		t.Fatal("非法 assetId 应被拒绝")
	}
}
