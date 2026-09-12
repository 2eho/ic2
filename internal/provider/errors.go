package provider

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// ErrorClass 决定重试策略（docs/design/05 §3.2）。
type ErrorClass string

// 错误分类。
const (
	ClassTransient     ErrorClass = "transient"
	ClassRateLimited   ErrorClass = "rate_limited"
	ClassPermanent     ErrorClass = "permanent"
	ClassContentPolicy ErrorClass = "content_policy"
	ClassCanceled      ErrorClass = "canceled"
)

// ProviderError 是上游调用错误。
type ProviderError struct {
	Class      ErrorClass    `json:"class"`
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	HTTPStatus int           `json:"httpStatus,omitempty"`
	RetryAfter time.Duration `json:"-"`
}

// Error 实现 error。
func (e *ProviderError) Error() string {
	return string(e.Class) + ": " + e.Message
}

// Retryable 表示是否应重试。
func (e *ProviderError) Retryable() bool {
	return e.Class == ClassTransient || e.Class == ClassRateLimited
}

// ClassifyHTTP 把 HTTP 状态码与响应体分类（对齐原项目 readStatusError 的语义）。
func ClassifyHTTP(status int, body string) *ProviderError {
	pe := &ProviderError{HTTPStatus: status, Message: summarize(body)}
	switch {
	case status == 408 || status == 504:
		pe.Class, pe.Code = ClassTransient, "upstream_timeout"
	case status == 429:
		pe.Class, pe.Code = ClassRateLimited, platform.CodeUpstreamRateLim
	case status >= 500:
		pe.Class, pe.Code = ClassTransient, "upstream_error"
	case status == 401 || status == 403:
		pe.Class, pe.Code = ClassPermanent, "upstream_unauthorized"
	case status == 404:
		pe.Class, pe.Code = ClassPermanent, "upstream_not_found"
	case status == 400 || status == 422:
		// 内容审核拒绝需要单独分类，便于 UI 提示「不是你的参数问题」
		if isContentPolicy(body) {
			pe.Class, pe.Code = ClassContentPolicy, platform.CodeContentPolicy
		} else {
			pe.Class, pe.Code = ClassPermanent, platform.CodeUpstreamInvalid
		}
	case status >= 400:
		pe.Class, pe.Code = ClassPermanent, platform.CodeUpstreamInvalid
	default:
		pe.Class, pe.Code = ClassPermanent, platform.CodeUpstreamInvalid
	}
	return pe
}

// ClassifyTransport 把网络层错误归为 transient。
func ClassifyTransport(err error) *ProviderError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context_Canceled) || errors.Is(err, context_DeadlineExceeded) {
		return &ProviderError{Class: ClassCanceled, Code: platform.CodeInterrupted, Message: err.Error()}
	}
	return &ProviderError{Class: ClassTransient, Code: "upstream_unreachable", Message: platform.Redact(err.Error())}
}

// ParseRetryAfter 解析 Retry-After 头（秒或 HTTP 日期）。
func ParseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

func isContentPolicy(body string) bool {
	l := strings.ToLower(body)
	for _, k := range []string{"content policy", "content_policy", "safety", "moderation", "blocked", "violat"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// summarize 截断错误体，避免把上游 HTML 错误页整段透出（原项目踩过的坑）。
func summarize(body string) string {
	b := strings.TrimSpace(body)
	// 上游返回 HTML 错误页时只给出类型提示，不解析
	if strings.HasPrefix(b, "<") {
		return "upstream returned an HTML error page"
	}
	b = platform.Redact(b)
	if len(b) > 300 {
		return b[:300] + "…"
	}
	return b
}
