package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// 幂等头（INV-3）：同一 request_id 上游只应计费一次。
//
// 背景：上游调用最常见的资损形态是「服务端超时但上游已扣费，客户端重试」。
// 我们从两侧防：服务端侧用 run_attempts.request_id 唯一索引保证只落一条记录（ATK-03），
// 上游侧则在支持幂等头的渠道上带上同一 request_id。
//
// 支持情况（不猜，按事实列举）：
//   - OpenAI：官方支持 `Idempotency-Key`（在 /v1/images/* 等 POST 上）；
//   - 多数 OpenAI 兼容中转站会忽略它——忽略是安全的（退化为普通请求）；
//   - Gemini：无官方幂等头，只有少数生成接口支持 `x-goog-request-params` 语义，
//     这里不发未知头，避免部分网关因未知头直接 4xx。
//
// 因此策略是「保守发送」：只对明确支持的协议发，且发送的头名固定，
// 不做「猜一个头名试试看」——那会让某些网关把请求判为非法。
const (
	headerIdempotencyKey = "Idempotency-Key"
	headerRequestID      = "X-Request-Id"
)

// ApplyIdempotencyHeaders 按协议给请求头补幂等相关字段。
// providerKind 取 "openai" / "gemini" / "custom"。
func ApplyIdempotencyHeaders(h http.Header, providerKind, requestID string) {
	if requestID == "" {
		return
	}
	switch strings.ToLower(providerKind) {
	case "gemini":
		// Gemini 不识别 Idempotency-Key；只带我们自己的追踪头（无害且便于排障）。
		h.Set(headerRequestID, requestID)
	default:
		h.Set(headerIdempotencyKey, requestID)
		h.Set(headerRequestID, requestID)
	}
}

// RequestFingerprint 计算一次上游请求的指纹（用于「同参数重复提交」的检测与日志比对）。
//
// 刻意不把 secret 计入：指纹会被写进日志，secret 进日志即违规（INV-5）。
func RequestFingerprint(cap Capability, model, prompt string, params map[string]any) string {
	h := sha256.New()
	h.Write([]byte(string(cap)))
	h.Write([]byte{0})
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(prompt))
	for _, k := range sortedKeys(params) {
		h.Write([]byte{0})
		h.Write([]byte(k))
		h.Write([]byte("="))
		h.Write([]byte(stringify(params[k])))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return itoa(int64(t))
	case int64:
		return itoa(t)
	case float64:
		return itoa(int64(t))
	default:
		return "?"
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
