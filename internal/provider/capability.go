// Package provider 定义模型能力、渠道适配器与调用传输层。
package provider

import "strings"

// Capability 是统一能力枚举，避免散落的字符串判断（docs/design/03 §2.3）。
type Capability string

// 能力枚举。
const (
	CapImageGenerate Capability = "image.generate"
	CapImageEdit     Capability = "image.edit"
	CapImageUpscale  Capability = "image.upscale"
	CapVideoGenerate Capability = "video.generate"
	CapTextGenerate  Capability = "text.generate"
	CapTextTools     Capability = "text.tools"
	CapAudioGenerate Capability = "audio.generate"
	CapModelList     Capability = "model.list"
)

// All 返回全部能力（供前端与文档校验）。
func All() []Capability {
	return []Capability{CapImageGenerate, CapImageEdit, CapImageUpscale, CapVideoGenerate,
		CapTextGenerate, CapTextTools, CapAudioGenerate, CapModelList}
}

// Valid 校验能力名。
func Valid(c Capability) bool {
	for _, x := range All() {
		if x == c {
			return true
		}
	}
	return false
}

// Parse 把字符串解析为能力（未知返回 false）。
func Parse(s string) (Capability, bool) {
	c := Capability(strings.TrimSpace(s))
	return c, Valid(c)
}

// ResourceKind 把能力映射到资源类型（用于画布端口类型校验）。
func (c Capability) ResourceKind() string {
	switch c {
	case CapImageGenerate, CapImageEdit, CapImageUpscale:
		return "image"
	case CapVideoGenerate:
		return "video"
	case CapTextGenerate, CapTextTools:
		return "text"
	case CapAudioGenerate:
		return "audio"
	}
	return "file"
}
