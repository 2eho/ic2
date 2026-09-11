// Package graph 是画布文档的领域核心：文档模型、op 校验与重放、版本、冲突 rebase、事件。
// 它不依赖 HTTP / DB / 上层模块（见 docs/design/01 §2 分层规则）。
package graph

import (
	"regexp"
	"time"
)

// NodeTypeID 是节点类型标识：内置类型为 "image"/"text"/...，插件节点为 "<pluginId>:<name>"。
type NodeTypeID string

// 内置节点类型。
const (
	NodeTypePrompt     NodeTypeID = "prompt"
	NodeTypeImage      NodeTypeID = "image"
	NodeTypeVideo      NodeTypeID = "video"
	NodeTypeAudio      NodeTypeID = "audio"
	NodeTypeGroup      NodeTypeID = "group"
	NodeTypeGeneration NodeTypeID = "generation"
	NodeTypeRun        NodeTypeID = "run"
)

// BuiltinNodeTypes 列出全部内置类型。
func BuiltinNodeTypes() []NodeTypeID {
	return []NodeTypeID{NodeTypePrompt, NodeTypeImage, NodeTypeVideo, NodeTypeAudio, NodeTypeGroup, NodeTypeGeneration, NodeTypeRun}
}

// validTypeRe 限定内置类型：^[a-z][a-z0-9-]*$。
var validTypeRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// pluginTypeRe 限定插件节点类型：<pluginKey>:<name>。
// pluginKey 形如 com.example.demo（点分段），name 为 kebab-case。
// 两者都禁止路径分隔符与控制字符，防路径穿越（见 11 §2.4）。
var pluginTypeRe = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9-]*)+:[a-z][a-z0-9-]*$`)

// ValidNodeTypeID 校验类型标识。
func ValidNodeTypeID(t string) bool { return validTypeRe.MatchString(t) }

// IsPluginType 判断是否为插件节点类型。
func IsPluginType(t NodeTypeID) bool {
	return regexp.MustCompile(`^[a-z][a-z0-9-]*:[a-z0-9-]+$`).MatchString(string(t))
}

// ResourceKind 是可流转的资源类型。
type ResourceKind string

// 资源类型枚举。
const (
	KindText  ResourceKind = "text"
	KindImage ResourceKind = "image"
	KindVideo ResourceKind = "video"
	KindAudio ResourceKind = "audio"
	KindFile  ResourceKind = "file"
	KindJSON  ResourceKind = "json"
)

// ValidResourceKind 校验资源类型。
func ValidResourceKind(k ResourceKind) bool {
	switch k {
	case KindText, KindImage, KindVideo, KindAudio, KindFile, KindJSON:
		return true
	}
	return false
}

// NodeState 是节点运行态（显式状态机，禁止用 status?: string 混合表达）。
type NodeState string

// 节点状态枚举。
const (
	NodeIdle      NodeState = "idle"
	NodePending   NodeState = "pending"
	NodeRunning   NodeState = "running"
	NodeSucceeded NodeState = "succeeded"
	NodeFailed    NodeState = "failed"
)

// ValidNodeState 校验节点状态。
func ValidNodeState(s NodeState) bool {
	switch s {
	case NodeIdle, NodePending, NodeRunning, NodeSucceeded, NodeFailed:
		return true
	}
	return false
}

// Rect 是世界坐标矩形。
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Viewport 是视口（x/y 平移，k 缩放）。
type Viewport struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	K float64 `json:"k"`
}

// CanvasSettings 是画布级设置。
type CanvasSettings struct {
	Background string `json:"background"` // lines | dots | blank
	ImageInfo  bool   `json:"imageInfo"`  // 是否展示图片信息
	GridSnap   bool   `json:"gridSnap"`   // 是否开启网格吸附
	ReadOnly   bool   `json:"readOnly"`   // 只读（viewer 视角）
	FreeResize bool   `json:"freeResize"` // 全局默认是否自由缩放
}

// DefaultSettings 返回默认画布设置（与原项目默认一致：点阵 + 显示图片信息）。
func DefaultSettings() CanvasSettings {
	return CanvasSettings{Background: "dots", ImageInfo: true}
}

// Port 声明一个输入/输出端口。
type Port struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Kind     ResourceKind `json:"kind"`
	Multiple bool         `json:"multiple"` // 是否允许多条入边
	Required bool         `json:"required"` // 执行前是否必须有值
	Order    int          `json:"order"`    // 输入顺序（决定提示词组装顺序）
}

// Ports 是一个节点的端口集合。
type Ports struct {
	Inputs  []Port `json:"inputs"`
	Outputs []Port `json:"outputs"`
}

// NodeSpec 是类型化配置。用 map 承载但由每类型的 schema 强校验（见 spec.go）。
type NodeSpec map[string]any

// ResultVariant 是生成结果的一项（多结果不再建父子节点，见 DIV-09）。
type ResultVariant struct {
	AssetID string       `json:"assetId,omitempty"`
	Text    string       `json:"text,omitempty"`
	Kind    ResourceKind `json:"kind"`
	Status  NodeState    `json:"status"`
	Error   string       `json:"error,omitempty"`
}

// NodeResult 是节点的最近一次结果集合。
type NodeResult struct {
	Variants []ResultVariant `json:"variants,omitempty"`
	Primary  int             `json:"primary"`
	RunID    string          `json:"runId,omitempty"`
	StepID   string          `json:"stepId,omitempty"`
}

// NodeError 是节点级错误（含稳定 code，便于 i18n）。
type NodeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Node 是画布中的一个节点。
type Node struct {
	ID       string         `json:"id"`
	Type     NodeTypeID     `json:"type"`
	Title    string         `json:"title"`
	Rect     Rect           `json:"rect"`
	Z        int            `json:"z"`
	ParentID string         `json:"parentId,omitempty"`
	Ports    Ports          `json:"ports"`
	Spec     NodeSpec       `json:"spec"`
	State    NodeState      `json:"state"`
	Result   *NodeResult    `json:"result,omitempty"`
	Error    *NodeError     `json:"error,omitempty"`
	Locked   bool           `json:"locked,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

// Endpoint 是连线端点。
type Endpoint struct {
	NodeID string `json:"nodeId"`
	PortID string `json:"portId"`
}

// Edge 是显式端口的连线。
type Edge struct {
	ID        string       `json:"id"`
	From      Endpoint     `json:"from"`
	To        Endpoint     `json:"to"`
	Kind      ResourceKind `json:"kind"`
	CreatedBy string       `json:"createdBy,omitempty"`
	CreatedAt time.Time    `json:"createdAt"`
}

// CanvasDocument 是画布权威文档。
type CanvasDocument struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Version   int64           `json:"version"`
	Viewport  Viewport        `json:"viewport"`
	Settings  CanvasSettings  `json:"settings"`
	Nodes     map[string]Node `json:"nodes"`
	Edges     map[string]Edge `json:"edges"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// NewDocument 构造空文档，视口 k=1。
func NewDocument(id, projectID string) *CanvasDocument {
	return &CanvasDocument{
		ID:        id,
		ProjectID: projectID,
		Version:   1,
		Viewport:  Viewport{X: 0, Y: 0, K: 1},
		Settings:  DefaultSettings(),
		Nodes:     map[string]Node{},
		Edges:     map[string]Edge{},
	}
}

// Clone 深拷贝文档（op 应用必须作用于副本，避免部分失败污染权威状态）。
func (d *CanvasDocument) Clone() *CanvasDocument {
	cp := &CanvasDocument{
		ID:        d.ID,
		ProjectID: d.ProjectID,
		Version:   d.Version,
		Viewport:  d.Viewport,
		Settings:  d.Settings,
		Nodes:     make(map[string]Node, len(d.Nodes)),
		Edges:     make(map[string]Edge, len(d.Edges)),
		UpdatedAt: d.UpdatedAt,
	}
	for k, v := range d.Nodes {
		n := v
		n.Spec = cloneSpec(v.Spec)
		n.Ports = Ports{Inputs: append([]Port(nil), v.Ports.Inputs...), Outputs: append([]Port(nil), v.Ports.Outputs...)}
		if v.Result != nil {
			r := *v.Result
			r.Variants = append([]ResultVariant(nil), v.Result.Variants...)
			n.Result = &r
		}
		if v.Error != nil {
			e := *v.Error
			n.Error = &e
		}
		if v.Meta != nil {
			m := make(map[string]any, len(v.Meta))
			for kk, vv := range v.Meta {
				m[kk] = vv
			}
			n.Meta = m
		}
		cp.Nodes[k] = n
	}
	for k, v := range d.Edges {
		cp.Edges[k] = v
	}
	return cp
}

func cloneSpec(s NodeSpec) NodeSpec {
	if s == nil {
		return NodeSpec{}
	}
	out := make(NodeSpec, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// NodeIDs 返回稳定排序的节点 ID 列表。
func (d *CanvasDocument) NodeIDs() []string {
	ids := make([]string, 0, len(d.Nodes))
	for id := range d.Nodes {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

// EdgeIDs 返回稳定排序的边 ID 列表。
func (d *CanvasDocument) EdgeIDs() []string {
	ids := make([]string, 0, len(d.Edges))
	for id := range d.Edges {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

// UpstreamOf 返回指向 nodeID 的边（按 ID 排序，保证确定性）。
func (d *CanvasDocument) UpstreamOf(nodeID string) []Edge {
	out := []Edge{}
	for _, id := range d.EdgeIDs() {
		if d.Edges[id].To.NodeID == nodeID {
			out = append(out, d.Edges[id])
		}
	}
	return out
}

// DownstreamOf 返回 nodeID 指出的边。
func (d *CanvasDocument) DownstreamOf(nodeID string) []Edge {
	out := []Edge{}
	for _, id := range d.EdgeIDs() {
		if d.Edges[id].From.NodeID == nodeID {
			out = append(out, d.Edges[id])
		}
	}
	return out
}

// ChildrenOf 返回某分组的直接子节点。
func (d *CanvasDocument) ChildrenOf(groupID string) []string {
	out := []string{}
	for _, id := range d.NodeIDs() {
		if d.Nodes[id].ParentID == groupID {
			out = append(out, id)
		}
	}
	return out
}
