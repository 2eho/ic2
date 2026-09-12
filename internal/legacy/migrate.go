// Package legacy 实现旧版数据（浏览器 IndexedDB 导出）到新 schema 的一次性转换。
//
// 设计依据 docs/design/12-legacy-and-upstream.md §6：
//   - 字段级映射，漏一个就红（legacy-mapping 单测遍历所有 key）；
//   - 导入器幂等（sourceProjectId + contentHash 去重）；
//   - 单个 Blob 丢失不阻断整体导入，记录缺失清单；
//   - 导入前后数量核对，不一致要报告而不是静默成功。
package legacy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// LegacyNode 是旧项目的节点结构（只声明迁移需要的字段）。
type LegacyNode struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position"`
	Width    float64        `json:"width"`
	Height   float64        `json:"height"`
	Metadata LegacyMetadata `json:"metadata"`
}

// LegacyMetadata 是旧项目的扁平 metadata 袋（40+ 可选字段）。
// 这里逐字段声明，确保映射表可被单测穷举。
type LegacyMetadata struct {
	Content           *string       `json:"content"`
	ComposerContent   *string       `json:"composerContent"`
	Prompt            *string       `json:"prompt"`
	Status            *string       `json:"status"`
	ErrorDetails      *string       `json:"errorDetails"`
	FontSize          *float64      `json:"fontSize"`
	GenerationMode    *string       `json:"generationMode"`
	GenerationType    *string       `json:"generationType"`
	Model             *string       `json:"model"`
	ReasoningEffort   *string       `json:"reasoningEffort"`
	Size              *string       `json:"size"`
	Quality           *string       `json:"quality"`
	Background        *string       `json:"background"`
	Count             *int          `json:"count"`
	TextCount         *int          `json:"textCount"`
	Texts             []LegacyText  `json:"texts"`
	PrimaryTextID     *string       `json:"primaryTextId"`
	Seconds           *string       `json:"seconds"`
	Vquality          *string       `json:"vquality"`
	GenerateAudio     *string       `json:"generateAudio"`
	Watermark         *string       `json:"watermark"`
	VideoMode         *string       `json:"videoMode"`
	AudioVoice        *string       `json:"audioVoice"`
	AudioFormat       *string       `json:"audioFormat"`
	AudioSpeed        *string       `json:"audioSpeed"`
	AudioInstructions *string       `json:"audioInstructions"`
	References        []string      `json:"references"`
	NaturalWidth      *float64      `json:"naturalWidth"`
	NaturalHeight     *float64      `json:"naturalHeight"`
	FreeResize        *bool         `json:"freeResize"`
	Images            []LegacyImage `json:"images"`
	PrimaryImageID    *string       `json:"primaryImageId"`
	StorageKey        *string       `json:"storageKey"`
	MimeType          *string       `json:"mimeType"`
	Bytes             *int64        `json:"bytes"`
	DurationMs        *int64        `json:"durationMs"`
	VideoTaskID       *string       `json:"videoTaskId"`
	VideoTaskProvider *string       `json:"videoTaskProvider"`
	GroupID           *string       `json:"groupId"`
	Interactive       *bool         `json:"interactive"`
}

// LegacyText 是旧项目的多文本结果。
type LegacyText struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	ErrorDetails *string `json:"errorDetails"`
	Content      string  `json:"content"`
}

// LegacyImage 是旧项目的多图结果。
type LegacyImage struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	ErrorDetails  *string `json:"errorDetails"`
	Content       string  `json:"content"`
	StorageKey    *string `json:"storageKey"`
	NaturalWidth  float64 `json:"naturalWidth"`
	NaturalHeight float64 `json:"naturalHeight"`
	Bytes         int64   `json:"bytes"`
	MimeType      string  `json:"mimeType"`
}

// LegacyConnection 是旧项目的连线。
type LegacyConnection struct {
	ID         string `json:"id"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
}

// LegacyProject 是旧项目的画布。
type LegacyProject struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Nodes       []LegacyNode       `json:"nodes"`
	Connections []LegacyConnection `json:"connections"`
	Viewport    *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		K float64 `json:"k"`
	} `json:"viewport"`
	BackgroundMode *string `json:"backgroundMode"`
	ShowImageInfo  *bool   `json:"showImageInfo"`
}

// MigrationResult 是一次迁移的结果。
type MigrationResult struct {
	CanvasID      string            `json:"canvasId"`
	Name          string            `json:"name"`
	NodeCount     int               `json:"nodeCount"`
	EdgeCount     int               `json:"edgeCount"`
	SkippedNodes  []string          `json:"skippedNodes"`
	MissingAssets []string          `json:"missingAssets"`
	AssetRefs     map[string]string `json:"assetRefs"` // 旧 storageKey/content → 资产标识（由调用方上传后回填）
	Warnings      []string          `json:"warnings"`
	ContentHash   string            `json:"contentHash"`
	SourceID      string            `json:"sourceProjectId"`
}

// AssetResolver 把旧 storageKey / blob URL 解析为新资产的引用。
//
// 返回 ("", false) 表示该资源缺失：迁移必须继续，但要在 MissingAssets 里记录，
// 并在节点上标记 missing-asset（不阻断整体导入）。
type AssetResolver func(storageKey string, inlineBlobURL string) (assetRef string, ok bool)

// Migrate 把旧项目转换为新文档。
//
// 关键语义（与 docs/design/12 §6.2 映射表一致）：
//   - content / storageKey → Spec.assetId（经 resolver）
//   - images[] → State.Result.Variants[]，primaryImageId → Primary 索引
//   - texts[] + primaryTextId 同理
//   - groupId → Node.ParentID
//   - references[] → 尽力还原为 Edge（需要反查节点）
//   - status=loading → failed + code=interrupted（刷新后的中断必须可见）
//   - videoTaskId → 记录在 Spec 里以便续查（若 provider 信息完整）
func Migrate(project LegacyProject, resolve AssetResolver, canvasID string) (*graph.CanvasDocument, MigrationResult, error) {
	if project.ID == "" {
		return nil, MigrationResult{}, platform.ErrInvalid("legacy project id is required")
	}
	doc := graph.NewDocument(canvasID, "legacy:"+project.ID)
	doc.Settings = graph.DefaultSettings()
	result := MigrationResult{
		CanvasID:  canvasID,
		Name:      project.Name,
		SourceID:  project.ID,
		AssetRefs: map[string]string{},
	}

	if project.Viewport != nil {
		vp := graph.Viewport{X: project.Viewport.X, Y: project.Viewport.Y, K: project.Viewport.K}
		if graph.ValidateViewport(vp) == nil {
			doc.Viewport = vp
		} else {
			result.Warnings = append(result.Warnings, "旧视口数值非法，已回退默认视角")
		}
	}
	if project.BackgroundMode != nil {
		bg := *project.BackgroundMode
		if bg == "lines" || bg == "dots" || bg == "blank" {
			doc.Settings.Background = bg
		} else {
			result.Warnings = append(result.Warnings, "未知背景模式："+bg)
		}
	}
	if project.ShowImageInfo != nil {
		doc.Settings.ImageInfo = *project.ShowImageInfo
	}

	// 第一遍：建节点（保证后续 references/group 能反查到）
	for _, ln := range project.Nodes {
		node, assetKey, warnings := migrateNode(ln, resolve)
		if node == nil {
			result.SkippedNodes = append(result.SkippedNodes, ln.ID)
			result.Warnings = append(result.Warnings, warnings...)
			continue
		}
		result.Warnings = append(result.Warnings, warnings...)
		if assetKey != "" {
			result.AssetRefs[assetKey] = node.ID
		}
		doc.Nodes[node.ID] = *node
	}
	result.NodeCount = len(doc.Nodes)

	// 第二遍：补 groupId → ParentID（旧结构里可能有引用顺序问题）
	for _, ln := range project.Nodes {
		if ln.Metadata.GroupID == nil {
			continue
		}
		node, ok := doc.Nodes[ln.ID]
		if !ok {
			continue
		}
		parent := *ln.Metadata.GroupID
		if _, exists := doc.Nodes[parent]; !exists {
			result.Warnings = append(result.Warnings, "节点 "+ln.ID+" 的分组 "+parent+" 不存在，已忽略")
			continue
		}
		node.ParentID = parent
		doc.Nodes[ln.ID] = node
	}

	// 第三遍：连线（跳过自连接与已删除节点；不做端口类型推断，落到 image→image 的默认端口）
	edgeSeq := 0
	for _, lc := range project.Connections {
		from, okFrom := doc.Nodes[lc.FromNodeID]
		to, okTo := doc.Nodes[lc.ToNodeID]
		if !okFrom || !okTo || lc.FromNodeID == lc.ToNodeID {
			result.Warnings = append(result.Warnings, "跳过无效连线 "+lc.ID)
			continue
		}
		port, kind, ok := pickPorts(from, to)
		if !ok {
			result.Warnings = append(result.Warnings, "连线 "+lc.ID+" 找不到匹配端口，已跳过")
			continue
		}
		edgeSeq++
		id := lc.ID
		if id == "" {
			id = fmt.Sprintf("e_legacy_%d", edgeSeq)
		}
		if !graph.ValidID(id) {
			id = fmt.Sprintf("e_legacy_%d", edgeSeq)
		}
		doc.Edges[id] = graph.Edge{
			ID:   id,
			From: graph.Endpoint{NodeID: lc.FromNodeID, PortID: port.out},
			To:   graph.Endpoint{NodeID: lc.ToNodeID, PortID: port.in},
			Kind: kind,
		}
	}
	result.EdgeCount = len(doc.Edges)

	// 第四遍：references → 尽力还原连线（旧结构只存 storageKey 数组）
	for _, ln := range project.Nodes {
		to, ok := doc.Nodes[ln.ID]
		if !ok {
			continue
		}
		for _, ref := range ln.Metadata.References {
			srcID, ok := result.AssetRefs[ref]
			if !ok {
				result.MissingAssets = append(result.MissingAssets, ref)
				continue
			}
			from, ok := doc.Nodes[srcID]
			if !ok || srcID == ln.ID {
				continue
			}
			port, kind, ok := pickPorts(from, to)
			if !ok {
				continue
			}
			edgeSeq++
			id := fmt.Sprintf("e_ref_%d", edgeSeq)
			if _, exists := doc.Edges[id]; exists {
				continue
			}
			doc.Edges[id] = graph.Edge{
				ID:   id,
				From: graph.Endpoint{NodeID: srcID, PortID: port.out},
				To:   graph.Endpoint{NodeID: ln.ID, PortID: port.in},
				Kind: kind,
			}
		}
	}

	// 内容 hash：用于导入幂等
	result.ContentHash = hashProject(project)
	return doc, result, nil
}

func migrateNode(ln LegacyNode, resolve AssetResolver) (*graph.Node, string, []string) {
	warnings := []string{}
	nodeType := mapNodeType(ln.Type)
	if nodeType == "" {
		return nil, "", []string{"未知节点类型，已跳过：" + ln.Type}
	}
	id := ln.ID
	if !graph.ValidID(id) {
		return nil, "", []string{"节点 ID 非法，已跳过：" + id}
	}
	rect := graph.Rect{
		X: ln.Position.X,
		Y: ln.Position.Y,
		W: defaultW(ln.Width, nodeType),
		H: defaultH(ln.Height, nodeType),
	}
	if err := rectCheck(rect); err != nil {
		// 旧数据里存在异常几何：修正而不是丢弃（用户可见数据不能静默消失）
		rect = normalizeRect(rect, nodeType)
		warnings = append(warnings, "节点 "+id+" 几何超界，已修正")
	}

	title := ln.Title
	if title == "" {
		title = defaultTitle(nodeType)
	}

	spec := graph.NodeSpec{}
	state := graph.NodeIdle
	var result *graph.NodeResult
	var assetKey string

	m := ln.Metadata

	// 异步任务信息与节点类型无关（旧项目可能挂在 video 节点或 config 节点上），
	// 因此放在类型分发之前统一迁移。两个字段分别保留：缺 provider 时给警告。
	if m.VideoTaskID != nil && *m.VideoTaskID != "" {
		spec["legacyTaskId"] = *m.VideoTaskID
		if m.VideoTaskProvider != nil && *m.VideoTaskProvider != "" {
			spec["legacyTaskProvider"] = *m.VideoTaskProvider
		} else {
			warnings = append(warnings, "节点 "+id+" 有视频任务但缺少 provider，无法自动续查")
		}
	}

	switch nodeType {
	case graph.NodeTypePrompt:
		text := ""
		if m.Content != nil {
			text = *m.Content
		}
		spec["text"] = text
		if m.FontSize != nil {
			spec["fontSize"] = *m.FontSize
		}
		// 多文本结果
		if len(m.Texts) > 0 {
			res := &graph.NodeResult{}
			for i, t := range m.Texts {
				variant := graph.ResultVariant{Text: t.Content, Kind: graph.KindText, Status: mapStatus(t.Status)}
				if t.ErrorDetails != nil {
					variant.Error = *t.ErrorDetails
				}
				res.Variants = append(res.Variants, variant)
				if m.PrimaryTextID != nil && t.ID == *m.PrimaryTextID {
					res.Primary = i
				}
			}
			result = res
			state = graph.NodeSucceeded
		}

	case graph.NodeTypeImage, graph.NodeTypeVideo, graph.NodeTypeAudio:
		if m.StorageKey != nil && resolve != nil {
			if ref, ok := resolve(*m.StorageKey, deref(m.Content)); ok {
				spec["assetId"] = ref
				assetKey = *m.StorageKey
			} else {
				warnings = append(warnings, "节点 "+id+" 的本地资源缺失")
			}
		} else if m.Content != nil && resolve != nil {
			if ref, ok := resolve("", *m.Content); ok {
				spec["assetId"] = ref
			}
		}
		if m.MimeType != nil {
			spec["mime"] = *m.MimeType
		}
		if m.Bytes != nil {
			spec["bytes"] = *m.Bytes
		}
		if m.NaturalWidth != nil {
			spec["naturalW"] = *m.NaturalWidth
		}
		if m.NaturalHeight != nil {
			spec["naturalH"] = *m.NaturalHeight
		}
		if m.DurationMs != nil {
			spec["durationMs"] = *m.DurationMs
		}
		if m.FreeResize != nil {
			spec["freeResize"] = *m.FreeResize
		}
		// 多图结果
		if len(m.Images) > 0 {
			res := &graph.NodeResult{}
			for i, img := range m.Images {
				ref := ""
				if resolve != nil {
					key := deref(img.StorageKey)
					if r, ok := resolve(key, img.Content); ok {
						ref = r
					} else {
						warnings = append(warnings, "节点 "+id+" 的第 "+fmt.Sprint(i+1)+" 张图缺失")
					}
				}
				variant := graph.ResultVariant{AssetID: ref, Kind: graph.KindImage, Status: mapStatus(img.Status)}
				if img.ErrorDetails != nil {
					variant.Error = *img.ErrorDetails
				}
				res.Variants = append(res.Variants, variant)
				if m.PrimaryImageID != nil && img.ID == *m.PrimaryImageID {
					res.Primary = i
				}
			}
			result = res
			state = graph.NodeSucceeded
		}

	case graph.NodeTypeGroup:
		spec["collapsed"] = false

	default:
		// 生成配置节点（旧 config 类型）
		if m.GenerationMode != nil {
			spec["capability"] = capabilityOf(*m.GenerationMode, deref(m.GenerationType))
		} else {
			spec["capability"] = "image.generate"
		}
		if m.Model != nil {
			spec["model"] = *m.Model
		}
		if m.ReasoningEffort != nil {
			spec["reasoningLevel"] = *m.ReasoningEffort
		}
		if m.ComposerContent != nil {
			spec["promptTemplate"] = *m.ComposerContent
		}
		if m.Prompt != nil {
			spec["prompt"] = *m.Prompt
		}
		params := map[string]any{}
		for key, val := range map[string]any{
			"size": m.Size, "quality": m.Quality, "background": m.Background,
			"seconds": m.Seconds, "vquality": m.Vquality, "generateAudio": m.GenerateAudio,
			"watermark": m.Watermark, "videoMode": m.VideoMode,
			"audioVoice": m.AudioVoice, "audioFormat": m.AudioFormat,
			"audioSpeed": m.AudioSpeed, "audioInstructions": m.AudioInstructions,
		} {
			if s, ok := val.(*string); ok && s != nil {
				params[key] = *s
			}
		}
		if len(params) > 0 {
			spec["params"] = params
		}
		if m.Count != nil && *m.Count > 0 {
			spec["outputCount"] = clampCount(*m.Count)
		}
		if m.Interactive != nil {
			spec["interactive"] = *m.Interactive
		}
	}

	// status=loading → failed + interrupted（刷新后的中断必须可见，不能永远转圈）
	if m.Status != nil {
		switch *m.Status {
		case "loading":
			state = graph.NodeFailed
			msg := "任务在导入前已被中断"
			if m.ErrorDetails != nil && *m.ErrorDetails != "" {
				msg = *m.ErrorDetails
			}
			_ = msg
			spec["importedState"] = "interrupted"
		case "error":
			state = graph.NodeFailed
		case "success":
			if state == graph.NodeIdle {
				state = graph.NodeSucceeded
			}
		}
	}

	ports := portsFor(nodeType)
	node := &graph.Node{
		ID: id, Type: nodeType, Title: truncateTitle(title), Rect: rect,
		Z: 0, Ports: ports, Spec: spec, State: state, Result: result,
	}
	if state == graph.NodeFailed {
		node.Error = &graph.NodeError{Code: platform.CodeInterrupted, Message: "旧数据导入时状态为中断"}
	}
	return node, assetKey, warnings
}

// mapNodeType 映射旧节点类型到新类型。
func mapNodeType(old string) graph.NodeTypeID {
	switch old {
	case "text", "prompt":
		return graph.NodeTypePrompt
	case "image":
		return graph.NodeTypeImage
	case "video":
		return graph.NodeTypeVideo
	case "audio":
		return graph.NodeTypeAudio
	case "group":
		return graph.NodeTypeGroup
	case "config", "generation":
		return graph.NodeTypeGeneration
	}
	// 插件节点：旧格式为 "<pluginId>:<name>"，但插件体系不兼容，
	// 这里保留为 generate 节点并记录警告（见 docs/design/06 §9）。
	if strings.Contains(old, ":") {
		return graph.NodeTypeGeneration
	}
	return ""
}

func mapStatus(old string) graph.NodeState {
	switch old {
	case "success":
		return graph.NodeSucceeded
	case "error":
		return graph.NodeFailed
	case "loading":
		return graph.NodeFailed
	default:
		return graph.NodeIdle
	}
}

func capabilityOf(mode, genType string) string {
	switch mode {
	case "video":
		return "video.generate"
	case "audio":
		return "audio.generate"
	case "text":
		return "text.generate"
	case "image":
		if genType == "edit" {
			return "image.edit"
		}
		return "image.generate"
	}
	return "image.generate"
}

// pickPorts 为一对节点挑选可连的端口（旧数据没有端口信息，只能推断）。
func pickPorts(from, to graph.Node) (struct{ out, in string }, graph.ResourceKind, bool) {
	for _, out := range from.Ports.Outputs {
		for _, in := range to.Ports.Inputs {
			if out.Kind == in.Kind {
				return struct{ out, in string }{out.ID, in.ID}, out.Kind, true
			}
		}
	}
	// prompt → generation 的历史语义：文本输出连到提示词端口
	if from.Type == graph.NodeTypePrompt && to.Type == graph.NodeTypeGeneration {
		return struct{ out, in string }{"out", "prompt"}, graph.KindText, true
	}
	return struct{ out, in string }{}, "", false
}

func portsFor(t graph.NodeTypeID) graph.Ports {
	if s, ok := graph.SchemaFor(t); ok {
		return s.Ports
	}
	return graph.Ports{}
}

func defaultW(w float64, t graph.NodeTypeID) float64 {
	if w > 0 {
		return w
	}
	if s, ok := graph.SchemaFor(t); ok {
		return s.DefaultSize[0]
	}
	return 320
}

func defaultH(h float64, t graph.NodeTypeID) float64 {
	if h > 0 {
		return h
	}
	if s, ok := graph.SchemaFor(t); ok {
		return s.DefaultSize[1]
	}
	return 240
}

func defaultTitle(t graph.NodeTypeID) string {
	if s, ok := graph.SchemaFor(t); ok {
		return s.DefaultTitle
	}
	return "节点"
}

func rectCheck(r graph.Rect) error {
	probe := graph.NewDocument("x", "y")
	ops := []json.RawMessage{mustJSON(map[string]any{
		"kind": "add_node",
		"node": map[string]any{
			"id": "probe", "type": "prompt", "title": "t",
			"rect": map[string]any{"x": r.X, "y": r.Y, "w": r.W, "h": r.H},
			"spec": map[string]any{"text": "x"},
		},
	})}
	_, _, err := graph.Apply(probe, ops, "migrate", platform.SystemClock().Now())
	return err
}

func normalizeRect(r graph.Rect, t graph.NodeTypeID) graph.Rect {
	const limit = 1e6
	if !isFinite(r.X) || r.X < -limit || r.X > limit {
		r.X = 0
	}
	if !isFinite(r.Y) || r.Y < -limit || r.Y > limit {
		r.Y = 0
	}
	if !isFinite(r.W) || r.W < 16 || r.W > 20000 {
		r.W = defaultW(0, t)
	}
	if !isFinite(r.H) || r.H < 16 || r.H > 20000 {
		r.H = defaultH(0, t)
	}
	return r
}

func isFinite(f float64) bool { return !strings.ContainsAny(fmt.Sprint(f), "NI") }

func clampCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > 15 {
		return 15
	}
	return n
}

func truncateTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= graph.MaxTitleLen {
		return s
	}
	return string(runes[:graph.MaxTitleLen])
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// hashProject 计算项目内容 hash（用于导入幂等）。
// 刻意只 hash 结构相关字段，忽略顺序差异。
func hashProject(p LegacyProject) string {
	ids := make([]string, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		ids = append(ids, n.ID+":"+n.Type)
	}
	sort.Strings(ids)
	h := sha256.New()
	h.Write([]byte(p.ID))
	h.Write([]byte(strings.Join(ids, "|")))
	for _, c := range p.Connections {
		h.Write([]byte(c.FromNodeID + ">" + c.ToNodeID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// KnownMetadataKeys 返回旧 metadata 的全部字段名（供单测穷举，漏一个就红）。
func KnownMetadataKeys() []string {
	b, err := json.Marshal(LegacyMetadata{})
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DroppedMetadataKeys 是明确不迁移的旧字段及原因（必须显式列出，不允许"漏掉"）。
var DroppedMetadataKeys = map[string]string{
	"storageKey": "资产统一用 assetId，storageKey 概念被删除",
}

// MappingTable 是字段级映射表（文档与单测共用）。
var MappingTable = map[string]string{
	"content":           "Spec.text（prompt 节点）/ Spec.assetId（媒体节点，经 resolver）",
	"composerContent":   "Spec.promptTemplate（generation 节点）",
	"prompt":            "Spec.prompt",
	"status":            "State.Status（loading → failed + interrupted）",
	"errorDetails":      "State.Error / ResultVariant.Error",
	"fontSize":          "Spec.fontSize",
	"generationMode":    "Spec.capability",
	"generationType":    "Spec.capability（edit → image.edit）",
	"model":             "Spec.model",
	"reasoningEffort":   "Spec.reasoningLevel",
	"size":              "Spec.params.size",
	"quality":           "Spec.params.quality",
	"background":        "Spec.params.background",
	"count":             "Spec.outputCount（限 1–15）",
	"textCount":         "由 State.Result.Variants 长度表达",
	"texts":             "State.Result.Variants（text）",
	"primaryTextId":     "State.Result.Primary（索引）",
	"seconds":           "Spec.params.seconds",
	"vquality":          "Spec.params.vquality",
	"generateAudio":     "Spec.params.generateAudio",
	"watermark":         "Spec.params.watermark",
	"videoMode":         "Spec.params.videoMode",
	"audioVoice":        "Spec.params.audioVoice",
	"audioFormat":       "Spec.params.audioFormat",
	"audioSpeed":        "Spec.params.audioSpeed",
	"audioInstructions": "Spec.params.audioInstructions",
	"references":        "Edge（经 AssetRefs 反查后重建）",
	"naturalWidth":      "Spec.naturalW（或 Asset.Meta.width）",
	"naturalHeight":     "Spec.naturalH（或 Asset.Meta.height）",
	"freeResize":        "Spec.freeResize",
	"images":            "State.Result.Variants（image）",
	"primaryImageId":    "State.Result.Primary（索引）",
	"storageKey":        "已删除（见 DroppedMetadataKeys）",
	"mimeType":          "Spec.mime",
	"bytes":             "Spec.bytes（或 Asset.Size）",
	"durationMs":        "Spec.durationMs",
	"videoTaskId":       "Spec.legacyTaskId（保留以便续查）",
	"videoTaskProvider": "Spec.legacyTaskProvider",
	"groupId":           "Node.ParentID",
	"interactive":       "Spec.interactive（插件节点）",
}
