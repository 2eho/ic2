package sandbox

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// 受限语言的**值语义**。
//
// 与 JS 的两处刻意差异，都是为了让「写错了」在运行时报错而不是静默给出错值：
//
//   - 不做字符串→数字的隐式转换（`"16:9" * 2` 报错，而不是 NaN）；
//   - 除零报错（而不是 Infinity）。
//
// 与 JS 一致的地方也刻意保留：`+` 遇到字符串就拼接、`==` 做宽松比较。
// 差异越大，「和 JS 不一样」就越容易变成难查的问题。

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	case []any:
		return true
	case map[string]any:
		return true
	}
	return true
}

func toNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case nil:
		return 0, false
	case string:
		// 有意不做隐式字符串→数字转换：`"16:9" * 2` 得到 NaN 是 JS 里
		// 最常见的静默错误来源，宁可报错。
		return 0, false
	}
	return 0, false
}

func toIndex(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	if f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return fmt.Sprintf("%.0f", t)
		}
		return fmt.Sprintf("%g", t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, it := range t {
			parts = append(parts, toString(it))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		// 对象转字符串没有合理默认值：返回 JSON 更可预测
		b, err := json.Marshal(t)
		if err != nil {
			return "[object]"
		}
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func lengthOf(v any) int {
	switch t := v.(type) {
	case string:
		return len([]rune(t))
	case []any:
		return len(t)
	case map[string]any:
		return len(t)
	}
	return 0
}

func looseEqual(l, r any) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	if ls, ok := l.(string); ok {
		if rs, ok := r.(string); ok {
			return ls == rs
		}
		return false
	}
	lf, lok := toNumber(l)
	rf, rok := toNumber(r)
	if lok && rok {
		return lf == rf
	}
	lb, lok2 := l.(bool)
	rb, rok2 := r.(bool)
	if lok2 && rok2 {
		return lb == rb
	}
	// 数组/对象按 JSON 相等比较（浅层足够：脚本里不会做深比较）
	lj, e1 := json.Marshal(l)
	rj, e2 := json.Marshal(r)
	if e1 == nil && e2 == nil {
		return string(lj) == string(rj)
	}
	return false
}

// member 访问对象属性。
//
// 这里是原型链攻击面在**求值层**的收口：白名单之外的名字一律拒绝。
// 词法层已经拒了 `__proto__` 这类字面量，但 `a["constr"+"uctor"]` 这类
// 拼接出来的名字只会在求值层出现——所以两处都要防。
func member(obj any, name string) (any, error) {
	if isProtoKey(name) {
		return nil, newErr(ErrForbidden, 0, "属性名 %q 被拒绝（原型链）", name)
	}
	switch t := obj.(type) {
	case map[string]any:
		return t[name], nil
	case []any:
		// 数组只读 length：不做 `length(x)` 这种函数式的额外语法。
		if name == "length" {
			return float64(len(t)), nil
		}
		return nil, newErr(ErrForbidden, 0, "数组只支持 .length 与下标访问")
	case string:
		if name == "length" {
			return float64(len([]rune(t))), nil
		}
		return nil, newErr(ErrForbidden, 0, "字符串只支持 .length 与下标访问")
	case nil:
		// 访问 null 的属性返回 undefined 而不是报错，
		// 因为 `params.size` 这类可选字段的访问是最常见写法。
		return nil, nil
	}
	return nil, newErr(ErrRuntime, 0, "不能读取 %s 的属性", typeName(obj))
}

// isProtoKey 判定原型链相关键。
func isProtoKey(name string) bool {
	switch name {
	case "__proto__", "constructor", "prototype", "toString", "valueOf",
		"hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable",
		"__defineGetter__", "__defineSetter__", "__lookupGetter__",
		"__lookupSetter__", "caller", "callee", "arguments":
		return true
	}
	return false
}
