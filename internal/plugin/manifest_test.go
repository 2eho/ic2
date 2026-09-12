package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

// testInstallInput 构造安装入参。
func testInstallInput(m *Manifest, trusted bool) api.PluginInstallInput {
	b, _ := jsonMarshal(m)
	var asMap map[string]any
	_ = jsonUnmarshal(b, &asMap)
	return api.PluginInstallInput{Manifest: asMap, Trusted: trusted}
}

func validManifest() *Manifest {
	return &Manifest{
		Key: "com.example.demo", Name: "示例插件", Version: "1.2.0", APIVersion: "^1.0.0",
		Entry: "dist/index.js", Permissions: []string{PermNodeRead, PermNodeWrite},
		Nodes: []NodeDef{{
			Type: "com.example.demo:viewer", Title: "查看器",
			DefaultSize:  &Size{Width: 400, Height: 300},
			ConfigSchema: map[string]any{"type": "object", "properties": map[string]any{"fov": map[string]any{"type": "number"}}},
			Ports:        PortsDef{Inputs: []PortDef{{ID: "in", Name: "输入", Kind: "image"}}},
		}},
	}
}

func TestValidateAcceptsGoodManifest(t *testing.T) {
	if err := Validate(validManifest()); err != nil {
		t.Fatalf("合法清单应通过: %v", err)
	}
}

func TestValidateRejectsBadKey(t *testing.T) {
	m := validManifest()
	for _, bad := range []string{"", "Demo", "com", "com..demo", "com.example.", "a/b"} {
		m.Key = bad
		if err := Validate(m); err == nil {
			t.Fatalf("key=%q 应被拒绝", bad)
		}
	}
}

func TestValidateRejectsBadVersion(t *testing.T) {
	m := validManifest()
	for _, bad := range []string{"1.0", "v1.0.0", "abc"} {
		m.Version = bad
		if err := Validate(m); err == nil {
			t.Fatalf("version=%q 应被拒绝", bad)
		}
	}
}

// 入口路径不允许逃出插件包（与 ATK-05 同族）。
func TestValidateRejectsPathTraversalInEntry(t *testing.T) {
	m := validManifest()
	for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "dist/../../x.js"} {
		m.Entry = bad
		if err := Validate(m); err == nil {
			t.Fatalf("entry=%q 应被拒绝", bad)
		}
	}
}

func TestValidateRejectsUnknownPermission(t *testing.T) {
	m := validManifest()
	m.Permissions = append(m.Permissions, "root.shell")
	if err := Validate(m); err == nil {
		t.Fatal("未知权限应被拒绝（不能忽略）")
	}
}

// 声明网络白名单必须同时声明 network 权限，避免隐式联网。
func TestValidateRejectsNetworkWithoutPermission(t *testing.T) {
	m := validManifest()
	m.Network = []string{"cdn.jsdelivr.net"}
	if err := Validate(m); err == nil {
		t.Fatal("未声明 network 权限却配置白名单应被拒绝")
	}
	m.Permissions = append(m.Permissions, PermNetwork)
	if err := Validate(m); err != nil {
		t.Fatalf("补齐权限后应通过: %v", err)
	}
}

func TestValidateRejectsWildcardHost(t *testing.T) {
	m := validManifest()
	m.Permissions = append(m.Permissions, PermNetwork)
	m.Network = []string{"*"}
	if err := Validate(m); err == nil {
		t.Fatal("通配主机应被拒绝")
	}
}

func TestValidateRejectsNodeTypeNamespaceViolation(t *testing.T) {
	m := validManifest()
	m.Nodes[0].Type = "other.key:viewer"
	if err := Validate(m); err == nil {
		t.Fatal("节点类型必须按插件 key 命名空间")
	}
}

func TestValidateRejectsDuplicateNodeType(t *testing.T) {
	m := validManifest()
	m.Nodes = append(m.Nodes, m.Nodes[0])
	if err := Validate(m); err == nil {
		t.Fatal("重复节点类型应被拒绝")
	}
}

func TestValidateRejectsInvalidPortKind(t *testing.T) {
	m := validManifest()
	m.Nodes[0].Ports = PortsDef{Inputs: []PortDef{{ID: "in", Name: "x", Kind: "sql"}}}
	if err := Validate(m); err == nil {
		t.Fatal("非法端口类型应被拒绝")
	}
}

func TestValidateRejectsDuplicatePortID(t *testing.T) {
	m := validManifest()
	m.Nodes[0].Ports = PortsDef{
		Inputs:  []PortDef{{ID: "io", Name: "a", Kind: "image"}},
		Outputs: []PortDef{{ID: "io", Name: "b", Kind: "image"}},
	}
	if err := Validate(m); err == nil {
		t.Fatal("重复端口 ID 应被拒绝")
	}
}

func TestValidateRejectsBadConfigSchema(t *testing.T) {
	m := validManifest()
	m.Nodes[0].ConfigSchema = map[string]any{"properties": map[string]any{}}
	if err := Validate(m); err == nil {
		t.Fatal("缺少 type 的 schema 应被拒绝")
	}
	m.Nodes[0].ConfigSchema = map[string]any{"type": "unknown"}
	if err := Validate(m); err == nil {
		t.Fatal("未知 schema type 应被拒绝")
	}
}

func TestValidateRejectsNoNodes(t *testing.T) {
	m := validManifest()
	m.Nodes = nil
	if err := Validate(m); err == nil {
		t.Fatal("没有节点的插件应被拒绝")
	}
}

func TestSpecSchemaForPlugin(t *testing.T) {
	m := validManifest()
	s, ok := SpecSchemaFor(m, "com.example.demo:viewer")
	if !ok {
		t.Fatal("应能生成 spec schema")
	}
	if s.Fields["fov"] != "number" {
		t.Fatalf("字段类型未映射: %v", s.Fields)
	}
	if s.Ports.Inputs[0].Kind != "image" {
		t.Fatalf("端口未映射: %+v", s.Ports.Inputs)
	}
	// 保留字段
	if _, ok := s.Fields["pluginKey"]; !ok {
		t.Fatal("缺少保留字段 pluginKey")
	}
}

func TestDiffPermissions(t *testing.T) {
	d := DiffPermissions([]string{PermNodeRead}, []string{PermNodeRead, PermAIGenerate})
	if !d.Expanded() || len(d.Added) != 1 || d.Added[0] != PermAIGenerate {
		t.Fatalf("diff=%+v", d)
	}
	shrink := DiffPermissions([]string{PermNodeRead, PermStorage}, []string{PermNodeRead})
	if shrink.Expanded() {
		t.Fatal("权限减少不算扩大")
	}
	if len(shrink.Removed) != 1 || shrink.Removed[0] != PermStorage {
		t.Fatalf("diff=%+v", shrink)
	}
}

func TestCostsMoney(t *testing.T) {
	if !CostsMoney("ai.generate:image") || CostsMoney("node.read") {
		t.Fatal("费用能力判定错误")
	}
}

// ATK-09：插件调用未声明能力必须拒绝并给出可审计的错误。
func TestATK09UndeclaredCapabilityRejected(t *testing.T) {
	m := validManifest() // 只有 node.read / node.write
	c := NewChecker(m)
	if err := c.Check(MethodAssetGetURL, ""); err == nil {
		t.Fatal("未声明 asset.read 却被允许")
	} else if de := platform.AsDomainError(err); de.Code != platform.CodePluginPermission {
		t.Fatalf("code=%s", de.Code)
	}
	if err := c.Check(MethodNodeGet, ""); err != nil {
		t.Fatalf("已声明能力应放行: %v", err)
	}
}

func TestUnknownHostMethodRejected(t *testing.T) {
	c := NewChecker(validManifest())
	if err := c.Check(HostMethod("process.exec"), ""); err == nil {
		t.Fatal("未知方法必须拒绝（白名单语义）")
	}
}

func TestAIGenerateScopedPermission(t *testing.T) {
	m := validManifest()
	m.Permissions = append(m.Permissions, "ai.generate:image")
	c := NewChecker(m)
	if err := c.Check(MethodAIGenerate, "image"); err != nil {
		t.Fatalf("已声明 ai.generate:image 应放行: %v", err)
	}
	if err := c.Check(MethodAIGenerate, "video"); err == nil {
		t.Fatal("未声明 ai.generate:video 应被拒绝")
	}
}

func TestNetworkPermissionAndAllowlist(t *testing.T) {
	m := validManifest()
	c := NewChecker(m)
	if err := c.CheckHost("cdn.jsdelivr.net"); err == nil {
		t.Fatal("无 network 权限时任何主机都应拒绝")
	}
	m.Permissions = append(m.Permissions, PermNetwork)
	m.Network = []string{"cdn.jsdelivr.net"}
	c = NewChecker(m)
	if err := c.CheckHost("cdn.jsdelivr.net"); err != nil {
		t.Fatalf("白名单主机应放行: %v", err)
	}
	if err := c.CheckHost("evil.example.com"); err == nil {
		t.Fatal("非白名单主机应拒绝")
	}
}

// INV-6：sandbox 属性绝不能包含 allow-same-origin。
func TestSandboxNeverAllowsSameOrigin(t *testing.T) {
	attrs := SandboxAttrs()
	if strings.Contains(attrs, "allow-same-origin") {
		t.Fatal("sandbox 绝不能包含 allow-same-origin（INV-6）")
	}
	if !strings.Contains(attrs, "allow-scripts") {
		t.Fatal("插件需要 allow-scripts 才能运行")
	}
}

func TestCSPForPlugin(t *testing.T) {
	noNet := CSPForPlugin(nil)
	if !strings.Contains(noNet, "connect-src 'none'") {
		t.Fatalf("无网络插件必须禁止连接: %s", noNet)
	}
	if strings.Contains(noNet, "unsafe-eval") {
		t.Fatalf("不得放开 unsafe-eval: %s", noNet)
	}
	withNet := CSPForPlugin([]string{"cdn.jsdelivr.net"})
	if !strings.Contains(withNet, "https://cdn.jsdelivr.net") {
		t.Fatalf("白名单未写入 CSP: %s", withNet)
	}
	withNet = CSPForPlugin([]string{"cdn.jsdelivr.net"})
	if !strings.Contains(withNet, "https://cdn.jsdelivr.net") {
		t.Fatalf("白名单未写入 CSP: %s", withNet)
	}
}

func TestBuiltinsAreValid(t *testing.T) {
	for _, m := range Builtins() {
		if err := Validate(m); err != nil {
			t.Fatalf("内置插件 %s 清单不合法: %v", m.Key, err)
		}
	}
}

func TestSchemasCoversBuiltinNodes(t *testing.T) {
	schemas := Schemas()
	if len(schemas) != len(Builtins()) {
		t.Fatalf("schema 数量=%d，插件数=%d", len(schemas), len(Builtins()))
	}
	for _, m := range Builtins() {
		if _, ok := schemas[graphType(m)]; !ok {
			t.Fatalf("插件 %s 的节点 schema 缺失", m.Key)
		}
	}
}

func graphType(m *Manifest) graph.NodeTypeID { return graph.NodeTypeID(m.Nodes[0].Type) }

func TestInstallRequiresTrustConfirmation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	_, err := svc.Install(ctx, "ws_1", testInstallInput(validManifest(), false))
	if err == nil {
		t.Fatal("未确认权限应拒绝安装（不能先装再问）")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeInvalidRequest {
		t.Fatalf("code=%s", de.Code)
	}
}

// 权限扩大必须重新确认。
func TestInstallRejectsPermissionExpansionWithoutTrust(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Install(ctx, "ws_1", testInstallInput(validManifest(), true)); err != nil {
		t.Fatal(err)
	}
	upgraded := validManifest()
	upgraded.Version = "1.3.0"
	upgraded.Permissions = append(upgraded.Permissions, PermAIGenerate)
	_, err := svc.Install(ctx, "ws_1", testInstallInput(upgraded, false))
	if err == nil {
		t.Fatal("权限扩大应要求重新确认")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeConflict {
		t.Fatalf("code=%s", de.Code)
	}
	if _, err := svc.Install(ctx, "ws_1", testInstallInput(upgraded, true)); err != nil {
		t.Fatalf("确认后应可升级: %v", err)
	}
}

func TestInstallRejectsInvalidManifest(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	bad := validManifest()
	bad.Nodes[0].Ports = PortsDef{Inputs: []PortDef{{ID: "x", Name: "x", Kind: "bogus"}}}
	if _, err := svc.Install(ctx, "ws_1", testInstallInput(bad, true)); err == nil {
		t.Fatal("非法清单应被拒绝")
	}
}

func TestEnableBuiltinPlugin(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	key := Builtins()[0].Key
	dto, err := svc.Enable(ctx, "ws_1", key, true)
	if err != nil {
		t.Fatalf("启用内置插件失败: %v", err)
	}
	if !dto.Enabled || !dto.Builtin {
		t.Fatalf("dto=%+v", dto)
	}
	dto, err = svc.Enable(ctx, "ws_1", key, false)
	if err != nil || dto.Enabled {
		t.Fatalf("禁用失败: %+v %v", dto, err)
	}
}

func TestEnableUnknownPlugin(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.Enable(context.Background(), "ws_1", "com.nope.nope", true); err == nil {
		t.Fatal("未知插件应报 404")
	}
}

func TestListMergesBuiltins(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	list, err := svc.List(ctx, "ws_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < len(Builtins()) {
		t.Fatalf("应至少包含全部内置插件: %d < %d", len(list), len(Builtins()))
	}
}

func TestBundleFallbackForBuiltin(t *testing.T) {
	svc, _ := newTestService(t)
	data, etag, err := svc.Bundle(context.Background(), Builtins()[0].Key, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || etag == "" {
		t.Fatalf("bundle 为空: len=%d etag=%q", len(data), etag)
	}
}

// ATK-10 的服务端侧：bundle 不得包含 allow-same-origin 相关指令。
func TestBundleDoesNotGrantSameOrigin(t *testing.T) {
	svc, _ := newTestService(t)
	data, _, err := svc.Bundle(context.Background(), Builtins()[0].Key, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "allow-same-origin") {
		t.Fatal("bundle 不应涉及 sandbox 属性（由宿主设置）")
	}
	if !strings.Contains(string(data), "postMessage") {
		t.Fatal("内置 bundle 应通过 postMessage 通信")
	}
}

// ---------------------------------------------------------------- helpers

func newTestService(t *testing.T) (*Service, *platform.DB) {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file::memory:?cache=shared"
	cfg.AllowInsecureDevKey = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	return New(db.DB, platform.SystemClock(), platform.DefaultIDGen(), nil, ""), db
}
