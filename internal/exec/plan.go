// Package exec 把画布的一部分编译成 DAG 并可靠地执行：
// Run → Step → Attempt 三层，支持重试分类、取消、计量与结果回写。
package exec

import (
	"time"

	"github.com/context-flow/ic/internal/provider"
)

// StepKind 是步骤类型。
type StepKind string

// 步骤类型。
const (
	StepGenerate  StepKind = "generate"
	StepTransform StepKind = "transform"
	StepFetch     StepKind = "fetch"
	StepAgent     StepKind = "agent"
)

// PlanStep 是编译后的一个步骤（拓扑序）。
type PlanStep struct {
	ID        string
	NodeID    string
	Kind      StepKind
	DependsOn []string
	// Capability/Model/Params 是执行参数（编译期已从节点 spec 解析并快照）。
	Capability   provider.Capability
	Model        string
	ProviderID   string
	CredentialID string
	Params       map[string]any
	Count        int
	// Inputs 是编译期解析的输入快照。**必须快照**：之后用户改上游节点不影响重放。
	Inputs []provider.ResolvedInput
	Prompt string
	// WriteBack 声明结果回写到哪个节点（执行完成 ≠ 改画布，回写走标准 op 路径）。
	WriteBack WriteBack
}

// WriteBack 描述结果的回写目标。
type WriteBack struct {
	NodeID string
	PortID string
}

// Plan 是一次运行的执行计划。
type Plan struct {
	Steps    []PlanStep
	Order    []string // 步骤 ID 的拓扑序
	Warnings []string
}

// RunStatus 是运行状态。
type RunStatus string

// 运行状态。
const (
	RunPending   RunStatus = "pending"
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
	RunPartial   RunStatus = "partial"
)

// StepStatus 是步骤状态。
type StepStatus string

// 步骤状态。
const (
	StepPending   StepStatus = "pending"
	StepReady     StepStatus = "ready"
	StepRunning   StepStatus = "running"
	StepRetrying  StepStatus = "retrying"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	StepCanceled  StepStatus = "canceled"
)

// Terminal 表示是否为终态。
func (s StepStatus) Terminal() bool {
	switch s {
	case StepSucceeded, StepFailed, StepSkipped, StepCanceled:
		return true
	}
	return false
}

// Attempt 是一次上游调用尝试。
type Attempt struct {
	Index      int
	ProviderID string
	ModelID    string
	RequestID  string
	Status     StepStatus
	HTTPStatus int
	Latency    time.Duration
	Error      *provider.ProviderError
	Usage      provider.Usage
	// RemoteTask 持久化异步任务 ID，进程重启后可续查（ATK-15）。
	RemoteTask *provider.RemoteTask
}

// Step 是运行中的一个步骤。
type Step struct {
	ID         string
	NodeID     string
	Kind       StepKind
	Status     StepStatus
	DependsOn  []string
	Attempts   []Attempt
	Outputs    []OutputAsset
	Text       string
	Error      *provider.ProviderError
	StartedAt  time.Time
	FinishedAt *time.Time
}

// OutputAsset 是步骤产出的资产（已入库）。
type OutputAsset struct {
	AssetID string
	Kind    string
	MIME    string
}

// Run 是一次运行。
type Run struct {
	ID             string
	WorkspaceID    string
	CanvasID       string
	ProjectID      string
	Trigger        string
	Status         RunStatus
	TargetNodes    []string
	Params         map[string]any
	Steps          []*Step
	Usage          provider.Usage
	Error          *provider.ProviderError
	ActorID        string
	IdempotencyKey string
	StartedAt      time.Time
	FinishedAt     *time.Time
	// AdHoc 表示不落画布的直通生成（工作台/调试）。
	AdHoc bool
}

// Usage 聚合计量。
func (r *Run) recomputeUsage() provider.Usage {
	var u provider.Usage
	for _, s := range r.Steps {
		for _, a := range s.Attempts {
			u = u.Add(a.Usage)
		}
	}
	r.Usage = u
	return u
}

// Duration 返回运行耗时。
func (r *Run) Duration() time.Duration {
	end := time.Now()
	if r.FinishedAt != nil {
		end = *r.FinishedAt
	}
	return end.Sub(r.StartedAt)
}

// Progress 返回完成比例（0–1）。
func (r *Run) Progress() float64 {
	if len(r.Steps) == 0 {
		return 0
	}
	done := 0
	for _, s := range r.Steps {
		if s.Status.Terminal() {
			done++
		}
	}
	return float64(done) / float64(len(r.Steps))
}
