// Package plugin 实现插件清单校验、注册表、权限模型与分发。
//
// 安全目标（INV-6）：插件代码永不获得宿主 origin 的 DOM/Cookie/localStorage 访问权。
// 手段：iframe sandbox="allow-scripts"（不加 allow-same-origin）+ 权限声明 + 能力代理。
package plugin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// Manifest 是插件清单（plugin.json）。
type Manifest struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	APIVersion  string    `json:"apiVersion"`
	Author      string    `json:"author,omitempty"`
	Homepage    string    `json:"homepage,omitempty"`
	Entry       string    `json:"entry"`
	Style       string    `json:"style,omitempty"`
	Permissions []string  `json:"permissions"`
	Network     []string  `json:"network,omitempty"`
	Nodes       []NodeDef `json:"nodes"`
	Integrity   *struct {
		SHA256 string `json:"sha256"`
	} `json:"integrity,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// NodeDef 是插件声明的节点定义。
type NodeDef struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	DefaultSize   *Size          `json:"defaultSize,omitempty"`
	MinSize       *Size          `json:"minSize,omitempty"`
	MinimapColor  string         `json:"minimapColor,omitempty"`
	ConfigSchema  map[string]any `json:"configSchema,omitempty"`
	ConfigVersion int            `json:"configVersion,omitempty"`
	Interactive   bool           `json:"interactive,omitempty"`
	Ports         PortsDef       `json:"ports"`
}

// Size 是节点默认尺寸。
type Size struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// PortsDef 是端口声明的传输结构。
type PortsDef struct {
	Inputs  []PortDef `json:"inputs"`
	Outputs []PortDef `json:"outputs"`
}

// PortDef 是单个端口声明。
type PortDef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Multiple bool   `json:"multiple,omitempty"`
	Required bool   `json:"required,omitempty"`
	Order    int    `json:"order,omitempty"`
}

// 权限枚举。默认最小权限：未声明的能力调用一律拒绝。
const (
	PermNodeRead   = "node.read"
	PermNodeWrite  = "node.write"
	PermAssetRead  = "asset.read"
	PermAssetWrite = "asset.write"
	PermAIGenerate = "ai.generate"
	PermStorage    = "storage"
	PermNetwork    = "network"
	PermGraphQuery = "graph.query"
)

// KnownPermission 判断权限是否为已知值（未知权限必须拒绝安装而不是忽略）。
func KnownPermission(p string) bool {
	base := p
	if i := strings.Index(p, ":"); i > 0 {
		base = p[:i] // 形如 ai.generate:image
	}
	switch base {
	case PermNodeRead, PermNodeWrite, PermAssetRead, PermAssetWrite, PermAIGenerate, PermStorage, PermNetwork, PermGraphQuery:
		return true
	}
	return false
}

// CostsMoney 表示该权限会产生真实费用（安装时必须高亮提示）。
func CostsMoney(p string) bool {
	return strings.HasPrefix(p, PermAIGenerate)
}

var (
	keyRe     = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9-]*)+$`)
	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	apiRe     = regexp.MustCompile(`^\^?\d+\.\d+\.\d+$`)
	// 节点类型格式：<pluginKey>:<name>，其中 pluginKey 形如 com.example.plugin
	// （可含多个点分段），name 为 kebab-case。
	nodeTypeRe = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9-]*)+:[a-z][a-z0-9-]*$`)
)

// Validate 校验清单。任何一项不合规都必须拒绝（而不是"尽力而为"）。
func Validate(m *Manifest) error {
	if m == nil {
		return platform.ErrInvalid("manifest is required")
	}
	if !keyRe.MatchString(m.Key) {
		return platform.ErrInvalid("plugin key must look like com.example.plugin").WithDetail("key", m.Key)
	}
	if strings.TrimSpace(m.Name) == "" {
		return platform.ErrInvalid("plugin name is required")
	}
	if !versionRe.MatchString(m.Version) {
		return platform.ErrInvalid("version must be semver").WithDetail("version", m.Version)
	}
	if !apiRe.MatchString(m.APIVersion) {
		return platform.ErrInvalid("apiVersion must be like ^1.0.0").WithDetail("apiVersion", m.APIVersion)
	}
	if m.Entry == "" {
		return platform.ErrInvalid("entry is required")
	}
	// 入口路径不允许逃出插件包（ATK-05 同族）
	if strings.Contains(m.Entry, "..") || strings.HasPrefix(m.Entry, "/") {
		return platform.ErrInvalid("entry must be a relative path inside the package")
	}
	if m.Homepage != "" {
		if u, err := url.Parse(m.Homepage); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return platform.ErrInvalid("homepage must be an http(s) url")
		}
	}
	for _, p := range m.Permissions {
		if !KnownPermission(p) {
			return platform.ErrInvalid("unknown permission").WithDetail("permission", p)
		}
	}
	if len(m.Network) > 0 {
		// 声明网络能力必须同时声明权限，否则拒绝（避免隐式联网）
		if !hasPermission(m.Permissions, PermNetwork) {
			return platform.ErrInvalid("network hosts declared without the network permission")
		}
		for _, h := range m.Network {
			if strings.ContainsAny(h, "/* ") || h == "*" {
				return platform.ErrInvalid("network allowlist must be exact hosts").WithDetail("host", h)
			}
		}
	}
	if len(m.Nodes) == 0 {
		return platform.ErrInvalid("plugin must declare at least one node")
	}
	seen := map[string]bool{}
	for i := range m.Nodes {
		n := &m.Nodes[i]
		if !nodeTypeRe.MatchString(n.Type) {
			return platform.ErrInvalid("node type must be <pluginId>:<name>").WithDetail("type", n.Type)
		}
		if !strings.HasPrefix(n.Type, m.Key+":") {
			return platform.ErrInvalid("node type must be namespaced by the plugin key").
				WithDetail("type", n.Type).WithDetail("key", m.Key)
		}
		if seen[n.Type] {
			return platform.ErrInvalid("duplicate node type").WithDetail("type", n.Type)
		}
		seen[n.Type] = true
		if strings.TrimSpace(n.Title) == "" {
			return platform.ErrInvalid("node title is required").WithDetail("type", n.Type)
		}
		if err := validatePorts(n.Type, n.Ports); err != nil {
			return err
		}
		if n.ConfigSchema != nil {
			if err := validateConfigSchema(n.ConfigSchema); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePorts(nodeType string, p PortsDef) error {
	ids := map[string]bool{}
	for _, group := range [][]PortDef{p.Inputs, p.Outputs} {
		for _, port := range group {
			if port.ID == "" {
				return platform.ErrInvalid("port id is required").WithDetail("node", nodeType)
			}
			if ids[port.ID] {
				return platform.ErrInvalid("duplicate port id").WithDetail("node", nodeType).WithDetail("port", port.ID)
			}
			ids[port.ID] = true
			if !graph.ValidResourceKind(graph.ResourceKind(port.Kind)) {
				return platform.ErrInvalid("invalid port kind").
					WithDetail("node", nodeType).WithDetail("port", port.ID).WithDetail("kind", port.Kind)
			}
		}
	}
	return nil
}

// validateConfigSchema 做 JSON Schema 的最小结构校验。
// 完整校验（类型/约束）由执行期的 configSchema 校验器负责；这里只挡住明显非法的结构。
func validateConfigSchema(schema map[string]any) error {
	t, ok := schema["type"]
	if !ok {
		return platform.ErrInvalid("configSchema must declare a type")
	}
	ts, ok := t.(string)
	if !ok {
		return platform.ErrInvalid("configSchema.type must be a string")
	}
	switch ts {
	case "object", "array", "string", "number", "integer", "boolean":
	default:
		return platform.ErrInvalid("unsupported configSchema.type").WithDetail("type", ts)
	}
	if ts == "object" {
		if props, ok := schema["properties"]; ok {
			if _, isMap := props.(map[string]any); !isMap {
				return platform.ErrInvalid("configSchema.properties must be an object")
			}
		}
	}
	return nil
}

// SpecSchemaFor 把插件节点声明转换为 graph 的节点 schema（供 op 校验）。
func SpecSchemaFor(m *Manifest, nodeType string) (graph.SpecSchema, bool) {
	for _, n := range m.Nodes {
		if n.Type != nodeType {
			continue
		}
		fields := map[string]graph.FieldKind{}
		if props, ok := n.ConfigSchema["properties"].(map[string]any); ok {
			for key, raw := range props {
				fields[key] = fieldKindOf(raw)
			}
		}
		// 插件节点的保留字段（由宿主管理，插件不能覆盖语义）
		fields["pluginKey"] = graph.FieldString
		fields["schemaVersion"] = graph.FieldInt
		return graph.SpecSchema{
			Type:         graph.NodeTypeID(n.Type),
			DefaultTitle: n.Title,
			DefaultSize:  defaultSize(n),
			MinimapColor: n.MinimapColor,
			Ports:        toGraphPorts(n.Ports),
			Fields:       fields,
			Description:  "插件节点，由 " + m.Key + " 提供",
		}, true
	}
	return graph.SpecSchema{}, false
}

func defaultSize(n NodeDef) [2]float64 {
	w, h := 320.0, 240.0
	if n.DefaultSize != nil && n.DefaultSize.Width > 0 {
		w = float64(n.DefaultSize.Width)
	}
	if n.DefaultSize != nil && n.DefaultSize.Height > 0 {
		h = float64(n.DefaultSize.Height)
	}
	return [2]float64{w, h}
}

func toGraphPorts(p PortsDef) graph.Ports {
	conv := func(in []PortDef) []graph.Port {
		out := make([]graph.Port, 0, len(in))
		for _, x := range in {
			out = append(out, graph.Port{
				ID: x.ID, Name: x.Name, Kind: graph.ResourceKind(x.Kind),
				Multiple: x.Multiple, Required: x.Required, Order: x.Order,
			})
		}
		return out
	}
	return graph.Ports{Inputs: conv(p.Inputs), Outputs: conv(p.Outputs)}
}

func fieldKindOf(raw any) graph.FieldKind {
	m, ok := raw.(map[string]any)
	if !ok {
		return graph.FieldAny
	}
	switch m["type"] {
	case "string":
		return graph.FieldString
	case "integer":
		return graph.FieldInt
	case "number":
		return graph.FieldNumber
	case "boolean":
		return graph.FieldBool
	case "array":
		if items, ok := m["items"].(map[string]any); ok && items["type"] == "string" {
			return graph.FieldStrings
		}
		return graph.FieldAny
	case "object":
		return graph.FieldObject
	}
	return graph.FieldAny
}

func hasPermission(list []string, want string) bool {
	for _, p := range list {
		if p == want || strings.HasPrefix(p, want+":") {
			return true
		}
	}
	return false
}

// PermissionDiff 计算权限变化（用于「更新后权限扩大需重新确认」）。
type PermissionDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// Expanded 表示权限是否扩大（新增了任何权限即为扩大）。
func (d PermissionDiff) Expanded() bool { return len(d.Added) > 0 }

// DiffPermissions 比较两版权限。
func DiffPermissions(before, after []string) PermissionDiff {
	bset := map[string]bool{}
	aset := map[string]bool{}
	for _, p := range before {
		bset[p] = true
	}
	for _, p := range after {
		aset[p] = true
	}
	diff := PermissionDiff{}
	for p := range aset {
		if !bset[p] {
			diff.Added = append(diff.Added, p)
		}
	}
	for p := range bset {
		if !aset[p] {
			diff.Removed = append(diff.Removed, p)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	return diff
}

// MarshalManifest 序列化（落库用）。
func MarshalManifest(m *Manifest) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// UnmarshalManifest 反序列化。
func UnmarshalManifest(raw string) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("manifest is not valid json: %w", err)
	}
	return &m, nil
}
