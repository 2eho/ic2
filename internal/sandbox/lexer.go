package sandbox

import (
	"strconv"
	"strings"
)

// tokenKind 是词法单元类型。
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokString
	tokIdent
	tokPunct
	tokKeyword
)

type token struct {
	kind tokenKind
	text string
	num  float64
	line int
	col  int
}

// 关键字白名单。
//
// 有意**不含** `for` / `while` / `do` / `function` / `class` / `new` / `import` /
// `await` / `yield` / `delete` / `typeof` 之外的元编程入口：
// 缺了循环，就不存在「语法合法但算不完」的脚本，超时只是最后一道兜底。
var keywords = map[string]bool{
	"var": true, "if": true, "else": true, "return": true,
	"true": true, "false": true, "null": true, "undefined": true,
	"in": true, "typeof": true,
}

// 禁止标识符：危险的全局与原型链入口。
//
// 这里是**显式拒绝清单**而不是「允许清单」，因为语言本身已经不提供任何宿主对象：
// 未声明的标识符只会解析失败。拒绝清单的作用是把错误信息做成「你想做的事不支持」，
// 而不是让用户看到一个费解的「undefined is not defined」。
var forbiddenIdents = map[string]string{
	"eval":             "不支持动态求值",
	"Function":         "不支持动态函数构造",
	"constructor":      "不支持访问原型链",
	"__proto__":        "不支持访问原型链",
	"prototype":        "不支持访问原型链",
	"globalThis":       "不支持访问宿主全局对象",
	"window":           "不支持访问宿主全局对象",
	"process":          "不支持访问宿主进程",
	"require":          "不支持动态导入",
	"import":           "不支持动态导入",
	"this":             "不支持 this 绑定",
	"class":            "不支持类定义",
	"new":              "不支持对象构造",
	"delete":           "不支持删除语法",
	"setTimeout":       "不支持定时器",
	"Promise":          "不支持异步",
	"Reflect":          "不支持反射",
	"Proxy":            "不支持代理",
	"Symbol":           "不支持符号",
	"arguments":        "不支持 arguments",
	"caller":           "不支持访问调用者",
	"callee":           "不支持访问被调用者",
	"toString":         "不支持访问原型链",
	"valueOf":          "不支持访问原型链",
	"hasOwnProperty":   "不支持访问原型链",
	"__defineGetter__": "不支持访问原型链",
}

const maxSourceBytes = 64 * 1024

// lex 把源码切成词法单元。注释（// 与 /* */）被跳过。
func lex(src string, maxString int) ([]token, error) {
	if len(src) > maxSourceBytes {
		return nil, newErr(ErrLimit, 0, "脚本源码超过 %d 字节", maxSourceBytes)
	}
	var out []token
	line, col := 1, 1
	i := 0
	advance := func(n int) {
		for k := 0; k < n && i < len(src); k++ {
			if src[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
			i++
		}
	}
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			advance(1)
		case c == ' ' || c == '\t' || c == '\r':
			advance(1)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				advance(1)
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			advance(2)
			closed := false
			for i < len(src) {
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					advance(2)
					closed = true
					break
				}
				advance(1)
			}
			if !closed {
				return nil, newErr(ErrSyntax, line, "块注释未闭合")
			}
		case c >= '0' && c <= '9', c == '.' && i+1 < len(src) && isDigit(src[i+1]):
			start, startCol := i, col
			for i < len(src) && (isDigit(src[i]) || src[i] == '.' || src[i] == 'e' || src[i] == 'E' ||
				((src[i] == '+' || src[i] == '-') && i > start && (src[i-1] == 'e' || src[i-1] == 'E'))) {
				advance(1)
			}
			text := src[start:i]
			f, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, newErr(ErrSyntax, line, "非法数字字面量 %q", text)
			}
			out = append(out, token{kind: tokNumber, text: text, num: f, line: line, col: startCol})
		case c == '\'' || c == '"' || c == '`':
			quote := c
			advance(1)
			var sb strings.Builder
			closed := false
			for i < len(src) {
				ch := src[i]
				if ch == '\\' && i+1 < len(src) {
					next := src[i+1]
					switch next {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					case 'r':
						sb.WriteByte('\r')
					case '\\', '\'', '"', '`', '/':
						sb.WriteByte(next)
					default:
						// 有意不支持 \uXXXX / \xXX：解码器与转义规则越多，
						// 「字符串里藏了什么」就越难在审阅时看出来。
						return nil, newErr(ErrForbidden, line, "不支持的转义序列 \\%c", next)
					}
					advance(2)
					continue
				}
				if ch == quote {
					advance(1)
					closed = true
					break
				}
				if ch == '\n' && quote != '`' {
					return nil, newErr(ErrSyntax, line, "字符串未闭合")
				}
				if quote == '`' && ch == '$' && i+1 < len(src) && src[i+1] == '{' {
					return nil, newErr(ErrForbidden, line, "模板字符串插值请改用 + 拼接，便于静态审阅")
				}
				sb.WriteByte(ch)
				advance(1)
			}
			if !closed {
				return nil, newErr(ErrSyntax, line, "字符串未闭合")
			}
			if sb.Len() > maxString {
				return nil, newErr(ErrLimit, line, "字符串字面量超过 %d 字节", maxString)
			}
			out = append(out, token{kind: tokString, text: sb.String(), line: line, col: col})
		case isIdentStart(c):
			start := i
			for i < len(src) && isIdentPart(src[i]) {
				advance(1)
			}
			text := src[start:i]
			kind := tokIdent
			if keywords[text] {
				kind = tokKeyword
			}
			if msg, bad := forbiddenIdents[text]; bad {
				return nil, newErr(ErrForbidden, line, "%s（%s）", msg, text)
			}
			out = append(out, token{kind: kind, text: text, line: line, col: col})
		default:
			// 多字符运算符
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||", "??", "=>", "+=", "-=", "**":
				out = append(out, token{kind: tokPunct, text: two, line: line, col: col})
				advance(2)
				continue
			}
			if strings.ContainsRune("+-*/%<>=!?:,.;(){}[]&|", rune(c)) {
				out = append(out, token{kind: tokPunct, text: string(c), line: line, col: col})
				advance(1)
				continue
			}
			return nil, newErr(ErrSyntax, line, "非法字符 %q", string(c))
		}
	}
	out = append(out, token{kind: tokEOF, line: line, col: col})
	return out, nil
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || c == '$' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) }
