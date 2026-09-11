package graph

import (
	"encoding/json"

	"github.com/context-flow/ic/internal/platform"
)

// json_Number 是 encoding/json 的 Number 别名，便于统一数值解析。
type json_Number = json.Number

// 复用 platform 的错误码常量，避免两处维护。
const (
	CodePayloadTooLarge = platform.CodePayloadTooLarge
	CodeInvalidSpec     = platform.CodeInvalidSpec
	CodeInvalidGeometry = platform.CodeInvalidGeometry
	CodeInvalidNodeType = platform.CodeInvalidNodeType
	CodeInvalidID       = platform.CodeInvalidID
	CodeNotFound        = platform.CodeNotFound
	CodeConflict        = platform.CodeConflict
	CodeForbidden       = platform.CodeForbidden
	CodeUnknownField    = platform.CodeUnknownField
)

// DomainError 别名，便于 graph 包内直接构造。
type DomainError = platform.DomainError

// NewError 别名。
var NewError = platform.NewError

// ErrNotFound 别名。
var ErrNotFound = platform.ErrNotFound

// ErrInvalid 别名。
var ErrInvalid = platform.ErrInvalid
