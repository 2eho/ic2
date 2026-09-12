package provider

import (
	"context"
	"database/sql"
	"io"
	"testing"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/platform"
	_ "modernc.org/sqlite"
)

// 这一组用例覆盖两个**真实缺陷**，它们都有同一个特征：
// 报错内容与真实原因完全无关，因此只能靠端到端路径发现。

// 缺陷 1：`Resolve` 用渠道行 id 去查适配器注册表。
//
// 适配器按**协议**注册（openai / gemini / script），而渠道行 id 是
// 用户可自定义的（`my-relay`、`dbg-1789...`）。用后者查表必然落空，
// 表现是「凭据齐全却报没有可用凭据」。
//
// 本地用例一直没发现它，因为种子数据恰好把渠道 id 设成 `openai`
// （与协议名相同）。e2e 用自定义 id 才暴露——所以这条用例必须用自定义 id。
func TestResolveUsesProtocolNotRowID(t *testing.T) {
	svc, db, wsID := newProviderService(t)
	ctx := context.Background()

	if _, err := svc.CreateProvider(ctx, wsID, api.ProviderInput{
		ID: "my-relay-01", Name: "我的中转", Kind: "openai",
		BaseURL: "https://relay.example/v1", AuthKind: "bearer",
		Capabilities: []string{"image.generate"},
	}); err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	if _, err := svc.CreateCredential(ctx, wsID, "my-relay-01", api.CredentialInput{
		Name: "k", Secret: "sk-test",
	}); err != nil {
		t.Fatalf("建凭据失败: %v", err)
	}

	resolver := NewCredentialResolver(svc)
	resolver.Register("openai", stubAdapter{id: "openai"})

	adapter, cred, ok := resolver.Resolve(CapImageGenerate, "my-relay-01", "")
	if !ok || adapter == nil {
		t.Fatalf("自定义渠道 id 必须能解析到适配器，实际失败: %v", resolver.LastError())
	}
	if cred.ProviderID != "openai" {
		t.Fatalf("ProviderID 应当是协议名，实际 %q", cred.ProviderID)
	}
	if cred.SourceProviderID != "my-relay-01" {
		t.Fatalf("SourceProviderID 应当保留渠道行 id，实际 %q", cred.SourceProviderID)
	}
	_ = db
}

// 缺陷 2：适配器未注册时，失败原因不能被丢掉。
//
// 原来的 Resolve 只返回 bool，调用方只能报「没有可用凭据」——
// 而真实原因可能是「协议没注册」「渠道没声明该能力」「密钥解不开」，
// 三者的处理方式完全不同。e2e 里为了定位这一点花了很多轮。
func TestResolveRecordsReasonWhenAdapterMissing(t *testing.T) {
	svc, _, wsID := newProviderService(t)
	ctx := context.Background()
	if _, err := svc.CreateProvider(ctx, wsID, api.ProviderInput{
		ID: "weird", Name: "怪协议", Kind: "totally-unknown",
		BaseURL: "https://x.example", Capabilities: []string{"image.generate"},
	}); err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	if _, err := svc.CreateCredential(ctx, wsID, "weird", api.CredentialInput{Name: "k", Secret: "s"}); err != nil {
		t.Fatalf("建凭据失败: %v", err)
	}
	resolver := NewCredentialResolver(svc)
	if _, _, ok := resolver.Resolve(CapImageGenerate, "weird", ""); ok {
		t.Fatal("未注册的协议不应解析成功")
	}
	err := resolver.LastError()
	if err == nil {
		t.Fatal("失败原因必须被记录下来（否则调用方只能报一句笼统的错误）")
	}
	if !contains([]string{"totally-unknown", "没有注册适配器"}, err.Error()) &&
		!containsSub(err.Error(), "适配器") {
		t.Fatalf("失败原因应当指出是协议未注册，实际 %v", err)
	}
}

// 缺陷 3：渠道未声明该能力时，原因也要能说出来。
// 「渠道建好了但没勾 image.generate」是配置阶段最常见的错误。
func TestResolveRecordsReasonWhenCapabilityMissing(t *testing.T) {
	svc, _, wsID := newProviderService(t)
	ctx := context.Background()
	if _, err := svc.CreateProvider(ctx, wsID, api.ProviderInput{
		ID: "text-only", Name: "只做文本", Kind: "openai",
		BaseURL: "https://x.example", Capabilities: []string{"text.generate"},
	}); err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	if _, err := svc.CreateCredential(ctx, wsID, "text-only", api.CredentialInput{Name: "k", Secret: "s"}); err != nil {
		t.Fatalf("建凭据失败: %v", err)
	}
	resolver := NewCredentialResolver(svc)
	resolver.Register("openai", stubAdapter{id: "openai"})
	if _, _, ok := resolver.Resolve(CapImageGenerate, "text-only", ""); ok {
		t.Fatal("未声明该能力的渠道不应被选中")
	}
	if !containsSub(resolver.LastError().Error(), "未声明能力") {
		t.Fatalf("原因应当指出能力未声明，实际 %v", resolver.LastError())
	}
}

// LastError 在成功之后必须清空：否则「已经修好的问题」会被一直报成当前问题。
func TestResolveClearsLastErrorAfterSuccess(t *testing.T) {
	svc, _, wsID := newProviderService(t)
	ctx := context.Background()
	if _, err := svc.CreateProvider(ctx, wsID, api.ProviderInput{
		ID: "ok", Name: "OK", Kind: "openai",
		BaseURL: "https://x.example", Capabilities: []string{"image.generate"},
	}); err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	if _, err := svc.CreateCredential(ctx, wsID, "ok", api.CredentialInput{Name: "k", Secret: "s"}); err != nil {
		t.Fatalf("建凭据失败: %v", err)
	}
	resolver := NewCredentialResolver(svc)

	if _, _, ok := resolver.Resolve(CapImageGenerate, "missing", ""); ok {
		t.Fatal("不存在的渠道不应成功")
	}
	if resolver.LastError() == nil {
		t.Fatal("首次失败应当记录原因")
	}
	resolver.Register("openai", stubAdapter{id: "openai"})
	if _, _, ok := resolver.Resolve(CapImageGenerate, "ok", ""); !ok {
		t.Fatalf("应当成功: %v", resolver.LastError())
	}
	if resolver.LastError() != nil {
		t.Fatalf("成功后必须清空上次原因，实际 %v", resolver.LastError())
	}
}

// stubAdapter 是最小适配器替身（只用于让 Resolve 能找到「协议」）。
type stubAdapter struct{ id string }

func (s stubAdapter) ID() string { return s.id }
func (s stubAdapter) Capabilities() []Capability {
	return []Capability{CapImageGenerate, CapTextGenerate}
}
func (s stubAdapter) Invoke(context.Context, Credential, Request) (Response, error) {
	return Response{}, nil
}
func (s stubAdapter) Stream(context.Context, Credential, Request) (Stream, error) {
	return nil, ErrStreamUnsupported{Adapter: s.id}
}
func (s stubAdapter) Poll(context.Context, Credential, string) (RemoteTask, error) {
	return RemoteTask{}, nil
}
func (s stubAdapter) ListModels(context.Context, Credential) ([]ModelInfo, error) { return nil, nil }
func (s stubAdapter) FetchAsset(context.Context, Credential, AssetRef) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// ---------------------------------------------------------------- 测试装置

// newProviderService 建一个最小可用的 Service（真实 SQLite，方案照 migrations 的子集）。
//
// 不共用 migrations 全量：这里只需要四张表，全量迁移会让这条用例
// 依赖与它无关的 schema 演进（改一个无关表就可能让它红）。
func newProviderService(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	// 用临时**文件**而不是 `:memory:`：内存库在每个连接上都是一份新库，
	// 而连接池（MaxOpenConns>1）会让「建表连 A、查询连 B」看到空 schema。
	// 这是本项目踩过的同一个坑的另一面（见 platform.OpenDB 的注释）。
	dbPath := t.TempDir() + "/provider_test.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ddl := []string{
		`CREATE TABLE providers (
			id TEXT NOT NULL, workspace_id TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'builtin',
			name TEXT NOT NULL, capabilities TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL DEFAULT '',
			auth_kind TEXT NOT NULL DEFAULT 'bearer', script TEXT,
			enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL,
			PRIMARY KEY (workspace_id, id))`,
		`CREATE TABLE provider_credentials (
			id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, provider_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '', secret_ref TEXT NOT NULL, masked TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 0, limits TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, created_by TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE models (
			id TEXT NOT NULL, provider_id TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '',
			capabilities TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (provider_id, id))`,
	}
	for _, d := range ddl {
		if _, err := db.Exec(d); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	// 用真实的 AES 密钥库（而不是替身）：凭据的密封/解封是这条路径的一部分，
	// 替身会让「limits 能否随凭据一起读出来」这件事无法被验证。
	secrets, err := NewAESSecretStore([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("构造密钥库失败: %v", err)
	}
	svc := New(Options{DB: db, Secrets: secrets, Clock: platform.SystemClock(), IDs: platform.DefaultIDGen()})
	return svc, db, "ws_test"
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
