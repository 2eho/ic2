package agent

import (
	"encoding/json"

	"github.com/context-flow/ic/internal/graph"
)

// ToolSet 是工具表。**由画布 op schema 生成**，不手写第二份定义，
// 避免 REST 工具与 MCP 工具漂移（见 docs/design/07 §4）。
func ToolSet() []ToolDef {
	return []ToolDef{
		{
			Name:        "canvas.get_state",
			Description: "读取画布当前状态：节点、连线、版本号。",
			InputSchema: schemaObject(map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
			}),
			Approval: ApprovalAuto,
			Scope:    "read",
		},
		{
			Name:        "canvas.get_selection",
			Description: "读取用户当前选中的节点与连线。",
			InputSchema: schemaObject(nil),
			Approval:    ApprovalAuto,
			Scope:       "read",
		},
		{
			Name:        "canvas.export_snapshot",
			Description: "导出画布快照（节点内容截断，用于上下文）。",
			InputSchema: schemaObject(nil),
			Approval:    ApprovalAuto,
			Scope:       "read",
		},
		{
			Name:        "canvas.apply_ops",
			Description: "对画布应用一批操作（add_node/move_node/set_spec/add_edge/...）。",
			InputSchema: schemaObject(map[string]any{
				"ops": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind": map[string]any{"type": "string", "enum": opKindNames()},
						},
						"required": []string{"kind"},
					},
					"maxItems": graph.MaxOpBatch,
				},
			}, []string{"ops"}),
			Approval: ApprovalConfirm,
			Scope:    "write",
		},
		{
			Name:        "canvas.create_text_node",
			Description: "创建一个文本/提示词节点。",
			InputSchema: schemaObject(map[string]any{
				"text":  map[string]any{"type": "string"},
				"x":     map[string]any{"type": "number"},
				"y":     map[string]any{"type": "number"},
				"title": map[string]any{"type": "string"},
			}, []string{"text"}),
			Approval: ApprovalAuto,
			Scope:    "write",
		},
		{
			Name:        "canvas.create_generation_flow",
			Description: "创建「提示词 → 生成」两节点并连线。会产生真实费用，需确认。",
			InputSchema: schemaObject(map[string]any{
				"prompt":     map[string]any{"type": "string"},
				"capability": map[string]any{"type": "string"},
				"model":      map[string]any{"type": "string"},
			}, []string{"prompt"}),
			Approval:   ApprovalConfirm,
			Scope:      "write",
			CostsMoney: true,
		},
		{
			Name:        "canvas.run_generation",
			Description: "触发一次生成运行。会产生真实费用，需确认。",
			InputSchema: schemaObject(map[string]any{
				"nodeIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, []string{"nodeIds"}),
			Approval:   ApprovalConfirm,
			Scope:      "write",
			CostsMoney: true,
		},
		{
			Name:        "assets.search",
			Description: "在工作区素材库中检索资源。",
			InputSchema: schemaObject(map[string]any{
				"query": map[string]any{"type": "string"},
				"kind":  map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
			Approval: ApprovalAuto,
			Scope:    "read",
		},
		{
			Name:        "prompts.search",
			Description: "在提示词库中检索。",
			InputSchema: schemaObject(map[string]any{
				"query": map[string]any{"type": "string"},
				"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
			Approval: ApprovalAuto,
			Scope:    "read",
		},
		{
			Name:        "runs.list",
			Description: "列出当前画布的运行记录。",
			InputSchema: schemaObject(map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
			Approval: ApprovalAuto,
			Scope:    "read",
		},
		{
			Name:        "runs.get",
			Description: "读取某次运行的详情（步骤、尝试、计量）。",
			InputSchema: schemaObject(map[string]any{
				"runId": map[string]any{"type": "string"},
			}, []string{"runId"}),
			Approval: ApprovalAuto,
			Scope:    "read",
		},
		{
			Name:        "canvas.create_attachment_nodes",
			Description: "把附件（图片/文件）落成画布图片节点，自动避让已有节点。",
			InputSchema: schemaObject(map[string]any{
				"attachments": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
							"mime": map[string]any{"type": "string"},
							"url":  map[string]any{"type": "string"},
							"data": map[string]any{"type": "string"},
						},
						"additionalProperties": false,
					},
					"minItems": 1,
					"maxItems": 20,
				},
				"originX": map[string]any{"type": "number"},
				"originY": map[string]any{"type": "number"},
			}, []string{"attachments"}),
			Approval: ApprovalConfirm,
			Scope:    "write",
		},
		{
			Name:        "skills.list",
			Description: "列出当前工作区可用的 Agent Skills。",
			InputSchema: schemaObject(nil),
			Approval:    ApprovalAuto,
			Scope:       "read",
		},
		{
			Name:        "skills.save",
			Description: "创建或更新一个 Skill（可复用的任务指令片段）。",
			InputSchema: schemaObject(map[string]any{
				"name":        map[string]any{"type": "string", "maxLength": 64},
				"description": map[string]any{"type": "string", "maxLength": 500},
				"instructions": map[string]any{
					"type": "string", "maxLength": graph.MaxPromptBytes,
				},
				"enabled": map[string]any{"type": "boolean"},
				"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, []string{"name", "instructions"}),
			Approval: ApprovalConfirm,
			Scope:    "write",
		},
	}
}

// ToolByName 查表。
func ToolByName(name string) (ToolDef, bool) {
	for _, t := range ToolSet() {
		if t.Name == name {
			return t, true
		}
	}
	return ToolDef{}, false
}

// ApprovalFor 结合会话权限档位决定实际审批策略。
//
// 关键约束：**产生费用的工具在任何档位下都需要确认**，
// 只有用户显式开启「本会话自动放行」才可免除（且随会话过期）。
func ApprovalFor(tool ToolDef, mode PermissionMode) ApprovalMode {
	if tool.CostsMoney {
		if mode == PermFull {
			return ApprovalAuto
		}
		return ApprovalConfirm
	}
	switch mode {
	case PermFull:
		return ApprovalAuto
	case PermAutomatic:
		if tool.Scope == "read" {
			return ApprovalAuto
		}
		return ApprovalConfirm
	default:
		if tool.Scope == "read" {
			return ApprovalAuto
		}
		return ApprovalConfirm
	}
}

func opKindNames() []string {
	kinds := graph.AllOpKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

func schemaObject(props map[string]any, required ...[]string) json.RawMessage {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 && len(required[0]) > 0 {
		s["required"] = required[0]
	}
	b, _ := json.Marshal(s)
	return b
}
