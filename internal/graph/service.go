package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// Event 是画布领域事件（SSE 广播用）。
type Event struct {
	CanvasID string          `json:"canvasId"`
	Seq      int64           `json:"seq"`
	Type     string          `json:"type"`
	Version  int64           `json:"version"`
	ActorID  string          `json:"actorId"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	At       time.Time       `json:"at"`
}

// Bus 是事件总线抽象（进程内 + Redis 两种实现，见 08-infra §2.2）。
type Bus interface {
	Publish(ev Event)
	Subscribe(canvasID string) (<-chan Event, func())
}

// Service 是画布用例编排层。
type Service struct {
	store Store
	bus   Bus
	clock platform.Clock
	ids   platform.IDGen

	// 尝试锁：同一画布串行化提交，避免同实例内竞态放大（跨实例靠版本号+DB 约束）。
	locks sync.Map // canvasID -> *sync.Mutex
}

// NewService 装配 graph 服务。
func NewService(store Store, bus Bus, clock platform.Clock, ids platform.IDGen) *Service {
	if clock == nil {
		clock = platform.SystemClock()
	}
	if ids == nil {
		ids = platform.DefaultIDGen()
	}
	return &Service{store: store, bus: bus, clock: clock, ids: ids}
}

func (s *Service) lock(canvasID string) func() {
	v, _ := s.locks.LoadOrStore(canvasID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Create 创建画布。
func (s *Service) Create(ctx context.Context, projectID, name string) (*CanvasMeta, error) {
	if projectID == "" {
		return nil, platform.ErrInvalid("projectId is required")
	}
	id := s.ids.NewID("cv")
	doc := NewDocument(id, projectID)
	doc.UpdatedAt = s.clock.Now()
	if err := s.store.CreateDocument(ctx, doc); err != nil {
		return nil, err
	}
	if name == "" {
		name = "未命名画布"
	}
	if err := s.store.UpdateMeta(ctx, id, name, doc.Settings); err != nil {
		return nil, err
	}
	return &CanvasMeta{ID: id, ProjectID: projectID, Name: name, Version: doc.Version,
		Nodes: 0, Settings: doc.Settings, UpdatedAt: doc.UpdatedAt}, nil
}

// Get 读取文档（version 为 0 表示最新）。
func (s *Service) Get(ctx context.Context, canvasID string) (*CanvasDocument, error) {
	if !ValidID(canvasID) {
		return nil, platform.ErrNotFound("canvas")
	}
	return s.store.GetDocument(ctx, canvasID)
}

// List 列出项目下画布。
func (s *Service) List(ctx context.Context, projectID string) ([]CanvasMeta, error) {
	return s.store.ListCanvases(ctx, projectID)
}

// AppendOps 是唯一写入口：所有写入路径（用户编辑、执行回写、Agent、导入）都走它。
func (s *Service) AppendOps(ctx context.Context, canvasID string, baseVersion int64,
	rawOps []json.RawMessage, actor string) (ApplyResult, *CanvasDocument, error) {

	if !ValidID(canvasID) {
		return ApplyResult{}, nil, platform.ErrNotFound("canvas")
	}
	if actor == "" {
		// INV-8：写操作必须可归属。
		return ApplyResult{}, nil, platform.NewError(403, CodeForbidden, "actor is required")
	}
	if len(rawOps) == 0 {
		doc, err := s.store.GetDocument(ctx, canvasID)
		if err != nil {
			return ApplyResult{}, nil, err
		}
		return ApplyResult{Version: doc.Version}, doc, nil
	}
	unlock := s.lock(canvasID)
	defer unlock()

	current, err := s.store.GetDocument(ctx, canvasID)
	if err != nil {
		return ApplyResult{}, nil, err
	}
	now := s.clock.Now()

	// 版本一致：直接应用。不一致：尝试 rebase（可交换的 op 集合）。
	//
	// baseVersion 必须是**客户端读到文档时的版本号**，不允许用 0 表示「随便」。
	// 旧实现把 0 当成「以服务端当前版本为准」，带来一个真实的静默丢数据缺陷：
	// 客户端在版本 5 读到文档、版本 6 时提交，若它传 0（或漏传），服务端会
	// 悄悄把 base 改成 6 并直接应用——本应触发的冲突检测被跳过，用户看不到
	// 任何提示就覆盖了别人的改动（ATK-11 实测：期望 409，实得 200）。
	//
	// 建画布时文档版本就是 0，因此「首次写入」天然合法；任何在 0 之后提交的
	// 客户端都必然持有非零版本号。真正"未知版本"的调用方应显式传 force。
	base := baseVersion
	var rebasedKinds []OpKind
	if base < 0 || base > current.Version {
		return ApplyResult{}, nil, platform.NewError(409, CodeConflict,
			"base version is ahead of server version").
			WithDetail("baseVersion", base).WithDetail("serverVersion", current.Version)
	}
	if base != current.Version {
		missing, lerr := s.store.ListOpsSinceVersion(ctx, canvasID, base, int(current.Version-base)+16)
		if lerr != nil {
			return ApplyResult{}, nil, lerr
		}
		mergeable, kinds := rebaseable(rawOps, missing)
		if !mergeable {
			return ApplyResult{}, nil, platform.NewError(409, CodeConflict,
				"concurrent modification on the same field").
				WithDetail("baseVersion", base).WithDetail("serverVersion", current.Version).
				WithDetail("authoritative", current)
		}
		rebasedKinds = kinds
	}

	result, next, err := Apply(current, rawOps, actor, now)
	if err != nil {
		return ApplyResult{}, nil, err
	}
	result.Rebased = len(rebasedKinds) > 0
	if len(rebasedKinds) > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("auto-rebased against %d in-flight ops of kinds %v", len(rebasedKinds), rebasedKinds))
	}
	version, records, err := s.store.AppendOps(ctx, canvasID, current.Version, next, rawOps, actor, now)
	if err != nil {
		return ApplyResult{}, nil, err
	}
	result.Version = version
	next.Version = version

	if s.bus != nil {
		for _, r := range records {
			s.bus.Publish(Event{
				CanvasID: canvasID, Seq: r.Seq, Type: "canvas.op", Version: r.Version,
				ActorID: actor, Payload: r.Op, At: r.CreatedAt,
			})
		}
	}
	return result, next, nil
}

// rebaseable 判断待提交的 op 集合与在途 op 是否「可交换」：
// 只有当两者操作的目标节点/边集合不相交，或双方都只是移动时，才允许自动合并。
func rebaseable(incoming []json.RawMessage, inflight []Record) (bool, []OpKind) {
	kinds := make([]OpKind, 0, len(inflight))
	touchedInflight := map[string]bool{}
	for _, r := range inflight {
		op, payload, err := DecodeOp(r.Op)
		if err != nil {
			return false, nil
		}
		kinds = append(kinds, op.Kind)
		switch p := payload.(type) {
		case *AddNodePayload:
			touchedInflight["node:"+p.Node.ID] = true
		case *RemoveNodePayload:
			touchedInflight["node:"+p.ID] = true
		case *MoveNodePayload:
			touchedInflight["geom:"+p.ID] = true
		case *ResizeNodePayload:
			touchedInflight["geom:"+p.ID] = true
		case *SetSpecPayload:
			touchedInflight["spec:"+p.ID] = true
		case *SetTitlePayload:
			touchedInflight["title:"+p.ID] = true
		case *SetStatePayload:
			touchedInflight["state:"+p.ID] = true
		case *AddEdgePayload:
			touchedInflight["edge:"+p.Edge.ID] = true
		case *RemoveEdgePayload:
			touchedInflight["edge:"+p.ID] = true
		case *SetParentPayload:
			touchedInflight["parent:"+p.ID] = true
		case *GroupPayload:
			for _, id := range p.NodeIDs {
				touchedInflight["parent:"+id] = true
			}
		default:
			// 视口/设置/分组等全局 op 无法安全自动合并，要求调用方重取版本。
			touchedInflight["global"] = true
		}
	}
	if touchedInflight["global"] {
		return false, kinds
	}
	for _, raw := range incoming {
		op, payload, err := DecodeOp(raw)
		if err != nil {
			return false, nil
		}
		switch p := payload.(type) {
		case *AddNodePayload:
			if touchedInflight["node:"+p.Node.ID] {
				return false, kinds
			}
		case *RemoveNodePayload:
			if touchedInflight["node:"+p.ID] || touchedInflight["spec:"+p.ID] || touchedInflight["geom:"+p.ID] {
				return false, kinds
			}
		case *MoveNodePayload:
			if touchedInflight["spec:"+p.ID] || touchedInflight["node:"+p.ID] || touchedInflight["title:"+p.ID] {
				return false, kinds
			}
		case *ResizeNodePayload:
			if touchedInflight["spec:"+p.ID] || touchedInflight["node:"+p.ID] {
				return false, kinds
			}
		case *SetSpecPayload:
			if touchedInflight["spec:"+p.ID] || touchedInflight["node:"+p.ID] {
				return false, kinds
			}
		case *SetTitlePayload:
			if touchedInflight["title:"+p.ID] || touchedInflight["node:"+p.ID] {
				return false, kinds
			}
		case *SetStatePayload:
			if touchedInflight["state:"+p.ID] || touchedInflight["node:"+p.ID] {
				return false, kinds
			}
		case *AddEdgePayload, *RemoveEdgePayload:
			// 连线冲突风险低（ID 唯一），但若目标节点正被删除则拒绝。
			var nodeID string
			if a, ok := payload.(*AddEdgePayload); ok {
				nodeID = a.Edge.To.NodeID
			}
			if touchedInflight["node:"+nodeID] {
				return false, kinds
			}
		case *SetParentPayload:
			if touchedInflight["parent:"+p.ID] || touchedInflight["node:"+p.ID] {
				return false, kinds
			}
		default:
			return false, kinds
		}
		_ = op
	}
	return true, kinds
}

// Subscribe 订阅画布事件，返回通道与取消函数。
func (s *Service) Subscribe(canvasID string) (<-chan Event, func()) {
	if s.bus == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	return s.bus.Subscribe(canvasID)
}

// OpenReplay 校验不变量 INV-1：op 日志重放必须等于权威快照。
func (s *Service) OpenReplay(ctx context.Context, canvasID string) error {
	records, err := s.store.ListOps(ctx, canvasID, 0, 1_000_000)
	if err != nil {
		return err
	}
	doc, err := s.store.GetDocument(ctx, canvasID)
	if err != nil {
		return err
	}
	raws := make([][]byte, 0, len(records))
	for _, r := range records {
		raws = append(raws, r.Op)
	}
	replayed, err := Replay(doc, raws, doc.UpdatedAt)
	if err != nil {
		return err
	}
	if diff := DiffDocuments(doc, replayed); diff != "" {
		return fmt.Errorf("INV-1 violated: %s", diff)
	}
	return nil
}

// IsNotFound 判断错误是否为 not found。
func IsNotFound(err error) bool {
	var de *platform.DomainError
	if errors.As(err, &de) {
		return de.Code == CodeNotFound
	}
	return false
}

// WorkspaceOf 反查画布所属工作区（供授权使用）。
func (s *Service) WorkspaceOf(ctx context.Context, canvasID string) (string, error) {
	if !ValidID(canvasID) {
		return "", platform.ErrNotFound("canvas")
	}
	type resolver interface {
		WorkspaceOf(ctx context.Context, canvasID string) (string, error)
	}
	if r, ok := s.store.(resolver); ok {
		return r.WorkspaceOf(ctx, canvasID)
	}
	return "", platform.NewError(501, platform.CodeNotImplemented, "store does not support workspace lookup")
}

// 编译期断言：SQLStore 必须实现 WorkspaceOf（授权路径依赖它）。
var _ interface {
	WorkspaceOf(ctx context.Context, canvasID string) (string, error)
} = (*SQLStore)(nil)
