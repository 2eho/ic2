package sandbox

import (
	"sort"
	"strings"
)

// 宿主函数白名单。
//
// 白名单刻意很小：脚本要干的事只有「拼字符串、截断、取枚举值」。
// 缺 JSON.parse 是刻意的 —— 脚本的入参已经是结构化对象，需要解析字符串
// 通常意味着设计错了位置（应该在参数面板里做成字段）。
//
// 单独一个文件而不是放在 eval.go 里：eval.go 已经接近代码规模上限
//（见 scripts/check-file-size.mjs 的 800 行硬上限）。这类「受限语言」的
// 求值器天然会长，因此从第一版就按职责拆开——宿主函数、值语义、求值各一处。

// hostFuncs 是宿主函数白名单。每个函数自带资源上限。
//
// 白名单刻意很小：脚本要干的事只有「拼字符串、截断、取枚举值」。
// 缺 JSON.parse 是刻意的——脚本的入参已经是结构化对象，需要解析字符串
// 通常意味着设计错了位置（应该在参数面板里做成字段）。
func hostFuncTable() map[string]hostFunc {
	return map[string]hostFunc{
		"string":  hostString,
		"number":  hostNumber,
		"len":     hostLen,
		"join":    hostJoin,
		"trim":    hostTrim,
		"slice":   hostSlice,
		"upper":   hostUpper,
		"lower":   hostLower,
		"replace": hostReplace,
		"default": hostDefault,
		"has":     hostHas,
		"toast":   hostToast,
		"log":     hostLog,
	}
}

// hostFunc 是宿主函数签名。
//
// 单独定义一个类型名（而不是直接写函数类型字面量）是为了让 hostFuncs 的声明
// 不引用任何具体函数——否则 `var hostFuncs = map[string]func(...){...}`
// 里的函数名会与 hostFuncs 自身构成 Go 的初始化循环。
type hostFunc func(in *interpreter, args []expr) (any, error)

func (in *interpreter) evalCall(t *callExpr) (any, error) {
	fns := hostFuncTable()
	fn, ok := fns[t.fn]
	if !ok {
		// 未声明的调用一律拒绝（与插件权限白名单同一语义）。
		names := make([]string, 0, len(fns))
		for k := range fns {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, newErr(ErrForbidden, 0, "不支持的函数 %s()，可用：%s", t.fn, strings.Join(names, ", "))
	}
	if len(t.args) > 8 {
		return nil, newErr(ErrLimit, 0, "%s() 参数过多", t.fn)
	}
	return fn(in, t.args)
}

func (in *interpreter) arg(a []expr, i int) (any, error) {
	if i >= len(a) {
		return nil, nil
	}
	return in.eval(a[i])
}

func hostString(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	s := toString(v)
	if len(s) > in.limit.MaxStringBytes {
		return nil, newErr(ErrLimit, 0, "string() 结果超过 %d 字节", in.limit.MaxStringBytes)
	}
	return s, nil
}

func hostNumber(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	f, ok := toNumber(v)
	if !ok {
		return nil, newErr(ErrRuntime, 0, "number() 无法把 %s 转成数字", typeName(v))
	}
	return f, nil
}

func hostLen(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	// 统一返回 float64：脚本里只有一种数字类型，
	// 返回 int 会让 `len(x) === 3` 变成 false（类型不一致的经典坑）。
	return float64(lengthOf(v)), nil
}

func hostJoin(in *interpreter, a []expr) (any, error) {
	sep := ""
	if len(a) > 1 {
		s, err := in.arg(a, 1)
		if err != nil {
			return nil, err
		}
		sep = toString(s)
	}
	items, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	list, ok := items.([]any)
	if !ok {
		return nil, newErr(ErrRuntime, 0, "join() 第一个参数必须是数组")
	}
	if len(list) > 256 {
		return nil, newErr(ErrLimit, 0, "join() 数组过长")
	}
	var sb strings.Builder
	for i, it := range list {
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(toString(it))
		if sb.Len() > in.limit.MaxStringBytes {
			return nil, newErr(ErrLimit, 0, "join() 结果超过 %d 字节", in.limit.MaxStringBytes)
		}
	}
	return sb.String(), nil
}

func hostTrim(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	return strings.TrimSpace(toString(v)), nil
}

func hostSlice(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	start, serr := in.intArg(a, 1, 0)
	if serr != nil {
		return nil, serr
	}
	switch t := v.(type) {
	case string:
		runes := []rune(t)
		end := len(runes)
		if len(a) > 2 {
			e, err := in.intArg(a, 2, end)
			if err != nil {
				return nil, err
			}
			end = e
		}
		return clampSlice(runes, start, end), nil
	case []any:
		end := len(t)
		if len(a) > 2 {
			e, err := in.intArg(a, 2, end)
			if err != nil {
				return nil, err
			}
			end = e
		}
		start, end = clampRange(len(t), start, end)
		out := make([]any, end-start)
		copy(out, t[start:end])
		return out, nil
	}
	return nil, newErr(ErrRuntime, 0, "slice() 只接受字符串或数组")
}

func clampSlice(runes []rune, start, end int) string {
	start, end = clampRange(len(runes), start, end)
	return string(runes[start:end])
}

func clampRange(n, start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	if end < start {
		end = start
	}
	if end > n {
		end = n
	}
	return start, end
}

func hostUpper(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	return strings.ToUpper(toString(v)), nil
}

func hostLower(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	return strings.ToLower(toString(v)), nil
}

func hostReplace(in *interpreter, a []expr) (any, error) {
	src, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	from, err := in.arg(a, 1)
	if err != nil {
		return nil, err
	}
	to, err := in.arg(a, 2)
	if err != nil {
		return nil, err
	}
	out := strings.ReplaceAll(toString(src), toString(from), toString(to))
	if len(out) > in.limit.MaxStringBytes {
		return nil, newErr(ErrLimit, 0, "replace() 结果超过 %d 字节", in.limit.MaxStringBytes)
	}
	return out, nil
}

func hostDefault(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	if v == nil || v == "" {
		return in.arg(a, 1)
	}
	return v, nil
}

func hostHas(in *interpreter, a []expr) (any, error) {
	obj, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	key, err := in.arg(a, 1)
	if err != nil {
		return nil, err
	}
	k := toString(key)
	if isProtoKey(k) {
		return false, nil
	}
	switch t := obj.(type) {
	case map[string]any:
		_, ok := t[k]
		return ok, nil
	case []any:
		return false, nil
	}
	return false, nil
}

func hostToast(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	in.notes = append(in.notes, toString(v))
	return nil, nil
}

func hostLog(in *interpreter, a []expr) (any, error) {
	v, err := in.arg(a, 0)
	if err != nil {
		return nil, err
	}
	in.notes = append(in.notes, toString(v))
	return nil, nil
}

func (in *interpreter) intArg(a []expr, i, def int) (int, error) {
	v, err := in.arg(a, i)
	if err != nil {
		return 0, err
	}
	if v == nil {
		return def, nil
	}
	f, ok := toNumber(v)
	if !ok {
		return 0, newErr(ErrRuntime, 0, "参数必须是数字")
	}
	return int(f), nil
}
