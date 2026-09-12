package graph

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// MemoryStore 是 Store 的内存实现（测试替身 + 无 DB 的 standalone 演示）。
// 解耦清单要求每个抽象至少两个实现，SQL 实现在 internal/graph/sqlstore.go。
type MemoryStore struct {
	mu      sync.RWMutex
	docs    map[string]*CanvasDocument
	metas   map[string]CanvasMeta
	ops     map[string][]Record
	version map[string]int64
	// versionIndex 记录每个版本对应的最后一条 op seq，用于按版本号定位增量。
	versionIndex struct {
		mu sync.RWMutex
		m  map[string][]versionEntry
	}
}

type versionEntry struct {
	version int64
	seq     int64
}

// NewMemoryStore 创建内存存储。
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		docs:    map[string]*CanvasDocument{},
		metas:   map[string]CanvasMeta{},
		ops:     map[string][]Record{},
		version: map[string]int64{},
	}
	s.versionIndex.m = map[string][]versionEntry{}
	return s
}

// GetDocument 见 Store。
func (s *MemoryStore) GetDocument(_ context.Context, canvasID string) (*CanvasDocument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.docs[canvasID]
	if !ok {
		return nil, platform.ErrNotFound("canvas")
	}
	return d.Clone(), nil
}

// CreateDocument 见 Store。
func (s *MemoryStore) CreateDocument(_ context.Context, doc *CanvasDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.docs[doc.ID]; ok {
		return platform.NewError(409, CodeConflict, "canvas already exists")
	}
	s.docs[doc.ID] = doc.Clone()
	s.metas[doc.ID] = CanvasMeta{ID: doc.ID, ProjectID: doc.ProjectID, Version: doc.Version,
		Settings: doc.Settings, UpdatedAt: doc.UpdatedAt}
	s.ops[doc.ID] = nil
	return nil
}

// UpdateMeta 见 Store。
func (s *MemoryStore) UpdateMeta(_ context.Context, canvasID, name string, settings CanvasSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.metas[canvasID]
	if !ok {
		return platform.ErrNotFound("canvas")
	}
	m.Name = name
	m.Settings = settings
	s.metas[canvasID] = m
	if d, ok := s.docs[canvasID]; ok {
		d.Settings = settings
	}
	return nil
}

// DeleteDocument 见 Store。
func (s *MemoryStore) DeleteDocument(_ context.Context, canvasID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.docs[canvasID]; !ok {
		return platform.ErrNotFound("canvas")
	}
	delete(s.docs, canvasID)
	delete(s.metas, canvasID)
	delete(s.ops, canvasID)
	return nil
}

// AppendOps 见 Store。
func (s *MemoryStore) AppendOps(_ context.Context, canvasID string, baseVersion int64,
	doc *CanvasDocument, ops []json.RawMessage, actor string, now time.Time) (int64, []Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.docs[canvasID]
	if !ok {
		return 0, nil, platform.ErrNotFound("canvas")
	}
	if cur.Version != baseVersion {
		return 0, nil, platform.NewError(409, CodeConflict, "version conflict").
			WithDetail("baseVersion", baseVersion).WithDetail("serverVersion", cur.Version)
	}
	next := doc.Clone()
	next.Version = cur.Version + 1
	next.UpdatedAt = now
	seq := s.version[canvasID]
	records := make([]Record, 0, len(ops))
	for _, raw := range ops {
		seq++
		records = append(records, Record{Seq: seq, CanvasID: canvasID, ActorID: actor,
			Op: append(json.RawMessage(nil), raw...), Version: next.Version, CreatedAt: now})
	}
	s.version[canvasID] = seq
	s.docs[canvasID] = next
	// version 索引用于按 baseVersion 定位增量 op（语义同 canvas_ops.version）。
	s.versionIndex.m[canvasID] = append(s.versionIndex.m[canvasID], versionEntry{version: next.Version, seq: seq})
	s.ops[canvasID] = append(s.ops[canvasID], records...)
	m := s.metas[canvasID]
	m.Version = next.Version
	m.Nodes = len(next.Nodes)
	m.Settings = next.Settings
	m.UpdatedAt = now
	s.metas[canvasID] = m
	return next.Version, records, nil
}

// ListOps 见 Store。
func (s *MemoryStore) ListOps(_ context.Context, canvasID string, since int64, limit int) ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 200
	}
	out := []Record{}
	for _, r := range s.ops[canvasID] {
		if r.Seq > since {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// ListCanvases 见 Store。
func (s *MemoryStore) ListCanvases(_ context.Context, projectID string) ([]CanvasMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []CanvasMeta{}
	for _, m := range s.metas {
		if m.ProjectID == projectID {
			out = append(out, m)
		}
	}
	return out, nil
}

// ListOpsSinceVersion 见 Store：按版本号定位增量 op。
func (s *MemoryStore) ListOpsSinceVersion(_ context.Context, canvasID string, sinceVersion int64, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 200
	}
	// 找到 sinceVersion 对应的最后 seq，取其后的 op。
	s.versionIndex.mu.RLock()
	entries := append([]versionEntry(nil), s.versionIndex.m[canvasID]...)
	s.versionIndex.mu.RUnlock()
	var seq int64
	for _, e := range entries {
		if e.version <= sinceVersion {
			seq = e.seq
		} else {
			break
		}
	}
	return s.ListOps(context.Background(), canvasID, seq, limit)
}
