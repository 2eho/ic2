package agent

import "encoding/json"

// 上游 34 个工具的定义。
//
// 与 tools.go 的关系：`ToolSet()` 返回本仓的**规范工具表**（14 个，
// 内部一致、语义清晰）；`UpstreamToolSet()` 返回上游工具名到规范的
// **完整映射表**（34 个名字全部可用）。
//
// 为什么保留两套而不是合并：规范表是「我们希望工具长什么样」，
// 上游表是「已有客户端期望工具长什么样」。把后者塞进前者会让
// 规范表变成上游的镜像，「为什么有两个 create_text_node」也无法解释。
// MCP 对外只暴露一张表：并集（见 mcp.go），但每个名字都标注了来源。

// upstreamToolDef 描述一个上游工具。
type upstreamToolDef struct {
	name        string
	description string
	// canonical 非空时表示「直通到规范工具」，不再单独实现。
	canonical string
	// schema 是入参 schema（canonical 非空时也用它的，但保留上游描述，
	// 因为描述是给模型看的，上游描述里写清了副作用与前置条件）。
	schema   json.RawMessage
	scope    string
	approval ApprovalMode
	costs    bool
}

var viewportSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"x": map[string]any{"type": "number"},
		"y": map[string]any{"type": "number"},
		"k": map[string]any{"type": "number"},
	},
	"required":             []string{"x", "y", "k"},
	"additionalProperties": false,
}

var nodeTypeEnum = []string{"text", "image", "config", "video", "audio", "group"}

// upstreamToolDefs 是全部 34 个上游工具的声明。
func upstreamToolDefs() []upstreamToolDef {
	return []upstreamToolDef{
		{
			name: "site_navigate",
			description: "跳转网站页面。path 可为 / (首页)、/canvas (我的画布)、" +
				"/canvas/:id (指定画布)、/image、/video、/prompts、/assets、/config。" +
				"服务端形态下返回受限的导航提示：前端路由由画布客户端执行，" +
				"服务端不代用户跳转（否则会变成一个可被 Agent 触发的开放重定向）。",
			schema: schemaProps(map[string]any{
				"path": map[string]any{"type": "string"},
			}),
			scope: "read",
		},
		{
			name:        "canvas_list_projects",
			description: "列出用户全部画布（仅标题、时间、节点数，不含完整数据），支持 keyword 与分页。",
			schema: schemaProps(map[string]any{
				"keyword":  map[string]any{"type": "string"},
				"page":     map[string]any{"type": "integer", "minimum": 1},
				"pageSize": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
			scope: "read",
		},
		{
			name: "canvas_create_node",
			description: "创建任意类型节点：text、image、config、video、audio、group。" +
				"适合创建占位图、媒体占位、配置节点或自定义 metadata 节点。",
			schema: schemaProps(map[string]any{
				"nodeType": map[string]any{"type": "string", "enum": nodeTypeEnum},
				"title":    map[string]any{"type": "string"},
				"x":        map[string]any{"type": "number"},
				"y":        map[string]any{"type": "number"},
				"width":    map[string]any{"type": "number"},
				"height":   map[string]any{"type": "number"},
				"metadata": map[string]any{"type": "object"},
			}, "nodeType"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name:        "canvas_create_text_nodes",
			description: "批量创建文本节点，适合生成标题、段落、脚本、说明等内容块。",
			schema: schemaProps(map[string]any{
				"items": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 20,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"text":   map[string]any{"type": "string"},
							"title":  map[string]any{"type": "string"},
							"x":      map[string]any{"type": "number"},
							"y":      map[string]any{"type": "number"},
							"width":  map[string]any{"type": "number"},
							"height": map[string]any{"type": "number"},
						},
						"required": []string{"text"},
					},
				},
				"x":         map[string]any{"type": "number"},
				"y":         map[string]any{"type": "number"},
				"gap":       map[string]any{"type": "number"},
				"direction": map[string]any{"type": "string", "enum": []string{"row", "column"}},
			}, "items"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name: "canvas_create_config_node",
			description: "创建生成配置节点，可指定 text/image/video/audio 模式与生成参数，" +
				"autoRun 为真时立即触发生成（会产生真实费用）。",
			schema: genFlowSchema(map[string]any{
				"prompt":   map[string]any{"type": "string"},
				"mode":     map[string]any{"type": "string", "enum": []string{"text", "image", "video", "audio"}},
				"title":    map[string]any{"type": "string"},
				"x":        map[string]any{"type": "number"},
				"y":        map[string]any{"type": "number"},
				"autoRun":  map[string]any{"type": "boolean"},
				"promptId": map[string]any{"type": "string"},
			}),
			scope:    "write",
			approval: ApprovalConfirm,
			costs:    true,
		},
		{
			name:        "canvas_create_image_prompt_flow",
			description: "创建提示词文本节点和图片生成配置节点并自动连线，autoRun 时立即生图。",
			schema: genFlowSchema(map[string]any{
				"prompt":  map[string]any{"type": "string"},
				"x":       map[string]any{"type": "number"},
				"y":       map[string]any{"type": "number"},
				"autoRun": map[string]any{"type": "boolean"},
			}),
			scope:    "write",
			approval: ApprovalConfirm,
			costs:    true,
		},
		{
			name:        "canvas_generate_text",
			description: "创建文本生成流程并立即触发生成。",
			schema:      genFlowSchema(map[string]any{"prompt": map[string]any{"type": "string"}}),
			scope:       "write",
			approval:    ApprovalConfirm,
			costs:       true,
		},
		{
			name:        "canvas_generate_image",
			description: "创建图片生成流程并立即触发生成。",
			schema:      genFlowSchema(map[string]any{"prompt": map[string]any{"type": "string"}}),
			scope:       "write",
			approval:    ApprovalConfirm,
			costs:       true,
		},
		{
			name:        "canvas_generate_video",
			description: "创建视频生成流程并立即触发生成。",
			schema:      genFlowSchema(map[string]any{"prompt": map[string]any{"type": "string"}}),
			scope:       "write",
			approval:    ApprovalConfirm,
			costs:       true,
		},
		{
			name:        "canvas_generate_audio",
			description: "创建音频生成流程并立即触发生成。",
			schema:      genFlowSchema(map[string]any{"prompt": map[string]any{"type": "string"}}),
			scope:       "write",
			approval:    ApprovalConfirm,
			costs:       true,
		},
		{
			name:        "canvas_update_node",
			description: "更新节点基础字段或 metadata。",
			schema: schemaProps(map[string]any{
				"id":       map[string]any{"type": "string"},
				"title":    map[string]any{"type": "string"},
				"patch":    map[string]any{"type": "object"},
				"metadata": map[string]any{"type": "object"},
			}, "id"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name:        "canvas_update_node_text",
			description: "更新文本节点内容和标题。",
			schema: schemaProps(map[string]any{
				"id":    map[string]any{"type": "string"},
				"text":  map[string]any{"type": "string"},
				"title": map[string]any{"type": "string"},
			}, "id", "text"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name:        "canvas_move_nodes",
			description: "移动一个或多个节点，支持绝对坐标或 dx/dy 偏移。",
			schema: schemaProps(map[string]any{
				"items": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 200,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{"type": "string"},
							"x":  map[string]any{"type": "number"},
							"y":  map[string]any{"type": "number"},
							"dx": map[string]any{"type": "number"},
							"dy": map[string]any{"type": "number"},
						},
						"required": []string{"id"},
					},
				},
			}, "items"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name:        "canvas_resize_node",
			description: "调整节点尺寸。",
			schema: schemaProps(map[string]any{
				"id":     map[string]any{"type": "string"},
				"width":  map[string]any{"type": "number"},
				"height": map[string]any{"type": "number"},
			}, "id", "width", "height"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name:        "canvas_delete_nodes",
			description: "删除指定节点及相关连线。",
			schema: schemaProps(map[string]any{
				"ids": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 200,
					"items": map[string]any{"type": "string"},
				},
			}, "ids"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name: "canvas_connect_nodes",
			description: "批量连接节点。form 与 to 的端口由服务端按类型自动选择，" +
				"因此不需要客户端知道端口 ID（端口是内部实现细节）。",
			schema: schemaProps(map[string]any{
				"connections": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 200,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"fromNodeId": map[string]any{"type": "string"},
							"toNodeId":   map[string]any{"type": "string"},
						},
						"required": []string{"fromNodeId", "toNodeId"},
					},
				},
			}, "connections"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
		{
			name: "canvas_select_nodes",
			description: "设置当前选中节点。" +
				"服务端形态下返回 not_implemented：选中态是**每个客户端各自**的视图状态，" +
				"放服务端会导致「A 的选中把 B 的选中改掉」。",
			schema: schemaProps(map[string]any{
				"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "ids"),
			scope: "write",
		},
		{
			name:        "canvas_set_viewport",
			description: "调整画布视口。视口是画布文档的一部分（多端一致），因此这里真的会写。",
			schema:      schemaProps(map[string]any{"viewport": viewportSchema}, "viewport"),
			scope:       "write",
			approval:    ApprovalConfirm,
		},
		{
			name: "generation_get_status",
			description: "查询生成任务状态。默认返回当前画布最近任务；" +
				"可用 nodeIds 过滤到指定节点。",
			schema: schemaProps(map[string]any{
				"scope":   map[string]any{"type": "string", "enum": []string{"all", "canvas", "image", "video"}},
				"taskId":  map[string]any{"type": "string"},
				"nodeIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
			scope: "read",
		},
		{
			name:        "workbench_image_get_config",
			description: "读取生图工作台的当前参数与可选项（模型、质量、分辨率 1k/2k/4k/auto、宽高比、张数范围）。",
			schema:      schemaProps(nil),
			scope:       "read",
		},
		{
			name: "workbench_image_generate",
			description: "在生图工作台发起生成（不落画布，走与画布同一个执行引擎）。" +
				"run 默认 true，返回 runId 供查询状态。",
			schema: schemaProps(map[string]any{
				"prompt":  map[string]any{"type": "string"},
				"model":   map[string]any{"type": "string"},
				"quality": map[string]any{"type": "string"},
				"size":    map[string]any{"type": "string"},
				"count":   map[string]any{"type": "integer", "minimum": 1, "maximum": 15},
				"run":     map[string]any{"type": "boolean"},
			}, "prompt"),
			scope:    "write",
			approval: ApprovalConfirm,
			costs:    true,
		},
		{
			name:        "workbench_video_get_config",
			description: "读取视频创作台的当前参数与可选项（清晰度 480/720/1080、比例、时长 4–30 秒、首尾帧/全能参考模式）。",
			schema:      schemaProps(nil),
			scope:       "read",
		},
		{
			name:        "workbench_video_generate",
			description: "在视频创作台发起生成（不落画布）。",
			schema: schemaProps(map[string]any{
				"prompt":        map[string]any{"type": "string"},
				"model":         map[string]any{"type": "string"},
				"size":          map[string]any{"type": "string"},
				"seconds":       map[string]any{"type": "string"},
				"resolution":    map[string]any{"type": "string"},
				"generateAudio": map[string]any{"type": "boolean"},
				"watermark":     map[string]any{"type": "boolean"},
				"mode":          map[string]any{"type": "string", "enum": []string{"frames", "reference"}},
				"run":           map[string]any{"type": "boolean"},
			}, "prompt"),
			scope:    "write",
			approval: ApprovalConfirm,
			costs:    true,
		},
		{
			name:        "assets_add",
			description: "向「我的素材」新增素材。kind=text 用 content；kind=image 用 imageUrl（或 dataURL）。",
			schema: schemaProps(map[string]any{
				"kind":     map[string]any{"type": "string", "enum": []string{"text", "image"}},
				"title":    map[string]any{"type": "string"},
				"content":  map[string]any{"type": "string"},
				"imageUrl": map[string]any{"type": "string"},
				"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"source":   map[string]any{"type": "string"},
				"note":     map[string]any{"type": "string"},
			}, "kind", "title"),
			scope:    "write",
			approval: ApprovalConfirm,
		},
	}
}

// genFlowSchema 是生成类工具共用的入参（生成参数 + 位置）。
func genFlowSchema(extra map[string]any, required ...string) json.RawMessage {
	props := map[string]any{}
	for k, v := range extra {
		props[k] = v
	}
	for k, v := range generationOptionProps() {
		props[k] = v
	}
	props["referenceNodeIds"] = map[string]any{
		"type": "array", "items": map[string]any{"type": "string"},
	}
	return schemaProps(props, required...)
}

// generationOptionProps 是生成参数（与上游 generationOptionsSchema 对齐）。
func generationOptionProps() map[string]any {
	return map[string]any{
		"model":             map[string]any{"type": "string"},
		"size":              map[string]any{"type": "string"},
		"quality":           map[string]any{"type": "string"},
		"count":             map[string]any{"type": "integer", "minimum": 1, "maximum": 15},
		"seconds":           map[string]any{"type": "string"},
		"vquality":          map[string]any{"type": "string"},
		"generateAudio":     map[string]any{"type": "string"},
		"watermark":         map[string]any{"type": "string"},
		"videoMode":         map[string]any{"type": "string"},
		"audioVoice":        map[string]any{"type": "string"},
		"audioFormat":       map[string]any{"type": "string"},
		"audioSpeed":        map[string]any{"type": "string"},
		"audioInstructions": map[string]any{"type": "string"},
	}
}

// schemaProps 构造入参 schema（与 tools.go 的 schemaObject 同一形状）。
func schemaProps(props map[string]any, required ...string) json.RawMessage {
	return schemaObject(props, required)
}
