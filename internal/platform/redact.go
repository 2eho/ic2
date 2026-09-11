package platform

import (
	"regexp"
	"strings"
)

// Redact 统一脱敏（INV-5：凭据明文永不出现在日志/错误/响应中）。
// 覆盖：Authorization / api_key 系列头与字段、sk-* 形态密钥、data URL 正文。
var (
	reBearer      = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(bearer\s+)?[A-Za-z0-9._\-]{6,}`)
	reKeyValue    = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|x-api-key|access[_-]?token|secret|client[_-]?secret|password)\b(\s*[:=]\s*)"?([^"\s,}]{4,})"?`)
	reSkToken     = regexp.MustCompile(`\b(sk|rk|pk)-[A-Za-z0-9_\-]{8,}`)
	reGoogleKey   = regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{20,}`)
	reDataURL     = regexp.MustCompile(`data:([a-zA-Z0-9.+/-]+);base64,[A-Za-z0-9+/=]{32,}`)
	reQuerySecret = regexp.MustCompile(`(?i)([?&](?:key|api_key|token|access_token)=)[^&\s]+`)
)

// Mask 保留尾部 4 位，用于凭据回显。
func Mask(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "****"
	}
	return strings.Repeat("*", 4) + secret[len(secret)-4:]
}

// Redact 返回脱敏后的字符串。
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = reBearer.ReplaceAllString(s, "${1}***")
	s = reKeyValue.ReplaceAllString(s, "${1}${2}***")
	s = reSkToken.ReplaceAllString(s, "***")
	s = reGoogleKey.ReplaceAllString(s, "***")
	s = reQuerySecret.ReplaceAllString(s, "${1}***")
	s = reDataURL.ReplaceAllString(s, "data:${1};base64,<redacted:len>")
	return s
}

// RedactHeaders 对 HTTP 头做脱敏（复制，不修改入参）。
func RedactHeaders(h map[string][]string) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "cookie" || lk == "set-cookie" || lk == "x-api-key" || lk == "proxy-authorization" {
			out[k] = []string{"***"}
			continue
		}
		cp := make([]string, len(v))
		for i, s := range v {
			cp[i] = Redact(s)
		}
		out[k] = cp
	}
	return out
}
