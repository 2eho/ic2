package platform

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 是单一配置源，优先级：环境变量 > 默认值（见 docs/design/08-infra §1）。
type Config struct {
	Mode       string // standalone | cluster
	Role       string // api | worker | all
	Listen     string
	PublicURL  string
	DBDriver   string // sqlite | postgres
	DBDSN      string
	RedisURL   string
	BlobDriver string // fs | s3
	BlobFSRoot string
	BlobMaxMB  int64

	// SecretKey 是凭据加密主密钥（32 字节）。缺失时启动失败，除非 AllowInsecureDevKey。
	SecretKey           []byte
	AllowInsecureDevKey bool

	EnableWorker      bool
	WorkerConcurrency int

	SessionCookieSecure bool
	AllowRegistration   bool
	// OpenAccess 为 true 时跳过登录墙：启动时引导本地用户并开放 /auth/open。
	OpenAccess bool

	PluginRegistry    string
	AgentLocalAllowed bool

	LogLevel  string
	LogFormat string // json | text

	// SSRFAllowPrivate 供内网自部署放开私网出网。
	SSRFAllowPrivate bool
	SSRFAllowHosts   []string

	// StaticDir 前端静态产物目录（为空则尝试 embed）。
	StaticDir string
}

// Defaults 返回全部默认值。
func Defaults() Config {
	return Config{
		Mode:                "standalone",
		Role:                "all",
		Listen:              ":8080",
		PublicURL:           "http://localhost:8080",
		DBDriver:            "sqlite",
		DBDSN:               "file:./data/ic.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		BlobDriver:          "fs",
		BlobFSRoot:          "./data/assets",
		BlobMaxMB:           512,
		EnableWorker:        true,
		WorkerConcurrency:   8,
		SessionCookieSecure: false,
		AllowRegistration:   true,
		OpenAccess:          false,
		AgentLocalAllowed:   true,
		LogLevel:            "info",
		LogFormat:           "text",
		StaticDir:           "",
	}
}

// LoadConfig 读取环境变量并校验。
func LoadConfig() (Config, error) {
	c := Defaults()
	c.Mode = envStr("IC_MODE", c.Mode)
	c.Role = envStr("IC_ROLE", c.Role)
	c.Listen = envStr("IC_LISTEN", c.Listen)
	c.PublicURL = envStr("IC_PUBLIC_URL", c.PublicURL)
	c.DBDriver = envStr("IC_DB_DRIVER", c.DBDriver)
	c.DBDSN = envStr("IC_DB_DSN", c.DBDSN)
	c.RedisURL = envStr("IC_REDIS_URL", c.RedisURL)
	c.BlobDriver = envStr("IC_BLOB_DRIVER", c.BlobDriver)
	c.BlobFSRoot = envStr("IC_BLOB_FS_ROOT", c.BlobFSRoot)
	c.BlobMaxMB = envInt64("IC_BLOB_MAX_MB", c.BlobMaxMB)
	c.EnableWorker = envBool("IC_ENABLE_WORKER", c.EnableWorker)
	c.WorkerConcurrency = int(envInt64("IC_WORKER_CONCURRENCY", int64(c.WorkerConcurrency)))
	c.SessionCookieSecure = envBool("IC_SESSION_COOKIE_SECURE", c.SessionCookieSecure)
	c.AllowRegistration = envBool("IC_ALLOW_REGISTRATION", c.AllowRegistration)
	c.OpenAccess = envBool("IC_OPEN_ACCESS", c.OpenAccess)
	c.PluginRegistry = envStr("IC_PLUGIN_REGISTRY", c.PluginRegistry)
	c.AgentLocalAllowed = envBool("IC_AGENT_LOCAL_ALLOWED", c.AgentLocalAllowed)
	c.LogLevel = envStr("IC_LOG_LEVEL", c.LogLevel)
	c.LogFormat = envStr("IC_LOG_FORMAT", c.LogFormat)
	c.SSRFAllowPrivate = envBool("IC_SSRF_ALLOW_PRIVATE", c.SSRFAllowPrivate)
	c.SSRFAllowHosts = splitCSV(envStr("IC_SSRF_ALLOW_HOSTS", ""))
	c.StaticDir = envStr("IC_STATIC_DIR", c.StaticDir)

	if raw := os.Getenv("IC_SECRET_KEY"); raw != "" {
		key, err := decodeKey(raw)
		if err != nil {
			return c, fmt.Errorf("IC_SECRET_KEY 无效: %w", err)
		}
		c.SecretKey = key
	}
	c.AllowInsecureDevKey = envBool("IC_ALLOW_INSECURE_DEV_KEY", false)

	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// Validate 做启动期配置校验（必填项、互斥项、密钥长度）。
func (c Config) Validate() error {
	switch c.Mode {
	case "standalone", "cluster":
	default:
		return fmt.Errorf("IC_MODE 必须是 standalone 或 cluster，当前 %q", c.Mode)
	}
	switch c.Role {
	case "api", "worker", "all":
	default:
		return fmt.Errorf("IC_ROLE 必须是 api/worker/all，当前 %q", c.Role)
	}
	switch c.DBDriver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("IC_DB_DRIVER 必须是 sqlite 或 postgres，当前 %q", c.DBDriver)
	}
	switch c.BlobDriver {
	case "fs", "s3":
	default:
		return fmt.Errorf("IC_BLOB_DRIVER 必须是 fs 或 s3，当前 %q", c.BlobDriver)
	}
	if c.DBDSN == "" {
		return fmt.Errorf("IC_DB_DSN 不能为空")
	}
	if c.Mode == "cluster" && c.DBDriver == "sqlite" {
		return fmt.Errorf("cluster 模式不支持 sqlite")
	}
	if len(c.SecretKey) == 0 && !c.AllowInsecureDevKey {
		return fmt.Errorf("缺少 IC_SECRET_KEY（32 字节 base64 或 64 位 hex）")
	}
	if n := len(c.SecretKey); n != 0 && n != 16 && n != 24 && n != 32 {
		return fmt.Errorf("IC_SECRET_KEY 长度必须是 16/24/32 字节，当前 %d", n)
	}
	if c.BlobMaxMB <= 0 {
		return fmt.Errorf("IC_BLOB_MAX_MB 必须为正数")
	}
	if c.WorkerConcurrency <= 0 {
		return fmt.Errorf("IC_WORKER_CONCURRENCY 必须为正数")
	}
	if c.SecretKey != nil && c.AllowInsecureDevKey {
		return fmt.Errorf("IC_SECRET_KEY 与 IC_ALLOW_INSECURE_DEV_KEY 互斥")
	}
	return nil
}

// IsDevKey 表示使用不安全的开发密钥（仅本地开发）。
func (c Config) IsDevKey() bool { return len(c.SecretKey) == 0 && c.AllowInsecureDevKey }

// EffectiveSecretKey 返回实际使用的密钥（开发模式下派生固定密钥）。
func (c Config) EffectiveSecretKey() []byte {
	if len(c.SecretKey) > 0 {
		return c.SecretKey
	}
	// 仅供本地开发，非生产：明文常量派生。
	return []byte("ic-insecure-dev-key-do-not-use!!")[:32]
}

// decodeKey 解析主密钥，只接受 AES 合法长度（16/24/32 字节）。
//
// 刻意在此处就做长度校验：如果只做"能解码"，16 进制字符组成的短串会被
// 当成密钥接受，然后在第一次加密时才失败——错误被推迟到运行期是设计缺陷。
func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	valid := func(b []byte) ([]byte, bool) {
		switch len(b) {
		case 16, 24, 32:
			return b, true
		}
		return nil, false
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
		if key, ok := valid(b); ok {
			return key, nil
		}
	}
	if b, err := hexDecode(raw); err == nil {
		if key, ok := valid(b); ok {
			return key, nil
		}
	}
	if len(raw) == 16 || len(raw) == 24 || len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("密钥必须是 16/24/32 字节的 base64、hex 或原文")
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex 长度必须为偶数")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

func envStr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v, ok := os.LookupEnv(k)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func envInt64(k string, def int64) int64 {
	v, ok := os.LookupEnv(k)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return def
	}
	return n
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
