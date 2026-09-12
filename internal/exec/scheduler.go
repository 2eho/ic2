package exec

import (
	"context"
	"sync"
)

// Queue 是执行队列抽象（内存实现用于 standalone，Redis Stream 用于集群）。
type Queue interface {
	Enqueue(runID string) error
	Dequeue(ctx context.Context) (string, bool)
	Len() int
	Close() error
}

// MemoryQueue 是进程内有界队列。
type MemoryQueue struct {
	mu     sync.Mutex
	items  []string
	notify chan struct{}
	closed bool
	cap    int
}

// NewMemoryQueue 创建有界内存队列。
func NewMemoryQueue(capacity int) *MemoryQueue {
	if capacity <= 0 {
		capacity = 1024
	}
	return &MemoryQueue{notify: make(chan struct{}, 1), cap: capacity}
}

// Enqueue 见 Queue。
func (q *MemoryQueue) Enqueue(runID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	if len(q.items) >= q.cap {
		// 队列水位保护：显式拒绝而不是静默丢弃
		return ErrQueueFull
	}
	q.items = append(q.items, runID)
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return nil
}

// Dequeue 见 Queue。
func (q *MemoryQueue) Dequeue(ctx context.Context) (string, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			id := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return id, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-q.notify:
		}
	}
}

// Len 见 Queue。
func (q *MemoryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Close 见 Queue。
func (q *MemoryQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return nil
}
