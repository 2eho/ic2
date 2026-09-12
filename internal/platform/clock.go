package platform

import (
	"sync"
	"time"
)

// Clock 抽象时间来源。租约、限额、重试退避一律用它，
// 禁止直接 time.Now()，否则时钟回拨类故障无法测试（见 docs/design/11 §2.2）。
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
}

type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

// SystemClock 使用真实的墙上时钟。
func SystemClock() Clock { return realClock{} }

// FakeClock 是测试替身（解耦清单要求每个抽象至少两个实现）。
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock 创建假时钟。
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

// Now 返回当前假时刻。
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since 返回相对假时刻的间隔。
func (c *FakeClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// Advance 手动推进假时钟。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set 直接设置假时钟时刻（用于模拟时钟回拨）。
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
