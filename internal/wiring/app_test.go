package wiring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/wiring"
)

// harness 是一套「真实装配」的测试环境。
//
// 这个测试文件存在的理由（上一轮的真实缺陷）：
// cmd/ic-server 只装了 Graph/Auth/Meta 三个依赖，exec/asset/provider/prompt/plugin/agent
// 全部为 nil，所以「资产上传」「生成」「插件」「Agent」在运行时全部返回 501，
// 而当时的单测是**对着手写的小依赖集合**跑的，因此全绿。
//
// 修法：让测试用与生产完全相同的 wiring.Build，并对**每个功能面**做一次真实调用。
// 任何「忘了接线」都会在这里变成 501，从而红掉。
type harness struct {
	t       *testing.T
	app     *wiring.App
	server  *httptest.Server
	token   string
	wsID    string
	baseDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + filepath.Join(dir, "ic.db")
	cfg.BlobDriver = "fs"
	cfg.BlobFSRoot = filepath.Join(dir, "assets")
	cfg.AllowInsecureDevKey = true
	cfg.AllowRegistration = true
	cfg.EnableWorker = false
	cfg.LogLevel = "error"

	app, err := wiring.Build(context.Background(), wiring.Options{Config: cfg, Migrate: true})
	if err != nil {
		t.Fatalf("装配失败: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	h := &harness{t: t, app: app, server: httptest.NewServer(app.Router), baseDir: dir}
	t.Cleanup(h.server.Close)
	h.register()
	return h
}

func (h *harness) register() {
	body := `{"email":"wiring@test.dev","name":"Wiring","password":"password123"}`
	resp := h.do("POST", "/api/v1/auth/register", body, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("注册失败: status=%d", resp.StatusCode)
	}
	var out struct {
		Token      string `json:"token"`
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	decode(h.t, resp, &out)
	h.token = out.Token
	if len(out.Workspaces) > 0 {
		h.wsID = out.Workspaces[0].ID
	}
}

func (h *harness) do(method, path, body, token string) *http.Response {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
}

// assertNotNotImplemented 断言接口没有被「未接线」挡住。
// 这是本文件的核心断言：501 + not_implemented 就是我们上一轮的病征。
func assertNotNotImplemented(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	if resp.StatusCode == http.StatusNotImplemented {
		body, _ := readAll(resp)
		t.Fatalf("%s 返回 501（依赖未装配）: %s", what, body)
	}
}

func readAll(resp *http.Response) (string, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// TestEveryServiceIsWired 逐个功能面确认「已装配」。
func TestEveryServiceIsWired(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"资产列表", "GET", "/api/v1/workspaces/" + h.wsID + "/assets", ""},
		{"渠道列表", "GET", "/api/v1/workspaces/" + h.wsID + "/providers", ""},
		{"模型列表", "GET", "/api/v1/workspaces/" + h.wsID + "/models?capability=image.generate", ""},
		{"提示词来源", "GET", "/api/v1/workspaces/" + h.wsID + "/prompt-sources", ""},
		{"提示词检索", "GET", "/api/v1/workspaces/" + h.wsID + "/prompt-sources", ""},
		{"插件列表", "GET", "/api/v1/workspaces/" + h.wsID + "/plugins", ""},
		{"插件注册表", "GET", "/api/v1/plugin-registry", ""},
		{"MCP 端点是 POST", "POST", "/api/v1/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := h.do(c.method, c.path, c.body, h.token)
			defer resp.Body.Close()
			assertNotNotImplemented(t, resp, c.name)
			if resp.StatusCode >= 500 {
				body, _ := readAll(resp)
				t.Fatalf("%s 返回 %d: %s", c.name, resp.StatusCode, body)
			}
		})
	}
}

// TestAssetUploadRoundTrip 资产上传 → 列表 → 原始内容 → 去重。
func TestAssetUploadRoundTrip(t *testing.T) {
	h := newHarness(t)
	content := []byte("IC-ASSET-PAYLOAD-0123456789")
	first := h.upload("note.txt", content)
	if first.ID == "" {
		t.Fatal("上传后未返回资产 id")
	}
	if first.Size != int64(len(content)) {
		t.Fatalf("资产大小不符: %d != %d", first.Size, len(content))
	}

	// 内容寻址去重：同内容再传一次必须复用同一条记录
	second := h.upload("note-copy.txt", content)
	if second.ID != first.ID {
		t.Fatalf("相同内容应去重：%s != %s", second.ID, first.ID)
	}

	// 原始内容可下载且字节一致
	resp := h.do("GET", fmt.Sprintf("/api/v1/assets/%s/raw?workspaceId=%s", first.ID, h.wsID), "", h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("下载资产失败: %d", resp.StatusCode)
	}
	got, _ := readAll(resp)
	if got != string(content) {
		t.Fatalf("下载内容不一致: %q", got)
	}
}

// TestCanvasOpWriteBackFlow 画布 op 路径：建项目 → 建画布 → 提交 op → 读取文档。
func TestCanvasOpWriteBackFlow(t *testing.T) {
	h := newHarness(t)
	projectID := h.createProject("wiring 项目")
	canvasID := h.createCanvas(projectID, "wiring 画布")

	ops := `[{"kind":"add_node","node":{"id":"n_prompt_1","type":"prompt","title":"提示词","rect":{"x":100,"y":100,"w":320,"h":220},"spec":{"text":"一只猫"}}}]`
	resp := h.do("POST", "/api/v1/canvases/"+canvasID+"/ops",
		fmt.Sprintf(`{"baseVersion":0,"ops":%s}`, ops), h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		t.Fatalf("提交 op 失败: %d %s", resp.StatusCode, body)
	}

	resp2 := h.do("GET", "/api/v1/canvases/"+canvasID, "", h.token)
	defer resp2.Body.Close()
	var doc struct {
		Version int `json:"version"`
		// 文档中节点是「按 id 索引的对象」而不是数组：
		// op 应用是按 id 定位的，用 map 让 set_spec/move_node 都是 O(1)。
		Nodes map[string]struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"nodes"`
	}
	decode(t, resp2, &doc)
	if doc.Version < 1 {
		t.Fatalf("版本未推进: %d", doc.Version)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("节点数量不符: %d", len(doc.Nodes))
	}
	if _, ok := doc.Nodes["n_prompt_1"]; !ok {
		t.Fatalf("节点未写入: %+v", doc.Nodes)
	}
}

// TestGenerateWithoutCredentialFailsClearly 没有凭据时，生成必须给出明确错误
// 而不是 500 或静默成功——用户需要知道「该去配 Key 了」。
func TestGenerateWithoutCredentialFailsClearly(t *testing.T) {
	h := newHarness(t)
	resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/generate",
		`{"capability":"image.generate","prompt":"a cat","outputCount":1}`, h.token)
	defer resp.Body.Close()
	assertNotNotImplemented(t, resp, "直通生成")
	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
		body, _ := readAll(resp)
		t.Fatalf("期望 422/400（缺凭据），实际 %d: %s", resp.StatusCode, body)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	if errBody.Code == "" {
		t.Fatal("错误响应缺少稳定 code")
	}
}

// TestProviderCreateAndCredentialMasked 渠道创建 + 凭据只回显掩码（INV-5）。
func TestProviderCreateAndCredentialMasked(t *testing.T) {
	h := newHarness(t)
	resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/providers",
		`{"id":"openai","name":"OpenAI","kind":"custom","baseUrl":"https://api.openai.com","authKind":"bearer","capabilities":["image.generate","text.generate","model.list"]}`,
		h.token)
	defer resp.Body.Close()
	assertNotNotImplemented(t, resp, "创建渠道")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		t.Fatalf("创建渠道失败: %d %s", resp.StatusCode, body)
	}

	const secret = "sk-test-SUPERSECRETVALUE-1234567890"
	resp2 := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/providers/openai/credentials",
		fmt.Sprintf(`{"name":"默认","secret":%q,"priority":0}`, secret), h.token)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		body, _ := readAll(resp2)
		t.Fatalf("创建凭据失败: %d %s", resp2.StatusCode, body)
	}
	raw, _ := readAll(resp2)
	if strings.Contains(raw, secret) {
		t.Fatalf("凭据响应泄露了明文 secret: %s", raw)
	}
	if !strings.Contains(raw, "****") {
		t.Fatalf("凭据响应应包含掩码: %s", raw)
	}

	// 数据库里也不得出现明文
	var stored string
	if err := h.app.DB.QueryRowContext(context.Background(),
		`SELECT secret_ref FROM provider_credentials LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "SUPERSECRETVALUE") {
		t.Fatal("数据库中的凭据未加密")
	}

	// 列表接口同样不得泄露
	resp3 := h.do("GET", "/api/v1/workspaces/"+h.wsID+"/providers", "", h.token)
	defer resp3.Body.Close()
	listRaw, _ := readAll(resp3)
	if strings.Contains(listRaw, secret) {
		t.Fatal("渠道列表泄露了明文 secret")
	}
}

// TestPromptSourceRejectsPrivateURL prompt 来源创建时即拒绝内网地址（SSRF 前置拦截）。
func TestPromptSourceRejectsPrivateURL(t *testing.T) {
	h := newHarness(t)
	resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/prompt-sources",
		`{"name":"内网","url":"http://169.254.169.254/latest/meta-data"}`,
		h.token)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		t.Fatal("内网地址应被拒绝（ssrf_blocked）")
	}
	var errBody struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	if errBody.Code != platform.CodeSSRFBlocked {
		t.Fatalf("期望 ssrf_blocked，实际 %s", errBody.Code)
	}
}

// TestPluginRegistryHasBuiltins 官方插件注册表可用（10.8 的落点）。
func TestPluginRegistryHasBuiltins(t *testing.T) {
	h := newHarness(t)
	resp := h.do("GET", "/api/v1/plugin-registry", "", h.token)
	defer resp.Body.Close()
	assertNotNotImplemented(t, resp, "插件注册表")
	raw, _ := readAll(resp)
	for _, want := range []string{"markdown", "svg", "html", "panorama", "sticky"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("内置插件注册表缺少 %s: %s", want, raw)
		}
	}
}

// ------------------------------------------------------------------ helpers

type assetDTO struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
}

func (h *harness) upload(name string, content []byte) assetDTO {
	h.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		h.t.Fatal(err)
	}
	w.Close()

	req, err := http.NewRequest("POST", h.server.URL+"/api/v1/workspaces/"+h.wsID+"/assets", &buf)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	assertNotNotImplemented(h.t, resp, "上传资产")
	if resp.StatusCode != http.StatusCreated {
		body, _ := readAll(resp)
		h.t.Fatalf("上传失败: %d %s", resp.StatusCode, body)
	}
	var dto assetDTO
	decode(h.t, resp, &dto)
	return dto
}

func (h *harness) createProject(name string) string {
	h.t.Helper()
	resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/projects",
		fmt.Sprintf(`{"name":%q}`, name), h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := readAll(resp)
		h.t.Fatalf("创建项目失败: %d %s", resp.StatusCode, body)
	}
	var out struct {
		ID string `json:"id"`
	}
	decode(h.t, resp, &out)
	return out.ID
}

func (h *harness) createCanvas(projectID, name string) string {
	h.t.Helper()
	resp := h.do("POST", "/api/v1/projects/"+projectID+"/canvases",
		fmt.Sprintf(`{"name":%q}`, name), h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := readAll(resp)
		h.t.Fatalf("创建画布失败: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Canvas struct {
			ID string `json:"id"`
		} `json:"canvas"`
	}
	decode(h.t, resp, &out)
	return out.Canvas.ID
}

var _ = os.Getenv
