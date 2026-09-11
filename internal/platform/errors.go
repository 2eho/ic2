package platform

import (
	"errors"
	"fmt"
	"net/http"
)

// DomainError 是跨模块统一错误类型：code 稳定枚举，前端按 code 做 i18n（DIV-07）。
type DomainError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	Status  int            `json:"-"`
	Cause   error          `json:"-"`
}

// Error 实现 error。
func (e *DomainError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap 支持 errors.Is / errors.As。
func (e *DomainError) Unwrap() error { return e.Cause }

// WithDetail 追加一项细节（链式）。
func (e *DomainError) WithDetail(k string, v any) *DomainError {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[k] = v
	return e
}

// WithCause 附加底层原因。
func (e *DomainError) WithCause(err error) *DomainError {
	e.Cause = err
	return e
}

// NewError 构造一个带 HTTP 状态的领域错误。
func NewError(status int, code, message string) *DomainError {
	return &DomainError{Code: code, Message: message, Status: status}
}

// 稳定错误码。新增必须同时在 contracts/errors.yaml 声明并附 i18n key。
const (
	CodeInvalidRequest   = "invalid_request"
	CodeInvalidGeometry  = "invalid_geometry"
	CodeInvalidNodeType  = "invalid_node_type"
	CodeInvalidSpec      = "invalid_spec"
	CodeNotFound         = "not_found"
	CodeConflict         = "conflict"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeRateLimited      = "rate_limited"
	CodePayloadTooLarge  = "payload_too_large"
	CodeSSRFBlocked      = "ssrf_blocked"
	CodeQuotaExceeded    = "quota_exceeded"
	CodeUpstreamInvalid  = "upstream_invalid"
	CodeUpstreamRateLim  = "upstream_rate_limited"
	CodeContentPolicy    = "content_policy"
	CodeInterrupted      = "interrupted"
	CodeInternal         = "internal"
	CodeNotImplemented   = "not_implemented"
	CodeUnknownField     = "unknown_field"
	CodeInvalidID        = "invalid_id"
	CodePluginPermission = "plugin_permission_denied"
)

// ErrNotFound 便捷构造。
func ErrNotFound(what string) *DomainError {
	return NewError(http.StatusNotFound, CodeNotFound, what+" not found")
}

// ErrForbidden 便捷构造。
func ErrForbidden(what string) *DomainError {
	return NewError(http.StatusForbidden, CodeForbidden, what)
}

// ErrInvalid 便捷构造（422）。
func ErrInvalid(message string) *DomainError {
	return NewError(http.StatusUnprocessableEntity, CodeInvalidRequest, message)
}

// AsDomainError 提取领域错误；非领域错误包装为 internal。
//
// 注意：本函数返回 *DomainError。当 err 为 nil 或本身就是 typed-nil 的
// *DomainError 时，返回的指针为 nil。**禁止**把它的返回值直接作为 error 返回，
// 否则会得到「接口非空但指针为 nil」的伪错误（Go typed-nil 陷阱）。
// 需要 error 时请用 AsError。
func AsDomainError(err error) *DomainError {
	if err == nil {
		return nil
	}
	var de *DomainError
	if errors.As(err, &de) {
		return de
	}
	return NewError(http.StatusInternalServerError, CodeInternal, "internal error").WithCause(err)
}

// AsError 把任意错误规范化为可安全返回的 error：nil 输入返回真正的 nil。
func AsError(err error) error {
	if err == nil {
		return nil
	}
	var de *DomainError
	if errors.As(err, &de) {
		if de == nil {
			return nil
		}
		return de
	}
	return NewError(http.StatusInternalServerError, CodeInternal, "internal error").WithCause(err)
}

// IsNil 判断 error 是否为「无错误」，包含 typed-nil 情形。
func IsNil(err error) bool {
	if err == nil {
		return true
	}
	var de *DomainError
	if errors.As(err, &de) {
		return de == nil
	}
	return false
}
