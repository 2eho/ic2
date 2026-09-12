package agent

import "github.com/context-flow/ic/internal/graph"

// 上游工具面对等（9.5）。
//
// 背景：矩阵 §9.5 曾标 done，实际只有 14 个工具，而上游
// `canvas-agent/src/canvas/schemas.ts` 的 `toolNames` 有 34 个。
// MCP 客户端是**按工具名**调用的，名字对不上就是「功能缺失」，
// 「语义等价」不能算对等 —— 这是上一轮自查里最典型的一处过度声明。
//
// 本文件的目标不是「再写 20 个函数」，而是让 34 个名字都能被调用，
// 且**每一条都落到已有实现**上。三种落位：
//
//	1. 直通：能力已在（如 prompt 检索）；
//	2. 薄封装：上游把「创建节点的 N 种形态」拆成 N 个工具，
//	   我们统一走 canvas.apply_ops（op 校验只有一条路径），
//	   在这里做参数到 op 的翻译。翻译函数纯函数化，便于穷举测试。
//	3. 明确 not_implemented：能力确实没有（例如 site_navigate 需要
//	   前端路由，服务端无法执行）。返回可行动的说明，
//	   **绝不返回 ok 而什么都没做**（那是最难排查的一类问题）。
//
// 为什么不做成「只会转发到 apply_ops 的空壳」：那会让
// `canvas_move_nodes` 在参数错误时表现成「op 校验失败」，
// 而用户以为是工具坏了。翻译层负责把参数校验做完，
// 错误信息才能指向用户真正填错的东西。

// UpstreamToolNames 是上游 34 个工具名的完整清单（v0.18.0 的 toolNames）。
//
// 这份清单**必须与上游逐字一致**：它就是契约面本身。
// `scripts/check-upstream-radar.mjs` 的离线夹具会断言这一点——
// 少一个名字意味着「MCP 客户端按名调用会 miss」，而那种失败没有任何报错。
func UpstreamToolNames() []string {
	return []string{
		"site_navigate",
		"canvas_list_projects",
		"canvas_get_state",
		"canvas_get_selection",
		"canvas_export_snapshot",
		"canvas_apply_ops",
		"canvas_create_node",
		"canvas_create_attachment_nodes",
		"canvas_create_text_node",
		"canvas_create_text_nodes",
		"canvas_create_config_node",
		"canvas_create_image_prompt_flow",
		"canvas_create_generation_flow",
		"canvas_generate_text",
		"canvas_generate_image",
		"canvas_generate_video",
		"canvas_generate_audio",
		"canvas_update_node",
		"canvas_update_node_text",
		"canvas_move_nodes",
		"canvas_resize_node",
		"canvas_delete_nodes",
		"canvas_connect_nodes",
		"canvas_select_nodes",
		"canvas_set_viewport",
		"canvas_run_generation",
		"generation_get_status",
		"workbench_image_get_config",
		"workbench_image_generate",
		"workbench_video_get_config",
		"workbench_video_generate",
		"prompts_search",
		"assets_list",
		"assets_add",
	}
}

// upstreamAliases 把上游工具名映射到本仓的规范工具名（若语义完全一致）。
//
// 这类别名是「同一个能力两个名字」，因此只需要一条转发表。
// 有实质差异的（例如参数形状不同）**不放进这张表**，
// 它们在 tools_dispatch.go 里各自实现 —— 混在一起会让
// 「为什么这个名字多了一层校验」变得无法解释。
var upstreamAliases = map[string]string{
	"canvas_get_state":               "canvas.get_state",
	"canvas_get_selection":           "canvas.get_selection",
	"canvas_export_snapshot":         "canvas.export_snapshot",
	"canvas_apply_ops":               "canvas.apply_ops",
	"canvas_create_text_node":        "canvas.create_text_node",
	"canvas_create_generation_flow":  "canvas.create_generation_flow",
	"canvas_create_attachment_nodes": "canvas.create_attachment_nodes",
	"canvas_run_generation":          "canvas.run_generation",
	"prompts_search":                 "prompts.search",
	"assets_list":                    "assets.search",
}

// UpstreamOnlyTools 是「只在 MCP 命名空间存在」的工具（无规范名别名）。
//
// 它们是上游语义化薄封装：画布改动统一走 canvas.apply_ops，
// 但参数校验在翻译层完成（见 opFromXxx 系列）。
func UpstreamOnlyTools() []string {
	var out []string
	for _, name := range UpstreamToolNames() {
		if _, ok := upstreamAliases[name]; ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

// kindFromNodeType 把上游节点类型名映射到本仓节点类型。
//
// 上游：image / text / config / video / audio（+ group）
// 本仓：image / prompt / generation / video / audio（+ group / run）
//
// 这一层映射是**上游兼容**的核心：老客户端的自动化脚本传 `"text"`，
// 我们存 `"prompt"`。不做映射的话表现是「创建成功但节点类型不认识」，
// 而画布上会渲染出一个空白节点。
func kindFromNodeType(t string) (graph.NodeTypeID, bool) {
	switch t {
	case "text":
		return graph.NodeTypePrompt, true
	case "config":
		return graph.NodeTypeGeneration, true
	case "image":
		return graph.NodeTypeImage, true
	case "video":
		return graph.NodeTypeVideo, true
	case "audio":
		return graph.NodeTypeAudio, true
	case "group":
		return graph.NodeTypeGroup, true
	}
	return "", false
}
