package graph

import (
	"fmt"
	"math"
	"strings"
)

// SpecSchema 描述某个节点类型的配置契约。服务端按 type 解码 Spec，
// 不做「万能 metadata 袋」的宽松解析（见 02-domain-model §2.1）。
type SpecSchema struct {
	Type  NodeTypeID
	Ports Ports
	// Fields 声明允许的字段与类型，未知字段直接拒绝（additionalProperties:false）。
	Fields map[string]FieldKind
	// Required 必填字段。
	Required []string
	// DefaultTitle 新节点默认标题。
	DefaultTitle string
	// DefaultSize 新节点默认尺寸。
	DefaultSize [2]float64
	// MinimapColor 前端小地图颜色。
	MinimapColor string
	// Executable 表示参与 DAG 编译（生成型节点）。
	Executable bool
	// Singleton 表示同类型节点在图中的语义（run 锚点等）。
	Description string
}

// FieldKind 是字段类型。
type FieldKind string

// 支持的字段类型。
const (
	FieldString  FieldKind = "string"
	FieldNumber  FieldKind = "number"
	FieldInt     FieldKind = "integer"
	FieldBool    FieldKind = "bool"
	FieldStrings FieldKind = "string[]"
	FieldObject  FieldKind = "object"
	FieldAny     FieldKind = "any"
)

func port(id, name string, kind ResourceKind, multiple, required bool, order int) Port {
	return Port{ID: id, Name: name, Kind: kind, Multiple: multiple, Required: required, Order: order}
}

var builtinSchemas = map[NodeTypeID]SpecSchema{
	NodeTypePrompt: {
		Type:         NodeTypePrompt,
		DefaultTitle: "提示词",
		DefaultSize:  [2]float64{320, 220},
		MinimapColor: "#7dd3fc",
		Ports: Ports{
			Outputs: []Port{port("out", "文本", KindText, false, false, 0)},
		},
		Fields: map[string]FieldKind{
			"text":       FieldString,
			"fontSize":   FieldNumber,
			"variables":  FieldObject,
			"promptWrap": FieldBool,
		},
		Required:    []string{"text"},
		Description: "文本/提示词，可含 {{var}} 变量与 @ref 引用",
	},
	NodeTypeImage: {
		Type:         NodeTypeImage,
		DefaultTitle: "图片",
		DefaultSize:  [2]float64{320, 320},
		MinimapColor: "#a5b4fc",
		Ports: Ports{
			Inputs:  []Port{port("in", "输入", KindImage, true, false, 0)},
			Outputs: []Port{port("out", "图片", KindImage, false, false, 0)},
		},
		Fields: map[string]FieldKind{
			"assetId":     FieldString,
			"fit":         FieldString,
			"freeResize":  FieldBool,
			"annotations": FieldAny,
			"naturalW":    FieldNumber,
			"naturalH":    FieldNumber,
			"bytes":       FieldInt,
			"mime":        FieldString,
		},
		Description: "图片资源节点，只存 assetId",
	},
	NodeTypeVideo: {
		Type:         NodeTypeVideo,
		DefaultTitle: "视频",
		DefaultSize:  [2]float64{400, 260},
		MinimapColor: "#fca5a5",
		Ports: Ports{
			Inputs:  []Port{port("in", "输入", KindVideo, true, false, 0)},
			Outputs: []Port{port("out", "视频", KindVideo, false, false, 0)},
		},
		Fields: map[string]FieldKind{
			"assetId":    FieldString,
			"poster":     FieldString,
			"loop":       FieldBool,
			"controls":   FieldBool,
			"durationMs": FieldInt,
			"naturalW":   FieldNumber,
			"naturalH":   FieldNumber,
			"bytes":      FieldInt,
			"mime":       FieldString,
		},
		Description: "视频资源节点",
	},
	NodeTypeAudio: {
		Type:         NodeTypeAudio,
		DefaultTitle: "音频",
		DefaultSize:  [2]float64{360, 140},
		MinimapColor: "#fcd34d",
		Ports: Ports{
			Inputs:  []Port{port("in", "输入", KindAudio, true, false, 0)},
			Outputs: []Port{port("out", "音频", KindAudio, false, false, 0)},
		},
		Fields: map[string]FieldKind{
			"assetId":    FieldString,
			"durationMs": FieldInt,
			"bytes":      FieldInt,
			"mime":       FieldString,
			"waveform":   FieldAny,
		},
		Description: "音频资源节点",
	},
	NodeTypeGroup: {
		Type:         NodeTypeGroup,
		DefaultTitle: "分组",
		DefaultSize:  [2]float64{480, 320},
		MinimapColor: "#cbd5e1",
		Fields: map[string]FieldKind{
			"collapsed": FieldBool,
			"tint":      FieldString,
		},
		Description: "布局容器，不参与执行（编译时展开为输入环境）",
	},
	NodeTypeGeneration: {
		Type:         NodeTypeGeneration,
		DefaultTitle: "生成",
		DefaultSize:  [2]float64{340, 260},
		MinimapColor: "#86efac",
		Executable:   true,
		Ports: Ports{
			Inputs: []Port{
				port("prompt", "提示词", KindText, true, false, 0),
				port("ref", "参考图", KindImage, true, false, 1),
			},
			Outputs: []Port{
				port("out", "结果", KindImage, false, false, 0),
				port("text", "文本", KindText, false, false, 1),
			},
		},
		Fields: map[string]FieldKind{
			"capability":     FieldString,
			"providerId":     FieldString,
			"credentialId":   FieldString,
			"model":          FieldString,
			"params":         FieldObject,
			"outputCount":    FieldInt,
			"promptTemplate": FieldString,
			"editMode":       FieldString,
			"reasoningLevel": FieldString,
		},
		Required:    []string{"capability"},
		Description: "生成配置节点：能力 + 模型 + 参数",
	},
	NodeTypeRun: {
		Type:         NodeTypeRun,
		DefaultTitle: "运行",
		DefaultSize:  [2]float64{280, 160},
		MinimapColor: "#f0abfc",
		Executable:   true,
		Ports: Ports{
			Outputs: []Port{port("out", "结果", KindJSON, false, false, 0)},
		},
		Fields: map[string]FieldKind{
			"runId": FieldString,
		},
		Description: "运行锚点：把一次 Run 的结果作为图上资源",
	},
}

// BuiltinSchema 返回内置类型的 schema。
func BuiltinSchema(t NodeTypeID) (SpecSchema, bool) {
	s, ok := builtinSchemas[t]
	return s, ok
}

// pluginSchemas 由插件清单注册（M5）。
type pluginSchemaRegistrar interface {
	SpecSchemaFor(t NodeTypeID) (SpecSchema, bool)
}

var pluginSchemas pluginSchemaRegistrar

// RegisterPluginSchemas 注册插件节点 schema 提供者（由 plugin 模块在装配时调用）。
func RegisterPluginSchemas(r pluginSchemaRegistrar) { pluginSchemas = r }

// SchemaFor 返回任意节点类型的 schema（内置或插件）。
func SchemaFor(t NodeTypeID) (SpecSchema, bool) {
	if !ValidNodeTypeID(string(t)) {
		return SpecSchema{}, false
	}
	if s, ok := BuiltinSchema(t); ok {
		return s, true
	}
	if IsPluginType(t) {
		if pluginSchemas != nil {
			return pluginSchemas.SpecSchemaFor(t)
		}
		return SpecSchema{}, false
	}
	return SpecSchema{}, false
}

// ValidateSpec 按 schema 校验 Spec，未知字段与类型不符一律拒绝。
func ValidateSpec(t NodeTypeID, spec NodeSpec) error {
	s, ok := SchemaFor(t)
	if !ok {
		return errInvalidSpec(t, "unknown node type")
	}
	for k := range spec {
		if _, allowed := s.Fields[k]; !allowed {
			return errInvalidSpec(t, fmt.Sprintf("unknown field %q", k))
		}
	}
	for _, req := range s.Required {
		if _, ok := spec[req]; !ok {
			return errInvalidSpec(t, fmt.Sprintf("missing required field %q", req))
		}
	}
	for k, v := range spec {
		kind := s.Fields[k]
		if v == nil {
			// 显式删除用 Unset 语义，null 不接受（见 11 §2.4）。
			return errInvalidSpec(t, fmt.Sprintf("field %q must not be null; use unset", k))
		}
		if err := checkField(k, kind, v); err != nil {
			return errInvalidSpec(t, fmt.Sprintf("field %q: %v", k, err))
		}
	}
	if t == NodeTypePrompt {
		if text, ok := spec["text"].(string); ok && len(text) > MaxPromptBytes {
			return NewError(413, CodePayloadTooLarge, "prompt text exceeds limit").
				WithDetail("limitBytes", MaxPromptBytes)
		}
	}
	if t == NodeTypeImage || t == NodeTypeVideo || t == NodeTypeAudio {
		if id, ok := spec["assetId"].(string); ok && id != "" && !ValidID(id) {
			return errInvalidSpec(t, "assetId is not a valid id")
		}
	}
	if t == NodeTypeGeneration {
		if raw, ok := spec["outputCount"]; ok {
			n, ok := asInt(raw)
			if !ok {
				return errInvalidSpec(t, "outputCount must be integer")
			}
			if n < 1 || n > 15 {
				return NewError(422, CodeInvalidSpec, "outputCount out of range").
					WithDetail("min", 1).WithDetail("max", 15).WithDetail("got", n)
			}
		}
		if raw, ok := spec["capability"]; ok {
			cap, _ := raw.(string)
			if !ValidCapabilityString(cap) {
				return errInvalidSpec(t, "unknown capability "+cap)
			}
		}
	}
	return nil
}

func errInvalidSpec(t NodeTypeID, msg string) *DomainError {
	return NewError(422, CodeInvalidSpec, string(t)+": "+msg)
}

func checkField(name string, kind FieldKind, v any) error {
	switch kind {
	case FieldString:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("expect string")
		}
	case FieldBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expect bool")
		}
	case FieldNumber:
		f, ok := asFloat(v)
		if !ok {
			return fmt.Errorf("expect number")
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("must be finite")
		}
	case FieldInt:
		if _, ok := asInt(v); !ok {
			return fmt.Errorf("expect integer")
		}
	case FieldStrings:
		arr, ok := v.([]any)
		if !ok {
			if ss, ok2 := v.([]string); ok2 {
				_ = ss
				return nil
			}
			return fmt.Errorf("expect string array")
		}
		for _, it := range arr {
			if _, ok := it.(string); !ok {
				return fmt.Errorf("expect string array element")
			}
		}
	case FieldObject:
		if _, ok := v.(map[string]any); !ok {
			if _, ok2 := v.(NodeSpec); !ok2 {
				return fmt.Errorf("expect object")
			}
		}
	case FieldAny:
	default:
		return fmt.Errorf("unknown field kind")
	}
	_ = name
	return nil
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json_Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != math.Trunc(n) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json_Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}

// ValidCapabilityString 校验能力名（与 exec 的能力枚举保持一致）。
func ValidCapabilityString(c string) bool {
	switch strings.TrimSpace(c) {
	case "image.generate", "image.edit", "image.upscale", "video.generate",
		"text.generate", "text.tools", "audio.generate", "model.list":
		return true
	}
	return false
}
