package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// ---------------------------------------------------------------- fakes

type fakeAdapter struct {
	id      string
	caps    []provider.Capability
	calls   int32
	failN   int32 // 前 N 次调用失败（transient）
	failErr *provider.ProviderError
	delay   time.Duration
	// onInvoke 可自定义行为
	onInvoke func(req provider.Request) (provider.Response, error)
	mu       sync.Mutex
	requests []provider.Request
}

func (a *fakeAdapter) ID() string                          { return a.id }
func (a *fakeAdapter) Capabilities() []provider.Capability { return a.caps }
func (a *fakeAdapter) Stream(context.Context, provider.Credential, provider.Request) (provider.Stream, error) {
	return nil, provider.ErrStreamUnsupported{Adapter: a.id}
}
func (a *fakeAdapter) Poll(context.Context, provider.Credential, string) (provider.RemoteTask, error) {
	return provider.RemoteTask{}, nil
}
func (a *fakeAdapter) ListModels(context.Context, provider.Credential) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (a *fakeAdapter) FetchAsset(context.Context, provider.Credential, provider.AssetRef) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("")), "application/octet-stream", nil
}

func (a *fakeAdapter) Invoke(ctx context.Context, _ provider.Credential, req provider.Request) (provider.Response, error) {
	a.mu.Lock()
	a.requests = append(a.requests, req)
	a.mu.Unlock()
	n := atomic.AddInt32(&a.calls, 1)
	if a.delay > 0 {
		select {
		case <-ctx.Done():
			return provider.Response{}, provider.ClassifyTransport(ctx.Err())
		case <-time.After(a.delay):
		}
	}
	if a.onInvoke != nil {
		return a.onInvoke(req)
	}
	if a.failN > 0 && n <= a.failN {
		e := a.failErr
		if e == nil {
			e = &provider.ProviderError{Class: provider.ClassTransient, Code: "upstream_error", Message: "boom", HTTPStatus: 502}
		}
		return provider.Response{}, e
	}
	return provider.Response{
		Assets: []provider.AssetRef{{Kind: "image", Bytes: []byte("PNGDATA"), MIME: "image/png"}},
		Usage:  provider.Usage{Images: 1, TextTokensIn: 5},
	}, nil
}

type fakeRegistry struct {
	adapter provider.Adapter
	cred    provider.Credential
	pricing provider.Pricing
}

func (r *fakeRegistry) Adapter(string) (provider.Adapter, bool) { return r.adapter, true }
func (r *fakeRegistry) Resolve(provider.Capability, string, string) (provider.Adapter, provider.Credential, bool) {
	return r.adapter, r.cred, true
}
func (r *fakeRegistry) Pricing(string, string) provider.Pricing { return r.pricing }

type fakeBlobs struct {
	mu     sync.Mutex
	stored map[string][]byte
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{stored: map[string][]byte{}} }

func (b *fakeBlobs) StoreBytes(_ context.Context, _, kind, mime, _ string, r io.Reader, _ int64, _ string, _ map[string]string) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := fmt.Sprintf("as_%d_%s", len(b.stored)+1, kind)
	b.stored[id] = data
	_ = mime
	return id, nil
}

func (b *fakeBlobs) StoreURL(_ context.Context, _, kind, _, _, _ string, _ map[string]string, _ string, _ map[string]string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := fmt.Sprintf("as_url_%d_%s", len(b.stored)+1, kind)
	b.stored[id] = []byte("remote")
	return id, nil
}

type fakeCanvas struct {
	mu       sync.Mutex
	writes   []string
	states   []graph.NodeState
	failWith error
}

func (c *fakeCanvas) WriteBack(_ context.Context, _, nodeID string, _ graph.NodeResult, state graph.NodeState, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failWith != nil {
		return c.failWith
	}
	c.writes = append(c.writes, nodeID)
	c.states = append(c.states, state)
	return nil
}

type fakeAssets struct{}

func (fakeAssets) ReadAsset(context.Context, string, string) ([]byte, string, error) {
	return []byte("FAKEIMG"), "image/png", nil
}

// ---------------------------------------------------------------- helpers

func engineWith(adapter provider.Adapter, pricing provider.Pricing) (*Engine, *MemoryStore, *fakeBlobs, *fakeCanvas) {
	store := NewMemoryStore()
	blobs := newFakeBlobs()
	canvas := &fakeCanvas{}
	reg := &fakeRegistry{
		adapter: adapter,
		cred:    provider.Credential{ID: "cred_1", ProviderID: "openai", BaseURL: "http://example.com", AuthKind: "bearer", Secret: "sk-x"},
		pricing: pricing,
	}
	e := New(Options{Store: store, Blobs: blobs, Adapters: reg, Canvas: canvas, Clock: platform.SystemClock()})
	e.SetAssetReader(fakeAssets{})
	e.policy = RetryPolicy{MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
	return e, store, blobs, canvas
}

func genDoc() *graph.CanvasDocument {
	doc := graph.NewDocument("cv_1", "pj_1")
	doc.Nodes["p_1"] = graph.Node{
		ID: "p_1", Type: graph.NodeTypePrompt, Title: "提示词", Rect: graph.Rect{X: 0, Y: 0, W: 320, H: 220},
		Spec: graph.NodeSpec{"text": "一只猫"}, State: graph.NodeIdle,
	}
	genSpec, _ := graph.SchemaFor(graph.NodeTypeGeneration)
	doc.Nodes["g_1"] = graph.Node{
		ID: "g_1", Type: graph.NodeTypeGeneration, Title: "生成", Rect: graph.Rect{X: 400, Y: 0, W: 340, H: 260},
		Ports: genSpec.Ports, Spec: graph.NodeSpec{"capability": "image.generate", "model": "gpt-image-1", "outputCount": 1},
		State: graph.NodeIdle,
	}
	doc.Edges["e_1"] = graph.Edge{ID: "e_1",
		From: graph.Endpoint{NodeID: "p_1", PortID: "out"},
		To:   graph.Endpoint{NodeID: "g_1", PortID: "prompt"}, Kind: graph.KindText}
	return doc
}

func compilePlan(t *testing.T, doc *graph.CanvasDocument, targets []string) *Plan {
	t.Helper()
	c := NewCompiler(func(provider.Capability, string, string) (provider.Credential, bool) {
		return provider.Credential{ID: "cred_1", ProviderID: "openai", BaseURL: "http://example.com"}, true
	})
	plan, err := c.Compile(doc, targets)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return plan
}

// ---------------------------------------------------------------- tests

// 端到端：编译 → 执行 → 回写 → 计量。
func TestRunSucceedsAndWritesBack(t *testing.T) {
	adapter := &fakeAdapter{id: "openai", caps: []provider.Capability{provider.CapImageGenerate}}
	e, store, blobs, canvas := engineWith(adapter, provider.Pricing{ImagePerUnit: 40_000})
	doc := genDoc()
	plan := compilePlan(t, doc, []string{"g_1"})

	run, err := e.Create(context.Background(), RunRequest{
		WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Execute(context.Background(), run, plan, doc); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if run.Status != RunSucceeded {
		t.Fatalf("status=%s", run.Status)
	}
	if len(blobs.stored) != 1 {
		t.Fatalf("产出资产数=%d", len(blobs.stored))
	}
	if len(canvas.writes) != 1 || canvas.states[0] != graph.NodeSucceeded {
		t.Fatalf("回写异常: %v %v", canvas.writes, canvas.states)
	}
	// 计量：1 张图 * 40000 微元
	if run.Usage.Images != 1 || run.Usage.CostMicros != 40_000 {
		t.Fatalf("计量错误: %+v", run.Usage)
	}
	// 持久化可读
	loaded, err := store.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != RunSucceeded || len(loaded.Steps) != 1 {
		t.Fatalf("持久化异常: %+v", loaded)
	}
}

// ATK-02：同一 Idempotency-Key 连发 10 次 Run，只创建 1 个。
// ATK-02：同一 Idempotency-Key 连发 10 次 Run，只创建 1 个。
//
// 判据必须包含「返回的是同一个 Run」而不只是「只有一条记录」：
// 若第二次请求拿到一个新 Run，用户会看到重复运行，等于幂等失效。
func TestATK02IdempotentRunCreation(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, _, _ := engineWith(adapter, provider.Pricing{})
	ctx := context.Background()

	var first *Run
	for i := 0; i < 10; i++ {
		run, err := e.Create(ctx, RunRequest{
			WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"},
			ActorID: "u_1", IdempotencyKey: "same-key",
		})
		if err != nil {
			t.Fatalf("第 %d 次创建失败: %v", i, err)
		}
		if first == nil {
			first = run
		} else if run.ID != first.ID {
			t.Fatalf("幂等键失效：产生了新 Run %s != %s", run.ID, first.ID)
		}
	}
}

// 无幂等键时每次都创建新 Run（语义必须明确，不能静默合并）。
func TestWithoutIdempotencyKeyEachCallCreatesRun(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, _, _ := engineWith(adapter, provider.Pricing{})
	ctx := context.Background()
	a, _ := e.Create(ctx, RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", ActorID: "u_1"})
	b, _ := e.Create(ctx, RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", ActorID: "u_1"})
	if a.ID == b.ID {
		t.Fatal("无幂等键时应创建不同 Run")
	}
}

// 瞬时错误按策略重试，最终成功。
func TestRetryOnTransientError(t *testing.T) {
	adapter := &fakeAdapter{id: "openai", failN: 2}
	e, store, _, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	if err := e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc); err != nil {
		t.Fatalf("应重试成功: %v", err)
	}
	if run.Status != RunSucceeded {
		t.Fatalf("status=%s", run.Status)
	}
	atts := store.Attempts(run.ID, run.Steps[0].ID)
	if len(atts) != 3 {
		t.Fatalf("应有 3 次尝试，实际 %d", len(atts))
	}
	if atomic.LoadInt32(&adapter.calls) != 3 {
		t.Fatalf("调用次数=%d", adapter.calls)
	}
}

// 永久错误不重试。
func TestNoRetryOnPermanentError(t *testing.T) {
	adapter := &fakeAdapter{id: "openai", failN: 99, failErr: &provider.ProviderError{
		Class: provider.ClassPermanent, Code: platform.CodeUpstreamInvalid, Message: "bad request", HTTPStatus: 400,
	}}
	e, store, _, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	err := e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
	if err == nil {
		t.Fatal("应失败")
	}
	if run.Status != RunFailed {
		t.Fatalf("status=%s", run.Status)
	}
	if n := len(store.Attempts(run.ID, run.Steps[0].ID)); n != 1 {
		t.Fatalf("永久错误应只尝试 1 次，实际 %d", n)
	}
}

// 内容审核拒绝不重试，且分类正确（用户可据此区分「不是参数问题」）。
func TestContentPolicyNotRetried(t *testing.T) {
	adapter := &fakeAdapter{id: "openai", failN: 99, failErr: &provider.ProviderError{
		Class: provider.ClassContentPolicy, Code: platform.CodeContentPolicy, Message: "blocked", HTTPStatus: 400,
	}}
	e, store, _, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	_ = e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
	if n := len(store.Attempts(run.ID, run.Steps[0].ID)); n != 1 {
		t.Fatalf("内容审核应只尝试 1 次，实际 %d", n)
	}
	if run.Steps[0].Error.Class != provider.ClassContentPolicy {
		t.Fatalf("class=%s", run.Steps[0].Error.Class)
	}
}

// 取消：进行中的 Run 应快速停止，状态为 canceled。
func TestCancelRun(t *testing.T) {
	adapter := &fakeAdapter{id: "openai", delay: 2 * time.Second}
	e, _, _, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	done := make(chan error, 1)
	go func() {
		done <- e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := e.Cancel(run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后应快速结束")
	}
	if run.Status != RunCanceled {
		t.Fatalf("status=%s", run.Status)
	}
}

func TestCancelUnknownRun(t *testing.T) {
	e, _, _, _ := engineWith(&fakeAdapter{id: "openai"}, provider.Pricing{})
	if err := e.Cancel("nope"); err == nil {
		t.Fatal("未知 Run 应报错")
	}
}

// 部分成功：两个步骤，一个失败一个成功 → partial（显式建模）。
func TestPartialFailure(t *testing.T) {
	var calls int32
	adapter := &fakeAdapter{id: "openai", onInvoke: func(req provider.Request) (provider.Response, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 2 {
			return provider.Response{}, &provider.ProviderError{Class: provider.ClassPermanent, Code: "x", Message: "fail", HTTPStatus: 400}
		}
		return provider.Response{Assets: []provider.AssetRef{{Kind: "image", Bytes: []byte("d"), MIME: "image/png"}}}, nil
	}}
	e, _, _, _ := engineWith(adapter, provider.Pricing{})

	doc := graph.NewDocument("cv_1", "pj_1")
	doc.Nodes["p_1"] = graph.Node{ID: "p_1", Type: graph.NodeTypePrompt, Rect: graph.Rect{X: 0, Y: 0, W: 320, H: 220}, Spec: graph.NodeSpec{"text": "x"}}
	genSpec, _ := graph.SchemaFor(graph.NodeTypeGeneration)
	doc.Nodes["g_1"] = graph.Node{ID: "g_1", Type: graph.NodeTypeGeneration, Rect: graph.Rect{X: 400, Y: 0, W: 340, H: 260},
		Ports: genSpec.Ports, Spec: graph.NodeSpec{"capability": "image.generate"}}
	doc.Nodes["g_2"] = graph.Node{ID: "g_2", Type: graph.NodeTypeGeneration, Rect: graph.Rect{X: 800, Y: 0, W: 340, H: 260},
		Ports: genSpec.Ports, Spec: graph.NodeSpec{"capability": "image.generate"}}

	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1", "g_2"}, ActorID: "u_1"})
	plan := compilePlan(t, doc, []string{"g_1", "g_2"})
	_ = e.Execute(context.Background(), run, plan, doc)
	if run.Status != RunPartial {
		t.Fatalf("status=%s 期望 partial", run.Status)
	}
}

// 回写失败不得导致 Run 失败（资产已产出），但必须有事件暴露。
func TestWriteBackFailureIsObservable(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, blobs, canvas := engineWith(adapter, provider.Pricing{})
	canvas.failWith = errors.New("canvas is locked")
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	ch, cancelSub := e.Sink().SubscribeRun(run.ID)
	defer cancelSub()
	go func() {
		_ = e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
	}()

	sawWritebackFailure := false
	deadline := time.After(2 * time.Second)
	for !sawWritebackFailure {
		select {
		case ev := <-ch:
			if ev.Type == "step.writeback_failed" {
				sawWritebackFailure = true
			}
		case <-deadline:
			t.Fatal("未收到回写失败事件（不能静默）")
		}
	}
	if len(blobs.stored) != 1 {
		t.Fatal("资产应已产出")
	}
}

// 事件流：started / succeeded / finished 必须齐备且带计量。
func TestEventSequence(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, _, _ := engineWith(adapter, provider.Pricing{ImagePerUnit: 1})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	ch, cancel := e.Sink().SubscribeRun(run.ID)
	defer cancel()
	go func() { _ = e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc) }()

	types := []string{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			types = append(types, ev.Type)
			if ev.Type == "run.finished" {
				goto done
			}
		case <-deadline:
			t.Fatalf("事件未齐备: %v", types)
		}
	}
done:
	want := []string{"step.started", "step.succeeded", "run.finished"}
	for _, w := range want {
		found := false
		for _, got := range types {
			if got == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少事件 %s，实际 %v", w, types)
		}
	}
}

// request_id 由 run+step 派生：同一 Run 重跑不产生新的计费维度（INV-3 前置条件）。
func TestRequestIDIsStable(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, _, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	_ = e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.requests) == 0 {
		t.Fatal("未记录请求")
	}
	want := deriveRequestID(run.ID, run.Steps[0].ID)
	if adapter.requests[0].RequestID != want {
		t.Fatalf("requestID=%q 期望 %q", adapter.requests[0].RequestID, want)
	}
}

func TestAdHocRunDoesNotWriteBack(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, _, canvas := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", TargetNodes: []string{"g_1"}, ActorID: "u_1", AdHoc: true})
	if err := e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc); err != nil {
		t.Fatal(err)
	}
	if len(canvas.writes) != 0 {
		t.Fatal("ad-hoc 运行不应回写画布")
	}
}

func TestEngineStopCancelsInFlight(t *testing.T) {
	adapter := &fakeAdapter{id: "openai", delay: time.Second}
	e, _, _, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	done := make(chan struct{})
	go func() {
		_ = e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	e.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 应取消在途 Run")
	}
	// 停止后不再接受新 Run
	_, err := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", ActorID: "u"})
	_ = err // Create 本身允许，但 Execute 应拒绝
	if err := e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc); err == nil {
		t.Fatal("停止后 Execute 应报错")
	}
}

func TestBytesRoundTripThroughBlobs(t *testing.T) {
	adapter := &fakeAdapter{id: "openai"}
	e, _, blobs, _ := engineWith(adapter, provider.Pricing{})
	doc := genDoc()
	run, _ := e.Create(context.Background(), RunRequest{WorkspaceID: "ws_1", CanvasID: "cv_1", TargetNodes: []string{"g_1"}, ActorID: "u_1"})
	_ = e.Execute(context.Background(), run, compilePlan(t, doc, []string{"g_1"}), doc)
	for _, data := range blobs.stored {
		if !bytes.Equal(data, []byte("PNGDATA")) {
			t.Fatalf("资产内容不符: %q", data)
		}
	}
}
