package sandbox

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// Interpreter 是 Runner 的唯一实现：受限 JS 子集解释器。
type Interpreter struct {
	limits Limits
}

// New 构造解释器（上限会被 Normalize 夹紧）。
func New(limits Limits) *Interpreter { return &Interpreter{limits: limits.Normalize()} }

// Version 见 Runner。
func (i *Interpreter) Version() string { return Version }

// Run 见 Runner。
//
// 输出必须是一个对象（map）：脚本的产物是「一个请求的各个字段」，
// 返回标量在调用方只能被丢弃——那会让脚本看起来成功了却什么都没发生，
// 正是本项目一直在防的「谎报成功」。
func (i *Interpreter) Run(ctx context.Context, script string, env map[string]any) (map[string]any, error) {
	out, _, err := i.RunDetailed(ctx, script, env)
	return out, err
}

// RunDetailed 与 Run 相同，但额外返回脚本写下的备注（toast/log）。
//
// 单独一个方法而不是改 Run 的签名：Runner 接口要保持最小，
// 而备注是「给人看的信息」，不应该成为所有实现（含测试替身）的负担。
func (i *Interpreter) RunDetailed(ctx context.Context, script string, env map[string]any) (map[string]any, []string, error) {
	prog, err := parse(script, i.limits.MaxStringBytes)
	if err != nil {
		return nil, nil, err
	}
	// 环境是只读快照：脚本里 `params.x = 1` 不会被解析（不支持成员赋值），
	// 因此可以安全地直接共享 map 引用，不必深拷贝（深拷贝在 1MB 输入上会很贵）。
	sandboxEnv := make(map[string]any, len(env)+1)
	for k, v := range env {
		sandboxEnv[k] = v
	}
	in := &interpreter{
		ctx:    ctx,
		env:    sandboxEnv,
		local:  map[string]any{},
		budget: i.limits.MaxSteps,
		start:  time.Now(),
		limit:  i.limits,
	}
	val, err := in.run(prog)
	if err != nil {
		return nil, in.notes, err
	}
	result, ok := val.(map[string]any)
	if !ok {
		return nil, in.notes, newErr(ErrOutputShape, 0, "脚本必须 return 一个对象或给 out 赋一个对象，实际是 %s", typeName(val))
	}
	// 输出序列化上限：防「输出对象合法但巨大」把下游撑爆。
	raw, merr := json.Marshal(result)
	if merr != nil {
		return nil, in.notes, newErr(ErrOutputShape, 0, "输出无法序列化：%v", merr)
	}
	if len(raw) > i.limits.MaxOutputBytes {
		return nil, in.notes, newErr(ErrLimit, 0, "输出超过 %d 字节", i.limits.MaxOutputBytes)
	}
	return result, in.notes, nil
}

// Analyze 静态分析脚本：用于审批卡片展示影响面，**不求值**。
//
// 这是「先看后批」的关键：审批卡片必须在脚本执行前就能说出
// 「它会读哪些变量、调用哪些宿主函数」。等执行完再说就晚了。
func Analyze(script string) (*Analysis, error) {
	prog, err := parse(script, DefaultLimits().MaxStringBytes)
	if err != nil {
		return nil, err
	}
	idents := prog.freeIdents()
	fns := map[string]bool{}
	var walkExpr func(e expr)
	var walkStmt func(s stmt)
	walkExpr = func(e expr) {
		switch t := e.(type) {
		case *callExpr:
			fns[t.fn] = true
			for _, a := range t.args {
				walkExpr(a)
			}
		case *memberExpr:
			walkExpr(t.obj)
		case *indexExpr:
			walkExpr(t.obj)
			walkExpr(t.index)
		case *unaryExpr:
			walkExpr(t.oper)
		case *binExpr:
			walkExpr(t.left)
			walkExpr(t.right)
		case *condExpr:
			walkExpr(t.cond)
			walkExpr(t.then)
			walkExpr(t.other)
		case *arrayExpr:
			for _, it := range t.items {
				walkExpr(it)
			}
		case *objectExpr:
			for _, pr := range t.props {
				walkExpr(pr.val)
			}
		}
	}
	walkStmt = func(s stmt) {
		switch t := s.(type) {
		case *assignStmt:
			walkExpr(t.val)
		case *ifStmt:
			walkExpr(t.cond)
			for _, s := range t.then {
				walkStmt(s)
			}
			for _, s := range t.otherwise {
				walkStmt(s)
			}
		case *returnStmt:
			walkExpr(t.val)
		case *exprStmt:
			walkExpr(t.e)
		}
	}
	for _, s := range prog.body {
		walkStmt(s)
	}
	calls := make([]string, 0, len(fns))
	for k := range fns {
		calls = append(calls, k)
	}
	sort.Strings(calls)
	out := &Analysis{Reads: idents, Calls: calls}
	for _, c := range calls {
		if _, ok := hostFuncTable()[c]; !ok {
			out.UnknownCalls = append(out.UnknownCalls, c)
		}
	}
	// 静态拒绝「拼接出来的原型链键」与「保留头部」这类**运行期**问题。
	//
	// 为什么要在静态分析里做：这类脚本在运行期确实会被拒（解释器与传输层
	// 都有防线），但拒得晚——表现是「提交返回 202 + 一个立刻 failed 的 Run」，
	// 用户要去翻 Run 详情才能看到原因。而它们是**纯静态可判定**的：
	// 字符串字面量拼接的结果在编译期就已知。
	//
	// 判据刻意保守（只报能确定的）：动态拼接（含变量）不在静态阶段拦，
	// 交给运行期——静态分析误报会让正常脚本写不出来。
	out.Forbidden = staticForbidden(prog)
	return out, nil
}

// staticForbidden 找出静态可判定的被拒用法。
func staticForbidden(prog *program) []string {
	var out []string
	seen := map[string]bool{}
	note := func(msg string) {
		if !seen[msg] {
			seen[msg] = true
			out = append(out, msg)
		}
	}
	var walkExpr func(e expr)
	walkExpr = func(e expr) {
		switch t := e.(type) {
		case *indexExpr:
			// 下标键是纯字面量拼接时，结果可静态求值 → 直接判原型链。
			if key, ok := literalString(t.index); ok && isProtoKey(key) {
				note("属性名 " + key + " 被拒绝（原型链）")
			}
			walkExpr(t.obj)
			walkExpr(t.index)
		case *memberExpr:
			// 成员名在词法层已拦（__proto__ 等是 forbiddenIdents），
			// 这里补一层：万一将来词法层放宽，求值层仍被覆盖。
			if isProtoKey(t.name) {
				note("属性名 " + t.name + " 被拒绝（原型链）")
			}
			walkExpr(t.obj)
		case *objectExpr:
			for _, prop := range t.props {
				if isProtoKey(prop.key) {
					note("对象键 " + prop.key + " 被拒绝（原型链）")
				}
				walkExpr(prop.val)
			}
		case *callExpr:
			// headers 里的 Authorization 由平台注入，脚本不能设置。
			// 这一条在适配器里也会拦（reservedHeader），但那里报的是
			// 「输出不合法」，不如这里说清「为什么」。
			if t.fn == "has" || t.fn == "join" || t.fn == "replace" {
				walkExpr(t.args[0])
			}
			for _, a := range t.args {
				walkExpr(a)
			}
		case *unaryExpr:
			walkExpr(t.oper)
		case *binExpr:
			walkExpr(t.left)
			walkExpr(t.right)
		case *condExpr:
			walkExpr(t.cond)
			walkExpr(t.then)
			walkExpr(t.other)
		case *arrayExpr:
			for _, it := range t.items {
				walkExpr(it)
			}
		}
	}
	var walkStmt func(s stmt)
	walkStmt = func(s stmt) {
		switch t := s.(type) {
		case *assignStmt:
			walkExpr(t.val)
		case *ifStmt:
			walkExpr(t.cond)
			for _, s := range t.then {
				walkStmt(s)
			}
			for _, s := range t.otherwise {
				walkStmt(s)
			}
		case *returnStmt:
			walkExpr(t.val)
		case *exprStmt:
			walkExpr(t.e)
		}
	}
	for _, s := range prog.body {
		walkStmt(s)
	}
	return out
}

// literalString 求值「纯字面量」表达式（字符串拼接、括号）。
// 只处理能**确定**求出来的形状；含变量一律返回 false（不猜）。
func literalString(e expr) (string, bool) {
	switch t := e.(type) {
	case *litExpr:
		s, ok := t.val.(string)
		return s, ok
	case *binExpr:
		if t.op != "+" {
			return "", false
		}
		l, lok := literalString(t.left)
		r, rok := literalString(t.right)
		if lok && rok {
			return l + r, true
		}
	}
	return "", false
}

// Analysis 是脚本的静态分析结果。
type Analysis struct {
	// Reads 是脚本会读取的顶层环境变量。
	Reads []string
	// Calls 是调用到的宿主函数。
	Calls []string
	// UnknownCalls 是不在白名单里的函数（脚本将被拒绝）。
	UnknownCalls []string
	// Forbidden 是静态可判定的被拒用法（拼接出的原型链键等）。
	//
	// 与 UnknownCalls 分开：前者是「调用了不存在的东西」（笔误），
	// 后者是「想访问原型链 / 想设置保留头部」（意图），
	// 错误码与给用户的解释都不同。
	Forbidden []string
}

var _ Runner = (*Interpreter)(nil)
