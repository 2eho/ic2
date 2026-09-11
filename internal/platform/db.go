package platform

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB 包装 *sql.DB 并提供方言信息。
type DB struct {
	*sql.DB
	Dialect string // sqlite | postgres
}

// OpenDB 按配置打开数据库连接池。
func OpenDB(cfg Config) (*DB, error) {
	switch cfg.DBDriver {
	case "sqlite":
		dsn := cfg.DBDSN
		if dsn == "" {
			dsn = ":memory:"
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		// SQLite 单写者：限制连接数避免 write lock 争用。
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		if err := db.Ping(); err != nil {
			return nil, fmt.Errorf("ping sqlite: %w", err)
		}
		return &DB{DB: db, Dialect: "sqlite"}, nil
	case "postgres":
		// Postgres 驱动由部署方按需注入（避免默认依赖过重）。
		return nil, fmt.Errorf("postgres driver 未编译进本构建；请使用带 pg 标签的构建或 standalone 模式")
	}
	return nil, fmt.Errorf("不支持的 IC_DB_DRIVER: %s", cfg.DBDriver)
}

// SetSQLitePragmas 设置 WAL 与 busy_timeout（standalone 场景）。
func (d *DB) SetSQLitePragmas(ctx context.Context) error {
	if d.Dialect != "sqlite" {
		return nil
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := d.ExecContext(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// Migrate 顺序执行 migrations 目录中的 SQL（幂等：以 schema_migrations 记录）。
func (d *DB) Migrate(ctx context.Context, fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	join := func(name string) string {
		if dir == "" || dir == "." {
			return name
		}
		return dir + "/" + name
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	applied := map[string]bool{}
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := fs.ReadFile(fsys, join(name))
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := d.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("migration %s failed: %w", name, err)
			}
		}
		if _, err := d.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, name, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements 按分号切分 SQL（迁移文件不包含函数体，按分号切分是安全的）。
func splitStatements(sqlText string) []string {
	out := []string{}
	var sb strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "--") || t == "" {
			continue
		}
		sb.WriteString(line)
		sb.WriteString("\n")
		if strings.HasSuffix(t, ";") {
			if s := strings.TrimSpace(sb.String()); s != "" && s != ";" {
				out = append(out, s)
			}
			sb.Reset()
		}
	}
	if s := strings.TrimSpace(sb.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// Ready 检查连接可用。
func (d *DB) Ready(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return d.PingContext(cctx)
}
