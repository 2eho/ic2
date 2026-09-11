package exec

import (
	"context"
	"sync"

	"github.com/context-flow/ic/internal/platform"
)

// MemoryStore 是运行持久化的内存实现（SQL 实现见 sqlstore.go）。
type MemoryStore struct {
	mu       sync.RWMutex
	runs     map[string]*Run
	byIdem   map[string]string
	attempts map[string][]*Attempt
	order    []string
}

// NewMemoryStore 创建内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runs: map[string]*Run{}, byIdem: map[string]string{}, attempts: map[string][]*Attempt{}}
}

// SaveRun 见 Store。
func (s *MemoryStore) SaveRun(_ context.Context, run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.IdempotencyKey != "" {
		key := run.WorkspaceID + "|" + run.IdempotencyKey
		if existing, ok := s.byIdem[key]; ok && existing != run.ID {
			return platform.NewError(409, platform.CodeConflict, "duplicate idempotency key")
		}
		s.byIdem[key] = run.ID
	}
	if _, exists := s.runs[run.ID]; !exists {
		s.order = append(s.order, run.ID)
	}
	s.runs[run.ID] = cloneRun(run)
	return nil
}

// LoadRun 见 Store。
func (s *MemoryStore) LoadRun(_ context.Context, runID string) (*Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[runID]
	if !ok {
		return nil, platform.ErrNotFound("run")
	}
	return cloneRun(r), nil
}

// FindByIdempotencyKey 见 Store。
func (s *MemoryStore) FindByIdempotencyKey(_ context.Context, wsID, key string) (*Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byIdem[wsID+"|"+key]
	if !ok {
		return nil, platform.ErrNotFound("run")
	}
	return cloneRun(s.runs[id]), nil
}

// ListRuns 见 Store。
func (s *MemoryStore) ListRuns(_ context.Context, wsID, canvasID string, limit int, _ string) ([]*Run, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	out := []*Run{}
	for i := len(s.order) - 1; i >= 0 && len(out) < limit; i-- {
		r := s.runs[s.order[i]]
		if r == nil {
			continue
		}
		if wsID != "" && r.WorkspaceID != wsID {
			continue
		}
		if canvasID != "" && r.CanvasID != canvasID {
			continue
		}
		out = append(out, cloneRun(r))
	}
	return out, "", nil
}

// SaveStep 见 Store。
func (s *MemoryStore) SaveStep(_ context.Context, runID string, step *Step) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return platform.ErrNotFound("run")
	}
	cp := cloneStep(step)
	for i, existing := range r.Steps {
		if existing.ID == step.ID {
			r.Steps[i] = cp
			return nil
		}
	}
	r.Steps = append(r.Steps, cp)
	return nil
}

// SaveAttempt 见 Store。
func (s *MemoryStore) SaveAttempt(_ context.Context, runID, stepID string, a *Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := runID + "|" + stepID
	cp := *a
	for i, existing := range s.attempts[key] {
		if existing.RequestID == a.RequestID && existing.Index == a.Index {
			s.attempts[key][i] = &cp
			return nil
		}
	}
	s.attempts[key] = append(s.attempts[key], &cp)
	return nil
}

// ListResumableRuns 见 Store：有未完成异步任务的运行。
func (s *MemoryStore) ListResumableRuns(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for _, r := range s.runs {
		if r.Status != RunRunning {
			continue
		}
		for _, st := range r.Steps {
			if st.Status == StepRunning || st.Status == StepRetrying {
				out = append(out, r.ID)
				break
			}
		}
	}
	return out, nil
}

// Attempts 返回某步骤的全部尝试（测试断言用）。
func (s *MemoryStore) Attempts(runID, stepID string) []*Attempt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.attempts[runID+"|"+stepID]
	out := make([]*Attempt, 0, len(src))
	for _, a := range src {
		cp := *a
		out = append(out, &cp)
	}
	return out
}

func cloneRun(r *Run) *Run {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Steps = make([]*Step, 0, len(r.Steps))
	for _, s := range r.Steps {
		cp.Steps = append(cp.Steps, cloneStep(s))
	}
	if r.FinishedAt != nil {
		t := *r.FinishedAt
		cp.FinishedAt = &t
	}
	if r.Params != nil {
		cp.Params = map[string]any{}
		for k, v := range r.Params {
			cp.Params[k] = v
		}
	}
	return &cp
}

func cloneStep(s *Step) *Step {
	if s == nil {
		return nil
	}
	cp := *s
	cp.DependsOn = append([]string(nil), s.DependsOn...)
	cp.Attempts = append([]Attempt(nil), s.Attempts...)
	cp.Outputs = append([]OutputAsset(nil), s.Outputs...)
	if s.FinishedAt != nil {
		t := *s.FinishedAt
		cp.FinishedAt = &t
	}
	return &cp
}
