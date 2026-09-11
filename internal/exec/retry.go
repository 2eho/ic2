package exec

import (
	"math"
	"math/rand"
	"time"

	"github.com/context-flow/ic/internal/provider"
)

// RetryPolicy 控制重试行为。所有边界值可配置并在 UI 可见（不做静默经验值）。
type RetryPolicy struct {
	// MaxAttempts 是含首次在内的总尝试次数。
	MaxAttempts int
	// BaseBackoff 是指数退避基数。
	BaseBackoff time.Duration
	// MaxBackoff 是单次退避上限。
	MaxBackoff time.Duration
	// Jitter 是抖动比例（0–1），避免惊群。
	Jitter float64
}

// DefaultRetryPolicy 默认最多 3 次、400ms 起、上限 30s、20% 抖动。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseBackoff: 400 * time.Millisecond, MaxBackoff: 30 * time.Second, Jitter: 0.2}
}

// Next 计算第 attempt 次失败后的退避时长（attempt 从 1 开始）。
// 返回 0 表示不再重试。
func (p RetryPolicy) Next(attempt int, err *provider.ProviderError) time.Duration {
	if !p.shouldRetry(attempt, err) {
		return 0
	}
	// 上游明确给出 Retry-After 时优先尊重它。
	if err != nil && err.Class == provider.ClassRateLimited && err.RetryAfter > 0 {
		return clampDuration(err.RetryAfter, p.MaxBackoff)
	}
	base := float64(p.BaseBackoff) * math.Pow(2, float64(attempt-1))
	if base > float64(p.MaxBackoff) {
		base = float64(p.MaxBackoff)
	}
	if p.Jitter > 0 {
		// 抖动范围 [base*(1-j), base*(1+j)]
		base *= 1 + (rand.Float64()*2-1)*p.Jitter
	}
	return clampDuration(time.Duration(base), p.MaxBackoff)
}

func (p RetryPolicy) shouldRetry(attempt int, err *provider.ProviderError) bool {
	if attempt >= p.MaxAttempts {
		return false
	}
	if err == nil {
		return false
	}
	switch err.Class {
	case provider.ClassTransient, provider.ClassRateLimited:
		return true
	}
	// permanent / content_policy / canceled 一律不重试
	return false
}

// ShouldRetry 对外暴露判定（UI 展示「将自动重试 N 次」需要它）。
func (p RetryPolicy) ShouldRetry(attempt int, err *provider.ProviderError) bool {
	return p.shouldRetry(attempt, err)
}

func clampDuration(d, max time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > max {
		return max
	}
	return d
}
