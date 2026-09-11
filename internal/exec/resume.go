package exec

import (
	"context"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// Resume 在进程重启后恢复运行中的异步任务（ATK-15）。
//
// 语义：
//   - 只有「有 RemoteTask 且未终态」的 Attempt 需要续查；
//   - 续查成功则落资产并回写，失败则按分类决定是否降级为失败；
//   - 不重复调用上游创建接口（不重复扣费，INV-3）。
func (e *Engine) Resume(ctx context.Context, run *Run) error {
	pending := findPendingAsync(run)
	if len(pending) == 0 {
		// 没有可续查的异步任务：把「运行中」收敛为明确结束状态，避免永久挂起。
		if run.Status == RunRunning {
			status := RunSucceeded
			for _, s := range run.Steps {
				if s.Status == StepRunning || s.Status == StepRetrying || s.Status == StepPending {
					pe := &provider.ProviderError{
						Class: provider.ClassTransient, Code: platform.CodeInterrupted,
						Message: "task was interrupted by a server restart",
					}
					s.Status = StepFailed
					s.Error = pe
					status = RunPartial
				}
			}
			e.finishRun(ctx, run, status, nil)
		}
		return nil
	}

	for _, p := range pending {
		step := p.step
		att := p.attempt
		// 从存储恢复时没有 plan；执行参数从 Attempt 与 Params 里恢复。
		// 这一步刻意只依赖持久化字段，避免「重启后参数丢失」的隐性依赖。
		capability := capabilityFromAttempt(att)
		providerID := att.ProviderID
		adapter, cred, ok := e.adapters.Resolve(capability, providerID, "")
		if !ok {
			step.Status = StepFailed
			step.Error = &provider.ProviderError{
				Class: provider.ClassPermanent, Code: platform.CodeInvalidRequest,
				Message: "credential unavailable after restart for provider " + providerID,
			}
			_ = e.store.SaveStep(ctx, run.ID, step)
			continue
		}
		deadline := e.clock.Now().Add(10 * time.Minute)
		for e.clock.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return ErrCanceled
			default:
			}
			task, err := adapter.Poll(ctx, cred, att.RemoteTask.ID)
			if err != nil {
				pe := toProviderError(err)
				if !pe.Retryable() {
					step.Status = StepFailed
					step.Error = pe
					step.FinishedAt = ptrTime(e.clock.Now().UTC())
					_ = e.store.SaveStep(ctx, run.ID, step)
					break
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			att.RemoteTask.Status = task.Status
			att.RemoteTask.Progress = task.Progress
			switch task.Status {
			case "succeeded", "completed":
				// 续查成功后需要取回产物：用 FetchAsset（URL 型）或标记完成（bytes 型已在首次响应里）
				res := provider.Response{}
				// 续查成功后取回产物 URL（异步任务的完成响应里带 URI）。
				if u := stringParam(run.Params, "resultUrl"); u != "" {
					res.Assets = append(res.Assets, provider.AssetRef{Kind: "video", URL: u, MIME: "video/mp4"})
				}
				outputs, oerr := e.persistOutputs(ctx, run, step, nil, res)
				if oerr != nil {
					return oerr
				}
				att.Status = StepSucceeded
				step.Outputs = append(step.Outputs, outputs...)
				step.Status = StepSucceeded
				step.FinishedAt = ptrTime(e.clock.Now().UTC())
				_ = e.store.SaveAttempt(ctx, run.ID, step.ID, att)
				_ = e.store.SaveStep(ctx, run.ID, step)
			case "failed", "error":
				step.Status = StepFailed
				step.Error = &provider.ProviderError{Class: provider.ClassPermanent, Code: platform.CodeUpstreamInvalid,
					Message: "async task failed upstream"}
				step.FinishedAt = ptrTime(e.clock.Now().UTC())
				_ = e.store.SaveStep(ctx, run.ID, step)
			}
			if step.Status == StepSucceeded || step.Status == StepFailed {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// 汇总
	status := RunSucceeded
	failed, succeeded := 0, 0
	for _, s := range run.Steps {
		switch s.Status {
		case StepFailed:
			failed++
		case StepSucceeded:
			succeeded++
		}
	}
	if failed > 0 && succeeded == 0 {
		status = RunFailed
	} else if failed > 0 {
		status = RunPartial
	}
	e.finishRun(ctx, run, status, nil)
	return nil
}

// capabilityFromAttempt 从 Attempt 恢复能力。Attempt 里保存了 model 与 provider，
// 能力由步骤 Kind + 模型关键词推断（重启路径必须能从持久化数据自愈）。
func capabilityFromAttempt(att *Attempt) provider.Capability {
	for _, c := range provider.All() {
		if c.ResourceKind() == "video" {
			if att.RemoteTask != nil && att.RemoteTask.Provider == "gemini" {
				return provider.CapVideoGenerate
			}
		}
	}
	// 异步任务的唯一场景是视频生成
	if att.RemoteTask != nil {
		return provider.CapVideoGenerate
	}
	return provider.CapImageGenerate
}

type pendingAsync struct {
	step    *Step
	attempt *Attempt
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func findPendingAsync(run *Run) []pendingAsync {
	out := []pendingAsync{}
	for _, s := range run.Steps {
		if s.Status == StepSucceeded || s.Status == StepFailed || s.Status == StepSkipped || s.Status == StepCanceled {
			continue
		}
		for i := range s.Attempts {
			a := &s.Attempts[i]
			if a.RemoteTask != nil && a.RemoteTask.ID != "" {
				out = append(out, pendingAsync{step: s, attempt: a})
				break
			}
		}
	}
	return out
}

// ResumeAll 恢复所有可续查的运行（服务启动时调用）。
func (e *Engine) ResumeAll(ctx context.Context) (int, error) {
	ids, err := e.store.ListResumableRuns(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		run, err := e.store.LoadRun(ctx, id)
		if err != nil {
			continue
		}
		if err := e.Resume(ctx, run); err != nil {
			continue
		}
		n++
	}
	return n, nil
}
