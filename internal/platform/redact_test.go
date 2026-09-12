package platform

import (
	"strings"
	"testing"
)

// ATK-06：日志中打入 Authorization: Bearer sk-x，日志中必须为 ***。
// ATK-06：日志中打入 Authorization: Bearer sk-x，落盘内容必须是掩码。
// 判据不止「含 ***」——还必须确认原文一字不剩，否则等于没脱敏。
func TestATK06LoggerNeverPersistsCredential(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // 不得出现的子串
	}{
		{"bearer", `Authorization: Bearer sk-abc123456789xyz`, []string{"sk-abc123456789xyz"}},
		{"api key json", `{"api_key":"sk-live-0123456789abcdef"}`, []string{"sk-live-0123456789abcdef"}},
		{"x-api-key header", `X-Api-Key: AIzaSyA1234567890abcdefghijklmnopq`, []string{"AIzaSyA1234567890abcdefghijklmnopq"}},
		{"data url", `payload=data:image/png;base64,` + strings.Repeat("A", 64), []string{strings.Repeat("A", 64)}},
		{"query token", `GET /v1/x?api_key=sk-secret-123456789`, []string{"sk-secret-123456789"}},
		{"secret field", `secret="abcdef123456"`, []string{`"abcdef123456"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in)
			for _, w := range c.want {
				if strings.Contains(got, w) {
					t.Fatalf("脱敏失败，仍包含 %q：%s", w, got)
				}
			}
		})
	}
}

func TestMask(t *testing.T) {
	if got := Mask("sk-1234567890"); got != "****7890" {
		t.Fatalf("Mask = %q", got)
	}
	if got := Mask("abc"); got != "****" {
		t.Fatalf("Mask short = %q", got)
	}
	if got := Mask(""); got != "" {
		t.Fatalf("Mask empty = %q", got)
	}
}

func TestRedactHeaders(t *testing.T) {
	h := map[string][]string{
		"Authorization": {"Bearer sk-aaaaaaaaaa"},
		"Cookie":        {"session=zzzz"},
		"X-Trace-Id":    {"abc"},
	}
	out := RedactHeaders(h)
	if out["Authorization"][0] != "***" || out["Cookie"][0] != "***" {
		t.Fatalf("敏感头未完全脱敏: %v", out)
	}
	if out["X-Trace-Id"][0] != "abc" {
		t.Fatalf("普通头被误改: %v", out)
	}
	if h["Authorization"][0] == "***" {
		t.Fatalf("不应修改入参")
	}
}
