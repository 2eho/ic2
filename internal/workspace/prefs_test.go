package workspace_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/workspace"
	"github.com/context-flow/ic/migrations"
)

/**
 * 工作区偏好的测试重点不是「能存能取」，而是三条安全/一致性要求：
 *   1. **秘密只进不出**（INV-5）：任何读接口都不得回明文；
 *   2. **at-rest 加密**：数据库里必须看不到明文；
 *   3. **合并语义**：只改一个字段不得抹掉其他字段（多端场景的关键）。
 */

const testKey = "0123456789abcdef0123456789abcdef"

func newSvc(t *testing.T) (*workspace.Service, *platform.DB, string) {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + t.TempDir() + "/ws.db"
	cfg.AllowInsecureDevKey = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	const wsID = "ws_test"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (id, name, slug, owner_id, plan, settings, created_at)
		 VALUES (?, '测试', 'test', 'u_1', 'free', '{}', '2026-01-01T00:00:00Z')`, wsID); err != nil {
		t.Fatal(err)
	}
	return workspace.New(db.DB, []byte(testKey), platform.SystemClock()), db, wsID
}

func TestPrefsMergeSemantics(t *testing.T) {
	svc, _, wsID := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Update(ctx, wsID, workspace.Prefs{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	// 只改语言：主题必须保留（整体替换会把它抹掉）
	if _, err := svc.Update(ctx, wsID, workspace.Prefs{Locale: "en-US"}); err != nil {
		t.Fatal(err)
	}
	prefs, _, err := svc.Read(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Theme != "dark" {
		t.Fatalf("主题被抹掉: %+v", prefs)
	}
	if prefs.Locale != "en-US" {
		t.Fatalf("语言未生效: %+v", prefs)
	}

	// 嵌套结构同样合并
	if _, err := svc.Update(ctx, wsID, workspace.Prefs{DefaultModels: &workspace.ModelDefaults{Image: "gpt-image-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, wsID, workspace.Prefs{DefaultModels: &workspace.ModelDefaults{Video: "veo"}}); err != nil {
		t.Fatal(err)
	}
	prefs2, _, _ := svc.Read(ctx, wsID)
	if prefs2.DefaultModels == nil || prefs2.DefaultModels.Video != "veo" {
		t.Fatalf("视频默认模型未生效: %+v", prefs2.DefaultModels)
	}
}

// INV-5：秘密只进不出；数据库里必须是密文。
func TestSecretsAreEncryptedAtRest(t *testing.T) {
	svc, db, wsID := newSvc(t)
	ctx := context.Background()
	const secret = "SUPER-SECRET-TOKEN-abcdef123456"

	if err := svc.PutSecrets(ctx, wsID, map[string]string{"relay": secret}); err != nil {
		t.Fatal(err)
	}

	// 1) 数据库里不得出现明文
	var raw string
	if err := db.QueryRow(`SELECT settings FROM workspaces WHERE id = ?`, wsID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) {
		t.Fatal("数据库中出现明文凭据")
	}
	if strings.Contains(raw, "SUPER-SECRET") {
		t.Fatal("数据库中出现明文片段")
	}

	// 2) 读接口只回掩码
	_, masked, err := svc.Read(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if masked["relay"] == secret {
		t.Fatal("读接口返回了明文")
	}
	if !strings.Contains(masked["relay"], "****") {
		t.Fatalf("掩码格式不正确: %q", masked["relay"])
	}

	// 3) 导出（默认）不含明文；含凭据导出才有
	plainExport, err := svc.Export(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	rawExport, _ := json.Marshal(plainExport)
	if strings.Contains(string(rawExport), secret) {
		t.Fatal("默认导出泄露了凭据")
	}
	fullExport, err := svc.ExportWithSecrets(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	fullRaw, _ := json.Marshal(fullExport)
	if !strings.Contains(string(fullRaw), secret) {
		t.Fatal("含凭据导出应包含明文（这是用户显式选择的行为）")
	}
	if !strings.Contains(string(fullRaw), "warning") {
		t.Fatal("含凭据导出必须带警告字段")
	}
}

// 空值表示删除，而不是存一个空串（空串会让读出来的掩码看起来「存在但为空」）。
func TestSecretEmptyValueDeletes(t *testing.T) {
	svc, _, wsID := newSvc(t)
	ctx := context.Background()
	if err := svc.PutSecrets(ctx, wsID, map[string]string{"a": "v1", "b": "v2"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutSecrets(ctx, wsID, map[string]string{"a": ""}); err != nil {
		t.Fatal(err)
	}
	_, masked, _ := svc.Read(ctx, wsID)
	if _, ok := masked["a"]; ok {
		t.Fatalf("空值应删除条目: %+v", masked)
	}
	if _, ok := masked["b"]; !ok {
		t.Fatalf("未提及的条目不应被删除: %+v", masked)
	}
}

// 老数据（settings 直接是 prefs，无 v/prefs 包装）必须能读出来，
// 否则第一次升级会让所有老工作区的设置消失。
func TestLegacySettingsShapeIsReadable(t *testing.T) {
	svc, db, wsID := newSvc(t)
	ctx := context.Background()
	legacy := `{"theme":"dark","locale":"zh-CN"}`
	if _, err := db.Exec(`UPDATE workspaces SET settings = ? WHERE id = ?`, legacy, wsID); err != nil {
		t.Fatal(err)
	}
	prefs, _, err := svc.Read(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Theme != "dark" || prefs.Locale != "zh-CN" {
		t.Fatalf("老格式未兼容: %+v", prefs)
	}
}

// 损坏的 settings 不应让读取失败（配置页必须能打开）。
func TestCorruptSettingsDegradesGracefully(t *testing.T) {
	svc, db, wsID := newSvc(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE workspaces SET settings = ? WHERE id = ?`, "{not json", wsID); err != nil {
		t.Fatal(err)
	}
	prefs, _, err := svc.Read(ctx, wsID)
	if err != nil {
		t.Fatalf("损坏的偏好不应导致读取失败: %v", err)
	}
	// 零值 = 全部用默认，比一片报错有用
	if prefs.Theme != "" {
		t.Fatalf("期望零值: %+v", prefs)
	}
}

// 导入：拒绝其他产品的配置，而不是「尽力而为」。
func TestImportRejectsForeignConfig(t *testing.T) {
	svc, _, wsID := newSvc(t)
	ctx := context.Background()
	_, err := svc.Import(ctx, wsID, map[string]any{"app": "infinite-canvas", "prefs": map[string]any{}})
	if err == nil {
		t.Fatal("其他产品的配置应被拒绝")
	}
	if _, err := svc.Import(ctx, wsID, map[string]any{"prefs": map[string]any{"theme": "light"}}); err != nil {
		t.Fatalf("无 app 字段时应按本产品处理: %v", err)
	}
}

// 超限的偏好文档必须被拒绝（避免 JSON 字段变成隐形数据库）。
func TestPrefsSizeLimit(t *testing.T) {
	svc, _, wsID := newSvc(t)
	ctx := context.Background()
	huge := workspace.Prefs{UI: map[string]any{"blob": strings.Repeat("x", workspace.MaxPrefsBytes+1000)}}
	_, err := svc.Update(ctx, wsID, huge)
	if err == nil {
		t.Fatal("超大偏好应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeInvalidRequest {
		t.Fatalf("期望 invalid_request，实际 %s", de.Code)
	}
}

// 已删除的工作区不可读写（不泄露存在性）。
func TestDeletedWorkspaceInvisible(t *testing.T) {
	svc, db, wsID := newSvc(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE workspaces SET deleted_at = '2026-01-02T00:00:00Z' WHERE id = ?`, wsID); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.Read(ctx, wsID)
	if err == nil {
		t.Fatal("已删除工作区应不可读")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeNotFound {
		t.Fatalf("期望 not_found，实际 %s", de.Code)
	}
}

// 无主密钥时必须明确报错，而不是存明文。
func TestNoSecretKeyRefusesToStoreSecrets(t *testing.T) {
	_, db, wsID := newSvc(t)
	ctx := context.Background()
	svc := workspace.New(db.DB, nil, platform.SystemClock())
	err := svc.PutSecrets(ctx, wsID, map[string]string{"a": "secret"})
	if err == nil {
		t.Fatal("无主密钥时应拒绝写入凭据")
	}
	var raw string
	_ = db.QueryRow(`SELECT settings FROM workspaces WHERE id = ?`, wsID).Scan(&raw)
	if strings.Contains(raw, "secret") {
		t.Fatal("无主密钥时仍写入了明文")
	}
}
