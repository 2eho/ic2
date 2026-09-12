// Package sandbox 执行「用户自定义调用脚本」——渠道、能力、变量到上游请求的映射。
//
// 这个包存在的唯一理由：上游原项目的同类能力用 `new Function(...)` 执行用户代码，
// 那等价于在服务端开放任意代码执行。照搬等于把那个缺陷一起搬过来，所以宁可
// 先缺功能也不能照抄。这里的顺序是反过来的：
//
//	先做「能力白名单 + 超时 + 输出上限」，再谈变量注入面。
//
// 也就是说脚本从来不是「通用 JS 运行时」，而是一个**受限映射语言**：
//
//  1. 无 eval / Function / 原型链逃逸（见 checkNoEscape）；
//  2. 无循环与递归（`for` / `while` / `do` 是语法错误）——这直接消掉了
//     「超时之前先卡死事件循环」这一整类问题；
//  3. 只能调用显式白名单里的宿主函数，且宿主函数自带资源上限；
//  4. 单次执行有墙钟超时兜底（防「指数级表达式」这类语法合法但代价巨大的输入）。
//
// 有意不做的：网络、文件、环境变量、动态导入、反射。
// 脚本的职责是**组装一个请求**，不是做一切。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Limits 是执行上限。零值会被 Normalize 补齐为保守默认值。
type Limits struct {
	// Timeout 是单次执行的墙钟上限。
	Timeout time.Duration
	// MaxSteps 是解释器最大步数（防止超时前的资源耗尽）。
	MaxSteps int
	// MaxOutputBytes 是输出序列化后的上限。
	MaxOutputBytes int
	// MaxStringBytes 是单条字符串上限（防止用一次字符串乘法撑爆内存）。
	MaxStringBytes int
}

// DefaultLimits 返回默认上限。
//
// 取值依据：脚本的合理用途是「把变量拼成一个请求体」，正常规模在毫秒级、
// 几 KB 输出。默认值给到 50ms / 2 万步 / 256KB，是正常用量的两个数量级以上，
// 同时又远小于「让一次请求超时」的量级。
func DefaultLimits() Limits {
	return Limits{
		Timeout:        50 * time.Millisecond,
		MaxSteps:       20000,
		MaxOutputBytes: 256 * 1024,
		MaxStringBytes: 64 * 1024,
	}
}

// Normalize 补齐零值并夹紧到硬上限。
//
// 调用方传入的上限只能**收紧**，不能放宽到硬上限之外——否则「限额」就变成了
// 一个可以由被约束方自己调整的参数。
func (l Limits) Normalize() Limits {
	def := DefaultLimits()
	if l.Timeout <= 0 || l.Timeout > def.Timeout*20 {
		l.Timeout = def.Timeout
	}
	if l.MaxSteps <= 0 || l.MaxSteps > def.MaxSteps*10 {
		l.MaxSteps = def.MaxSteps
	}
	if l.MaxOutputBytes <= 0 || l.MaxOutputBytes > def.MaxOutputBytes*8 {
		l.MaxOutputBytes = def.MaxOutputBytes
	}
	if l.MaxStringBytes <= 0 || l.MaxStringBytes > def.MaxStringBytes*8 {
		l.MaxStringBytes = def.MaxStringBytes
	}
	return l
}

// ErrorCode 是沙箱错误的稳定分类（前端要能区分「脚本写错了」与「脚本想干坏事」）。
type ErrorCode string

// 错误分类。
const (
	ErrSyntax      ErrorCode = "script_syntax"
	ErrForbidden   ErrorCode = "script_forbidden"
	ErrRuntime     ErrorCode = "script_runtime"
	ErrTimeout     ErrorCode = "script_timeout"
	ErrLimit       ErrorCode = "script_limit"
	ErrOutputShape ErrorCode = "script_output_shape"
)

// Error 是沙箱错误。
type Error struct {
	Code   ErrorCode
	Detail string
	Line   int
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s: line %d: %s", e.Code, e.Line, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func newErr(code ErrorCode, line int, format string, args ...any) *Error {
	return &Error{Code: code, Line: line, Detail: fmt.Sprintf(format, args...)}
}

// IsForbidden 判定错误是否属于「被拒绝的能力」而非「写错了」。
func IsForbidden(err error) bool {
	var se *Error
	if errors.As(err, &se) {
		return se.Code == ErrForbidden
	}
	return false
}

// Runner 执行脚本。用接口而非直接函数，是为了让「沙箱」有两个实现
// （受限解释器 + 测试替身），否则它只是「预留了扩展点」。
type Runner interface {
	// Run 执行脚本，入参为只读的环境对象，返回脚本产出的映射。
	Run(ctx context.Context, script string, env map[string]any) (map[string]any, error)
	// Version 是沙箱语义版本，写入 Run 快照以便重放（INV-1）。
	Version() string
}

// Version 是当前沙箱语义版本。
//
// 语义变更（例如新增一个宿主函数、改一处类型提升规则）必须递增：
// Run 的输入快照里带这个版本，重放时才不会用新语义解释旧脚本。
const Version = "sandbox/1"
