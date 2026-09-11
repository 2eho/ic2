package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

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

func cred(url string) provider.Credential {
	return provider.Credential{ID: "c1", ProviderID: "gemini", BaseURL: url, AuthKind: "x-goog-api-key", Secret: "AIza-test"}
}

func TestImageGenerationContract(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "generate_content_image.json"))
	}))
	defer srv.Close()

	res, err := newAdapter().Invoke(context.Background(), cred(srv.URL), provider.Request{
		Capability: provider.CapImageGenerate,
		Model:      "gemini-2.5-flash-image",
		Prompt:     "一只猫",
		Params:     map[string]any{"aspectRatio": "16:9", "imageSize": "2K"},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if gotPath != "/v1beta/models/gemini-2.5-flash-image:generateContent" {
		t.Fatalf("路径错误: %s", gotPath)
	}
	gc, _ := gotBody["generationConfig"].(map[string]any)
	if gc == nil {
		t.Fatalf("缺少 generationConfig: %v", gotBody)
	}
	mods, _ := gc["responseModalities"].([]any)
	if len(mods) != 2 || mods[0] != "TEXT" || mods[1] != "IMAGE" {
		t.Fatalf("responseModalities 错误: %v", mods)
	}
	ic, _ := gc["imageConfig"].(map[string]any)
	if ic["aspectRatio"] != "16:9" || ic["imageSize"] != "2K" {
		t.Fatalf("imageConfig 错误: %v", ic)
	}
	if len(res.Assets) != 1 || len(res.Assets[0].Bytes) == 0 {
		t.Fatalf("未解析出图片: %+v", res.Assets)
	}
	if res.Text != "这是生成的图片" {
		t.Fatalf("文本解析错误: %q", res.Text)
	}
	if res.Usage.TextTokensIn != 7 || res.Usage.Images != 1 {
		t.Fatalf("计量错误: %+v", res.Usage)
	}
}

// 内容审核拒绝必须映射为 content_policy 且不重试。
func TestBlockedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "generate_content_blocked.json"))
	}))
	defer srv.Close()

	_, err := newAdapter().Invoke(context.Background(), cred(srv.URL), provider.Request{
		Capability: provider.CapImageGenerate, Model: "m", Prompt: "x",
	})
	if err == nil {
		t.Fatal("被拦截应报错")
	}
	pe, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("类型错误: %T", err)
	}
	if pe.Class != provider.ClassContentPolicy || pe.Code != platform.CodeContentPolicy {
		t.Fatalf("class=%s code=%s", pe.Class, pe.Code)
	}
}

func TestVideoPredictLongRunningAndPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, ":predictLongRunning"):
			_, _ = w.Write(readSample(t, "predict_long_running.json"))
		case strings.Contains(r.URL.Path, "operations/"):
			_, _ = w.Write(readSample(t, "operation_done.json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := newAdapter()
	c := cred(srv.URL)
	res, err := a.Invoke(context.Background(), c, provider.Request{
		Capability: provider.CapVideoGenerate, Model: "veo-3.0-generate-preview", Prompt: "海边",
		Params: map[string]any{"aspectRatio": "16:9", "seconds": "8", "resolution": "1080p"},
	})
	if err != nil {
		t.Fatalf("video create: %v", err)
	}
	if res.RemoteTask == nil || !strings.Contains(res.RemoteTask.ID, "operations/") {
		t.Fatalf("任务名必须可持久化以支持重启续查: %+v", res.RemoteTask)
	}
	task, err := a.Poll(context.Background(), c, res.RemoteTask.ID)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if task.Status != "succeeded" || task.Progress != 100 {
		t.Fatalf("轮询结果错误: %+v", task)
	}
}

func TestRateLimitedClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write(readSample(t, "error_rate_limited.json"))
	}))
	defer srv.Close()

	_, err := newAdapter().Invoke(context.Background(), cred(srv.URL), provider.Request{
		Capability: provider.CapTextGenerate, Model: "m", Prompt: "x",
	})
	if err == nil {
		t.Fatal("应报错")
	}
	pe, ok := err.(*provider.ProviderError)
	if !ok {
		t.Fatalf("类型错误: %T", err)
	}
	if pe.Class != provider.ClassRateLimited || !pe.Retryable() {
		t.Fatalf("class=%s retryable=%v", pe.Class, pe.Retryable())
	}
}

func TestListModelsContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "models.json"))
	}))
	defer srv.Close()

	models, err := newAdapter().ListModels(context.Background(), cred(srv.URL))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 4 {
		t.Fatalf("模型数=%d", len(models))
	}
	byID := map[string][]provider.Capability{}
	for _, m := range models {
		byID[m.ID] = m.Capabilities
	}
	if !hasCap(byID["veo-3.0-generate-preview"], provider.CapVideoGenerate) {
		t.Fatalf("veo 应识别为视频: %v", byID["veo-3.0-generate-preview"])
	}
	if !hasCap(byID["imagen-3.0-generate-002"], provider.CapImageGenerate) {
		t.Fatalf("imagen 应识别为图片: %v", byID["imagen-3.0-generate-002"])
	}
	if !hasCap(byID["gemini-2.5-flash-preview-tts"], provider.CapAudioGenerate) {
		t.Fatalf("tts 应识别为音频: %v", byID["gemini-2.5-flash-preview-tts"])
	}
}

func TestTTSConfig(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readSample(t, "generate_content_image.json"))
	}))
	defer srv.Close()

	_, err := newAdapter().Invoke(context.Background(), cred(srv.URL), provider.Request{
		Capability: provider.CapAudioGenerate, Model: "gemini-2.5-flash-preview-tts",
		Prompt: "说一句话", Params: map[string]any{"audioVoice": "Puck"},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	gc, _ := gotBody["generationConfig"].(map[string]any)
	mods, _ := gc["responseModalities"].([]any)
	if len(mods) != 1 || mods[0] != "AUDIO" {
		t.Fatalf("responseModalities 应为 AUDIO: %v", mods)
	}
	sc, _ := gc["speechConfig"].(map[string]any)
	if sc == nil {
		t.Fatalf("缺少 speechConfig: %v", gc)
	}
	vc, _ := sc["voiceConfig"].(map[string]any)
	pv, _ := vc["prebuiltVoiceConfig"].(map[string]any)
	if pv["voiceName"] != "Puck" {
		t.Fatalf("voiceName 错误: %v", pv)
	}
}

func TestAdapterCapabilities(t *testing.T) {
	caps := newAdapter().Capabilities()
	for _, want := range []provider.Capability{
		provider.CapImageGenerate, provider.CapImageEdit, provider.CapTextGenerate,
		provider.CapVideoGenerate, provider.CapAudioGenerate, provider.CapModelList,
	} {
		if !hasCap(caps, want) {
			t.Fatalf("gemini 适配器缺少能力 %s", want)
		}
	}
}

func TestTLSRedirectBlockedToPrivate(t *testing.T) {
	// 重定向到私网必须被拦（11 §2.6「白名单域名 302 到内网」）
	guard := platform.NewNetGuard()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("重定向目标不应被访问")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	client := platform.NewGuardedClient(guard, &http.Client{Timeout: 2 * time.Second}, 2*time.Second)
	tr := provider.NewTransport(client, guard, platform.SystemClock())
	if err := tr.DoJSON(context.Background(), http.MethodGet, redirector.URL, nil, nil, nil); err == nil {
		t.Fatal("重定向到回环地址应被阻止")
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
