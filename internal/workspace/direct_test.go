package workspace

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------- 本地直连（4.21）
//
// 这一组用例的共同点：每一条都对应「如果不拦会怎样」。
// 本地直连是一个**兼容降级通道**，它的风险不在功能，而在它绕过了服务端编排。

func TestDirectDisabledByDefault(t *testing.T) {
	var p Prefs
	if p.DirectEnabled() {
		t.Fatal("未设置时直连必须默认关闭（默认开启会让升级静默改变请求路径）")
	}
	if got := p.DirectScope(); len(got) != 1 || got[0] != "image.generate" {
		t.Fatalf("默认生效范围错误: %v", got)
	}
}

func TestDirectRequiresAcknowledgement(t *testing.T) {
	// 只开开关、不确认风险 → 不生效。分开存是为了让 UI 能区分
	// 「还差一步」与「没开」这两种状态。
	p := Prefs{Direct: &DirectPrefs{Enabled: true, BaseURL: "http://127.0.0.1:8317"}}
	if p.DirectEnabled() {
		t.Fatal("未确认风险时不应生效")
	}
	p.Direct.AcknowledgedAt = time.Now().UTC().Format(time.RFC3339)
	if !p.DirectEnabled() {
		t.Fatal("确认后应当生效")
	}
}

// ATK-26：本地直连的地址必须回环（否则它就是「服务端代任意地址发请求」的入口）。
func TestValidateDirectRejectsNonLoopback(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	cases := []string{
		"http://10.0.0.5:8317",
		"http://169.254.169.254/latest/meta-data/",
		"http://evil.example:80",
		"file:///etc/passwd",
		"http://127.0.0.1.evil.example:80",
	}
	for _, base := range cases {
		err := validateDirect(&DirectPrefs{Enabled: true, BaseURL: base, AcknowledgedAt: now})
		if err == nil {
			t.Fatalf("非回环地址应当被拒绝：%s（否则这个开关就是服务端代任意地址发请求的入口）", base)
		}
	}
	for _, base := range []string{"http://127.0.0.1:8317", "http://localhost:8317", "http://[::1]:8317"} {
		if err := validateDirect(&DirectPrefs{Enabled: true, BaseURL: base, AcknowledgedAt: now}); err != nil {
			t.Fatalf("回环地址应当被接受：%s -> %v", base, err)
		}
	}
}

func TestValidateDirectRequiresBaseURLAndTimestamp(t *testing.T) {
	if err := validateDirect(&DirectPrefs{Enabled: true, AcknowledgedAt: time.Now().UTC().Format(time.RFC3339)}); err == nil {
		t.Fatal("缺少 baseUrl 应当被拒绝")
	}
	if err := validateDirect(&DirectPrefs{Enabled: true, BaseURL: "http://127.0.0.1:1", AcknowledgedAt: "yesterday"}); err == nil {
		t.Fatal("非法时间戳应当被拒绝")
	}
}

func TestValidateDirectRejectsUnknownCapability(t *testing.T) {
	err := validateDirect(&DirectPrefs{
		Enabled: true, BaseURL: "http://127.0.0.1:1",
		AcknowledgedAt: time.Now().UTC().Format(time.RFC3339),
		Scope:          []string{"image.generate", "admin.delete_everything"},
	})
	if err == nil {
		t.Fatal("未知能力名应当被拒绝（否则 typos 会静默变成「该能力不走直连」）")
	}
}

func TestValidateDirectAllowsDisabledWithoutConfig(t *testing.T) {
	if err := validateDirect(&DirectPrefs{}); err != nil {
		t.Fatalf("关闭状态不应当要求填配置: %v", err)
	}
	if err := validateDirect(nil); err != nil {
		t.Fatalf("nil 应当被接受: %v", err)
	}
}
