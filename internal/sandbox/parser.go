package sandbox

// parser 是递归下降解析器。优先级与 JS 基本一致，避免用户凭直觉写出的
// `a + b * c` 得到意外结果——「和 JS 不一样」是最难排查的一类问题。
type parser struct {
	toks []token
	pos  int
	// depth 限制嵌套深度：递归下降在深嵌套下会吃栈，
	// 而「深嵌套」正是把解析器本身变成放大器的常见手法。
	depth int
}

const maxDepth = 32

func parse(src string, maxString int) (*program, error) {
	toks, err := lex(src, maxString)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	body, err := p.parseStatements(false)
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tokEOF {
		return nil, newErr(ErrSyntax, p.cur().line, "多余的内容 %q", p.cur().text)
	}
	return &program{body: body}, nil
}

func (p *parser) cur() token  { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) at(text string) bool { return p.cur().text == text && p.cur().kind != tokString }

func (p *parser) expect(text string) error {
	if !p.at(text) {
		return newErr(ErrSyntax, p.cur().line, "期望 %q，实际是 %q", text, p.cur().text)
	}
	p.pos++
	return nil
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return newErr(ErrLimit, p.cur().line, "嵌套过深（上限 %d 层）", maxDepth)
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

// parseStatements 解析语句序列，直到遇到终止符或 EOF。
func (p *parser) parseStatements(nested bool) ([]stmt, error) {
	var out []stmt
	for {
		t := p.cur()
		if t.kind == tokEOF {
			if nested {
				return nil, newErr(ErrSyntax, t.line, "缺少 }")
			}
			return out, nil
		}
		if nested && t.text == "}" {
			return out, nil
		}
		// 可选分号：`x = 1;` 与 `x = 1` 都接受。JS 里分号是可选风格，
		// 强制要求会让用户写出「看着没问题但报语法错」的脚本。
		if t.text == ";" {
			p.next()
			continue
		}
		s, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		if p.at(";") {
			p.next()
		}
	}
}

func (p *parser) parseStatement() (stmt, error) {
	t := p.cur()
	// var 声明：[var] name = expr
	if t.text == "var" && t.kind == tokKeyword {
		p.next()
		if err := p.expectAssignTarget(); err != nil {
			return nil, err
		}
		return p.parseAssignAfterName()
	}
	if t.text == "if" && t.kind == tokKeyword {
		return p.parseIf()
	}
	if t.text == "return" && t.kind == tokKeyword {
		p.next()
		if p.at(";") || p.at("}") || p.cur().kind == tokEOF {
			return &returnStmt{val: &litExpr{val: nil}}, nil
		}
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		return &returnStmt{val: e}, nil
	}
	// 赋值语句：name = expr（不带 var 也允许，脚本里更省事）
	if t.kind == tokIdent && p.toks[p.pos+1].text == "=" {
		p.next()
		p.next()
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		return &assignStmt{name: t.text, val: e}, nil
	}
	// 表达式语句（允许 `toast("x")` 这类纯副作用调用）
	e, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	return &exprStmt{e: e}, nil
}

func (p *parser) expectAssignTarget() error {
	if p.cur().kind != tokIdent {
		return newErr(ErrSyntax, p.cur().line, "变量名不合法")
	}
	return nil
}

func (p *parser) parseAssignAfterName() (stmt, error) {
	name := p.next().text
	if err := p.expect("="); err != nil {
		return nil, err
	}
	e, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	return &assignStmt{name: name, val: e}, nil
}

func (p *parser) parseIf() (stmt, error) {
	p.next() // if
	if err := p.expect("("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	then, err := p.parseStatements(true)
	if err != nil {
		return nil, err
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	var otherwise []stmt
	if p.at("else") {
		p.next()
		if p.at("if") {
			// else if → 嵌套 if，避免再写一个分支解析
			inner, err := p.parseIf()
			if err != nil {
				return nil, err
			}
			otherwise = []stmt{inner}
		} else {
			if err := p.expect("{"); err != nil {
				return nil, err
			}
			otherwise, err = p.parseStatements(true)
			if err != nil {
				return nil, err
			}
			if err := p.expect("}"); err != nil {
				return nil, err
			}
		}
	}
	return &ifStmt{cond: cond, then: then, otherwise: otherwise}, nil
}

// ---------------------------------------------------------------- 表达式

func (p *parser) parseExpression() (expr, error) { return p.parseTernary() }

func (p *parser) parseTernary() (expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	cond, err := p.parseBinary(0)
	if err != nil {
		return nil, err
	}
	if !p.at("?") {
		return cond, nil
	}
	p.next()
	then, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	if err := p.expect(":"); err != nil {
		return nil, err
	}
	other, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	return &condExpr{cond: cond, then: then, other: other}, nil
}

// 优先级表（数字越大结合越紧）。
var precedence = []struct {
	ops  []string
	prec int
}{
	{[]string{"??"}, 1},
	{[]string{"||"}, 2},
	{[]string{"&&"}, 3},
	{[]string{"==", "!="}, 4},
	{[]string{"<", ">", "<=", ">="}, 5},
	{[]string{"+", "-"}, 6},
	{[]string{"*", "/", "%"}, 7},
	{[]string{"**"}, 8},
}

func (p *parser) parseBinary(minPrec int) (expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op, prec := p.peekBinOp()
		if prec < minPrec {
			return left, nil
		}
		p.next()
		right, err := p.parseBinary(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &binExpr{op: op, left: left, right: right}
	}
}

func (p *parser) peekBinOp() (string, int) {
	if p.cur().kind != tokPunct {
		return "", -1
	}
	for _, level := range precedence {
		for _, op := range level.ops {
			if p.cur().text == op {
				return op, level.prec
			}
		}
	}
	return "", -1
}

func (p *parser) parseUnary() (expr, error) {
	t := p.cur()
	if t.kind == tokPunct && (t.text == "!" || t.text == "-" || t.text == "+") {
		p.next()
		oper, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unaryExpr{op: t.text, oper: oper}, nil
	}
	if t.kind == tokKeyword && t.text == "typeof" {
		p.next()
		oper, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unaryExpr{op: "typeof", oper: oper}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (expr, error) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.at("."):
			p.next()
			if p.cur().kind != tokIdent && p.cur().kind != tokKeyword {
				return nil, newErr(ErrSyntax, p.cur().line, "属性名不合法")
			}
			e = &memberExpr{obj: e, name: p.next().text}
		case p.at("["):
			p.next()
			idx, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			e = &indexExpr{obj: e, index: idx}
		default:
			return e, nil
		}
	}
}

func (p *parser) parsePrimary() (expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	t := p.cur()
	switch {
	case t.kind == tokNumber:
		p.next()
		return &litExpr{val: t.num, raw: t.text}, nil
	case t.kind == tokString:
		p.next()
		return &litExpr{val: t.text, raw: t.text}, nil
	case t.kind == tokKeyword && (t.text == "true" || t.text == "false"):
		p.next()
		return &litExpr{val: t.text == "true", raw: t.text}, nil
	case t.kind == tokKeyword && (t.text == "null" || t.text == "undefined"):
		p.next()
		return &litExpr{val: nil, raw: t.text}, nil
	case t.text == "(":
		p.next()
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return e, nil
	case t.text == "[":
		return p.parseArray()
	case t.text == "{":
		return p.parseObject()
	case t.kind == tokIdent:
		p.next()
		if p.at("(") {
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			return &callExpr{fn: t.text, args: args}, nil
		}
		return &identExpr{name: t.text}, nil
	case t.kind == tokPunct && t.text == "=":
		return nil, newErr(ErrSyntax, t.line, "不支持赋值表达式，请用独立语句 x = ...")
	}
	return nil, newErr(ErrSyntax, t.line, "无法解析的表达式 %q", t.text)
}

func (p *parser) parseArgs() ([]expr, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	var args []expr
	if p.at(")") {
		p.next()
		return args, nil
	}
	for {
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		if p.at(",") {
			p.next()
			if p.at(")") {
				break
			}
			continue
		}
		break
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *parser) parseArray() (expr, error) {
	p.next() // [
	var items []expr
	if p.at("]") {
		p.next()
		return &arrayExpr{items: items}, nil
	}
	for {
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		items = append(items, e)
		if p.at(",") {
			p.next()
			if p.at("]") {
				break
			}
			continue
		}
		break
	}
	if err := p.expect("]"); err != nil {
		return nil, err
	}
	return &arrayExpr{items: items}, nil
}

func (p *parser) parseObject() (expr, error) {
	p.next() // {
	var props []objProp
	if p.at("}") {
		p.next()
		return &objectExpr{props: props}, nil
	}
	for {
		t := p.cur()
		var key string
		switch {
		case t.kind == tokIdent, t.kind == tokKeyword, t.kind == tokString:
			key = t.text
			p.next()
		case t.kind == tokNumber:
			key = t.text
			p.next()
		default:
			return nil, newErr(ErrSyntax, t.line, "对象键不合法 %q", t.text)
		}
		// 对象键也要过原型链检查：`{"__proto__": {...}}` 是一个可序列化的
		// 污染源（下游 JSON.parse 到真实 JS 对象时才会生效，也就是**跨边界**生效），
		// 在沙箱里看起来无害，所以必须在产生它的地方就拒绝。
		if isProtoKey(key) {
			return nil, newErr(ErrForbidden, t.line, "对象键 %q 被拒绝（原型链）", key)
		}
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		v, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		props = append(props, objProp{key: key, val: v})
		if p.at(",") {
			p.next()
			if p.at("}") {
				break
			}
			continue
		}
		break
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return &objectExpr{props: props}, nil
}
