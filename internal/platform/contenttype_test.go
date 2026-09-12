package platform

import (
	"strings"
	"testing"
)

// ATK-19：恶意 SVG / HTML 以「图片」身份上传后不得被以可执行类型下发。
//
// 这是一条真实缺陷的回归用例（实测：上传 xss.svg → 同源 GET /raw 原样返回
// image/svg+xml、无 nosniff → 浏览器打开即执行脚本）。
func TestATK19SafeContentTypeDemotesExecutables(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		blocked bool
	}{
		{"image/svg+xml", "text/plain; charset=utf-8", true},
		{"image/svg+xml; charset=utf-8", "text/plain; charset=utf-8", true},
		{"IMAGE/SVG+XML", "text/plain; charset=utf-8", true},
		{"text/html", "text/plain; charset=utf-8", true},
		{"application/xhtml+xml", "text/plain; charset=utf-8", true},
		{"application/xml", "text/plain; charset=utf-8", true},
		{"text/xml", "text/plain; charset=utf-8", true},
		// 正常图片类型必须原样保留：过度降级会让「安全」变成「不能看图」
		{"image/png", "image/png", false},
		{"image/jpeg", "image/jpeg", false},
		{"image/webp", "image/webp", false},
		{"video/mp4", "video/mp4", false},
		{"application/octet-stream", "application/octet-stream", false},
	}
	for _, c := range cases {
		got := SafeContentType(c.in)
		if got != c.want {
			t.Errorf("SafeContentType(%q) = %q, want %q", c.in, got, c.want)
		}
		if IsExecutableType(c.in) != c.blocked {
			t.Errorf("IsExecutableType(%q) = %v, want %v", c.in, !c.blocked, c.blocked)
		}
	}
}

// 穷举断言：ExecutableTypes 里的每一种都必须被降级。
// 这条防的是「往集合里加了新类型但忘了让 SafeContentType 处理」。
func TestATK19AllExecutableTypesAreDemoted(t *testing.T) {
	for _, m := range ExecutableTypes() {
		out := SafeContentType(m)
		if IsExecutableType(out) {
			t.Errorf("类型 %q 未被降级（仍为 %q），可执行文档会被下发", m, out)
		}
		if !strings.HasPrefix(out, "text/plain") {
			t.Errorf("类型 %q 降级后为 %q，期望 text/plain", m, out)
		}
	}
}

// 声明与内容不符时不得采信声明：否则「上传者选类型 = 选是否可执行」。
func TestATK19DoesNotTrustDeclaredMIME(t *testing.T) {
	// 内容是 HTML，却声明成 svg → 不得认成 svg
	htmlAsSVG := []byte("<html><body><script>alert(1)</script></body></html>")
	if got := SniffContentType(htmlAsSVG, "image/svg+xml", "evil.svg"); got == "image/svg+xml" {
		t.Fatalf("伪装成 svg 的 HTML 被认成 svg：%q", got)
	}

	// 内容是 svg 但扩展名是 .png → 不认 svg（两条件必须同时成立）
	realSVG := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	if got := SniffContentType(realSVG, "image/png", "x.png"); got == "image/svg+xml" {
		t.Fatalf("扩展名不匹配时仍认成 svg：%q", got)
	}

	// 真 SVG 且扩展名匹配 → 认 svg（合法路径不能被砍掉）
	if got := SniffContentType(realSVG, "", "ok.svg"); got != "image/svg+xml" {
		t.Fatalf("合法 SVG 未被识别：%q", got)
	}

	// 魔数优先于声明：PNG 字节声明成 svg → 必须是 png
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	if got := SniffContentType(png, "image/svg+xml", "x.svg"); got != "image/png" {
		t.Fatalf("PNG 字节被声明覆盖：%q", got)
	}
}

// 非标记语言的普通文本不得被误判成可执行文档。
func TestATK19PlainTextStaysPlain(t *testing.T) {
	plain := []byte("hello world, this is a note")
	if got := SniffContentType(plain, "text/html", "note.txt"); got == "text/html" {
		t.Fatalf("纯文本被认成 HTML：%q", got)
	}
}
