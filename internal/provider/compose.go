package provider

import (
	"fmt"
	"strings"
)

// ComposePrompt 按结构化输入组装提示词。
// 不再依赖「参考图片编号：图片1、图片2」这类脆弱文本约定来决定语义（见 05 §2.3），
// 但仍附加编号说明，让模型能把参考素材与顺序对应起来（对齐原项目行为）。
func ComposePrompt(req Request) string {
	var sb strings.Builder
	sb.WriteString(req.Prompt)
	var refs []ResolvedInput
	var texts []ResolvedInput
	for _, in := range req.Inputs {
		switch in.Kind {
		case "image", "video", "audio":
			refs = append(refs, in)
		case "text":
			texts = append(texts, in)
		}
	}
	if len(refs) > 0 {
		names := make([]string, 0, len(refs))
		for i, r := range refs {
			label := r.Label
			if label == "" {
				label = KindLabelCN(r.Kind) + fmt.Sprint(i+1)
			}
			names = append(names, label)
		}
		sb.WriteString("\n\n参考素材编号：" + strings.Join(names, "、"))
	}
	for i, t := range texts {
		fmt.Fprintf(&sb, "\n\n文本%d：%s", i+1, t.Value)
	}
	return sb.String()
}

// KindLabelCN 返回资源类型的中文标签（用于引用编号，与原项目一致）。
func KindLabelCN(kind string) string {
	switch kind {
	case "image":
		return "图片"
	case "video":
		return "视频"
	case "audio":
		return "音频"
	case "text":
		return "文本"
	case "group":
		return "组"
	}
	return "素材"
}
