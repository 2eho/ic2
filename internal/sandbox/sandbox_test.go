package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, script string, env map[string]any) (map[string]any, error) {
	t.Helper()
	return New(DefaultLimits()).Run(context.Background(), script, env)
}

func mustRun(t *testing.T, script string, env map[string]any) map[string]any {
	t.Helper()
	out, err := run(t, script, env)
	if err != nil {
		t.Fatalf("脚本应当成功，实际报错：%v", err)
	}
	return out
}

func TestBasicMapping(t *testing.T) {
	env := map[string]any{
		"params":  map[string]any{"size": "2k", "quality": "high"},
		"prompt":  "一只猫",
		"cap":     "image.generate",
		"variant": float64(3),
	}
	out := mustRun(t, `
		return {
			prompt: prompt,
			size: params.size,
			quality: default(params.quality, "medium"),
			n: variant + 1,
			model: cap == "image.generate" ? "gpt-image-1" : "other",
		};
	`, env)
	if out["prompt"] != "一只猫" {
		t.Fatalf("prompt 传递失败: %#v", out["prompt"])
	}
	if out["size"] != "2k" || out["quality"] != "high" {
		t.Fatalf("成员访问失败: %#v", out)
	}
	if out["n"] != float64(4) {
		t.Fatalf("算术失败: %#v", out["n"])
	}
	if out["model"] != "gpt-image-1" {
		t.Fatalf("三元表达式失败: %#v", out["model"])
	}
}

func TestUsesOutVariableWhenNoReturn(t *testing.T) {
	out := mustRun(t, `out = { body: prompt + "!" };`, map[string]any{"prompt": "hi"})
	if out["body"] != "hi!" {
		t.Fatalf("out 变量语义失败: %#v", out)
	}
}

func TestIfElseAndVar(t *testing.T) {
	out := mustRun(t, `
		var label = "小图";
		if (width > 2000) { label = "大图"; } else if (width > 1000) { label = "中图"; }
		return { label: label };
	`, map[string]any{"width": float64(1500)})
	if out["label"] != "中图" {
		t.Fatalf("分支语义失败: %#v", out)
	}
}

func TestStringHelpers(t *testing.T) {
	out := mustRun(t, `
		return {
			j: join(["a", "b", 1], "-"),
			t: trim("  x  "),
			s: slice("abcdef", 1, 3),
			u: upper("ab"),
			r: replace("a-b", "-", "_"),
			l: len([1,2,3]),
		};
	`, nil)
	want := map[string]any{"j": "a-b-1", "t": "x", "s": "bc", "u": "AB", "r": "a_b", "l": float64(3)}
	for k, v := range want {
		if out[k] != v {
			t.Fatalf("%s 期望 %v，实际 %v", k, v, out[k])
		}
	}
}

func TestToastNotesAreReturned(t *testing.T) {
	_, notes, err := New(DefaultLimits()).RunDetailed(context.Background(),
		`toast("模型未指定，已用默认值"); return { ok: true };`, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "默认值") {
		t.Fatalf("备注未返回: %#v", notes)
	}
}

// ---------------------------------------------------------------- 拒绝清单（ATK-23）
//
// ATK-23：自定义调用脚本里的逃逸手法必须被拒，且以 4xx（不是 500）返回。
// 下面的用例断言的是**拒绝生效**；「错误分类正确」（invalid_request
// 而不是 internal）由 TestSandboxRejectionSurfacesAsInvalidRequestNot500
// 与 e2e 侧的 ATK-23 用例共同覆盖。
//
// 这些用例的共同点：它们都是**真实可用的逃逸手法**，不是假想的。
// 每一条都对应一次「如果不拦会怎样」的实测。

func TestRejectsEvalAndFunctionConstructor(t *testing.T) {
	cases := []string{
		`return { x: eval("1+1") };`,
		`var f = Function("return 1"); return { x: f() };`,
		`return { x: new Function("return 1")() };`,
		`return { x: globalThis };`,
		`return { x: process.env.SECRET };`,
		`return { x: require("fs") };`,
	}
	for _, script := range cases {
		_, err := run(t, script, nil)
		if err == nil {
			t.Fatalf("应当被拒绝：%s", script)
		}
		if !IsForbidden(err) {
			t.Fatalf("应当是 forbidden 类错误（区分「写错了」与「想干坏事」）：%s -> %v", script, err)
		}
	}
}

func TestRejectsProtoChainWriteStyleAccess(t *testing.T) {
	// 属性名字面量在词法层就被拒绝
	if _, err := run(t, `return { x: params.__proto__ };`, map[string]any{"params": map[string]any{}}); err == nil {
		t.Fatal("__proto__ 字面量应当被拒绝")
	}
	// 拼接出来的名字在求值层被拒绝——词法层看不见它，所以两层都要防
	_, err := run(t, `return { x: params["constr" + "uctor"] };`, map[string]any{"params": map[string]any{}})
	if err == nil || !IsForbidden(err) {
		t.Fatalf("拼接出的 constructor 应当被拒绝，实际：%v", err)
	}
	// 数组/字符串不提供任意属性读取
	if _, err := run(t, `return { x: [1].constructor };`, nil); err == nil {
		t.Fatal("数组 .constructor 应当被拒绝")
	}
}

func TestRejectsLoopsAndAsync(t *testing.T) {
	for _, script := range []string{
		`for (var i = 0; i < 10; i = i + 1) { } return { ok: true };`,
		`while (true) { } return { ok: true };`,
		`await fetch("http://x"); return { ok: true };`,
		`class A {} return { ok: true };`,
		`import("fs"); return { ok: true };`,
	} {
		if _, err := run(t, script, nil); err == nil {
			t.Fatalf("应当被拒绝：%s", script)
		}
	}
}

func TestRejectsTemplateInterpolationForReviewability(t *testing.T) {
	_, err := run(t, "return { x: `a${params.b}` };", map[string]any{"params": map[string]any{"b": "c"}})
	if err == nil || !IsForbidden(err) {
		t.Fatalf("模板插值应当被拒绝（提示改用 + 拼接），实际：%v", err)
	}
}

// ---------------------------------------------------------------- 资源上限

func TestStepBudgetStopsRunawayScript(t *testing.T) {
	// 没有循环语法，所以「卡死」只能靠巨大表达式树：这里用超深嵌套验证
	// 预算与深度限制至少有一道会先命中。
	deep := "return { x: " + strings.Repeat("(", 200) + "1" + strings.Repeat(")", 200) + " };"
	if _, err := run(t, deep, nil); err == nil {
		t.Fatal("超深嵌套应当被拒绝")
	}
}

func TestExponentLimit(t *testing.T) {
	if _, err := run(t, `return { x: 2 ** 1000 };`, nil); err == nil {
		t.Fatal("过大的指数应当被限制")
	}
	out := mustRun(t, `return { x: 2 ** 10 };`, nil)
	if out["x"] != float64(1024) {
		t.Fatalf("正常幂运算失败: %#v", out["x"])
	}
}

func TestStringAndOutputLimits(t *testing.T) {
	lim := Limits{MaxStringBytes: 16}
	if _, err := New(lim).Run(context.Background(),
		`return { x: "0123456789012345678901234567890" };`, nil); err == nil {
		t.Fatal("超长字符串字面量应当被拒绝")
	}
	lim2 := Limits{MaxOutputBytes: 64}
	if _, err := New(lim2).Run(context.Background(),
		`return { x: join(["aaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbb","cccccccccccccccccccc"], "") };`, nil); err == nil {
		t.Fatal("超长输出应当被拒绝")
	}
}

func TestTimeout(t *testing.T) {
	// 用极短超时验证墙钟兜底确实生效（不是「永远不可能触发」的装饰）
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	_, err := New(DefaultLimits()).Run(ctx, `return { x: 1 };`, nil)
	if err == nil {
		t.Fatal("已取消的 context 应当导致失败")
	}
}

func TestLimitsCanOnlyTighten(t *testing.T) {
	// 调用方传入的「宽松上限」必须被夹回默认值：
	// 否则限额就成了一个由被约束方自己调的参数。
	n := Limits{Timeout: time.Hour, MaxSteps: 1 << 30, MaxOutputBytes: 1 << 30}.Normalize()
	def := DefaultLimits()
	if n.Timeout != def.Timeout || n.MaxSteps != def.MaxSteps || n.MaxOutputBytes != def.MaxOutputBytes {
		t.Fatalf("宽松上限未被夹紧: %#v", n)
	}
	if n.MaxSteps != def.MaxSteps {
		t.Fatal("步数上限必须回到默认")
	}
}

func TestOutputMustBeObject(t *testing.T) {
	_, err := run(t, `return 42;`, nil)
	if err == nil {
		t.Fatal("返回标量应当被拒绝（否则脚本「成功了」却没有任何效果）")
	}
	var se *Error
	if !errors.As(err, &se) || se.Code != ErrOutputShape {
		t.Fatalf("应当是 output_shape 错误，实际 %v", err)
	}
}

func TestSyntaxErrorHasLine(t *testing.T) {
	_, err := run(t, "return {\n  x: ,\n};", nil)
	var se *Error
	if !errors.As(err, &se) || se.Line != 2 {
		t.Fatalf("语法错误应当带行号 2，实际 %v", err)
	}
}

func TestUnknownVariableIsActionable(t *testing.T) {
	_, err := run(t, `return { x: nosuch };`, nil)
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("未定义变量应当报出变量名，实际 %v", err)
	}
}

func TestNoImplicitStringToNumber(t *testing.T) {
	if _, err := run(t, `return { x: "16:9" * 2 };`, nil); err == nil {
		t.Fatal("字符串隐式转数字应当报错（JS 里这是最常见的静默错误来源）")
	}
}

func TestDivisionByZeroIsError(t *testing.T) {
	if _, err := run(t, `return { x: 1 / 0 };`, nil); err == nil {
		t.Fatal("除零应当报错，而不是得到 Inf")
	}
}

func TestUndefinedMemberAccessIsNullNotError(t *testing.T) {
	out := mustRun(t, `return { x: params.missing };`, map[string]any{"params": map[string]any{}})
	if out["x"] != nil {
		t.Fatalf("可选字段访问应当得到 null，实际 %#v", out["x"])
	}
}

func TestAnalyzeListsReadsAndCallsBeforeExecution(t *testing.T) {
	a, err := Analyze(`toast("hi"); return { p: prompt, s: params.size, n: len([1]) };`)
	if err != nil {
		t.Fatalf("分析失败: %v", err)
	}
	gotReads := strings.Join(a.Reads, ",")
	for _, want := range []string{"prompt", "params"} {
		if !strings.Contains(gotReads, want) {
			t.Fatalf("Reads 缺少 %s：%v", want, a.Reads)
		}
	}
	if strings.Join(a.Calls, ",") != "len,toast" {
		t.Fatalf("Calls 不正确: %v", a.Calls)
	}
	if len(a.UnknownCalls) != 0 {
		t.Fatalf("不应有未知调用: %v", a.UnknownCalls)
	}
}

func TestAnalyzeFlagsUnknownCalls(t *testing.T) {
	a, err := Analyze(`return { x: fetch("http://x") };`)
	if err != nil {
		t.Fatalf("分析不应因未知调用失败（拒绝发生在执行期）: %v", err)
	}
	if len(a.UnknownCalls) != 1 || a.UnknownCalls[0] != "fetch" {
		t.Fatalf("应当报出 fetch 未在白名单: %#v", a.UnknownCalls)
	}
}

func TestNoProtoPollutionViaObjectLiteralKeys(t *testing.T) {
	// 对象字面量里的键也必须过原型链检查：
	// 否则脚本可以用 {"__proto__": {...}} 构造出污染源，再由下游 JSON 序列化传播。
	if _, err := run(t, `return { "__proto__": { polluted: 1 } };`, nil); err == nil {
		t.Fatal("对象字面量里的 __proto__ 键应当被拒绝")
	}
}

func TestEmptyScriptIsRejectedWithClearMessage(t *testing.T) {
	_, err := run(t, ``, nil)
	if err == nil || !strings.Contains(err.Error(), "out") {
		t.Fatalf("空脚本应当给出可行动的错误，实际 %v", err)
	}
}
