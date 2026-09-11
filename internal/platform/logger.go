package platform

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger 构造结构化日志器，并注册全局脱敏 handler（INV-5）。
func NewLogger(level, format string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv, ReplaceAttr: redactAttr}
	var h slog.Handler
	if strings.ToLower(format) == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

// redactAttr 对日志字段做统一脱敏，防止凭据进日志。
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case "authorization", "api_key", "apikey", "x-api-key", "token", "access_token", "secret", "password", "cookie":
		return slog.String(a.Key, "***")
	}
	if a.Value.Kind() == slog.KindString {
		s := a.Value.String()
		if r := Redact(s); r != s {
			return slog.String(a.Key, r)
		}
	}
	return a
}
