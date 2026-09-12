// ic-server 是 IC 服务端入口：加载配置 → 装配服务 → 启动 HTTP/SSE → 优雅退出。
//
// 本文件刻意保持「薄」：所有装配在 internal/wiring，所有业务在各自的领域包。
// 上一轮的教训是这个文件里只装了 3 个依赖，导致其余接口全部返回 501；
// 现在装配集中在 wiring 并由 wiring/app_test.go 对真实装配结果做断言。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/wiring"
)

func main() {
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ic-server %s (%s) built %s\n", platform.Version, platform.Commit, platform.Date)
		return
	}

	cfg, err := platform.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		os.Exit(2)
	}
	logger := platform.NewLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	// 启动前检查数据目录可写：等到第一次上传才发现「目录不存在」太晚了。
	if cfg.DBDriver == "sqlite" {
		if err := ensureWritableDir(dirOf(cfg.DBDSNPath())); err != nil {
			logger.Error("数据目录不可写", "path", cfg.DBDSNPath(), "err", platform.Redact(err.Error()))
			os.Exit(1)
		}
	}
	if cfg.BlobDriver == "fs" {
		if err := ensureWritableDir(cfg.BlobFSRoot); err != nil {
			logger.Error("Blob 目录不可写", "path", cfg.BlobFSRoot, "err", platform.Redact(err.Error()))
			os.Exit(1)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := wiring.Build(ctx, wiring.Options{
		Config: cfg, Logger: logger, Migrate: true, StartWorker: true,
	})
	if err != nil {
		logger.Error("启动失败", "err", platform.Redact(err.Error()))
		os.Exit(1)
	}
	defer func() { _ = app.Close() }()

	if cfg.IsDevKey() {
		logger.Warn("使用不安全的开发密钥，切勿用于生产", "env", "IC_ALLOW_INSECURE_DEV_KEY")
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.Router,
		ReadHeaderTimeout: 10 * time.Second,
		// 不设 WriteTimeout：SSE 是长连接，写超时会把它们掐断。
		// 连接级的保护由 IdleTimeout + SSE 自身的心跳与寿命上限承担。
		IdleTimeout: 120 * time.Second,
		BaseContext: func(net.Listener) context.Context { return context.Background() },
	}
	if err := serve(ctx, srv, logger, cfg.Mode); err != nil {
		logger.Error("服务异常退出", "err", platform.Redact(err.Error()))
		os.Exit(1)
	}
	logger.Info("已退出", "version", platform.Version, "commit", platform.Commit)
}

func serve(ctx context.Context, srv *http.Server, logger *slog.Logger, mode string) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	logger.Info("ic-server 已启动",
		"addr", ln.Addr().String(),
		"version", platform.Version,
		"commit", platform.Commit,
		"mode", mode,
	)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		logger.Info("收到退出信号，停止接收新请求")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func dirOf(path string) string {
	if path == "" || path == "." {
		return "."
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func ensureWritableDir(dir string) error {
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	probe := dir + "/.ic-write-probe"
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return err
	}
	return os.Remove(probe)
}
