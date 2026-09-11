package platform

import (
	"context"
	"testing"
)

// ATK-04：Base URL = http://169.254.169.254 必须被拒绝，code=ssrf_blocked。
func TestSSRFBlocksMetadataAndPrivate(t *testing.T) {
	g := NewNetGuard()
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/",
		"http://[::1]/",
		"http://10.0.0.5/v1",
		"http://192.168.1.1/",
		"http://172.16.9.9/",
		"http://localhost:17371/",
		"http://0.0.0.0/",
		"http://100.100.100.200/", // 阿里云元数据
	}
	for _, u := range blocked {
		err := g.CheckURL(context.Background(), u)
		if err == nil {
			t.Fatalf("%s 未被拦截", u)
		}
		de := AsDomainError(err)
		if de.Code != CodeSSRFBlocked {
			t.Fatalf("%s code=%s 期望 %s", u, de.Code, CodeSSRFBlocked)
		}
	}
}

func TestSSRFAllowsPublic(t *testing.T) {
	g := NewNetGuard()
	if err := g.CheckURL(context.Background(), "https://api.openai.com/v1"); err != nil {
		t.Fatalf("公网地址被误拦: %v", err)
	}
}

func TestSSRFRejectsBadScheme(t *testing.T) {
	g := NewNetGuard()
	for _, u := range []string{"file:///etc/passwd", "gopher://x/", "ftp://a/b", "not-a-url"} {
		if err := g.CheckURL(context.Background(), u); err == nil {
			t.Fatalf("%s 未被拦截", u)
		}
	}
}

func TestSSRFAllowPrivateOptIn(t *testing.T) {
	g := &NetGuard{AllowPrivate: true}
	// 私网放开后 localhost 允许
	if err := g.CheckURL(context.Background(), "http://localhost:9000/v1"); err != nil {
		t.Fatalf("显式放开后仍被拦: %v", err)
	}
}

func TestSSRFExtraAllowlist(t *testing.T) {
	g := &NetGuard{ExtraAllow: []string{"internal.registry.local"}}
	if err := g.CheckURL(context.Background(), "http://internal.registry.local:5000/x"); err != nil {
		t.Fatalf("白名单主机被拦: %v", err)
	}
}
