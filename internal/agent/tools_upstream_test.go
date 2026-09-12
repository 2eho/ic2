package agent

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// 9.5 的核心断言：**上游 34 个工具名必须全部可调用**。
//
// 上一轮这里是假的：矩阵标 done，实际只有 14 个名字，缺 20 个。
// MCP 客户端按名调用，miss 的表现是「工具不存在」——没有任何报错，
// 只是「这个能力用不了」。所以这条用例必须穷举清单本身。
func TestUpstreamToolNamesAllRegistered(t *testing.T) {
	names := UpstreamToolNames()
	if len(names) != 34 {
		t.Fatalf("上游工具名数量应为 34，实际 %d", len(names))
	}
	registered := map[string]bool{}
	for _, def := range UpstreamToolSet() {
		registered[def.Name] = true
	}
	// 别名工具（直通规范工具）也在 UpstreamToolSet 里，因此这一条覆盖全部
	var missing []string
	for _, n := range names {
		if !registered[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("以下上游工具名没有注册（MCP 客户端会 miss）：%v", missing)
	}
}

// 每个上游工具都必须能通过 ToolByName 查到（执行分发的入口）。
func TestUpstreamToolsResolvableByName(t *testing.T) {
	for _, name := range UpstreamToolNames() {
		if _, ok := ToolByName(name); !ok {
			t.Fatalf("ToolByName 查不到 %s", name)
		}
	}
}

// 每个工具都必须声明非空的入参 schema 与描述。
// 缺 schema 时 MCP 客户端无法构造调用；缺描述时模型不知道何时该用。
func TestUpstreamToolsHaveSchemaAndDescription(t *testing.T) {
	for _, def := range UpstreamToolSet() {
		if len(def.InputSchema) == 0 {
			t.Fatalf("%s 没有入参 schema", def.Name)
		}
		var parsed map[string]any
		if err := json.Unmarshal(def.InputSchema, &parsed); err != nil {
			t.Fatalf("%s 的 schema 不是合法 JSON: %v", def.Name, err)
		}
		if parsed["type"] != "object" {
			t.Fatalf("%s 的 schema 顶层必须是 object", def.Name)
		}
		if strings.TrimSpace(def.Description) == "" {
			t.Fatalf("%s 没有描述", def.Name)
		}
		if def.Scope != "read" && def.Scope != "write" {
			t.Fatalf("%s 的 scope 非法: %q", def.Name, def.Scope)
		}
	}
}

// 会产生费用的工具必须标记 CostsMoney —— 否则权限档位为 full 时
// 会被自动放行，用户的费用在没有确认的情况下产生。
func TestCostlyUpstreamToolsAreMarked(t *testing.T) {
	costly := []string{
		"canvas_create_config_node", "canvas_create_image_prompt_flow",
		"canvas_generate_text", "canvas_generate_image",
		"canvas_generate_video", "canvas_generate_audio",
		"workbench_image_generate", "workbench_video_generate",
	}
	byName := map[string]ToolDef{}
	for _, def := range UpstreamToolSet() {
		byName[def.Name] = def
	}
	for _, name := range costly {
		def, ok := byName[name]
		if !ok {
			t.Fatalf("%s 未注册", name)
		}
		if !def.CostsMoney {
			t.Fatalf("%s 会产生费用但未标记 CostsMoney", name)
		}
		if def.Approval == ApprovalAuto {
			t.Fatalf("%s 会产生费用但默认自动放行", name)
		}
	}
}

// 只读工具不得被判定为需要确认（否则每次读画布都要点一次，
// 用户会习惯性放行，审批就失去意义）。
func TestReadOnlyUpstreamToolsAreAuto(t *testing.T) {
	for _, name := range []string{
		"canvas_get_state", "canvas_get_selection", "canvas_export_snapshot",
		"canvas_list_projects", "generation_get_status",
		"workbench_image_get_config", "workbench_video_get_config",
		"prompts_search", "assets_list",
	} {
		def, ok := ToolByName(name)
		if !ok {
			t.Fatalf("%s 未注册", name)
		}
		if def.Scope != "read" {
			t.Fatalf("%s 应当是只读，实际 %q", name, def.Scope)
		}
		if def.Approval != ApprovalAuto {
			t.Fatalf("%s 是只读但需要确认（%q）", name, def.Approval)
		}
	}
}

// 别名工具的权限必须与规范工具**完全一致**：
// 否则会出现「canvas.get_state 只读、canvas_get_state 可写」这种
// 按名字绕过权限的路径。
func TestAliasToolsInheritCanonicalPermission(t *testing.T) {
	for alias, canon := range upstreamAliases {
		a, ok := ToolByName(alias)
		if !ok {
			t.Fatalf("别名 %s 未注册", alias)
		}
		c, ok := canonicalTool(canon)
		if !ok {
			t.Fatalf("规范工具 %s 不存在", canon)
		}
		if a.Scope != c.Scope {
			t.Fatalf("%s 的 scope（%s）与 %s（%s）不一致", alias, a.Scope, canon, c.Scope)
		}
		if a.CostsMoney != c.CostsMoney {
			t.Fatalf("%s 的 CostsMoney 与 %s 不一致", alias, canon)
		}
		if string(a.InputSchema) != string(c.InputSchema) {
			t.Fatalf("%s 的 schema 与 %s 不一致（应当复用，复制会漂移）", alias, canon)
		}
	}
}

// 上游清单与别名表必须自洽：别名指向的规范工具必须存在。
func TestAliasTargetsExist(t *testing.T) {
	declared := map[string]bool{}
	for _, n := range UpstreamToolNames() {
		declared[n] = true
	}
	for alias, canon := range upstreamAliases {
		if !declared[alias] {
			t.Fatalf("别名 %s 不在上游清单里（多出的名字没有依据）", alias)
		}
		if _, ok := canonicalTool(canon); !ok {
			t.Fatalf("别名 %s 指向的规范工具 %s 不存在", alias, canon)
		}
	}
}

// 上游清单内部不得有重复名（重复会让 MCP 暴露两个同名工具）。
func TestUpstreamToolNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range UpstreamToolNames() {
		if seen[n] {
			t.Fatalf("上游清单重复: %s", n)
		}
		seen[n] = true
	}
}

// 规范工具与上游工具的**并集**必须都能查到，且没有重名冲突。
func TestCanonicalAndUpstreamDoNotCollide(t *testing.T) {
	canonical := map[string]bool{}
	for _, d := range ToolSet() {
		canonical[d.Name] = true
	}
	for _, n := range UpstreamToolNames() {
		if canonical[n] {
			t.Fatalf("%s 同时是规范名与上游名，会让 Scope 判定出现两条路径", n)
		}
	}
}

// 节点类型映射：上游 5 种类型必须全部有落位。
// 少一个的表现是「创建成功但节点类型不认识」→ 界面上一个空白节点。
func TestKindFromNodeTypeCoversUpstreamTypes(t *testing.T) {
	for _, up := range []string{"text", "image", "config", "video", "audio", "group"} {
		if _, ok := kindFromNodeType(up); !ok {
			t.Fatalf("上游节点类型 %s 没有映射", up)
		}
	}
	if _, ok := kindFromNodeType("nope"); ok {
		t.Fatal("未知类型不应被接受")
	}
}

// 上游清单的排序在这里固化一次：清单本身是契约面，
// 顺序变化会让 diff 噪音掩盖真正的增删。
func TestUpstreamToolNamesAreStable(t *testing.T) {
	names := UpstreamToolNames()
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	// 上游用的是「按能力分组」的顺序，不是字典序；这里只断言
	// 关键分组位置，避免「不小心整体重排」。
	if names[0] != "site_navigate" {
		t.Fatalf("首个工具应为 site_navigate，实际 %s", names[0])
	}
	if names[len(names)-1] != "assets_add" {
		t.Fatalf("末个工具应为 assets_add，实际 %s", names[len(names)-1])
	}
}
