package platform

import "testing"

func TestConfigValidate(t *testing.T) {
	base := Defaults()
	if err := base.Validate(); err == nil {
		t.Fatal("无密钥时应当校验失败（除非显式开发模式）")
	}
	dev := Defaults()
	dev.AllowInsecureDevKey = true
	if err := dev.Validate(); err != nil {
		t.Fatalf("开发模式应通过: %v", err)
	}
	bad := Defaults()
	bad.AllowInsecureDevKey = true
	bad.Mode = "nope"
	if err := bad.Validate(); err == nil {
		t.Fatal("非法 mode 应被拒绝")
	}
	cluster := Defaults()
	cluster.AllowInsecureDevKey = true
	cluster.Mode = "cluster"
	if err := cluster.Validate(); err == nil {
		t.Fatal("cluster + sqlite 应被拒绝")
	}
	short := Defaults()
	short.SecretKey = []byte("short")
	if err := short.Validate(); err == nil {
		t.Fatal("密钥长度非法应被拒绝")
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("IC_MODE", "standalone")
	t.Setenv("IC_ALLOW_INSECURE_DEV_KEY", "true")
	t.Setenv("IC_BLOB_MAX_MB", "1024")
	t.Setenv("IC_SSRF_ALLOW_HOSTS", "a.local, b.local")
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.BlobMaxMB != 1024 {
		t.Fatalf("BlobMaxMB=%d", c.BlobMaxMB)
	}
	if len(c.SSRFAllowHosts) != 2 || c.SSRFAllowHosts[1] != "b.local" {
		t.Fatalf("SSRFAllowHosts=%v", c.SSRFAllowHosts)
	}
}

func TestDecodeKeyForms(t *testing.T) {
	if _, err := decodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY="); err != nil {
		t.Fatalf("base64 解析失败: %v", err)
	}
	if _, err := decodeKey("0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("hex 解析失败: %v", err)
	}
	if _, err := decodeKey("x"); err == nil {
		t.Fatal("非法密钥应报错")
	}
}
