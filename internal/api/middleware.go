package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get("X-Trace-Id")
		if tid == "" {
			tid, _ = platform.RandomToken(8)
		}
		w.Header().Set("X-Trace-Id", tid)
		next.ServeHTTP(w, r.WithContext(platform.WithTraceID(r.Context(), tid)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush 支持 SSE。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func loggingMiddleware(next http.Handler, d Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if d.Logger == nil {
			return
		}
		lvl := slog.LevelInfo
		if sw.status >= 500 {
			lvl = slog.LevelError
		} else if sw.status >= 400 {
			lvl = slog.LevelWarn
		}
		d.Logger.Log(r.Context(), lvl, "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"ms", time.Since(start).Milliseconds(),
			"trace_id", platform.TraceID(r.Context()),
			"ip", clientIP(r),
		)
	})
}

func recoverMiddleware(next http.Handler, d Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if d.Logger != nil {
					d.Logger.Error("panic",
						"err", fmt.Sprintf("%v", rec),
						"stack", platform.Redact(string(debug.Stack())),
						"path", r.URL.Path,
						"trace_id", platform.TraceID(r.Context()),
					)
				}
				writeError(w, r, platform.NewError(http.StatusInternalServerError, platform.CodeInternal, "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
