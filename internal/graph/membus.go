package graph

import "sync"

// MemoryBus 是进程内事件总线（Redis 实现见 internal/eventbus）。
type MemoryBus struct {
	mu   sync.RWMutex
	subs map[string]map[int]chan Event
	seq  int
	// 可丢弃事件缓冲：慢消费者不阻塞发布者（见 03 §2.6 背压策略）。
	buffer int
}

// NewMemoryBus 创建进程内总线。
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{subs: map[string]map[int]chan Event{}, buffer: 256}
}

// Publish 发布事件（非阻塞）。
func (b *MemoryBus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[ev.CanvasID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe 订阅某画布事件。
func (b *MemoryBus) Subscribe(canvasID string) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := b.seq
	ch := make(chan Event, b.buffer)
	if b.subs[canvasID] == nil {
		b.subs[canvasID] = map[int]chan Event{}
	}
	b.subs[canvasID][id] = ch
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if m, ok := b.subs[canvasID]; ok {
			if c, ok := m[id]; ok {
				delete(m, id)
				close(c)
			}
			if len(m) == 0 {
				delete(b.subs, canvasID)
			}
		}
	}
	return ch, cancel
}

// SubscriberCount 返回订阅者数量（可观测指标）。
func (b *MemoryBus) SubscriberCount(canvasID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[canvasID])
}
