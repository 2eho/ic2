package apitest

import (
	"context"
	"encoding/json"
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

// 端到端：注册 → 建项目 → 建画布 → 提交 op → 读取文档。
func TestE2EFlow(t *testing.T) {
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file::memory:?cache=shared"
	cfg.AllowInsecureDevKey = true
	cfg.AllowRegistration = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	clock := platform.SystemClock()
	ids := platform.DefaultIDGen()
	graphSvc := graph.NewService(graph.NewSQLStore(db.DB, db.Dialect), graph.NewMemoryBus(), clock, ids)
	authSvc := identity.New(db.DB, clock, ids, cfg)
	logger := platform.NewLogger("debug", "text")
	srv := httptest.NewServer(api.NewRouter(api.Deps{Config: cfg, Logger: logger, Graph: graphSvc, Auth: authSvc, Meta: &stubMeta{}}))
	defer srv.Close()

	// 注册
	body := `{"email":"a@b.com","name":"Zeho","password":"password123"}`
	resp := do(t, srv, "POST", "/api/v1/auth/register", body, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("register status=%d body=%s", resp.StatusCode, readAll(t, resp))
	}
	var sess map[string]any
	decode(t, resp, &sess)
	token := sess["token"].(string)
	ws := sess["workspaces"].([]any)[0].(map[string]any)
	wsID := ws["id"].(string)

	// 建项目
	resp = do(t, srv, "POST", "/api/v1/workspaces/"+wsID+"/projects", `{"name":"项目A"}`, &token)
	if resp.StatusCode != 201 {
		b := readAll(t, resp)
		t.Fatalf("project status=%d body=%s hdr=%v", resp.StatusCode, b, resp.Header)
	}
	var pj map[string]any
	decode(t, resp, &pj)
	pjID := pj["id"].(string)

	// 建画布
	resp = do(t, srv, "POST", "/api/v1/projects/"+pjID+"/canvases", `{"name":"画布1"}`, &token)
	if resp.StatusCode != 201 {
		t.Fatalf("canvas status=%d body=%s", resp.StatusCode, readAll(t, resp))
	}
	var cv struct {
		Canvas map[string]any `json:"canvas"`
	}
	decode(t, resp, &cv)
	cvID := cv.Canvas["id"].(string)

	// 提交 op
	ops := `{"baseVersion":1,"ops":[{"kind":"add_node","node":{"id":"p_1","type":"prompt","title":"提示词","rect":{"x":0,"y":0,"w":320,"h":220},"spec":{"text":"一只猫"}}}]}`
	resp = do(t, srv, "POST", "/api/v1/canvases/"+cvID+"/ops", ops, &token)
	if resp.StatusCode != 200 {
		t.Fatalf("ops status=%d body=%s", resp.StatusCode, readAll(t, resp))
	}
	var applied struct {
		Version  int64 `json:"version"`
		Applied  int   `json:"applied"`
		Document struct {
			Nodes map[string]any `json:"nodes"`
		} `json:"document"`
	}
	decode(t, resp, &applied)
	if applied.Version != 2 || applied.Applied != 1 || len(applied.Document.Nodes) != 1 {
		t.Fatalf("bad apply result: %+v", applied)
	}

	// 读取文档
	resp = do(t, srv, "GET", "/api/v1/canvases/"+cvID, "", &token)
	var doc map[string]any
	decode(t, resp, &doc)
	if doc["version"].(float64) != 2 {
		t.Fatalf("version=%v", doc["version"])
	}

	// 越权访问：另一用户读该画布 → 未认证应 401
	resp = do(t, srv, "GET", "/api/v1/canvases/"+cvID, "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("未认证访问应 401，实际 %d", resp.StatusCode)
	}

	// 非法几何 → 422 invalid_geometry
	bad := `{"baseVersion":2,"ops":[{"kind":"add_node","node":{"id":"p_x","type":"prompt","rect":{"x":1e15,"y":0,"w":320,"h":220},"spec":{"text":"x"}}}]}`
	resp = do(t, srv, "POST", "/api/v1/canvases/"+cvID+"/ops", bad, &token)
	if resp.StatusCode != 422 {
		t.Fatalf("非法几何应 422，实际 %d body=%s", resp.StatusCode, readAll(t, resp))
	}
	var derr map[string]any
	decode2(t, resp, &derr)
	if derr["code"] != "invalid_geometry" {
		t.Fatalf("code=%v", derr["code"])
	}

	// meta
	resp = do(t, srv, "GET", "/api/v1/meta", "", nil)
	var meta map[string]any
	decode(t, resp, &meta)
	if meta["build"] == nil || meta["limits"] == nil {
		t.Fatal("meta 缺字段")
	}
}

type stubMeta struct{}

func (s *stubMeta) Ready(context.Context) error { return nil }

func do(t *testing.T, srv *httptest.Server, method, path, body string, token *string) *http.Response {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != nil {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readAll(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func decode(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func decode2(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
