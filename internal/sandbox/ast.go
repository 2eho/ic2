package sandbox

// 表达式 AST。
//
// 之所以要建 AST 而不是边解析边求值：审批卡片需要展示「脚本会访问哪些变量」，
// 而这必须在**求值之前**就能算出来（否则用户是在看完结果之后才被问是否授权）。
type expr interface{ exprNode() }

type (
	// litExpr 是字面量（数字 / 字符串 / 布尔 / null）。
	litExpr struct {
		val any
		raw string
	}
	// identExpr 是环境变量引用。
	identExpr struct{ name string }
	// memberExpr 是属性访问 a.b（不允许 a[b]，也就没有动态取键）。
	memberExpr struct {
		obj  expr
		name string
	}
	// indexExpr 是下标访问 a[0]（只接受数字下标，用于数组）。
	indexExpr struct {
		obj   expr
		index expr
	}
	// unaryExpr 是一元运算。
	unaryExpr struct {
		op   string
		oper expr
	}
	// binExpr 是二元运算。
	binExpr struct {
		op          string
		left, right expr
	}
	// condExpr 是三元表达式。
	condExpr struct {
		cond, then, other expr
	}
	// callExpr 是宿主函数调用。
	callExpr struct {
		fn   string
		args []expr
	}
	// arrayExpr 是数组字面量。
	arrayExpr struct{ items []expr }
	// objectExpr 是对象字面量。
	objectExpr struct{ props []objProp }

	// stmt 是语句。
	assignStmt struct {
		name string
		val  expr
	}
	ifStmt struct {
		cond      expr
		then      []stmt
		otherwise []stmt
	}
	returnStmt struct{ val expr }
	exprStmt   struct{ e expr }
)

func (*litExpr) exprNode()    {}
func (*identExpr) exprNode()  {}
func (*memberExpr) exprNode() {}
func (*indexExpr) exprNode()  {}
func (*unaryExpr) exprNode()  {}
func (*binExpr) exprNode()    {}
func (*condExpr) exprNode()   {}
func (*callExpr) exprNode()   {}
func (*arrayExpr) exprNode()  {}
func (*objectExpr) exprNode() {}

type objProp struct {
	key string
	val expr
}

type stmt interface{ stmtNode() }

func (*assignStmt) stmtNode() {}
func (*ifStmt) stmtNode()     {}
func (*returnStmt) stmtNode() {}
func (*exprStmt) stmtNode()   {}

// program 是一次解析的产物。
type program struct {
	body []stmt
}

// freeIdents 收集脚本引用的**顶层环境变量名**（用于审批卡片显示影响面）。
//
// 只看根标识符：`params.size` 依赖 `params`，`hosts.http` 依赖 `hosts`。
// 这是「脚本会读到什么」的最小上界，正是审批时真正需要的信息。
func (p *program) freeIdents() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	var walkExpr func(e expr)
	walkExpr = func(e expr) {
		switch t := e.(type) {
		case *identExpr:
			add(t.name)
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
		case *callExpr:
			for _, a := range t.args {
				walkExpr(a)
			}
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
	for _, s := range p.body {
		walkStmt(s)
	}
	return out
}
