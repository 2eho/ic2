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
		// SQLite 单写者：**写**必须串行，因此限制连接数避免 write lock 争用。
		//
		// 但连接数不能是 1：一个 HTTP 请求里常常需要「先查 A、再查 B」，
		// 而单连接下如果任一查询的 Rows 未关闭（或被外层持有），
		// 后续查询会阻塞到超时。实测表现极具误导性：报错是
		// 「context deadline exceeded」，而真正的原因是连接池饿死。
		//
		// 取值 4：足够覆盖「一个请求内的多步查询」与少量并发读，
		// 同时远小于「写争用变得频繁」的阈值。写串行由 SQLite 的
		// 文件锁 + busy_timeout 保证，不依赖连接数。
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
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
		// 只执行上行迁移：`*.down.sql` 是回滚脚本，正常迁移路径必须跳过。
		// 这里踩过一次真实事故：把 down 脚本一起执行会让「迁移」在跑到 down 时
		// 把 schema_migrations 删掉，随后所有迁移记录静默丢失。
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || strings.HasSuffix(e.Name(), ".down.sql") {
			continue
		}
		names = append(names, e.Name())
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

// AppliedMigrations 返回已应用的迁移版本（按文件名升序）。
//
// 备份恢复演练（docs/design/13 §4.2）需要据此核对「恢复出来的库是不是同一代 schema」，
// 因此这个查询必须是纯读、不修改任何状态。
func (d *DB) AppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MigrateDown 回滚最近 n 个迁移。
//
// 约定：迁移文件 `NNNN_name.sql` 对应的回滚脚本是 `NNNN_name.down.sql`。
// 规则（刻意保守）：
//   - 没有对应 down 文件的迁移**不允许**回滚，直接报错而不是「跳过」——
//     静默跳过会让运维以为回滚成功；
//   - 回滚按逆序执行，并在同一事务内完成（SQLite 支持 DDL 事务）。
func (d *DB) MigrateDown(ctx context.Context, fsys fs.FS, dir string, n int) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("回滚步数必须为正数，当前 %d", n)
	}
	applied, err := d.AppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}
	if len(applied) == 0 {
		return nil, nil
	}
	join := func(name string) string {
		if dir == "" || dir == "." {
			return name
		}
		return dir + "/" + name
	}
	if n > len(applied) {
		n = len(applied)
	}
	rolled := make([]string, 0, n)
	for i := len(applied) - 1; i >= len(applied)-n; i-- {
		version := applied[i]
		downName := strings.TrimSuffix(version, ".sql") + ".down.sql"
		body, err := fs.ReadFile(fsys, join(downName))
		if err != nil {
			return rolled, fmt.Errorf("迁移 %s 没有回滚脚本 %s，拒绝回滚", version, downName)
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return rolled, err
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return rolled, fmt.Errorf("回滚 %s 失败: %w", version, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
			_ = tx.Rollback()
			return rolled, err
		}
		if err := tx.Commit(); err != nil {
			return rolled, err
		}
		rolled = append(rolled, version)
	}
	return rolled, nil
}

// DBDSNPath 从 DSN 中提取文件路径（用于创建父目录与体检）。
// 非文件型 DSN（内存库、postgres URL）返回 "."，调用方据此忽略目录创建。
func (c Config) DBDSNPath() string {
	dsn := c.DBDSN
	if !strings.HasPrefix(dsn, "file:") {
		return "."
	}
	path := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if path == "" || strings.Contains(path, ":memory:") {
		return "."
	}
	return path
}
