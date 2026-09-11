package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// Service 实现 api.PluginService。
type Service struct {
	db      *sql.DB
	clock   platform.Clock
	ids     platform.IDGen
	fetcher ManifestFetcher
	// registry 是官方注册表地址（可为空 = 只用内置插件）。
	registryURL string
}

// ManifestFetcher 拉取第三方清单（走 SSRF 防护过的 HTTP 客户端）。
type ManifestFetcher interface {
	FetchManifest(ctx context.Context, url string) (*Manifest, []byte, error)
	FetchBundle(ctx context.Context, url string) ([]byte, string, error)
}

// New 构造插件服务。
func New(db *sql.DB, clock platform.Clock, ids platform.IDGen, fetcher ManifestFetcher, registryURL string) *Service {
	if clock == nil {
		clock = platform.SystemClock()
	}
	if ids == nil {
		ids = platform.DefaultIDGen()
	}
	return &Service{db: db, clock: clock, ids: ids, fetcher: fetcher, registryURL: registryURL}
}

// Builtins 是内置官方插件（重写后的示例插件，对齐 docs/design/06 §9）。
func Builtins() []*Manifest {
	mk := func(key, name, nodeType, title string, w, h int, perms []string, config map[string]any, interactive bool) *Manifest {
		return &Manifest{
			Key: key, Name: name, Version: "1.0.0", APIVersion: "^1.0.0",
			Author: "context-flow.cloud", Entry: "index.js", Permissions: perms,
			Nodes: []NodeDef{{
				Type: nodeType, Title: title,
				DefaultSize:  &Size{Width: w, Height: h},
				MinSize:      &Size{Width: 160, Height: 120},
				MinimapColor: "#c4b5fd",
				ConfigSchema: config,
				Interactive:  interactive,
			}},
		}
	}
	objSchema := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	return []*Manifest{
		mk("cloud.ic.markdown", "Markdown 渲染", "cloud.ic.markdown:viewer", "Markdown", 420, 320,
			[]string{PermNodeRead, PermNodeWrite},
			objSchema(map[string]any{
				"content": map[string]any{"type": "string"},
				"theme":   map[string]any{"type": "string"},
			}), true),
		mk("cloud.ic.svg", "SVG 渲染", "cloud.ic.svg:viewer", "SVG", 400, 300,
			[]string{PermNodeRead, PermNodeWrite},
			objSchema(map[string]any{"source": map[string]any{"type": "string"}}), true),
		mk("cloud.ic.html", "HTML 预览", "cloud.ic.html:viewer", "HTML", 520, 360,
			[]string{PermNodeRead, PermNodeWrite},
			objSchema(map[string]any{"source": map[string]any{"type": "string"}}), true),
		mk("cloud.ic.panorama", "3D 全景", "cloud.ic.panorama:viewer", "全景", 640, 360,
			[]string{PermNodeRead, PermNodeWrite, PermAssetRead},
			objSchema(map[string]any{
				"fov":     map[string]any{"type": "number"},
				"assetId": map[string]any{"type": "string"},
			}), true),
		mk("cloud.ic.sticky", "便利贴", "cloud.ic.sticky:note", "便利贴", 220, 180,
			[]string{PermNodeRead, PermNodeWrite},
			objSchema(map[string]any{
				"text":  map[string]any{"type": "string"},
				"color": map[string]any{"type": "string"},
			}), true),
		mk("cloud.ic.template", "模板节点", "cloud.ic.template:prompt", "模板", 320, 220,
			[]string{PermNodeRead, PermNodeWrite},
			objSchema(map[string]any{
				"template": map[string]any{"type": "string"},
				"vars":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}), true),
	}
}

// List 见 api.PluginService：返回内置插件与已安装插件的合并视图。
func (s *Service) List(ctx context.Context, wsID string) ([]api.PluginDTO, error) {
	installed, err := s.installed(ctx, wsID)
	if err != nil {
		return nil, err
	}
	byKey := map[string]api.PluginDTO{}
	for _, d := range installed {
		byKey[d.Key] = d
	}
	out := make([]api.PluginDTO, 0, len(installed)+len(Builtins()))
	for _, b := range Builtins() {
		dto := toDTO(b, true)
		if existing, ok := byKey[b.Key]; ok {
			// 已安装时保留启用状态，但以安装版本为准
			existing.Builtin = true
			out = append(out, existing)
			delete(byKey, b.Key)
			continue
		}
		out = append(out, dto)
	}
	for _, d := range byKey {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *Service) installed(ctx context.Context, wsID string) ([]api.PluginDTO, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT plugin_key, version, manifest, permissions, enabled, trusted, builtin, config
		 FROM plugins WHERE workspace_id = ?`, wsID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []api.PluginDTO{}
	for rows.Next() {
		var key, version, manifestRaw, permsRaw, configRaw string
		var enabled, trusted, builtin int
		if err := rows.Scan(&key, &version, &manifestRaw, &permsRaw, &enabled, &trusted, &builtin, &configRaw); err != nil {
			return nil, platform.AsError(err)
		}
		m, err := UnmarshalManifest(manifestRaw)
		if err != nil {
			continue
		}
		dto := toDTO(m, builtin == 1)
		dto.Enabled = enabled == 1
		if len(splitList(permsRaw)) > 0 {
			dto.Permissions = splitList(permsRaw)
		}
		_ = json.Unmarshal([]byte(configRaw), &dto.Config)
		out = append(out, dto)
	}
	return out, platform.AsError(rows.Err())
}

func toDTO(m *Manifest, builtin bool) api.PluginDTO {
	nodes := make([]api.PluginNodeDTO, 0, len(m.Nodes))
	for _, n := range m.Nodes {
		dto := api.PluginNodeDTO{
			Type: n.Type, Title: n.Title, MinimapColor: n.MinimapColor,
			ConfigSchema: n.ConfigSchema, ConfigVersion: n.ConfigVersion,
			Interactive: n.Interactive, Ports: toGraphPorts(n.Ports),
		}
		if n.DefaultSize != nil {
			dto.DefaultSize = map[string]int{"width": n.DefaultSize.Width, "height": n.DefaultSize.Height}
		}
		if n.MinSize != nil {
			dto.MinSize = map[string]int{"width": n.MinSize.Width, "height": n.MinSize.Height}
		}
		nodes = append(nodes, dto)
	}
	integrity := ""
	if m.Integrity != nil {
		integrity = m.Integrity.SHA256
	}
	return api.PluginDTO{
		Key: m.Key, Name: m.Name, Version: m.Version, APIVersion: m.APIVersion,
		Author: m.Author, Homepage: m.Homepage, Permissions: m.Permissions,
		Nodes: nodes, Builtin: builtin, Integrity: integrity, Signed: m.Signature != "",
	}
}

// Install 见 api.PluginService。
//
// 语义要点：
//   - 权限未确认（trusted=false）时拒绝安装，而不是"装了再问"；
//   - 已安装且新版本权限扩大时，必须重新确认；
//   - 清单校验失败即拒绝。
func (s *Service) Install(ctx context.Context, wsID string, in api.PluginInstallInput) (*api.PluginDTO, error) {
	if wsID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	var (
		manifest *Manifest
		bundle   []byte
	)
	switch {
	case in.Manifest != nil:
		raw, err := json.Marshal(in.Manifest)
		if err != nil {
			return nil, platform.AsError(err)
		}
		manifest, err = UnmarshalManifest(string(raw))
		if err != nil {
			return nil, platform.ErrInvalid("manifest is not valid json")
		}
	case in.ManifestURL != "":
		if s.fetcher == nil {
			return nil, platform.NewError(501, platform.CodeNotImplemented, "manifest fetching is not configured")
		}
		var err error
		manifest, bundle, err = s.fetcher.FetchManifest(ctx, in.ManifestURL)
		if err != nil {
			return nil, err
		}
	default:
		return nil, platform.ErrInvalid("manifest or manifestUrl is required")
	}

	if err := Validate(manifest); err != nil {
		return nil, err
	}

	// 权限变更检查。权限以逗号分隔的字符串落库，读取后拆分为切片。
	var prevPermsRaw, prevVersion string
	var prevEnabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT permissions, version, enabled FROM plugins WHERE workspace_id = ? AND plugin_key = ?`,
		wsID, manifest.Key).Scan(&prevPermsRaw, &prevVersion, &prevEnabled)
	switch {
	case err == nil:
		prevPerms := splitList(prevPermsRaw)
		diff := DiffPermissions(prevPerms, manifest.Permissions)
		if diff.Expanded() && !in.Trusted {
			return nil, platform.NewError(409, platform.CodeConflict,
				"plugin update requests additional permissions; explicit confirmation required").
				WithDetail("addedPermissions", diff.Added)
		}
	case errors.Is(err, sql.ErrNoRows):
		if !in.Trusted {
			return nil, platform.NewError(400, platform.CodeInvalidRequest,
				"permission list must be confirmed before installation").
				WithDetail("permissions", manifest.Permissions)
		}
	default:
		return nil, platform.AsError(err)
	}

	now := s.clock.Now().UTC()
	manifestJSON := MarshalManifest(manifest)
	perms := strings.Join(manifest.Permissions, ",")
	configJSON := "{}"
	if in.Config != nil {
		b, _ := json.Marshal(in.Config)
		configJSON = string(b)
	}
	builtin := 0
	for _, b := range Builtins() {
		if b.Key == manifest.Key {
			builtin = 1
		}
	}

	// 已安装时保留 enabled 状态，避免升级后被静默禁用
	enabled := 0
	if err == nil && prevEnabled == 1 {
		enabled = 1
	}

	var bundleVal any
	if len(bundle) > 0 {
		bundleVal = bundle
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO plugins (workspace_id, plugin_key, version, manifest, permissions, enabled, trusted, builtin, bundle, config, installed_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, '', ?, ?)
		 ON CONFLICT(workspace_id, plugin_key) DO UPDATE SET
		   version = excluded.version,
		   manifest = excluded.manifest,
		   permissions = excluded.permissions,
		   builtin = excluded.builtin,
		   bundle = COALESCE(excluded.bundle, plugins.bundle),
		   config = excluded.config,
		   updated_at = excluded.updated_at`,
		wsID, manifest.Key, manifest.Version, manifestJSON, perms, enabled, builtin, bundleVal, configJSON, now, now); err != nil {
		return nil, platform.AsError(err)
	}

	dto := toDTO(manifest, builtin == 1)
	dto.Enabled = enabled == 1
	return &dto, nil
}

// Enable 见 api.PluginService。
func (s *Service) Enable(ctx context.Context, wsID, key string, enabled bool) (*api.PluginDTO, error) {
	// 内置插件允许"启用"而不需要先安装（首次启用时写入安装记录）
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM plugins WHERE workspace_id = ? AND plugin_key = ?`, wsID, key).Scan(&count); err != nil {
		return nil, platform.AsError(err)
	}
	if count == 0 {
		var found *Manifest
		for _, b := range Builtins() {
			if b.Key == key {
				found = b
			}
		}
		if found == nil {
			return nil, platform.ErrNotFound("plugin")
		}
		if _, err := s.Install(ctx, wsID, api.PluginInstallInput{Manifest: manifestToMap(found), Trusted: true}); err != nil {
			return nil, err
		}
	}
	flag := 0
	if enabled {
		flag = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE plugins SET enabled = ?, updated_at = ? WHERE workspace_id = ? AND plugin_key = ?`,
		flag, s.clock.Now().UTC(), wsID, key); err != nil {
		return nil, platform.AsError(err)
	}
	all, err := s.List(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if p.Key == key {
			return &p, nil
		}
	}
	return nil, platform.ErrNotFound("plugin")
}

// Bundle 见 api.PluginService：分发插件 bundle（带 ETag，可缓存）。
func (s *Service) Bundle(ctx context.Context, key, version string) ([]byte, string, error) {
	var data []byte
	var storedVersion string
	err := s.db.QueryRowContext(ctx,
		`SELECT bundle, version FROM plugins WHERE plugin_key = ? ORDER BY updated_at DESC LIMIT 1`, key).
		Scan(&data, &storedVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", platform.AsError(err)
	}
	if len(data) == 0 {
		// 内置插件：返回最小可运行宿主桩（真实渲染由前端协议负责）
		data = defaultBundle(key, version)
		storedVersion = version
	}
	etag := `"` + key + "@" + storedVersion + `"`
	return data, etag, nil
}

// Registry 见 api.PluginService：官方注册表（含内置插件 + 远程索引）。
func (s *Service) Registry(ctx context.Context) (any, error) {
	entries := []map[string]any{}
	for _, b := range Builtins() {
		entries = append(entries, map[string]any{
			"key": b.Key, "name": b.Name, "version": b.Version,
			"author": b.Author, "permissions": b.Permissions,
			"nodes": len(b.Nodes), "official": true,
		})
	}
	return map[string]any{
		"apiVersion": "^1.0.0",
		"source":     s.registryURL,
		"plugins":    entries,
	}, nil
}

// Schemas 返回全部内置插件的节点 schema（供 graph 注册）。
func Schemas() map[graph.NodeTypeID]graph.SpecSchema {
	out := map[graph.NodeTypeID]graph.SpecSchema{}
	for _, m := range Builtins() {
		for _, n := range m.Nodes {
			if schema, ok := SpecSchemaFor(m, n.Type); ok {
				out[graph.NodeTypeID(n.Type)] = schema
			}
		}
	}
	return out
}

// SchemaProvider 实现 graph 的插件 schema 注册接口。
type SchemaProvider struct {
	db *sql.DB
}

// NewSchemaProvider 构造 schema 提供者（含内置与已安装插件）。
func NewSchemaProvider(db *sql.DB) *SchemaProvider { return &SchemaProvider{db: db} }

// SpecSchemaFor 见 graph 的插件 schema 接口。
func (p *SchemaProvider) SpecSchemaFor(t graph.NodeTypeID) (graph.SpecSchema, bool) {
	for _, m := range Builtins() {
		if s, ok := SpecSchemaFor(m, string(t)); ok {
			return s, true
		}
	}
	if p.db == nil {
		return graph.SpecSchema{}, false
	}
	rows, err := p.db.Query(`SELECT manifest FROM plugins`)
	if err != nil {
		return graph.SpecSchema{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		m, err := UnmarshalManifest(raw)
		if err != nil {
			continue
		}
		if s, ok := SpecSchemaFor(m, string(t)); ok {
			return s, true
		}
	}
	return graph.SpecSchema{}, false
}

// splitList 拆分逗号分隔的列表（空串返回空切片而非 [""]）。
func splitList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func manifestToMap(m *Manifest) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// defaultBundle 生成内置插件的最小 renderer 桩。
// 真实插件渲染由前端 iframe 宿主 + postMessage 协议完成（见 docs/design/06 §4）。
func defaultBundle(key, version string) []byte {
	return []byte(`/* ` + key + `@` + version + ` — builtin renderer stub.
   插件运行在 sandbox iframe 中：无法访问宿主 DOM/Cookie/localStorage（INV-6）。
   渲染与能力调用通过 postMessage 协议完成。 */
(function () {
  window.addEventListener('message', function (ev) {
    var msg = ev.data || {};
    if (msg.type === 'plugin:init') {
      document.body.innerHTML = '<div style="font-family:sans-serif;padding:12px;color:inherit">' + msg.node.title + '</div>';
    }
  });
  parent.postMessage({ type: 'plugin:ready' }, '*');
})();
`)
}

// Now 便于测试注入时间。
var Now = func() time.Time { return time.Now().UTC() }
