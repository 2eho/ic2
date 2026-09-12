package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/identity"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

// API 层对抗用例。文件独立于 apitest/e2e_test.go：
// 这里只放「攻击视角」的用例，每条对应 docs/design/11 §3 的一个 ATK 编号。
// 独立文件的目的是让评审时能一眼看出「门禁覆盖了哪些攻击面」，
// 而不是把它们埋在主链路的 happy path 之间。

type apiHarness struct {
	t      *testing.T
	srv    *httptest.Server
	token  string
	userID string
	wsID   string
	db     *platform.DB
	graph  *graph.Service
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:" + t.TempDir() + "/api.db"
	cfg.AllowInsecureDevKey = true
	cfg.AllowRegistration = true
	cfg.LogLevel = "error"
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	clock := platform.SystemClock()
	ids := platform.DefaultIDGen()
	graphSvc := graph.NewService(graph.NewSQLStore(db.DB, db.Dialect), graph.NewMemoryBus(), clock, ids)
	authSvc := identity.New(db.DB, clock, ids, cfg)
	logger := platform.NewLogger("error", "text")
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Config: cfg, Logger: logger, Graph: graphSvc, Auth: authSvc, Meta: &stubMeta{},
	}))
	t.Cleanup(srv.Close)

	h := &apiHarness{t: t, srv: srv, db: db, graph: graphSvc}
	h.register("atk@test.dev", "ATK", "password123")
	return h
}

func (h *apiHarness) register(email, name, password string) {
	body := fmt.Sprintf(`{"email":%q,"name":%q,"password":%q}`, email, name, password)
	resp := h.do("POST", "/api/v1/auth/register", body, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw := readBody(resp)
		h.t.Fatalf("注册失败 %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Token      string `json:"token"`
		User       struct{ ID string }
		Workspaces []struct{ ID string }
	}
	decode(h.t, resp, &out)
	h.token = out.Token
	h.userID = out.User.ID
	if len(out.Workspaces) > 0 {
		h.wsID = out.Workspaces[0].ID
	}
}

func (h *apiHarness) do(method, path, body, token string) *http.Response {
	req, err := http.NewRequest(method, h.srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func readBody(resp *http.Response) string {
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return buf.String()
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	raw := readBody(resp)
	_ = json.Unmarshal([]byte(raw), &body)
	return body.Code
}

// ATK-08：用他人工作区的 id 读取资源必须 404（不泄露存在性）。
//
// 判据不只是「读不到」，还必须是 404 而不是 403——403 等于告诉攻击者
// 「这个工作区存在，只是你没权限」，可用于枚举。
func TestATK08CrossWorkspaceAccessReturns404(t *testing.T) {
	h := newAPIHarness(t)

	// 另一个用户 + 他的工作区
	other := &apiHarness{t: t, srv: h.srv, db: h.db}
	other.register("other@test.dev", "Other", "password123")
	if other.wsID == "" || other.wsID == h.wsID {
		t.Fatal("需要两个不同的工作区才能验证隔离")
	}

	// 用自己的 token 访问他人的工作区（providers 面：只需 auth 依赖，稳定可测）
	resp := h.do("GET", "/api/v1/workspaces/"+other.wsID+"/projects", "", h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("跨工作区访问应返回 404（不泄露存在性），实际 %d: %s", resp.StatusCode, readBody(resp))
	}

	// 他人画布同样不可读
	projectID := other.createProject("other 项目")
	canvasID := other.createCanvas(projectID, "other 画布")
	resp2 := h.do("GET", "/api/v1/canvases/"+canvasID, "", h.token)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden && resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("跨用户读画布应被拒绝，实际 %d", resp2.StatusCode)
	}
}

// ATK-07：viewer 角色提交 op 必须 403。
//
// 这条曾真实失效：appendOps 只校验「已认证」，没有校验角色，
// 因此 viewer 能直接改画布。修法是把「动作 → 所需角色」收敛成矩阵并统一执行。
func TestATK07ViewerCannotWriteCanvas(t *testing.T) {
	h := newAPIHarness(t)
	projectID := h.createProject("atk 项目")
	canvasID := h.createCanvas(projectID, "atk 画布")

	// 造一个 viewer 成员身份
	viewer := &apiHarness{t: t, srv: h.srv, db: h.db}
	viewer.register("viewer@test.dev", "Viewer", "password123")
	if _, err := h.db.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, 'viewer', '2026-01-01T00:00:00Z')`,
		h.wsID, viewer.userID); err != nil {
		t.Fatal(err)
	}

	ops := `[{"kind":"add_node","node":{"id":"n_v_1","type":"prompt","title":"x","rect":{"x":0,"y":0,"w":320,"h":220},"spec":{"text":"hi"}}}]`
	resp := viewer.do("POST", "/api/v1/canvases/"+canvasID+"/ops",
		fmt.Sprintf(`{"baseVersion":0,"ops":%s}`, ops), viewer.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer 提交 op 应 403，实际 %d: %s", resp.StatusCode, readBody(resp))
	}
	if code := errorCode(t, resp); code != platform.CodeForbidden {
		t.Fatalf("期望 code=forbidden，实际 %s", code)
	}

	// editor 应当可以写（确认拒绝是基于角色而不是「一律拒绝」）
	editor := &apiHarness{t: t, srv: h.srv, db: h.db}
	editor.register("editor@test.dev", "Editor", "password123")
	if _, err := h.db.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, 'editor', '2026-01-01T00:00:00Z')`,
		h.wsID, editor.userID); err != nil {
		t.Fatal(err)
	}
	resp2 := editor.do("POST", "/api/v1/canvases/"+canvasID+"/ops",
		fmt.Sprintf(`{"baseVersion":0,"ops":%s}`, ops), editor.token)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("editor 应可写，实际 %d: %s", resp2.StatusCode, readBody(resp2))
	}
}

// ATK-20：1MB 提示词必须 422 且不产生上游调用。
//
// 「不产生上游调用」是这条用例的重点：如果校验只在执行期做，
// 请求已经发往上游（并可能计费）之后才失败，那就等于用用户的额度做校验。
func TestATK20OversizePromptRejectedBeforeDispatch(t *testing.T) {
	h := newAPIHarness(t)
	huge := strings.Repeat("x", graph.MaxPromptBytes+1024)
	body, _ := json.Marshal(map[string]any{
		"capability":  "image.generate",
		"prompt":      huge,
		"outputCount": 1,
	})
	resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/generate", string(body), h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("超限提示词应 422，实际 %d: %s", resp.StatusCode, readBody(resp))
	}
	// 不得创建 Run（创建了就说明校验发生在派发之后）
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("超限请求不应创建 Run，实际创建了 %d 个", n)
	}
	// 响应必须是稳定的边界错误（带 limit 详情，便于 UI 显示具体限制）
	if code := errorCode(t, resp); code != platform.CodeInvalidRequest {
		t.Fatalf("期望 code=%s，实际 %s", platform.CodeInvalidRequest, code)
	}
}

// ATK-20 补充：空白提示词与越界张数同样在提交前被拒绝。
func TestATK20InvalidGenerateParamsRejected(t *testing.T) {
	h := newAPIHarness(t)
	cases := []struct{ name, body string }{
		{"空提示词", `{"capability":"image.generate","prompt":"   ","outputCount":1}`},
		{"张数越界", `{"capability":"image.generate","prompt":"cat","outputCount":99}`},
		{"未知能力", `{"capability":"image.telepathy","prompt":"cat","outputCount":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/generate", c.body, h.token)
			defer resp.Body.Close()
			if resp.StatusCode < 400 || resp.StatusCode >= 500 {
				t.Fatalf("%s 应返回 4xx，实际 %d: %s", c.name, resp.StatusCode, readBody(resp))
			}
		})
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("非法请求不应创建 Run，实际 %d 个", n)
	}
}

// ------------------------------------------------------------------ helpers

func (h *apiHarness) createProject(name string) string {
	h.t.Helper()
	resp := h.do("POST", "/api/v1/workspaces/"+h.wsID+"/projects", fmt.Sprintf(`{"name":%q}`, name), h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("创建项目失败 %d: %s", resp.StatusCode, readBody(resp))
	}
	var out struct{ ID string }
	decode(h.t, resp, &out)
	return out.ID
}

func (h *apiHarness) createCanvas(projectID, name string) string {
	h.t.Helper()
	resp := h.do("POST", "/api/v1/projects/"+projectID+"/canvases", fmt.Sprintf(`{"name":%q}`, name), h.token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("创建画布失败 %d: %s", resp.StatusCode, readBody(resp))
	}
	var out struct {
		Canvas struct{ ID string } `json:"canvas"`
	}
	decode(h.t, resp, &out)
	return out.Canvas.ID
}

type stubMeta struct{}

func (stubMeta) Ready(context.Context) error { return nil }
