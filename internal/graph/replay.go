package graph

// 本文件实现 op 日志重放（INV-1：重放结果必须等于权威快照）。
// 它是「服务端权威」这条设计的**可证伪点**：只要重放对不上，就说明幂等/顺序/校验
// 有一处不成立。docs/design/13 §3.2 的 ATK-21 直接引这里。

import (
	"encoding/json"
	"fmt"
	"time"
)

// Replay 从空文档重放全部 op，用于验证 INV-1（op 日志重放 == 权威快照）。
func Replay(doc *CanvasDocument, ops [][]byte, now time.Time) (*CanvasDocument, error) {
	out := NewDocument(doc.ID, doc.ProjectID)
	out.Settings = doc.Settings
	for i, raw := range ops {
		_, next, err := Apply(out, []json.RawMessage{json.RawMessage(raw)}, "replay", now)
		if err != nil {
			return nil, fmt.Errorf("replay failed at op %d: %w", i, err)
		}
		out = next
	}
	return out, nil
}
