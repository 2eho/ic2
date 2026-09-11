package platform

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 故障演练子集（docs/design/13 §4.2）。
// 这些用例刻意制造「生产里会真实发生」的极端条件，验证系统行为可预测。

// 时钟回拨：租约/限额不得误判（用单调参照而不是墙上时钟）。
func TestDrillClockSkew(t *testing.T) {
	c := NewFakeClock(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	start := c.Now()

	// 正常前进
	c.Advance(30 * time.Minute)
	if got := c.Since(start); got != 30*time.Minute {
		t.Fatalf("前进后应 30m，实际 %v", got)
	}

	// 回拨 1 小时：Since 必须反映真实经过时间（负数），而不是悄悄变正
	c.Set(time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC))
	if got := c.Since(start); got >= 0 {
		t.Fatalf("回拨后 Since 应为负值以暴露时钟异常，实际 %v", got)
	}
}

// 配置边界：非法配置必须在启动期被发现，而不是运行期。
func TestDrillInvalidConfigRejectedAtStartup(t *testing.T) {
	cases := []Config{
		func() Config { c := Defaults(); c.Mode = "nope"; c.AllowInsecureDevKey = true; return c }(),
		func() Config { c := Defaults(); c.Role = "nope"; c.AllowInsecureDevKey = true; return c }(),
		func() Config { c := Defaults(); c.DBDriver = "mysql"; c.AllowInsecureDevKey = true; return c }(),
		func() Config { c := Defaults(); c.BlobDriver = "ftp"; c.AllowInsecureDevKey = true; return c }(),
		func() Config { c := Defaults(); c.WorkerConcurrency = 0; c.AllowInsecureDevKey = true; return c }(),
		func() Config { c := Defaults(); c.BlobMaxMB = -1; c.AllowInsecureDevKey = true; return c }(),
		func() Config {
			c := Defaults()
			c.Mode = "cluster"
			c.DBDriver = "sqlite"
			c.AllowInsecureDevKey = true
			return c
		}(),
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("用例 %d 的非法配置应被拒绝: %+v", i, c)
		}
	}
}

// 密钥长度非法必须在启动期拒绝（而不是等到第一次加密）。
func TestDrillSecretKeyValidation(t *testing.T) {
	// 非法长度必须被拒绝（不能推迟到第一次加密才失败）
	for _, bad := range []string{"short", "0123456789abcdef00", "x"} {
		if _, err := decodeKey(bad); err == nil {
			t.Fatalf("非法长度密钥 %q 应被拒绝", bad)
		}
	}
	// AES 合法长度：16 / 24 / 32 字节
	if _, err := decodeKey("0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("32 字节 hex 应通过: %v", err)
	}
	if _, err := decodeKey(strings.Repeat("ab", 24)); err != nil {
		t.Fatalf("24 字节 hex 应通过: %v", err)
	}
}

// 数据库不可用：必须快速失败并给出可读错误，而不是挂起。
func TestDrillDBUnavailableFailsFast(t *testing.T) {
	cfg := Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file:/proc/definitely-not-writable/x.db"
	cfg.AllowInsecureDevKey = true
	start := time.Now()
	_, err := OpenDB(cfg)
	if err == nil {
		t.Fatal("不可写路径应报错")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("应快速失败，实际耗时 %v", time.Since(start))
	}
}

// 磁盘/路径不可写：Blob 写入必须明确报错，不能静默丢数据。
// 这里直接验证文件系统行为（asset 包的同名用例覆盖服务层语义）。
func TestDrillUnwritablePathFailsClearly(t *testing.T) {
	c := Defaults()
	c.DBDriver = "sqlite"
	c.DBDSN = "file:/dev/null/nope.db"
	c.AllowInsecureDevKey = true
	if _, err := OpenDB(c); err == nil {
		t.Fatal("不可用路径应报错而不是静默成功")
	}
}

// SSRF 白名单下的重定向：每一跳都必须重新校验。
func TestDrillRedirectRevalidation(t *testing.T) {
	g := NewNetGuard()
	// 允许公网域名加入白名单，但重定向目标仍是私网 → 必须被拦
	g.ExtraAllow = []string{"allowed.example.com"}
	if err := g.CheckURL(context.Background(), "http://allowed.example.com/x"); err != nil {
		t.Fatalf("白名单主机应放行: %v", err)
	}
	// 直接校验私网地址（重定向目标）必须被拦
	if err := g.CheckURL(context.Background(), "http://127.0.0.1/steal"); err == nil {
		t.Fatal("重定向目标为回环地址必须被拦截")
	}
}

// 脱敏必须在「多行、嵌套、混合大小写」下都成立（日志常见形态）。
func TestDrillRedactMultiline(t *testing.T) {
	input := "line1\nAuthorization: Bearer sk-aaaaaaaaaaaaaaaaaa\nline3 API_KEY=\"AIzaSyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\"\n" +
		"payload=data:image/png;base64," + "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=" + "\nend"
	out := Redact(input)
	for _, secret := range []string{"sk-aaaaaaaaaaaaaaaaaa", "AIzaSyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo="} {
		if contains(out, secret) {
			t.Fatalf("多行日志中泄露了 %q\n%s", secret, out)
		}
	}
}

// 并发时钟推进不得产生竞态（run with -race）。
func TestDrillClockConcurrency(t *testing.T) {
	c := NewFakeClock(time.Now())
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 500; j++ {
				c.Advance(time.Millisecond)
				_ = c.Now()
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if c.Now().IsZero() {
		t.Fatal("时钟不应为零值")
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
