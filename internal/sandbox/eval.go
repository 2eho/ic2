package sandbox

import (
	"context"
	"math"
	"time"
)

// interpreter 是受限语言的求值器。
//
// 预算（步数）在求值前预扣、求值中各占一部分，是因为「步数」必须能被
// **被动消耗**：如果只在语句层面计数，一个 `a ** b ** c` 就绕过了所有计数。
type interpreter struct {
	ctx    context.Context
	env    map[string]any
	local  map[string]any
	budget int
	start  time.Time
	limit  Limits
	// notes 是脚本通过 toast()/log() 写下的备注，随结果一起返回给用户，
	// 而不是只写日志——「脚本自己说它干了什么」在排障时价值很高。
	notes []string
}

func (in *interpreter) tick(n int) error {
	in.budget -= n
	if in.budget <= 0 {
		return newErr(ErrLimit, 0, "超过步数上限 %d", in.limit.MaxSteps)
	}
	// 每 256 步检查一次墙钟：time.Now() 本身有成本，
	// 而在纯算术脚本上它会成为主要开销。
	if in.budget%256 == 0 {
		if time.Since(in.start) > in.limit.Timeout {
			return newErr(ErrTimeout, 0, "脚本执行超过 %s", in.limit.Timeout)
		}
	}
	select {
	case <-in.ctx.Done():
		return newErr(ErrTimeout, 0, "脚本执行被取消")
	default:
		return nil
	}
}

// run 执行程序并返回 return 的值。
func (in *interpreter) run(p *program) (any, error) {
	for _, s := range p.body {
		if err := in.tick(1); err != nil {
			return nil, err
		}
		v, ret, err := in.execStmt(s)
		if err != nil {
			return nil, err
		}
		if ret {
			return v, nil
		}
	}
	// 隐式返回 out 变量：脚本最自然的写法是「把结果放进 out」。
	if v, ok := in.local["out"]; ok {
		return v, nil
	}
	return nil, newErr(ErrOutputShape, 0, "脚本没有 return，也没有给 out 赋值")
}

func (in *interpreter) execStmt(s stmt) (any, bool, error) {
	switch t := s.(type) {
	case *assignStmt:
		v, err := in.eval(t.val)
		if err != nil {
			return nil, false, err
		}
		in.local[t.name] = v
		return nil, false, nil
	case *ifStmt:
		cond, err := in.eval(t.cond)
		if err != nil {
			return nil, false, err
		}
		branch := t.otherwise
		if truthy(cond) {
			branch = t.then
		}
		for _, bs := range branch {
			if err := in.tick(1); err != nil {
				return nil, false, err
			}
			v, ret, err := in.execStmt(bs)
			if err != nil {
				return nil, false, err
			}
			if ret {
				return v, true, nil
			}
		}
		return nil, false, nil
	case *returnStmt:
		v, err := in.eval(t.val)
		if err != nil {
			return nil, false, err
		}
		return v, true, nil
	case *exprStmt:
		_, err := in.eval(t.e)
		return nil, false, err
	}
	return nil, false, newErr(ErrSyntax, 0, "不支持的语句")
}

func (in *interpreter) eval(e expr) (any, error) {
	if err := in.tick(1); err != nil {
		return nil, err
	}
	switch t := e.(type) {
	case *litExpr:
		return t.val, nil
	case *identExpr:
		if v, ok := in.local[t.name]; ok {
			return v, nil
		}
		if v, ok := in.env[t.name]; ok {
			return v, nil
		}
		return nil, newErr(ErrRuntime, 0, "未定义的变量 %s（可用变量见脚本编辑器左侧说明）", t.name)
	case *memberExpr:
		obj, err := in.eval(t.obj)
		if err != nil {
			return nil, err
		}
		return member(obj, t.name)
	case *indexExpr:
		return in.evalIndex(t)
	case *unaryExpr:
		return in.evalUnary(t)
	case *binExpr:
		return in.evalBinary(t)
	case *condExpr:
		cond, err := in.eval(t.cond)
		if err != nil {
			return nil, err
		}
		if truthy(cond) {
			return in.eval(t.then)
		}
		return in.eval(t.other)
	case *callExpr:
		return in.evalCall(t)
	case *arrayExpr:
		out := make([]any, 0, len(t.items))
		for _, it := range t.items {
			v, err := in.eval(it)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *objectExpr:
		out := make(map[string]any, len(t.props))
		for _, pr := range t.props {
			v, err := in.eval(pr.val)
			if err != nil {
				return nil, err
			}
			out[pr.key] = v
		}
		return out, nil
	}
	return nil, newErr(ErrRuntime, 0, "不支持的表达式")
}

func (in *interpreter) evalIndex(t *indexExpr) (any, error) {
	obj, err := in.eval(t.obj)
	if err != nil {
		return nil, err
	}
	idx, err := in.eval(t.index)
	if err != nil {
		return nil, err
	}
	// 只接受整数下标与字符串键。**不支持表达式取键的后果**是：
	// 脚本无法构造出「运行时才知道的属性名」，从根上堵住了用计算属性名触碰原型链。
	switch o := obj.(type) {
	case []any:
		return indexSlice(o, idx)
	case []map[string]any:
		// 宿主注入的 []map[string]any 必须走这一支：
		// Go 的切片类型不协变，`[]map[string]any` 不是 `[]any`，
		// 只写 `case []any` 会让 `inputs[0]` 静默返回 null
		// （实测：脚本里 `inputs[0].label` 得到 nil，但 `inputs[0]["label"]` 正常
		//  ——这种「换个写法就对」的差异最难排查）。
		return indexSlice(o, idx)
	case []string:
		return indexSlice(o, idx)
	case []float64:
		return indexSlice(o, idx)
	case []int:
		return indexSliceStr(o, idx)
	case map[string]any:
		key, ok := idx.(string)
		if !ok {
			return nil, newErr(ErrRuntime, 0, "对象键必须是字符串")
		}
		if isProtoKey(key) {
			return nil, newErr(ErrForbidden, 0, "属性名 %q 被拒绝（原型链）", key)
		}
		return o[key], nil
	case string:
		n, ok := toIndex(idx)
		if !ok || n < 0 || n >= len(o) {
			return nil, nil
		}
		return string(o[n]), nil
	}
	return nil, nil
}

// indexSlice 是泛型下标读取：统一下标校验与越界语义（越界返回 null，与 JS 一致）。
func indexSlice[T any](list []T, idx any) (any, error) {
	n, ok := toIndex(idx)
	if !ok {
		return nil, newErr(ErrRuntime, 0, "数组下标必须是整数")
	}
	if n < 0 || n >= len(list) {
		return nil, nil
	}
	return list[n], nil
}

func indexSliceStr(list []int, idx any) (any, error) {
	n, ok := toIndex(idx)
	if !ok {
		return nil, newErr(ErrRuntime, 0, "数组下标必须是整数")
	}
	if n < 0 || n >= len(list) {
		return nil, nil
	}
	return float64(list[n]), nil
}

func (in *interpreter) evalUnary(t *unaryExpr) (any, error) {
	v, err := in.eval(t.oper)
	if err != nil {
		return nil, err
	}
	switch t.op {
	case "!":
		return !truthy(v), nil
	case "-":
		f, ok := toNumber(v)
		if !ok {
			return nil, newErr(ErrRuntime, 0, "一元 - 需要数字")
		}
		return -f, nil
	case "+":
		f, ok := toNumber(v)
		if !ok {
			return nil, newErr(ErrRuntime, 0, "一元 + 需要数字")
		}
		return f, nil
	case "typeof":
		return typeName(v), nil
	}
	return nil, newErr(ErrRuntime, 0, "不支持的一元运算 %s", t.op)
}

func (in *interpreter) evalBinary(t *binExpr) (any, error) {
	// 短路求值必须真的短路：否则 `a && a.b` 会在 a 为 null 时报错。
	switch t.op {
	case "&&":
		l, err := in.eval(t.left)
		if err != nil {
			return nil, err
		}
		if !truthy(l) {
			return l, nil
		}
		return in.eval(t.right)
	case "||":
		l, err := in.eval(t.left)
		if err != nil {
			return nil, err
		}
		if truthy(l) {
			return l, nil
		}
		return in.eval(t.right)
	case "??":
		l, err := in.eval(t.left)
		if err != nil {
			return nil, err
		}
		if l != nil {
			return l, nil
		}
		return in.eval(t.right)
	}

	l, err := in.eval(t.left)
	if err != nil {
		return nil, err
	}
	r, err := in.eval(t.right)
	if err != nil {
		return nil, err
	}
	switch t.op {
	case "+":
		// 字符串拼接优先：与 JS 一致（否则 `"a" + 1` 会得到 NaN 这类意外）
		if ls, ok := l.(string); ok {
			return ls + toString(r), nil
		}
		lf, lok := toNumber(l)
		rf, rok := toNumber(r)
		if !lok || !rok {
			return nil, newErr(ErrRuntime, 0, "+ 需要数字或字符串")
		}
		return lf + rf, nil
	case "-", "*", "/", "%", "**":
		return arith(t.op, l, r)
	case "==", "!=":
		eq := looseEqual(l, r)
		if t.op == "!=" {
			return !eq, nil
		}
		return eq, nil
	case "<", ">", "<=", ">=":
		return compare(t.op, l, r)
	}
	return nil, newErr(ErrRuntime, 0, "不支持的运算符 %s", t.op)
}

func arith(op string, l, r any) (any, error) {
	lf, lok := toNumber(l)
	rf, rok := toNumber(r)
	if !lok || !rok {
		return nil, newErr(ErrRuntime, 0, "%s 需要数字", op)
	}
	switch op {
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, newErr(ErrRuntime, 0, "除数为 0")
		}
		return lf / rf, nil
	case "%":
		if rf == 0 {
			return nil, newErr(ErrRuntime, 0, "取模的除数为 0")
		}
		return math.Mod(lf, rf), nil
	case "**":
		// `**` 是唯一的「一次表达式就能消耗任意 CPU」的入口：
		// 100000 ** 100000 在 Go 里是 Inf 而不是死循环，但 2 ** 0.5 会走浮点
		// 幂运算，而浮点幂在极端参数下代价不可忽略。限制指数范围即可。
		if math.Abs(rf) > 64 {
			return nil, newErr(ErrLimit, 0, "** 的指数绝对值不得超过 64")
		}
		v := math.Pow(lf, rf)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, newErr(ErrRuntime, 0, "** 结果不是有限数")
		}
		return v, nil
	}
	return nil, newErr(ErrRuntime, 0, "不支持的运算符 %s", op)
}

func compare(op string, l, r any) (any, error) {
	if ls, ok := l.(string); ok {
		rs, ok := r.(string)
		if !ok {
			return nil, newErr(ErrRuntime, 0, "字符串只能与字符串比较")
		}
		switch op {
		case "<":
			return ls < rs, nil
		case ">":
			return ls > rs, nil
		case "<=":
			return ls <= rs, nil
		case ">=":
			return ls >= rs, nil
		}
	}
	lf, lok := toNumber(l)
	rf, rok := toNumber(r)
	if !lok || !rok {
		return nil, newErr(ErrRuntime, 0, "比较需要数字或字符串")
	}
	switch op {
	case "<":
		return lf < rf, nil
	case ">":
		return lf > rf, nil
	case "<=":
		return lf <= rf, nil
	case ">=":
		return lf >= rf, nil
	}
	return nil, newErr(ErrRuntime, 0, "不支持的比较 %s", op)
}
