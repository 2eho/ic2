package platform

import (
	"bytes"
	"path"
	"strings"
)

// 本文件处理一个具体的攻击面（ATK-19）：**上传的内容会不会被浏览器当成可执行文档**。
//
// 为什么必须放在服务端而不是前端：
// 上传者控制两件事——文件字节、以及 Content-Type 声明。只要服务端把声明原样下发，
// 上传者就能选一个「可执行类型」，然后把同源地址发给别人打开。SVG 是最典型的一种
// （唯一可携带脚本的图片格式），但同样适用于 HTML、XHTML、XML。
//
// 修法不是「删掉 SVG 支持」（那会把合法图片一起砍掉），而是把**类型判定**与
// **下发方式**分开：
//   1. 类型判定不采信声明：以字节嗅探为准，声明只在嗅探得不出结论时兜底；
//   2. 下发时把「可执行文档类型」一律降级为不可执行的文本类型；
//   3. 所有 raw 响应都带 nosniff，避免浏览器把兜底类型嗅探成 HTML。
//
// 这与 docs/design/11 §1 的不变量一致：INV-6（插件/第三方内容永不获得宿主执行权）
// 的同类要求——第三方内容不得在应用 origin 下执行。

// CORS 无关的「可执行文档」类型集合：浏览器会把这些类型当作文档解析并执行脚本。
// 注意 application/xml 与 text/xml 在被导航到时也可能渲染出脚本（XSLT / 内联脚本），
// 因此一并纳入。
var executableTextTypes = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
	"image/svg+xml":         true,
	"application/xml":       true,
	"text/xml":              true,
	"application/xhtml":     true,
	"application/rss+xml":   true,
	"application/atom+xml":  true,
}

// SafeContentType 返回**可以安全下发**的 Content-Type。
//
// 规则：
//   - 可执行文档类型 → text/plain（内容仍可下载/预览，但绝不会被当作文档执行）
//   - 其余类型原样返回
//
// 为什么选 text/plain 而不是 application/octet-stream：
// octet-stream 会触发「下载」而浏览器不再内联显示，用户无法在页面里看到 SVG；
// text/plain 兼顾「能看」与「不执行」。这在 nosniff 之下是安全的下发方式。
func SafeContentType(mime string) string {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if executableTextTypes[m] {
		return "text/plain; charset=utf-8"
	}
	if m == "" {
		return "application/octet-stream"
	}
	return mime
}

// IsExecutableType 报告某个类型是否会被浏览器当作可执行文档。
// 供测试与审计使用（门禁需要能穷举「哪些类型必须被降级」）。
func IsExecutableType(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	return executableTextTypes[m]
}

// ExecutableTypes 返回全部被降级处理的类型（供门禁穷举断言，避免漏掉新增类型）。
func ExecutableTypes() []string {
	out := make([]string, 0, len(executableTextTypes))
	for k := range executableTextTypes {
		out = append(out, k)
	}
	return out
}

// sniffDeclared 用「字节 + 扩展名」交叉判定真实类型，**不采信**客户端声明。
//
// 判定顺序有讲究：
//  1. 已知魔数 → 直接定类型（最可靠，SVG 除外——它是纯文本，没有魔数）；
//  2. SVG 例外：它是 XML 文本，只能用「内容看起来像 XML/SVG」+ 扩展名共同判断。
//     放宽到「只要声明是 svg 就认」会把 XSS 的开关交回上传者，所以必须两条件同时成立。
//
// 返回空串表示「无法判定」，交给调用方兜底。
func SniffContentType(buf []byte, declared, name string) string {
	if byMagic := sniffMagic(buf); byMagic != "" {
		return byMagic
	}
	trimmed := bytes.TrimLeft(buf, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '<' {
		// 文本型标记语言：只有扩展名也匹配时才认 SVG，否则一律按「不可执行文本」处理。
		if strings.HasSuffix(strings.ToLower(path.Ext(name)), ".svg") && looksLikeSVG(trimmed) {
			return "image/svg+xml"
		}
		if strings.HasSuffix(strings.ToLower(path.Ext(name)), ".pdf") {
			return "application/pdf"
		}
		// 其余标记语言统一按文本处理：即使内容是 HTML，也不会被当成 HTML 下发。
		return "text/plain"
	}
	_ = declared
	return ""
}

// looksLikeSVG 判断内容是否真的像 SVG 根元素（而不是恰好扩展名叫 .svg 的 HTML）。
func looksLikeSVG(b []byte) bool {
	// 只看开头一小段，避免对大文件做 O(n) 扫描。
	limit := len(b)
	if limit > 4096 {
		limit = 4096
	}
	head := strings.ToLower(string(b[:limit]))
	// 允许 XML 声明、注释、DOCTYPE 在根元素之前。
	i := strings.Index(head, "<svg")
	if i < 0 {
		return false
	}
	// 根元素之前不得出现 <script>（否则是「伪装成 SVG 的 HTML」）
	if j := strings.Index(head, "<script"); j >= 0 && j < i {
		return false
	}
	if j := strings.Index(head, "<html"); j >= 0 && j < i {
		return false
	}
	return true
}

// sniffMagic 以字节前缀判定类型（魔数是最难伪造的判据）。
func sniffMagic(buf []byte) string {
	switch {
	case bytes.HasPrefix(buf, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(buf, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(buf, []byte("GIF87a")), bytes.HasPrefix(buf, []byte("GIF89a")):
		return "image/gif"
	case len(buf) > 12 && bytes.Equal(buf[0:4], []byte("RIFF")) && bytes.Equal(buf[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(buf, []byte("%PDF")):
		return "application/pdf"
	case len(buf) > 12 && bytes.Equal(buf[4:8], []byte("ftyp")):
		return "video/mp4"
	case bytes.HasPrefix(buf, []byte("ID3")):
		return "audio/mpeg"
	case bytes.HasPrefix(buf, []byte("OggS")):
		return "audio/ogg"
	case bytes.HasPrefix(buf, []byte("fLaC")):
		return "audio/flac"
	case len(buf) > 44 && bytes.Equal(buf[0:4], []byte("RIFF")) && bytes.Equal(buf[8:12], []byte("WAVE")):
		return "audio/wav"
	}
	return ""
}
