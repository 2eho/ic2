package exec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// Engine 执行 Run：编译 → 调度 → 重试 → 回写 → 计量。
type Engine struct {
	store    Store
	blobs    BlobWriter
	adapters AdapterRegistry
	canvas   CanvasWriter
	sink     EventSink
	clock    platform.Clock
	ids      platform.IDGen
	assets   AssetReader

	policy      RetryPolicy
	concurrency int

	mu      sync.Mutex
	runs    map[string]*runCtx
	stopped bool
}

type runCtx struct {
	run    *Run
	cancel context.CancelFunc
}

// Options 构造参数。
type Options struct {
	Store       Store
	Blobs       BlobWriter
	Adapters    AdapterRegistry
	Canvas      CanvasWriter
	Sink        EventSink
	Clock       platform.Clock
	IDs         platform.IDGen
	Policy      RetryPolicy
	Concurrency int
}

// New 构造执行引擎。
func New(o Options) *Engine {
	if o.Clock == nil {
		o.Clock = platform.SystemClock()
	}
	if o.IDs == nil {
		o.IDs = platform.DefaultIDGen()
	}
	if o.Sink == nil {
		o.Sink = NewMemorySink()
	}
	if o.Policy.MaxAttempts == 0 {
		o.Policy = DefaultRetryPolicy()
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	return &Engine{
		store: o.Store, blobs: o.Blobs, adapters: o.Adapters, canvas: o.Canvas,
		sink: o.Sink, clock: o.Clock, ids: o.IDs, policy: o.Policy,
		concurrency: o.Concurrency, runs: map[string]*runCtx{},
	}
}

// Sink 暴露事件总线（API 层订阅 SSE 用）。
func (e *Engine) Sink() EventSink { return e.sink }

// SetStore 注入存储（让 cmd 可以在构造后装配 SQL 实现）。
func (e *Engine) SetStore(st Store) { e.store = st }

// SetBlobs 注入资产写入能力。
func (e *Engine) SetBlobs(b BlobWriter) { e.blobs = b }

// SetAdapters 注入渠道注册表。
func (e *Engine) SetAdapters(a AdapterRegistry) { e.adapters = a }

// SetCanvasWriter 注入画布回写能力。
func (e *Engine) SetCanvasWriter(c CanvasWriter) { e.canvas = c }

// Create 创建一个 Run（未开始执行）。幂等键命中时返回已有 Run（INV-2/INV-3）。
func (e *Engine) Create(ctx context.Context, req RunRequest) (*Run, error) {
	if req.WorkspaceID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	if req.IdempotencyKey != "" {
		if existing, err := e.store.FindByIdempotencyKey(ctx, req.WorkspaceID, req.IdempotencyKey); err == nil && existing != nil {
			return existing, nil
		}
	}
	run := &Run{
		ID:             e.ids.NewID("run"),
		WorkspaceID:    req.WorkspaceID,
		CanvasID:       req.CanvasID,
		ProjectID:      req.ProjectID,
		Trigger:        firstNonEmpty(req.Trigger, "manual"),
		Status:         RunPending,
		TargetNodes:    req.TargetNodes,
		Params:         req.Params,
		ActorID:        req.ActorID,
		IdempotencyKey: req.IdempotencyKey,
		StartedAt:      e.clock.Now().UTC(),
		AdHoc:          req.AdHoc,
	}
	if err := e.store.SaveRun(ctx, run); err != nil {
		// 唯一键冲突 → 并发重复提交，回退到已有 Run
		if req.IdempotencyKey != "" {
			if existing, ferr := e.store.FindByIdempotencyKey(ctx, req.WorkspaceID, req.IdempotencyKey); ferr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, err
	}
	return run, nil
}

// RunRequest 是触发运行的请求。
type RunRequest struct {
	WorkspaceID    string
	CanvasID       string
	ProjectID      string
	TargetNodes    []string
	Trigger        string
	ActorID        string
	Params         map[string]any
	IdempotencyKey string
	AdHoc          bool
}

// Execute 同步执行一个 Run（调用方通常放在 goroutine 或 worker 中）。
func (e *Engine) Execute(ctx context.Context, run *Run, plan *Plan, doc *graph.CanvasDocument) error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return errors.New("engine stopped")
	}
	runCtxParent := ctx
	cctx, cancel := context.WithCancel(runCtxParent)
	e.runs[run.ID] = &runCtx{run: run, cancel: cancel}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.runs, run.ID)
		e.mu.Unlock()
		cancel()
	}()

	run.Status = RunRunning
	if err := e.store.SaveRun(cctx, run); err != nil {
		return err
	}

	// 1) 建步骤
	run.Steps = make([]*Step, 0, len(plan.Steps))
	byNode := map[string]*Step{}
	for _, ps := range plan.Steps {
		st := &Step{
			ID: ps.ID, NodeID: ps.NodeID, Kind: ps.Kind, Status: StepPending,
			DependsOn: ps.DependsOn, StartedAt: e.clock.Now().UTC(),
		}
		run.Steps = append(run.Steps, st)
		byNode[ps.NodeID] = st
	}
	// 由节点依赖推导步骤依赖
	for i := range plan.Steps {
		ps := &plan.Steps[i]
		step := byNode[ps.NodeID]
		for _, dep := range dependsOnNodes(doc, ps.NodeID) {
			if d, ok := byNode[dep]; ok && d.ID != step.ID {
				step.DependsOn = append(step.DependsOn, d.ID)
			}
		}
		e.store.SaveStep(cctx, run.ID, step)
	}

	// 2) 按拓扑序执行（同层可并行由 worker 池控制；这里顺序执行以免依赖发散）
	select {
	case <-cctx.Done():
		e.finishRun(cctx, run, RunCanceled, nil)
		return ErrCanceled
	default:
	}

	for i := range plan.Steps {
		ps := &plan.Steps[i]
		step := byNode[ps.NodeID]
		if !e.depsSatisfied(byNode, step) {
			step.Status = StepSkipped
			step.FinishedAt = ptrTime(e.clock.Now().UTC())
			e.store.SaveStep(cctx, run.ID, step)
			continue
		}
		if err := e.runStep(cctx, run, step, ps); err != nil {
			if errors.Is(err, ErrCanceled) || cctx.Err() != nil {
				e.markCanceled(run, step)
				e.finishRun(cctx, run, RunCanceled, nil)
				return ErrCanceled
			}
			// 单步失败：标记并继续（部分成功显式建模，见 11 §2.7）
			step.Status = StepFailed
			step.Error = provider.ClassifyTransport(err)
			if pe, ok := err.(*provider.ProviderError); ok {
				step.Error = pe
			}
			step.FinishedAt = ptrTime(e.clock.Now().UTC())
			e.store.SaveStep(cctx, run.ID, step)
			e.sink.EmitRun(RunEvent{
				RunID: run.ID, StepID: step.ID, NodeID: step.NodeID, Type: "step.failed",
				Status: string(StepFailed), Error: step.Error, At: e.clock.Now().UTC(),
			})
			if e.canvas != nil && !run.AdHoc {
				_ = e.canvas.WriteBack(cctx, run.CanvasID, step.NodeID, graph.NodeResult{},
					graph.NodeFailed, run.ActorID)
			}
		}
	}

	// 3) 汇总状态
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
	var primaryErr *provider.ProviderError
	if failed > 0 {
		for _, s := range run.Steps {
			if s.Status == StepFailed && s.Error != nil {
				primaryErr = s.Error
				break
			}
		}
	}
	e.finishRun(cctx, run, status, primaryErr)
	if status == RunFailed {
		return primaryErr
	}
	return nil
}

func (e *Engine) depsSatisfied(byNode map[string]*Step, step *Step) bool {
	for _, dep := range step.DependsOn {
		found := false
		for _, s := range byNode {
			if s.ID == dep {
				found = true
				// 依赖失败则跳过（策略：skip，不整体失败）
				if s.Status != StepSucceeded {
					return false
				}
				break
			}
		}
		if !found {
			// 依赖不在本次计划内：视为已满足
			continue
		}
	}
	return true
}

func (e *Engine) runStep(ctx context.Context, run *Run, step *Step, ps *PlanStep) error {
	step.Status = StepRunning
	e.store.SaveStep(ctx, run.ID, step)
	e.sink.EmitRun(RunEvent{RunID: run.ID, StepID: step.ID, NodeID: step.NodeID,
		Type: "step.started", Status: string(StepRunning), At: e.clock.Now().UTC()})

	// 幂等：request_id 由 run+step 派生，保证同一 Run 重跑不重复计费（INV-3）
	requestID := deriveRequestID(run.ID, step.ID)

	adapter, cred, ok := e.adapters.Resolve(ps.Capability, ps.ProviderID, ps.CredentialID)
	if !ok {
		step.Status = StepFailed
		return &provider.ProviderError{Class: provider.ClassPermanent, Code: platform.CodeInvalidRequest,
			Message: "no available credential for " + string(ps.Capability)}
	}

	// 输入资产需要转成 data URI 才能传给上游（exec 层负责，adapter 不接触存储）
	inputs, err := e.materializeInputs(ctx, run.WorkspaceID, ps.Inputs)
	if err != nil {
		return err
	}

	attempt := 0
	for {
		attempt++
		start := e.clock.Now()
		res, ierr := adapter.Invoke(ctx, cred, provider.Request{
			Capability: ps.Capability, Model: ps.Model, Prompt: ps.Prompt,
			Inputs: inputs, Params: ps.Params, Count: ps.Count, RequestID: requestID,
		})
		latency := e.clock.Since(start)

		att := Attempt{
			Index: attempt, ProviderID: cred.ProviderID, ModelID: ps.Model,
			RequestID: requestID, Latency: latency,
		}
		if ierr != nil {
			pe := toProviderError(ierr)
			att.Status = StepFailed
			att.Error = pe
			att.HTTPStatus = pe.HTTPStatus
			step.Attempts = append(step.Attempts, att)
			e.store.SaveAttempt(ctx, run.ID, step.ID, &att)

			if backoff := e.policy.Next(attempt, pe); backoff > 0 {
				step.Status = StepRetrying
				e.store.SaveStep(ctx, run.ID, step)
				e.sink.EmitRun(RunEvent{RunID: run.ID, StepID: step.ID, NodeID: step.NodeID,
					Type: "step.retrying", Status: string(StepRetrying), Attempt: &att,
					Error: pe, At: e.clock.Now().UTC()})
				select {
				case <-ctx.Done():
					return ErrCanceled
				case <-time.After(backoff):
				}
				continue
			}
			return pe
		}

		// 成功：落资产 → 回写画布 → 计量
		outputs, oerr := e.persistOutputs(ctx, run, step, ps, res)
		if oerr != nil {
			return oerr
		}
		usage := ApplyCost(e.adapters.Pricing(cred.ProviderID, ps.Model), res.Usage)
		att.Status = StepSucceeded
		att.Usage = usage
		att.RemoteTask = res.RemoteTask
		step.Attempts = append(step.Attempts, att)
		step.Outputs = append(step.Outputs, outputs...)
		step.Text = res.Text
		step.Status = StepSucceeded
		step.FinishedAt = ptrTime(e.clock.Now().UTC())
		e.store.SaveAttempt(ctx, run.ID, step.ID, &att)
		e.store.SaveStep(ctx, run.ID, step)

		if e.canvas != nil && !run.AdHoc {
			nr := graph.NodeResult{RunID: run.ID, StepID: step.ID, Primary: 0}
			for _, o := range outputs {
				nr.Variants = append(nr.Variants, graph.ResultVariant{
					AssetID: o.AssetID, Kind: graph.ResourceKind(o.Kind), Status: graph.NodeSucceeded,
				})
			}
			if res.Text != "" && len(outputs) == 0 {
				nr.Variants = append(nr.Variants, graph.ResultVariant{
					Text: res.Text, Kind: graph.KindText, Status: graph.NodeSucceeded,
				})
			}
			if err := e.canvas.WriteBack(ctx, run.CanvasID, step.NodeID, nr, graph.NodeSucceeded, run.ActorID); err != nil {
				// 回写失败不影响已产出的资产，但必须让用户看到（不静默）
				e.sink.EmitRun(RunEvent{RunID: run.ID, StepID: step.ID, NodeID: step.NodeID,
					Type: "step.writeback_failed", Status: string(StepSucceeded),
					Error: provider.ClassifyTransport(err), At: e.clock.Now().UTC()})
			}
		}

		ids := make([]string, 0, len(outputs))
		for _, o := range outputs {
			ids = append(ids, o.AssetID)
		}
		e.sink.EmitRun(RunEvent{RunID: run.ID, StepID: step.ID, NodeID: step.NodeID,
			Type: "step.succeeded", Status: string(StepSucceeded), Outputs: ids,
			Usage: &usage, At: e.clock.Now().UTC()})
		return nil
	}
}

func (e *Engine) materializeInputs(ctx context.Context, wsID string, inputs []provider.ResolvedInput) ([]provider.ResolvedInput, error) {
	out := make([]provider.ResolvedInput, 0, len(inputs))
	for _, in := range inputs {
		if in.AssetID == "" || in.Kind == "text" || strings.HasPrefix(in.AssetID, "data:") {
			out = append(out, in)
			continue
		}
		// 适配器只理解 data URI 或公网 URL：这里把资产内容读出来转 data URI。
		dataURI, err := e.assetAsDataURI(ctx, wsID, in.AssetID)
		if err != nil {
			return nil, err
		}
		in.AssetID = dataURI
		out = append(out, in)
	}
	return out, nil
}

func (e *Engine) persistOutputs(ctx context.Context, run *Run, step *Step, _ *PlanStep, res provider.Response) ([]OutputAsset, error) {
	out := make([]OutputAsset, 0, len(res.Assets))
	for i, ref := range res.Assets {
		source := map[string]string{"runId": run.ID, "stepId": step.ID}
		name := step.NodeID + "-" + itoa(i+1)
		var (
			id  string
			err error
		)
		if len(ref.Bytes) > 0 {
			id, err = e.blobs.StoreBytes(ctx, run.WorkspaceID, ref.Kind, ref.MIME, name,
				bytes.NewReader(ref.Bytes), int64(len(ref.Bytes)), "generated", source)
		} else if ref.URL != "" {
			id, err = e.blobs.StoreURL(ctx, run.WorkspaceID, ref.Kind, ref.MIME, name, ref.URL,
				nil, "generated", source)
		} else {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, OutputAsset{AssetID: id, Kind: ref.Kind, MIME: ref.MIME})
	}
	return out, nil
}

func (e *Engine) markCanceled(run *Run, current *Step) {
	for _, s := range run.Steps {
		switch s.Status {
		case StepSucceeded, StepFailed:
			continue
		default:
			s.Status = StepCanceled
			s.FinishedAt = ptrTime(e.clock.Now().UTC())
		}
	}
	if current != nil && current.Status == StepRunning {
		current.Status = StepCanceled
	}
}

func (e *Engine) finishRun(ctx context.Context, run *Run, status RunStatus, err *provider.ProviderError) {
	now := e.clock.Now().UTC()
	run.Status = status
	run.Error = err
	run.FinishedAt = &now
	run.recomputeUsage()
	_ = e.store.SaveRun(ctx, run)
	e.sink.EmitRun(RunEvent{RunID: run.ID, Type: "run.finished", Status: string(status),
		Usage: &run.Usage, Error: err, At: now})
}

// Cancel 取消一个正在执行的 Run。
func (e *Engine) Cancel(runID string) error {
	e.mu.Lock()
	rc, ok := e.runs[runID]
	e.mu.Unlock()
	if !ok {
		return platform.NewError(404, platform.CodeNotFound, "run is not executing")
	}
	rc.cancel()
	return nil
}

// Stop 停止引擎（等待在途 Run 结束由调用方控制）。
func (e *Engine) Stop() {
	e.mu.Lock()
	e.stopped = true
	for _, rc := range e.runs {
		rc.cancel()
	}
	e.mu.Unlock()
}

func deriveRequestID(runID, stepID string) string {
	return runID + ":" + stepID
}

func dependsOnNodes(doc *graph.CanvasDocument, nodeID string) []string {
	out := []string{}
	for _, e := range doc.UpstreamOf(nodeID) {
		src, ok := doc.Nodes[e.From.NodeID]
		if !ok {
			continue
		}
		if src.Type == graph.NodeTypeGeneration || src.Type == graph.NodeTypeRun {
			out = append(out, src.ID)
		}
	}
	return out
}

func toProviderError(err error) *provider.ProviderError {
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	de := platform.AsDomainError(err)
	if de != nil && de.Code != platform.CodeInternal {
		return &provider.ProviderError{Class: provider.ClassPermanent, Code: de.Code, Message: de.Message}
	}
	return provider.ClassifyTransport(err)
}

func ptrTime(t time.Time) *time.Time { return &t }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// RandomHex 生成随机十六进制串（幂等键兜底、trace 用）。
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// AssetReader 让 exec 能在不依赖 asset 包的前提下读取资产内容。
type AssetReader interface {
	ReadAsset(ctx context.Context, wsID, assetID string) (data []byte, mime string, err error)
}

// SetAssetReader 注入资产读取能力（由 cmd 装配）。
func (e *Engine) SetAssetReader(r AssetReader) { e.assets = r }

// assetAsDataURI 读取资产并转成 data URI（上游适配器只接受 data URI / URL）。
func (e *Engine) assetAsDataURI(ctx context.Context, wsID, assetID string) (string, error) {
	if e.assets == nil {
		return "", platform.NewError(500, platform.CodeInternal, "asset reader is not configured")
	}
	data, mime, err := e.assets.ReadAsset(ctx, wsID, assetID)
	if err != nil {
		return "", err
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
