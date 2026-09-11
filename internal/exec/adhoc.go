package exec

import (
	"context"
	"fmt"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// maxAdHocReferences 与前端 ReferenceBar 的上限保持一致（图片 7 张）。
//
// 两边各写一个数字会漂移：前端允许 7 张而后端只收 5 张时，
// 用户会在第 6 张收到一个参数错误，且看不出为什么。
const maxAdHocReferences = 7

// stringSlice 把任意 JSON 值宽松地转成字符串数组（只接受字符串元素，忽略其余）。
// 不报错是刻意的：references 是可选参数，形状不对时按「没有参考图」处理更合理，
// 而真正的类型错误会在下面用 ValidID 拦住。
func stringSlice(v any) []string {
	list, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// 直通生成（工作台）：不落画布，但**复用同一个执行引擎**。
//
// 为什么必须复用：原项目的工作台与画布各写一套生成逻辑（image.ts 921 行 + video.ts 428 行），
// 结果是重试策略、错误分类、取消语义、成本核算四处不一致，
// 同一个模型在两个入口表现不同。重写的取舍是把差异压到「计划怎么来」这一层，
// 执行、重试、计量、回写全部共用。
//
// AdHocPlan 用一个节点占位，从而复用 DAG 调度器（含并发、取消、事件）。
func CompileAdHoc(run Run) (*Plan, error) {
	capability, _ := run.Params["capability"].(string)
	if capability == "" {
		// 工作台未指定能力时按图片生成处理（与原项目默认行为一致）。
		capability = string(provider.CapImageGenerate)
	}
	if !graph.ValidCapabilityString(capability) {
		return nil, platform.ErrInvalid("unknown capability: " + capability)
	}
	prompt, _ := run.Params["prompt"].(string)
	if prompt == "" {
		return nil, platform.ErrInvalid("prompt is required")
	}
	// 参考图以 assetId 数组传入（工作台的「图生图」路径）。
	// 这里只做形状与上限校验；真正的取字节在 engine.materializeInputs 里完成
	// （那里才有 AssetReader，能把资产读成 dataURI 交给适配器）。
	references := stringSlice(run.Params["references"])
	if len(references) > maxAdHocReferences {
		return nil, platform.NewError(422, platform.CodeInvalidRequest,
			"参考图数量超限").WithDetail("limit", maxAdHocReferences).WithDetail("got", len(references))
	}
	inputs := make([]provider.ResolvedInput, 0, len(references))
	for i, assetID := range references {
		if !graph.ValidID(assetID) {
			return nil, platform.NewError(422, platform.CodeInvalidRequest,
				"参考图 assetId 不合法").WithDetail("index", i)
		}
		inputs = append(inputs, provider.ResolvedInput{
			Kind:    "image",
			AssetID: assetID,
			// Label 会进提示词的引用说明，必须与 UI 的角标编号一致
			Label: fmt.Sprintf("图片%d", i+1),
		})
	}
	count, ok := intFromAny(run.Params["outputCount"])
	if !ok || count <= 0 {
		count = graph.MinVariantsPerNode
	}
	if count > graph.MaxVariantsPerNode {
		count = graph.MaxVariantsPerNode
	}
	model, _ := run.Params["model"].(string)
	providerID, _ := run.Params["providerId"].(string)
	credentialID, _ := run.Params["credentialId"].(string)
	params, _ := run.Params["params"].(map[string]any)

	stepID := adHocStepID(run.ID)
	return &Plan{
		Steps: []PlanStep{{
			ID:           stepID,
			NodeID:       "adhoc",
			Kind:         StepGenerate,
			Capability:   provider.Capability(capability),
			Model:        model,
			ProviderID:   providerID,
			CredentialID: credentialID,
			Params:       params,
			Count:        count,
			Prompt:       prompt,
			Inputs:       inputs,
			// AdHoc 不回写画布：结果由 Run 的 outputs 承载，前端直接展示。
			WriteBack: WriteBack{},
		}},
		Order: []string{stepID},
	}, nil
}

// ExecuteAdHoc 执行直通生成。与画布运行共用 runStep（重试、计量、事件、落盘）。
func (e *Engine) ExecuteAdHoc(ctx context.Context, run *Run, plan *Plan) error {
	if run == nil || plan == nil || len(plan.Steps) == 0 {
		return platform.ErrInvalid("plan is empty")
	}
	run.Status = RunRunning
	_ = e.store.SaveRun(ctx, run)
	step := &Step{
		ID: plan.Steps[0].ID, NodeID: plan.Steps[0].NodeID, Kind: plan.Steps[0].Kind,
		Status: StepRunning, StartedAt: e.clock.Now().UTC(),
	}
	run.Steps = []*Step{step}
	_ = e.store.SaveRun(ctx, run)

	err := e.runStep(ctx, run, step, &plan.Steps[0])
	if err != nil {
		e.finishRun(ctx, run, RunFailed, provider.ClassifyTransport(err))
		return err
	}
	e.finishRun(ctx, run, RunSucceeded, nil)
	return nil
}

// MarkFailed 把 Run 收敛到失败态（执行前错误，例如无可用凭据）。
// 必须显式落库：否则前端会一直轮询一个永远 pending 的 Run。
func (e *Engine) MarkFailed(ctx context.Context, run *Run, err error) {
	e.finishRun(ctx, run, RunFailed, provider.ClassifyTransport(err))
}

// adHocStepID 生成确定性步骤 ID（同一 Run 重放时步骤 ID 相同，便于比对）。
func adHocStepID(runID string) string { return "step_" + runID }

func intFromAny(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		n := 0
		for _, r := range t {
			if r < '0' || r > '9' {
				return 0, false
			}
			n = n*10 + int(r-'0')
		}
		return n, true
	default:
		return 0, false
	}
}
