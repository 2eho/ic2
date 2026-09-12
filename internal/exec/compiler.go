package exec

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// MaxDAGNodes 是单次编译允许的最大步骤数（防爆炸）。
const MaxDAGNodes = 500

// CompileError 携带可点击定位的节点信息（校验失败必须在提交前返回）。
type CompileError struct {
	Code   string
	NodeID string
	Reason string
}

// Error 实现 error。
func (e *CompileError) Error() string {
	if e.NodeID != "" {
		return fmt.Sprintf("%s at node %s: %s", e.Code, e.NodeID, e.Reason)
	}
	return e.Code + ": " + e.Reason
}

// Compiler 把图的一部分编译为 DAG。
type Compiler struct {
	// CredentialFor 按能力解析可用凭据（由 provider registry 提供）。
	CredentialFor func(cap provider.Capability, providerID, credentialID string) (provider.Credential, bool)
}

// NewCompiler 构造编译器。
func NewCompiler(credentialFor func(provider.Capability, string, string) (provider.Credential, bool)) *Compiler {
	return &Compiler{CredentialFor: credentialFor}
}

// Compile 从目标节点反向遍历，收集执行型节点并解析输入。
func (c *Compiler) Compile(doc *graph.CanvasDocument, targets []string) (*Plan, error) {
	if len(targets) == 0 {
		return nil, platform.ErrInvalid("targetNodes is required")
	}
	plan := &Plan{}
	visited := map[string]bool{}
	var order []string

	// 1) 反向收集所有执行型节点。
	var collect func(nodeID string) error
	collect = func(nodeID string) error {
		if visited[nodeID] {
			return nil
		}
		visited[nodeID] = true
		n, ok := doc.Nodes[nodeID]
		if !ok {
			return &CompileError{Code: platform.CodeNotFound, NodeID: nodeID, Reason: "node not found"}
		}
		// group 只是布局容器：展开为输入环境，不产生步骤。
		if n.Type == graph.NodeTypeGroup {
			for _, child := range doc.ChildrenOf(nodeID) {
				if err := collect(child); err != nil {
					return err
				}
			}
			return nil
		}
		if n.Type != graph.NodeTypeGeneration && n.Type != graph.NodeTypeRun {
			return nil // 资源节点终止收集，转为输入
		}
		for _, e := range doc.UpstreamOf(nodeID) {
			if err := collect(e.From.NodeID); err != nil {
				return err
			}
		}
		if len(plan.Steps) >= MaxDAGNodes {
			return &CompileError{Code: platform.CodePayloadTooLarge, Reason: "plan exceeds max steps"}
		}
		step, err := c.stepFor(doc, n)
		if err != nil {
			return err
		}
		plan.Steps = append(plan.Steps, step)
		order = append(order, step.ID)
		return nil
	}

	for _, t := range targets {
		if err := collect(t); err != nil {
			return nil, err
		}
	}
	if len(plan.Steps) == 0 {
		// 目标里没有执行型节点：显式报错而不是静默成功。
		return nil, &CompileError{Code: platform.CodeInvalidRequest, Reason: "no executable node in targets"}
	}

	// 2) 拓扑排序（Kahn），保证依赖先执行；同时检测环。
	sorted, err := topoSort(plan.Steps)
	if err != nil {
		return nil, err
	}
	plan.Steps = sorted
	plan.Order = make([]string, 0, len(sorted))
	for _, s := range sorted {
		plan.Order = append(plan.Order, s.ID)
	}

	// 3) 解析每个步骤的输入快照。
	for i := range plan.Steps {
		inputs, prompt, err := c.resolveInputs(doc, &plan.Steps[i])
		if err != nil {
			return nil, err
		}
		plan.Steps[i].Inputs = inputs
		plan.Steps[i].Prompt = prompt
	}
	return plan, nil
}

func (c *Compiler) stepFor(doc *graph.CanvasDocument, n graph.Node) (PlanStep, error) {
	step := PlanStep{
		ID:        "st_" + n.ID,
		NodeID:    n.ID,
		Kind:      StepGenerate,
		Inputs:    nil,
		WriteBack: WriteBack{NodeID: n.ID, PortID: "out"},
	}
	if n.Type == graph.NodeTypeRun {
		step.Kind = StepFetch
		return step, nil
	}
	capStr, _ := n.Spec["capability"].(string)
	cap, ok := provider.Parse(capStr)
	if !ok {
		return step, &CompileError{Code: platform.CodeInvalidSpec, NodeID: n.ID,
			Reason: "capability is required and must be a known capability"}
	}
	step.Capability = cap
	step.Model, _ = n.Spec["model"].(string)
	step.ProviderID, _ = n.Spec["providerId"].(string)
	step.CredentialID, _ = n.Spec["credentialId"].(string)
	if params, ok := n.Spec["params"].(map[string]any); ok {
		step.Params = params
	}
	if params, ok := n.Spec["params"].(graph.NodeSpec); ok {
		step.Params = map[string]any(params)
	}
	step.Count = intFromSpec(n.Spec, "outputCount", 1)
	if tpl, ok := n.Spec["promptTemplate"].(string); ok {
		step.Prompt = tpl
	}

	if c.CredentialFor != nil {
		if _, ok := c.CredentialFor(cap, step.ProviderID, step.CredentialID); !ok {
			return step, &CompileError{Code: platform.CodeInvalidRequest, NodeID: n.ID,
				Reason: "no credential available for capability " + string(cap)}
		}
	}
	return step, nil
}

// resolveInputs 解析上游输入并做快照（保证重放不受后续画布修改影响）。
func (c *Compiler) resolveInputs(doc *graph.CanvasDocument, step *PlanStep) ([]provider.ResolvedInput, string, error) {
	node := doc.Nodes[step.NodeID]
	// 按端口 Order 排序，保证「文本1/文本2」的编号稳定（对齐原项目经验教训）。
	inEdges := doc.UpstreamOf(step.NodeID)
	orderIndex := map[string]int{}
	for _, p := range node.Ports.Inputs {
		orderIndex[p.ID] = p.Order
	}
	sort.SliceStable(inEdges, func(i, j int) bool {
		oi, oj := orderIndex[inEdges[i].To.PortID], orderIndex[inEdges[j].To.PortID]
		if oi != oj {
			return oi < oj
		}
		return inEdges[i].ID < inEdges[j].ID
	})

	inputs := []provider.ResolvedInput{}
	labelCounter := map[string]int{}
	for _, e := range inEdges {
		src, ok := doc.Nodes[e.From.NodeID]
		if !ok {
			return nil, "", &CompileError{Code: platform.CodeNotFound, NodeID: e.From.NodeID, Reason: "edge source missing"}
		}
		in, err := c.resolveNodeAsInput(src)
		if err != nil {
			return nil, "", err
		}
		labelCounter[in.Kind]++
		// 标签统一为「类型N」，由端口 order 决定顺序（不再依赖文本约定）。
		in.Label = provider.KindLabelCN(in.Kind) + itoa(labelCounter[in.Kind])
		in.Origin = provider.Origin{NodeID: src.ID, PortID: e.From.PortID}
		inputs = append(inputs, in)
	}
	return inputs, step.Prompt, nil
}

func (c *Compiler) resolveNodeAsInput(n graph.Node) (provider.ResolvedInput, error) {
	switch n.Type {
	case graph.NodeTypePrompt:
		text, _ := n.Spec["text"].(string)
		if text == "" {
			return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidSpec, NodeID: n.ID,
				Reason: "prompt node has empty text"}
		}
		return provider.ResolvedInput{Kind: "text", Value: text}, nil
	case graph.NodeTypeImage:
		asset, _ := n.Spec["assetId"].(string)
		if asset == "" {
			// 上游结果尚未产出时，使用节点最近一次结果
			if n.Result != nil && len(n.Result.Variants) > 0 && n.Result.Variants[0].AssetID != "" {
				asset = n.Result.Variants[0].AssetID
			}
		}
		if asset == "" {
			return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidSpec, NodeID: n.ID,
				Reason: "image node has no asset"}
		}
		return provider.ResolvedInput{Kind: "image", AssetID: asset, Value: asset}, nil
	case graph.NodeTypeVideo:
		asset, _ := n.Spec["assetId"].(string)
		if asset == "" && n.Result != nil && len(n.Result.Variants) > 0 {
			asset = n.Result.Variants[0].AssetID
		}
		if asset == "" {
			return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidSpec, NodeID: n.ID,
				Reason: "video node has no asset"}
		}
		return provider.ResolvedInput{Kind: "video", AssetID: asset, Value: asset}, nil
	case graph.NodeTypeAudio:
		asset, _ := n.Spec["assetId"].(string)
		if asset == "" && n.Result != nil && len(n.Result.Variants) > 0 {
			asset = n.Result.Variants[0].AssetID
		}
		if asset == "" {
			return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidSpec, NodeID: n.ID,
				Reason: "audio node has no asset"}
		}
		return provider.ResolvedInput{Kind: "audio", AssetID: asset, Value: asset}, nil
	case graph.NodeTypeGeneration, graph.NodeTypeRun:
		// 上游是执行型节点：引用其最近一次成功结果（若尚未运行则报错，避免静默空输入）。
		if n.Result == nil || len(n.Result.Variants) == 0 {
			return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidRequest, NodeID: n.ID,
				Reason: "upstream generation has no result yet; run it first"}
		}
		v := n.Result.Variants[0]
		if v.AssetID != "" {
			return provider.ResolvedInput{Kind: string(v.Kind), AssetID: v.AssetID, Value: v.AssetID}, nil
		}
		return provider.ResolvedInput{Kind: string(v.Kind), Value: v.Text}, nil
	case graph.NodeTypeGroup:
		return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidSpec, NodeID: n.ID,
			Reason: "group node cannot be used as input directly"}
	}
	return provider.ResolvedInput{}, &CompileError{Code: platform.CodeInvalidNodeType, NodeID: n.ID,
		Reason: "unsupported input node type " + string(n.Type)}
}

// topoSort 对步骤做拓扑排序并检测环。
func topoSort(steps []PlanStep) ([]PlanStep, error) {
	byID := map[string]PlanStep{}
	for _, s := range steps {
		byID[s.ID] = s
	}
	// 依赖只保留存在于本次计划中的步骤
	deps := map[string][]string{}
	indeg := map[string]int{}
	for _, s := range steps {
		deps[s.ID] = nil
		indeg[s.ID] = 0
	}
	for _, s := range steps {
		for _, d := range s.DependsOn {
			if _, ok := byID[d]; ok {
				deps[d] = append(deps[d], s.ID)
				indeg[s.ID]++
			}
		}
	}
	// 同时从边推导隐式依赖：同一 Run 中，上游执行节点的步骤必须先完成。
	nodeToStep := map[string]string{}
	for _, s := range steps {
		nodeToStep[s.NodeID] = s.ID
	}

	queue := []string{}
	for id, n := range indeg {
		if n == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	out := make([]PlanStep, 0, len(steps))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, byID[id])
		next := append([]string(nil), deps[id]...)
		sort.Strings(next)
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if len(out) != len(steps) {
		return nil, &CompileError{Code: platform.CodeInvalidSpec, Reason: "plan contains a cycle"}
	}
	// 按节点依赖关系补充 DependsOn，便于 UI 展示时间线
	for i := range out {
		for _, other := range steps {
			if other.NodeID == out[i].NodeID {
				out[i].DependsOn = other.DependsOn
			}
		}
	}
	return out, nil
}

// IntFromSpec 读取整型 spec 字段（导出供测试）。
func IntFromSpec(spec graph.NodeSpec, key string, def int) int { return intFromSpec(spec, key, def) }

func intFromSpec(spec graph.NodeSpec, key string, def int) int {
	v, ok := spec[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		if n != float64(int(n)) {
			return def
		}
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return def
}

func itoa(n int) string {
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
