package exec

import (
	"context"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

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
