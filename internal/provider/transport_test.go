package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

func newTestTransport() *Transport {
	guard := &platform.NetGuard{AllowPrivate: true} // 测试目标是回环地址
	client := platform.NewGuardedClient(guard, &http.Client{Timeout: 5 * time.Second}, 5*time.Second)
	return NewTransport(client, guard, platform.SystemClock())
}

func TestTransportRetriesTransient(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer srv.Close()

	tr := newTestTransport()
	var out map[string]string
	if err := tr.DoJSON(context.Background(), http.MethodGet, srv.URL, nil, nil, &out); err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if out["ok"] != "yes" {
		t.Fatalf("out=%v", out)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("调用次数应为 3，实际 %d", calls)
	}
}

func TestTransportDoesNotRetryPermanent(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	tr := newTestTransport()
	err := tr.DoJSON(context.Background(), http.MethodGet, srv.URL, nil, nil, nil)
	if err == nil {
		t.Fatal("401 应报错")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("错误类型不对: %T", err)
	}
	if pe.Class != ClassPermanent {
		t.Fatalf("class=%s 期望 permanent", pe.Class)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("永久错误不应重试，实际调用 %d 次", calls)
	}
}

// ATK-03：上游 429 且带 Retry-After 时按响应头退避。
func TestTransportHonorsRetryAfter(t *testing.T) {
	var calls int32
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "y"})
	}))
	defer srv.Close()

	tr := newTestTransport()
	var out map[string]string
	if err := tr.DoJSON(context.Background(), http.MethodGet, srv.URL, nil, nil, &out); err != nil {
		t.Fatalf("应重试成功: %v", err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("未遵守 Retry-After，仅等待 %v", time.Since(start))
	}
}

// 内容审核拒绝必须单独分类，UI 才能给出正确提示。
func TestClassifyContentPolicy(t *testing.T) {
	pe := ClassifyHTTP(400, `{"error":{"message":"Your request was rejected by our content policy"}}`)
	if pe.Class != ClassContentPolicy {
		t.Fatalf("class=%s", pe.Class)
	}
	if pe.Code != platform.CodeContentPolicy {
		t.Fatalf("code=%s", pe.Code)
	}
}

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		status int
		want   ErrorClass
	}{
		{408, ClassTransient},
		{429, ClassRateLimited},
		{500, ClassTransient},
		{502, ClassTransient},
		{503, ClassTransient},
		{401, ClassPermanent},
		{403, ClassPermanent},
		{404, ClassPermanent},
		{422, ClassPermanent},
	}
	for _, c := range cases {
		if got := ClassifyHTTP(c.status, "").Class; got != c.want {
			t.Fatalf("status=%d class=%s 期望 %s", c.status, got, c.want)
		}
	}
}

// 上游返回 HTML 错误页时不得把整段 HTML 透给用户。
func TestClassifyHidesHTMLErrorPage(t *testing.T) {
	body := "<html><body><h1>502 Bad Gateway</h1><script>alert(1)</script></body></html>"
	pe := ClassifyHTTP(502, body)
	if strings.Contains(pe.Message, "<script>") {
		t.Fatalf("不应透出原始 HTML: %s", pe.Message)
	}
	if !strings.Contains(pe.Message, "HTML") {
		t.Fatalf("应提示为 HTML 错误页: %s", pe.Message)
	}
}

// 凭据不得出现在错误信息里。
func TestClassifyRedactsSecret(t *testing.T) {
	pe := ClassifyHTTP(400, `{"error":"bad key sk-abcdef1234567890abcdef"}`)
	if strings.Contains(pe.Message, "sk-abcdef1234567890abcdef") {
		t.Fatalf("错误信息泄露密钥: %s", pe.Message)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := ParseRetryAfter("2"); d != 2*time.Second {
		t.Fatalf("d=%v", d)
	}
	if d := ParseRetryAfter(""); d != 0 {
		t.Fatalf("d=%v", d)
	}
	if d := ParseRetryAfter("garbage"); d != 0 {
		t.Fatalf("d=%v", d)
	}
}

func TestAuthHeaders(t *testing.T) {
	if h := AuthHeaders(Credential{AuthKind: "bearer", Secret: "s"}); h["Authorization"] != "Bearer s" {
		t.Fatalf("bearer=%v", h)
	}
	if h := AuthHeaders(Credential{AuthKind: "x-api-key", Secret: "s"}); h["X-Api-Key"] != "s" {
		t.Fatalf("x-api-key=%v", h)
	}
	if h := AuthHeaders(Credential{AuthKind: "none", Secret: "s"}); len(h) != 0 {
		t.Fatalf("none=%v", h)
	}
}

// ATK-04 复核：transport 必须拒绝私网目标（除非显式放开）。
func TestTransportBlocksSSRF(t *testing.T) {
	guard := platform.NewNetGuard()
	client := platform.NewGuardedClient(guard, &http.Client{Timeout: time.Second}, time.Second)
	tr := NewTransport(client, guard, platform.SystemClock())
	err := tr.DoJSON(context.Background(), http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil, nil, nil)
	if err == nil {
		t.Fatal("元数据地址应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeSSRFBlocked {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestUsageAddIsInteger(t *testing.T) {
	a := Usage{CostMicros: 1}
	b := Usage{CostMicros: 2}
	for i := 0; i < 1000; i++ {
		a = a.Add(b)
	}
	if a.CostMicros != 2001 {
		t.Fatalf("微元累加应精确，实际 %d", a.CostMicros)
	}
}
