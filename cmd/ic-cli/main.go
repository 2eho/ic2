// ic-cli 是运维 CLI：迁移、导入导出、密钥轮换、体检。
//
// 设计依据 docs/design/08-infra.md §7。
// 纪律：所有子命令都必须能在「服务未运行」时安全执行（直接操作 DB / 文件系统），
// 且不得打印任何明文密钥（INV-5，输出统一走 platform.Redact）。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

// usage 是 CLI 的帮助文本。刻意手写而不是引第三方 flag 库：
// 这个二进制会在生产容器里以运维身份执行，依赖越少越好。
const usage = `ic-cli — IC 运维命令行

用法:
  ic-cli <command> [flags]

命令:
  migrate        执行数据库迁移（幂等）
  migrate-down   回滚最后一次迁移（需 migrations/*.down.sql）
  doctor         体检：配置、DB、迁移、Blob 目录、密钥长度
  keys rotate    轮换主密钥（旧密钥解密 → 新密钥加密，双密钥期）
  keys gen       生成一个合规的主密钥
  export         导出画布与资产清单（JSON，用于备份核对）
  version        打印版本

通用 flag:
  -config-dir     数据目录（默认 ./data）
  -json           以 JSON 输出结果（便于 CI 断言）
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "migrate":
		err = runMigrate(args, false)
	case "migrate-down":
		err = runMigrate(args, true)
	case "doctor":
		err = runDoctor(args)
	case "keys":
		err = runKeys(args)
	case "export":
		err = runExport(args)
	case "version":
		fmt.Printf("ic-cli %s (%s) built %s\n", platform.Version, platform.Commit, platform.Date)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %s\n", platform.Redact(err.Error()))
		os.Exit(1)
	}
}

// loadConfig 读取配置，但**放宽启动校验**中与「本次动作无关」的部分：
// 例如 keys rotate 需要配置密钥，但 doctor 在没有密钥时也应该能告诉你缺什么。
func loadConfig(strict bool) (platform.Config, error) {
	if strict {
		return platform.LoadConfig()
	}
	c := platform.Defaults()
	c.DBDriver = envOr("IC_DB_DRIVER", c.DBDriver)
	c.DBDSN = envOr("IC_DB_DSN", c.DBDSN)
	c.BlobDriver = envOr("IC_BLOB_DRIVER", c.BlobDriver)
	c.BlobFSRoot = envOr("IC_BLOB_FS_ROOT", c.BlobFSRoot)
	c.StaticDir = envOr("IC_STATIC_DIR", c.StaticDir)
	c.AllowInsecureDevKey = envOr("IC_ALLOW_INSECURE_DEV_KEY", "") != ""
	if raw := os.Getenv("IC_SECRET_KEY"); raw != "" {
		key, err := platform.DecodeSecretKey(raw)
		if err != nil {
			return c, fmt.Errorf("IC_SECRET_KEY 无效: %w", err)
		}
		c.SecretKey = key
	}
	return c, nil
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func openDB(ctx context.Context) (*platform.DB, platform.Config, error) {
	cfg, err := loadConfig(false)
	if err != nil {
		return nil, cfg, err
	}
	db, err := platform.OpenDB(cfg)
	if err != nil {
		return nil, cfg, err
	}
	if err := db.SetSQLitePragmas(ctx); err != nil {
		_ = db.Close()
		return nil, cfg, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBDSNPath()), 0o750); err != nil {
		// 数据目录创建失败不阻断（内存库或无路径 DSN 场景）
		_ = err
	}
	return db, cfg, nil
}

func runMigrate(args []string, down bool) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON 输出")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db, _, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	if down {
		applied, err := db.MigrateDown(ctx, migrations.FS, ".", 1)
		if err != nil {
			return err
		}
		return emit(*asJSON, map[string]any{"rolledBack": applied})
	}
	if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
		return err
	}
	applied, err := db.AppliedMigrations(ctx)
	if err != nil {
		return err
	}
	return emit(*asJSON, map[string]any{"applied": applied, "count": len(applied)})
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON 输出")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := map[string]any{}
	ok := true

	cfg, cfgErr := platform.LoadConfig()
	if cfgErr != nil {
		// 严格加载失败通常是「没设密钥」，这在体检里是**警告**而非错误：
		// 用户明确要求体检，就应该得到一份完整报告而不是一条错误。
		report["config"] = map[string]any{"ok": false, "reason": platform.Redact(cfgErr.Error())}
		ok = false
		cfg, _ = loadConfig(false)
	} else {
		report["config"] = map[string]any{"ok": true, "mode": cfg.Mode, "role": cfg.Role, "listen": cfg.Listen}
	}

	keyLen := len(cfg.SecretKey)
	report["secretKey"] = map[string]any{
		"present": keyLen > 0, "bytes": keyLen,
		"devKey": cfg.IsDevKey(),
	}
	if keyLen == 0 && !cfg.AllowInsecureDevKey {
		ok = false
	}

	db, err := platform.OpenDB(cfg)
	if err != nil {
		report["database"] = map[string]any{"ok": false, "reason": platform.Redact(err.Error())}
		ok = false
	} else {
		defer db.Close()
		if err := db.SetSQLitePragmas(ctx); err != nil {
			report["database"] = map[string]any{"ok": false, "reason": platform.Redact(err.Error())}
			ok = false
		} else if err := db.Ready(ctx); err != nil {
			report["database"] = map[string]any{"ok": false, "reason": platform.Redact(err.Error())}
			ok = false
		} else {
			applied, _ := db.AppliedMigrations(ctx)
			report["database"] = map[string]any{"ok": true, "driver": db.Dialect, "migrations": len(applied)}
			if len(applied) == 0 {
				report["migrations"] = map[string]any{"ok": false, "reason": "尚未执行迁移，请先 ic-cli migrate"}
				ok = false
			}
		}
	}

	if cfg.BlobDriver == "fs" {
		root := cfg.BlobFSRoot
		info, statErr := os.Stat(root)
		switch {
		case statErr != nil:
			report["blob"] = map[string]any{"ok": false, "root": root, "reason": "目录不可访问", "hint": "mkdir -p 并确保进程可写"}
			ok = false
		case !info.IsDir():
			report["blob"] = map[string]any{"ok": false, "root": root, "reason": "不是目录"}
			ok = false
		default:
			// 写权限用真实探测，不用 mode 位猜测（容器里 mode 常不可靠）
			probe := filepath.Join(root, ".ic-doctor-probe")
			writeErr := os.WriteFile(probe, []byte("ok"), 0o600)
			if writeErr == nil {
				_ = os.Remove(probe)
			}
			report["blob"] = map[string]any{"ok": writeErr == nil, "root": root, "driver": cfg.BlobDriver, "writable": writeErr == nil}
			if writeErr != nil {
				ok = false
			}
		}
	} else {
		report["blob"] = map[string]any{"ok": true, "driver": cfg.BlobDriver}
	}

	report["ok"] = ok
	if err := emit(*asJSON, report); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("体检未通过，请按上方各项修复")
	}
	return nil
}

func runKeys(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ic-cli keys gen|rotate")
	}
	switch args[0] {
	case "gen":
		key, err := platform.GenerateSecretKey()
		if err != nil {
			return err
		}
		// 这是唯一一次「有意打印密钥」的地方：用户显式要求生成密钥。
		// 提示写清楚，避免被误当访问令牌写进日志。
		fmt.Printf("IC_SECRET_KEY=%s\n", key)
		fmt.Fprintln(os.Stderr, "请立即保存到密钥管理系统；该值不会再次显示，也不要写入版本库。")
		return nil
	case "rotate":
		fs := flag.NewFlagSet("keys rotate", flag.ExitOnError)
		newKeyRaw := fs.String("new-key", "", "新主密钥（缺省则随机生成）")
		asJSON := fs.Bool("json", false, "JSON 输出")
		_ = fs.Parse(args[1:])

		newKey, err := resolveNewKey(*newKeyRaw)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		cfg, err := loadConfig(true)
		if err != nil {
			return err
		}
		db, err := platform.OpenDB(cfg)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.SetSQLitePragmas(ctx); err != nil {
			return err
		}

		old := cfg.EffectiveSecretKey()
		res, err := platform.RotateSecretKey(ctx, db.DB, old, newKey)
		if err != nil {
			return err
		}
		out := map[string]any{
			"credentials": res.Credentials,
			"secrets":     res.Secrets,
			"durationMs":  res.DurationMS,
			"hint":        "已轮换。请更新 IC_SECRET_KEY 后重启服务；旧密文在双密钥期内仍可读。",
		}
		if *newKeyRaw == "" {
			out["newKey"] = platform.FormatSecretKey(newKey)
		}
		return emit(*asJSON, out)
	default:
		return fmt.Errorf("未知 keys 子命令 %q", args[0])
	}
}

func resolveNewKey(raw string) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return platform.GenerateSecretKeyBytes()
	}
	return platform.DecodeSecretKey(raw)
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs.String("out", "", "输出文件（缺省打印到 stdout）")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	db, _, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	// 导出的是「结构 + 计数 + 引用关系」，不含资产二进制。
	// 二进制由 Blob 目录整体备份，两者配合才能还原（docs/design/13 §4.2 备份恢复演练）。
	manifest, err := platform.ExportManifest(ctx, db.DB)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Println(string(raw))
		return nil
	}
	if err := os.WriteFile(*out, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("写入 %s: %w", *out, err)
	}
	fmt.Fprintf(os.Stderr, "已导出 %s（%d 字节，生成于 %s）\n", *out, len(raw), timestamp())
	return nil
}

func emit(asJSON bool, v any) error {
	if !asJSON {
		for _, line := range humanLines(v) {
			fmt.Println(line)
		}
		return nil
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}

// humanLines 把结果渲染成人可读的行。刻意保持「键排序 + 一层缩进」，
// 这样 diff 两次执行结果时有意义。
func humanLines(v any) []string {
	raw, _ := json.Marshal(v)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return []string{string(raw)}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		inner, _ := json.Marshal(m[k])
		lines = append(lines, fmt.Sprintf("%-14s %s", k+":", string(inner)))
	}
	return lines
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339) }
