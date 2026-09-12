package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// Transport 是上游 HTTP 调用的统一出口：超时、重试、脱敏、SSRF 防护、幂等头。
type Transport struct {
	client platform.HTTPDoer
	guard  *platform.NetGuard
	clock  platform.Clock
	// MaxAttempts 是传输层重试次数（业务层还有自己的重试策略）。
	MaxAttempts int
	// BaseBackoff 指数退避基数。
	BaseBackoff time.Duration
}

// NewTransport 构造传输层。
func NewTransport(client platform.HTTPDoer, guard *platform.NetGuard, clock platform.Clock) *Transport {
	if clock == nil {
		clock = platform.SystemClock()
	}
	if guard == nil {
		guard = platform.NewNetGuard()
	}
	return &Transport{client: client, guard: guard, clock: clock, MaxAttempts: 3, BaseBackoff: 400 * time.Millisecond}
}

// DoJSON 发起 JSON 请求并解析响应，错误已按类分类。
func (t *Transport) DoJSON(ctx context.Context, method, url string, headers map[string]string,
	body any, out any) error {

	if err := t.guard.CheckURL(ctx, url); err != nil {
		return err
	}
	var payload []byte
	var contentType string
	switch b := body.(type) {
	case nil:
	case rawPayload:
		payload = b.bytes()
		contentType = b.contentType()
	default:
		enc, err := json.Marshal(body)
		if err != nil {
			return platform.AsError(err)
		}
		payload = enc
		contentType = "application/json"
	}

	var lastErr error
	for attempt := 0; attempt < t.MaxAttempts; attempt++ {
		if attempt > 0 {
			backoff := t.BaseBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return ClassifyTransport(ctx.Err())
			case <-time.After(backoff):
			}
		}
		err := t.once(ctx, method, url, headers, payload, contentType, out)
		if err == nil {
			return nil
		}
		lastErr = err
		var pe *ProviderError
		if !asProviderError(err, &pe) || !pe.Retryable() {
			return err
		}
		if pe.RetryAfter > 0 {
			select {
			case <-ctx.Done():
				return ClassifyTransport(ctx.Err())
			case <-time.After(pe.RetryAfter):
			}
		}
	}
	return lastErr
}

func (t *Transport) once(ctx context.Context, method, url string, headers map[string]string, payload []byte, contentType string, out any) error {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return platform.AsError(err)
	}
	if payload != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := t.client.Do(req)
	if err != nil {
		return ClassifyTransport(err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		pe := ClassifyHTTP(res.StatusCode, string(body))
		pe.RetryAfter = ParseRetryAfter(res.Header.Get("Retry-After"))
		return pe
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return ClassifyTransport(err)
	}
	return nil
}

// RawBodyType 让适配器可以传非 JSON 请求体（multipart 等）。
type RawBodyType interface {
	bytes() []byte
	contentType() string
}

type rawPayload = RawBodyType

// DoRawWithBody 发起带请求体的原始请求，返回响应流。
func (t *Transport) DoRawWithBody(ctx context.Context, method, url string, headers map[string]string, payload []byte) (io.ReadCloser, string, error) {
	if err := t.guard.CheckURL(ctx, url); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return nil, "", ClassifyTransport(err)
	}
	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		return nil, "", ClassifyHTTP(res.StatusCode, string(body))
	}
	return res.Body, res.Header.Get("Content-Type"), nil
}

// RawBody 供适配器构造非 JSON 请求体。
func RawBody(payload []byte, contentType string) RawBodyType {
	return rawBodyPayload{payload: payload, ct: contentType}
}

type rawBodyPayload struct {
	payload []byte
	ct      string
}

func (r rawBodyPayload) bytes() []byte       { return r.payload }
func (r rawBodyPayload) contentType() string { return r.ct }

// DoRaw 发起请求并返回原始响应体（用于下载二进制资产）。
func (t *Transport) DoRaw(ctx context.Context, method, url string, headers map[string]string) (io.ReadCloser, string, error) {
	if err := t.guard.CheckURL(ctx, url); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return nil, "", ClassifyTransport(err)
	}
	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		return nil, "", ClassifyHTTP(res.StatusCode, string(body))
	}
	return res.Body, res.Header.Get("Content-Type"), nil
}

// RequestHeaders 生成一次调用的完整请求头：鉴权 + 幂等 + 追踪。
//
// 统一出口的意义：幂等头如果只在某几个适配器里加，就会出现「图片生成有幂等、
// 视频生成没有」的不一致，而视频恰好是最贵、最容易重复计费的能力（INV-3）。
// 因此把构造收敛到这里，所有适配器调用时都会带上。
func RequestHeaders(cred Credential, providerKind, requestID string) map[string]string {
	h := AuthHeaders(cred)
	header := http.Header{}
	for k, v := range h {
		header.Set(k, v)
	}
	ApplyIdempotencyHeaders(header, providerKind, requestID)
	out := make(map[string]string, len(header))
	for k := range header {
		out[k] = header.Get(k)
	}
	return out
}

func providerKindOf(cred Credential) string {
	if cred.AuthKind == "gemini" || cred.AuthKind == "x-goog-api-key" {
		return "gemini"
	}
	return "openai"
}

// AuthHeaders 按凭据类型生成鉴权头。
func AuthHeaders(cred Credential) map[string]string {
	h := map[string]string{}
	switch strings.ToLower(cred.AuthKind) {
	case "bearer", "":
		h["Authorization"] = "Bearer " + cred.Secret
	case "x-api-key":
		h["X-Api-Key"] = cred.Secret
	case "api-key":
		h["api-key"] = cred.Secret
	case "x-goog-api-key":
		h["x-goog-api-key"] = cred.Secret
	case "none":
		// 自定义中转站可能不需要鉴权
	}
	return h
}

// TrimBaseURL 去掉末尾斜杠，拼接时统一。
func TrimBaseURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

func asProviderError(err error, out **ProviderError) bool {
	if pe, ok := err.(*ProviderError); ok {
		*out = pe
		return true
	}
	return false
}
