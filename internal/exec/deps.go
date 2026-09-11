package exec

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// ErrQueueFull 表示队列已满（背压信号）。
var ErrQueueFull = errors.New("exec queue is full")

// ErrCanceled 表示运行被取消。
var ErrCanceled = errors.New("run canceled")

// Store 是运行持久化抽象。
type Store interface {
	SaveRun(ctx context.Context, run *Run) error
	LoadRun(ctx context.Context, runID string) (*Run, error)
	FindByIdempotencyKey(ctx context.Context, wsID, key string) (*Run, error)
	ListRuns(ctx context.Context, wsID, canvasID string, limit int, cursor string) ([]*Run, string, error)
	SaveStep(ctx context.Context, runID string, step *Step) error
	SaveAttempt(ctx context.Context, runID, stepID string, a *Attempt) error
	// ListResumableRuns 返回需要重启后续跑的运行（有未完成的异步任务）。
	ListResumableRuns(ctx context.Context) ([]string, error)
}

// BlobWriter 把上游产出的字节流写入资产库并返回资产 ID（内容寻址去重由 asset 层负责）。
type BlobWriter interface {
	StoreBytes(ctx context.Context, wsID, kind, mime, name string, r io.Reader, size int64, origin string, source map[string]string) (string, error)
	StoreURL(ctx context.Context, wsID, kind, mime, name, url string, headers map[string]string, origin string, source map[string]string) (string, error)
}

// AdapterRegistry 按 provider id 取适配器。
type AdapterRegistry interface {
	Adapter(providerID string) (provider.Adapter, bool)
	// Resolve 按能力选择凭据（考虑优先级与健康度）。
	Resolve(cap provider.Capability, providerID, credentialID string) (provider.Adapter, provider.Credential, bool)
	// Pricing 返回价格表。
	Pricing(providerID, model string) provider.Pricing
}

// CanvasWriter 把执行结果回写画布（走标准 op 路径，与其他写入同源）。
type CanvasWriter interface {
	WriteBack(ctx context.Context, canvasID string, nodeID string, result graph.NodeResult, state graph.NodeState, actor string) error
}

// EventSink 推送运行事件（SSE）。
type EventSink interface {
	EmitRun(ev RunEvent)
	SubscribeRun(runID string) (<-chan RunEvent, func())
}

// RunEvent 是运行事件。
type RunEvent struct {
	RunID   string                  `json:"runId"`
	StepID  string                  `json:"stepId,omitempty"`
	NodeID  string                  `json:"nodeId,omitempty"`
	Type    string                  `json:"type"` // step.started | step.succeeded | step.failed | step.retrying | step.delta | run.finished
	Status  string                  `json:"status,omitempty"`
	Delta   string                  `json:"delta,omitempty"`
	Attempt *Attempt                `json:"attempt,omitempty"`
	Outputs []string                `json:"outputs,omitempty"`
	Usage   *provider.Usage         `json:"usage,omitempty"`
	Error   *provider.ProviderError `json:"error,omitempty"`
	At      time.Time               `json:"at"`
}

// MemorySink 是事件总线内存实现。
type MemorySink struct {
	mu   sync.RWMutex
	subs map[string][]chan RunEvent
}

// NewMemorySink 创建内存事件总线。
func NewMemorySink() *MemorySink { return &MemorySink{subs: map[string][]chan RunEvent{}} }

// EmitRun 见 EventSink。
func (s *MemorySink) EmitRun(ev RunEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.subs[ev.RunID] {
		select {
		case ch <- ev:
		default: // 慢消费者丢弃，不阻塞执行
		}
	}
}

// SubscribeRun 见 EventSink。
func (s *MemorySink) SubscribeRun(runID string) (<-chan RunEvent, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan RunEvent, 128)
	s.subs[runID] = append(s.subs[runID], ch)
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		list := s.subs[runID]
		for i, c := range list {
			if c == ch {
				s.subs[runID] = append(list[:i], list[i+1:]...)
				close(c)
				break
			}
		}
		if len(s.subs[runID]) == 0 {
			delete(s.subs, runID)
		}
	}
	return ch, cancel
}

// Clock 别名（便于测试注入）。
type Clock = platform.Clock
