package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
	"github.com/context-flow/ic/internal/provider/adapter/openai"
)

// ATK-03：Provider 超时但上游已扣费，客户端重试时必须让上游只被调用一次计费。
//
// 分两侧验证，缺一不可：
//  1. 服务端侧（本测试）：同一 request_id 的重复 attempt 不会在上游产生两次计费请求
//     —— 由「重试沿用同一 request_id + 上游幂等头」保证；
//  2. 落库侧（internal/exec）：run_attempts.request_id 唯一索引，
//     见 TestATK03RequestIDUniqueInStore。
func TestATK03UpstreamCalledOncePerRequestID(t *testing.T) {
	var (
		mu       sync.Mutex
		seenKeys = map[string]int{}
		calls    int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		key := r.Header.Get("Idempotency-Key")
		if key != "" {
			seenKeys[key]++
		}
		n := len(seenKeys)
		mu.Unlock()

		// 第一次调用「超时但已扣费」：返回 500，客户端会重试。
		if n <= 1 && calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream timeout after charge"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()

	guard := platform.NewNetGuard()
	guard.AllowPrivate = true // 测试服务器在回环地址上
	client := platform.DefaultHTTPClient(guard)
	transport := provider.NewTransport(client, guard, platform.SystemClock())
	transport.MaxAttempts = 3
	transport.BaseBackoff = time.Millisecond
	adapter := openai.New(transport)

	cred := provider.Credential{
		ID: "cred_1", ProviderID: "openai", BaseURL: srv.URL, AuthKind: "bearer", Secret: "sk-test",
	}
	req := provider.Request{
		Capability: provider.CapImageGenerate,
		Model:      "gpt-image-1",
		Prompt:     "a cat",
		Count:      1,
		// 关键：同一 request_id 贯穿全部重试
		RequestID: "run_1:step_1",
	}
	if _, err := adapter.Invoke(context.Background(), cred, req); err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("应至少发生一次重试，实际调用 %d 次", calls)
	}
	// 幂等头必须每次都带，且完全相同——否则上游无法识别为同一请求
	if len(seenKeys) != 1 {
		t.Fatalf("重试期间 Idempotency-Key 应保持一致，实际观测到 %d 个不同值: %v", len(seenKeys), seenKeys)
	}
	if seenKeys["run_1:step_1"] != calls {
		t.Fatalf("每次调用都应携带同一幂等键：键计数 %d，调用数 %d", seenKeys["run_1:step_1"], calls)
	}
}

// ATK-03 补充：鉴权头与幂等头必须共存（不能因为加幂等头把鉴权覆盖掉）。
func TestATK03RequestHeadersKeepAuthAndIdempotency(t *testing.T) {
	cred := provider.Credential{ProviderID: "openai", BaseURL: "https://api.openai.com", AuthKind: "bearer", Secret: "sk-abc"}
	h := provider.RequestHeaders(cred, "openai", "run_9:step_9")
	if h["Authorization"] != "Bearer sk-abc" {
		t.Fatalf("鉴权头丢失: %v", h)
	}
	if h["Idempotency-Key"] != "run_9:step_9" {
		t.Fatalf("幂等头缺失: %v", h)
	}

	// Gemini 不发 Idempotency-Key（部分网关会因未知头直接 4xx），只带追踪头。
	gh := provider.RequestHeaders(cred, "gemini", "run_9:step_9")
	if _, ok := gh["Idempotency-Key"]; ok {
		t.Fatalf("Gemini 不应发送 Idempotency-Key: %v", gh)
	}
	if gh["X-Request-Id"] != "run_9:step_9" {
		t.Fatalf("Gemini 应带追踪头: %v", gh)
	}
	// 空 request_id 时不发任何幂等头（避免发出无意义的值）
	eh := provider.RequestHeaders(cred, "openai", "")
	if _, ok := eh["Idempotency-Key"]; ok {
		t.Fatalf("空 request_id 不应发送幂等头: %v", eh)
	}
}

// 指纹不含 secret：指纹会进日志，secret 进日志即违规（INV-5）。
func TestRequestFingerprintNeverContainsSecret(t *testing.T) {
	fp := provider.RequestFingerprint(provider.CapImageGenerate, "m", "p", map[string]any{"size": "1024x1024"})
	if len(fp) == 0 {
		t.Fatal("指纹不应为空")
	}
	if fp == provider.RequestFingerprint(provider.CapImageGenerate, "m", "p", map[string]any{"size": "512x512"}) {
		t.Fatal("不同参数应产生不同指纹")
	}
}

// 上游不认识幂等头时的行为：只要不因此报错即可（这是多数中转站的真实情况）。
func TestATK03UnknownIdempotencyHeaderIsHarmless(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟「忽略未知头」的网关
		b, _ := io.ReadAll(r.Body)
		got = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()

	guard := platform.NewNetGuard()
	guard.AllowPrivate = true
	adapter := openai.New(provider.NewTransport(platform.DefaultHTTPClient(guard), guard, platform.SystemClock()))
	cred := provider.Credential{ProviderID: "openai", BaseURL: srv.URL, AuthKind: "bearer", Secret: "sk"}
	if _, err := adapter.Invoke(context.Background(), cred, provider.Request{
		Capability: provider.CapImageGenerate, Model: "m", Prompt: "p", Count: 1, RequestID: "r:s",
	}); err != nil {
		t.Fatalf("上游忽略幂等头时不应失败: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if body["prompt"] != "p" {
		t.Fatalf("请求体内容不符: %v", body)
	}
}
