package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoggerRedactsSecrets(t *testing.T) {
	// 直接验证 ReplaceAttr 行为（不依赖 stdout 捕获）。
	h := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{ReplaceAttr: redactAttr})
	l := slog.New(h)
	if l == nil {
		t.Fatal("logger is nil")
	}
	attrs := redactAttr(nil, slog.String("authorization", "Bearer sk-abcdef123456"))
	if strings.Contains(attrs.Value.String(), "sk-abcdef123456") {
		t.Fatal("authorization 未脱敏")
	}
	attrs2 := redactAttr(nil, slog.String("note", "api_key=sk-abcdef123456"))
	if strings.Contains(attrs2.Value.String(), "sk-abcdef123456") {
		t.Fatal("内容中的 key 未脱敏")
	}
}

func TestClockFake(t *testing.T) {
	c := NewFakeClock(mustTime("2026-01-01T00:00:00Z"))
	start := c.Now()
	c.Advance(90 * 1000 * 1000 * 1000)
	if c.Since(start).Seconds() != 90 {
		t.Fatalf("advance 未生效: %v", c.Since(start))
	}
	// 模拟时钟回拨
	c.Set(mustTime("2025-01-01T00:00:00Z"))
	if c.Since(start) >= 0 {
		t.Fatal("时钟回拨未生效")
	}
}

func mustTime(s string) time.Time {
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return tt
}
