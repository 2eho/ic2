// ic-server 是 IC 服务端入口：装配依赖、启动 HTTP/SSE、优雅退出。
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

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/identity"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
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

	app, err := build(cfg, logger)
	if err != nil {
		logger.Error("启动失败", "err", err)
		os.Exit(1)
	}
	defer app.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Serve(ctx, logger); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("服务异常退出", "err", err)
		os.Exit(1)
	}
	logger.Info("已退出", "version", platform.Version, "commit", platform.Commit)
}

type application struct {
	cfg    platform.Config
	db     *platform.DB
	router http.Handler
	server *http.Server
	meta   *metaSvc
}

func build(cfg platform.Config, logger *slog.Logger) (*application, error) {
	if cfg.IsDevKey() {
		logger.Warn("使用不安全的开发密钥，切勿用于生产", "env", "IC_ALLOW_INSECURE_DEV_KEY")
	}
	db, err := platform.OpenDB(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.SetSQLitePragmas(ctx); err != nil {
		return nil, fmt.Errorf("sqlite pragma: %w", err)
	}
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	clock := platform.SystemClock()
	ids := platform.DefaultIDGen()

	bus := graph.NewMemoryBus()
	store := graph.NewSQLStore(db.DB, db.Dialect)
	graphSvc := graph.NewService(store, bus, clock, ids)

	authSvc := identity.New(db.DB, clock, ids, cfg)

	mux := api.NewRouter(api.Deps{
		Config: cfg,
		Logger: logger,
		Graph:  graphSvc,
		Auth:   authSvc,
		Meta:   &metaSvc{db: db},
	})

	return &application{
		cfg:    cfg,
		db:     db,
		router: mux,
		meta:   &metaSvc{db: db},
		server: &http.Server{
			Addr:              cfg.Listen,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return context.Background() },
		},
	}, nil
}

func (a *application) Serve(ctx context.Context, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return err
	}
	logger.Info("ic-server 已启动",
		"addr", ln.Addr().String(),
		"mode", a.cfg.Mode,
		"role", a.cfg.Role,
		"version", platform.Version,
		"commit", platform.Commit,
	)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.server.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		logger.Info("收到退出信号，停止接收新请求")
		return a.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (a *application) Close() {
	if a.db != nil {
		_ = a.db.Close()
	}
}

type metaSvc struct{ db *platform.DB }

func (m *metaSvc) Ready(ctx context.Context) error {
	if m.db == nil {
		return errors.New("db is not configured")
	}
	return m.db.Ready(ctx)
}
