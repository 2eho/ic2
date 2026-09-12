package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// 契约测试：用录制样本校验请求体字段与响应解析（docs/design/13 §3.1）。
// 每个适配器至少一组真实样本；样本变更即视为协议变更，必须同步文档。

func readSample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("读取样本失败: %v", err)
	}
	return b
}

func newAdapter() *Adapter {
	guard := &platform.NetGuard{AllowPrivate: true}
	client := platform.NewGuardedClient(guard, &http.Client{Timeout: 5 * time.Second}, 5*time.Second)
	return New(provider.NewTransport(client, guard, platform.SystemClock()))
}

func testCred(url string) provider.Credential {
	return provider.Credential{ID: "c1", ProviderID: "openai", BaseURL: url, AuthKind: "bearer", Secret: "sk-test"}
}

func TestImageGenerationContract(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "images_generations.json"))
	}))
	defer srv.Close()

	res, err := newAdapter().Invoke(context.Background(), testCred(srv.URL), provider.Request{
		Capability: provider.CapImageGenerate,
		Model:      "gpt-image-1",
		Prompt:     "一只猫",
		Count:      2,
		Params:     map[string]any{"size": "1024x1024", "quality": "high"},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("鉴权头错误: %q", gotAuth)
	}
	if gotBody["model"] != "gpt-image-1" || gotBody["n"].(float64) != 2 || gotBody["size"] != "1024x1024" {
		t.Fatalf("请求体字段不符: %v", gotBody)
	}
	if gotBody["response_format"] != "b64_json" {
		t.Fatalf("必须请求 b64_json 便于服务端统一落库: %v", gotBody)
	}
	if len(res.Assets) != 1 || len(res.Assets[0].Bytes) == 0 {
		t.Fatalf("未解析出图片: %+v", res.Assets)
	}
	if res.Usage.Images != 1 {
		t.Fatalf("计量错误: %+v", res.Usage)
	}
}

// 参考图必须用 image[] 数组字段，避免被中转站拒绝（原项目踩过的坑）。
func TestImageEditUsesArrayField(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "images_generations.json"))
	}))
	defer srv.Close()

	creds := testCred(srv.URL)
	_, err := newAdapter().Invoke(context.Background(), creds, provider.Request{
		Capability: provider.CapImageEdit,
		Model:      "gpt-image-1",
		Prompt:     "把背景换成雪山",
		Inputs: []provider.ResolvedInput{{
			Kind:    "image",
			AssetID: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
		}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(raw, `name="image[]"`) {
		t.Fatalf("必须使用 image[] 字段: %.300s", raw)
	}
	if !strings.Contains(raw, "multipart/form-data") && !strings.Contains(raw, "Content-Disposition") {
		t.Fatalf("请求体不是 multipart: %.200s", raw)
	}
}

func TestImageEditRequiresReference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发出请求")
	}))
	defer srv.Close()
	_, err := newAdapter().Invoke(context.Background(), testCred(srv.URL), provider.Request{
		Capability: provider.CapImageEdit,
		Model:      "gpt-image-1",
		Prompt:     "x",
	})
	if err == nil {
		t.Fatal("无参考图应报错")
	}
	pe, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("类型错误: %T", err)
	}
	if pe.Code != platform.CodeInvalidRequest {
		t.Fatalf("code=%s", pe.Code)
	}
	if pe.Retryable() {
		t.Fatal("参数错误不应重试")
	}
}

func TestTextGenerationContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "responses.json"))
	}))
	defer srv.Close()

	res, err := newAdapter().Invoke(context.Background(), testCred(srv.URL), provider.Request{
		Capability: provider.CapTextGenerate,
		Model:      "gpt-4o-mini",
		Prompt:     "打个招呼",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.Text != "你好，世界" {
		t.Fatalf("文本解析错误: %q", res.Text)
	}
	if res.Usage.TextTokensIn != 10 || res.Usage.TextTokensOut != 4 {
		t.Fatalf("计量错误: %+v", res.Usage)
	}
}

func TestVideoCreateAndPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			_, _ = w.Write(readSample(t, "videos_create.json"))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/videos/"):
			_, _ = w.Write(readSample(t, "videos_poll.json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := newAdapter()
	creds := testCred(srv.URL)
	res, err := a.Invoke(context.Background(), creds, provider.Request{
		Capability: provider.CapVideoGenerate,
		Model:      "sora-2",
		Prompt:     "海边日落",
		Params:     map[string]any{"seconds": "8", "size": "1280x720"},
	})
	if err != nil {
		t.Fatalf("video create: %v", err)
	}
	if res.RemoteTask == nil || res.RemoteTask.ID != "video_abc" {
		t.Fatalf("未返回可续查的任务 ID: %+v", res.RemoteTask)
	}
	task, err := a.Poll(context.Background(), creds, res.RemoteTask.ID)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if task.Status != "succeeded" || task.Progress != 100 {
		t.Fatalf("轮询结果错误: %+v", task)
	}
}

func TestContentPolicyClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(readSample(t, "error_content_policy.json"))
	}))
	defer srv.Close()

	_, err := newAdapter().Invoke(context.Background(), testCred(srv.URL), provider.Request{
		Capability: provider.CapImageGenerate, Model: "gpt-image-1", Prompt: "x",
	})
	if err == nil {
		t.Fatal("应报错")
	}
	pe, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("类型错误: %T", err)
	}
	if pe.Class != provider.ClassContentPolicy {
		t.Fatalf("class=%s 期望 content_policy", pe.Class)
	}
	if pe.Retryable() {
		t.Fatal("内容审核拒绝不应重试")
	}
}

func TestListModelsContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "models.json"))
	}))
	defer srv.Close()

	models, err := newAdapter().ListModels(context.Background(), testCred(srv.URL))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 5 {
		t.Fatalf("模型数=%d", len(models))
	}
	byID := map[string][]provider.Capability{}
	for _, m := range models {
		byID[m.ID] = m.Capabilities
	}
	if !hasCap(byID["gpt-image-1"], provider.CapImageGenerate) {
		t.Fatalf("gpt-image-1 应识别为图片生成: %v", byID["gpt-image-1"])
	}
	if !hasCap(byID["sora-2"], provider.CapVideoGenerate) {
		t.Fatalf("sora-2 应识别为视频生成: %v", byID["sora-2"])
	}
	if !hasCap(byID["gpt-4o-mini-tts"], provider.CapAudioGenerate) {
		t.Fatalf("tts 模型应识别为音频: %v", byID["gpt-4o-mini-tts"])
	}
}

func TestGuessCapabilities(t *testing.T) {
	cases := []struct {
		model string
		want  provider.Capability
	}{
		{"dall-e-3", provider.CapImageGenerate},
		{"gpt-image-1", provider.CapImageGenerate},
		{"flux-pro", provider.CapImageGenerate},
		{"stable-diffusion-xl", provider.CapImageGenerate},
		{"sora-2", provider.CapVideoGenerate},
		{"kling-v1", provider.CapVideoGenerate},
		{"tts-1", provider.CapAudioGenerate},
		{"gpt-4o", provider.CapTextGenerate},
		{"unknown-model-xyz", provider.CapTextGenerate},
	}
	for _, c := range cases {
		if !hasCap(GuessCapabilities(c.model), c.want) {
			t.Fatalf("%s 应包含 %s，实际 %v", c.model, c.want, GuessCapabilities(c.model))
		}
	}
}

func TestPromptCompositionNumbering(t *testing.T) {
	// 对齐原项目：上游文本按「文本N」分块编号，参考素材按类型编号，顺序由端口 order 决定
	req := provider.Request{
		Prompt: "画一张图",
		Inputs: []provider.ResolvedInput{
			{Kind: "text", Value: "风格：赛博朋克"},
			{Kind: "text", Value: "比例：16:9"},
			{Kind: "image", Label: "图片1"},
		},
	}
	got := ComposePrompt(req)
	if !strings.Contains(got, "文本1：风格：赛博朋克") || !strings.Contains(got, "文本2：比例：16:9") {
		t.Fatalf("文本编号错误: %s", got)
	}
	if !strings.Contains(got, "参考素材编号：图片1") {
		t.Fatalf("参考编号错误: %s", got)
	}
}

// TestUnsupportedCapability 覆盖「适配器不认识的能力」这条路径。
//
// 上一版这里用 CapImageUpscale 当例子，但 5.5 补齐后 upscale 已经是支持的能力，
// 于是这条用例变成在验「upscale 需要源图」——那是另一件事。
// 现在改成构造一个确实不在能力表里的值，用例名与断言才重新对得上。
func TestUnsupportedCapability(t *testing.T) {
	_, err := newAdapter().Invoke(context.Background(), testCred("http://example.com"), provider.Request{
		Capability: provider.Capability("image.unknown"),
	})
	if err == nil {
		t.Fatal("不支持的能力应报错")
	}
	pe, ok := err.(*provider.ProviderError)
	if !ok || pe.Code != "unsupported_capability" {
		t.Fatalf("err=%v", err)
	}
}

// TestUpscaleRequiresSourceImage：超分没有源图时必须**在发请求之前**失败，
// 否则用户会看到一个从上游返回的、与本地上传无关的错误。
func TestUpscaleRequiresSourceImage(t *testing.T) {
	_, err := newAdapter().Invoke(context.Background(), testCred("http://example.com"), provider.Request{
		Capability: provider.CapImageUpscale,
	})
	pe, ok := err.(*provider.ProviderError)
	if !ok || pe.Code != platform.CodeInvalidRequest {
		t.Fatalf("err=%v", err)
	}
}

// TestUpscaleSendsReferenceImage：超分必须真的把源图放进 multipart，
// 而不是「返回一个空的成功」。
func TestUpscaleSendsReferenceImage(t *testing.T) {
	var body string
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()

	res, err := newAdapter().Invoke(context.Background(), testCred(srv.URL), provider.Request{
		Capability: provider.CapImageUpscale,
		Model:      "my-superres-model",
		Count:      1,
		Inputs: []provider.ResolvedInput{
			{Kind: "image", AssetID: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNG")), Label: "图片1"},
		},
		Params: map[string]any{"scale": float64(4)},
	})
	if err != nil {
		t.Fatalf("超分调用失败: %v", err)
	}
	if !strings.Contains(contentType, "multipart/form-data") {
		t.Fatalf("应当是 multipart 请求: %s", contentType)
	}
	// multipart 里的文件名必须带序号，否则「图片2」在日志里无法定位
	if !strings.Contains(body, "ref1.png") {
		t.Fatalf("参考图未按序号上传: %s", body[:min(400, len(body))])
	}
	if len(res.Assets) != 1 {
		t.Fatalf("结果解析失败: %#v", res.Assets)
	}
}

// TestImageEditUploadsAllReferencesInOrder：蒙版双参考（5.1）依赖
// 「上传顺序 = 语义顺序」。一旦顺序被打乱，症状是「蒙版被当成原图」，
// 而界面上参数全部正常。
func TestImageEditUploadsAllReferencesInOrder(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()

	_, err := newAdapter().Invoke(context.Background(), testCred(srv.URL), provider.Request{
		Capability: provider.CapImageEdit,
		Model:      "edit-model",
		Prompt:     "只改遮罩区域",
		Count:      1,
		Inputs: []provider.ResolvedInput{
			{Kind: "image", AssetID: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("ORIGINAL")), Label: "图片1"},
			{Kind: "image", AssetID: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("MASK")), Label: "图片2"},
		},
	})
	if err != nil {
		t.Fatalf("编辑调用失败: %v", err)
	}
	i1 := strings.Index(body, "ref1.png")
	i2 := strings.Index(body, "ref2.png")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("参考图顺序错误（图片1 必须在图片2 之前）: %d %d", i1, i2)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestAdapterCapabilitiesCoverMatrix(t *testing.T) {
	caps := newAdapter().Capabilities()
	// docs/design/10-parity-matrix.md §4.1–4.5 覆盖的能力
	for _, want := range []provider.Capability{
		provider.CapImageGenerate, provider.CapImageEdit, provider.CapTextGenerate,
		provider.CapVideoGenerate, provider.CapAudioGenerate, provider.CapModelList,
	} {
		if !hasCap(caps, want) {
			t.Fatalf("openai 适配器缺少能力 %s", want)
		}
	}
}

func hasCap(caps []provider.Capability, want provider.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
