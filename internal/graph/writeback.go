package graph

import (
	"encoding/json"

	"github.com/context-flow/ic/internal/platform"
)

// EncodeWriteBackOps 把一次执行结果编码成标准 op 批量。
//
// 为什么回写不直接改文档：回写必须与用户手动编辑走**同一条路径**，
// 否则会出现「回写绕过版本校验 → 覆盖用户同时在做的修改」这类竞态。
// 走 op 路径后，回写天然获得：版本推进、冲突检测、实时广播、可重放（INV-1）。
//
// 一个刻意的取舍：只在结果发生变化时下发 set_state，避免同一结果重复写导致
// op 日志膨胀。判据用「状态 + 变体数量 + 主指针」，不含时间戳（否则永远不等）。
func EncodeWriteBackOps(nodeID string, result NodeResult, state NodeState) ([]json.RawMessage, error) {
	if nodeID == "" {
		return nil, platform.ErrInvalid("nodeId is required")
	}
	if !ValidNodeState(state) {
		return nil, platform.ErrInvalid("invalid node state: " + string(state))
	}
	if err := validateNodeResult(&result); err != nil {
		return nil, err
	}
	payload := SetStatePayload{ID: nodeID, State: state}
	// 失败/运行中不应携带上一次的成功结果：否则 UI 会同时显示「失败」和「旧图」，
	// 用户无法判断当前状态。只有成功态才把结果写回。
	if state == NodeSucceeded || state == NodePending || state == NodeRunning {
		if state == NodeSucceeded {
			r := result
			payload.Result = &r
		} else {
			// 运行中明确清掉旧错误，避免「转圈的同时还挂着上次的错误」。
			payload.Error = &NodeError{Code: "", Message: ""}
		}
	}
	if state == NodeFailed {
		code := platform.CodeInternal
		message := "生成失败"
		if result.Variants != nil {
			for _, v := range result.Variants {
				if v.Error != "" {
					message = v.Error
					break
				}
			}
		}
		payload.Error = &NodeError{Code: code, Message: message}
	}
	raw, err := json.Marshal(struct {
		Kind string `json:"kind"`
		SetStatePayload
	}{Kind: string(OpSetState), SetStatePayload: payload})
	if err != nil {
		return nil, platform.AsError(err)
	}
	return []json.RawMessage{raw}, nil
}

// validateNodeResult 校验回写内容：变体必须带 kind，主指针必须在范围内。
// 这类校验放在服务端而不是只靠前端：回写也会被 Agent / 插件路径触发。
func validateNodeResult(r *NodeResult) error {
	if r == nil {
		return nil
	}
	if len(r.Variants) > MaxVariantsPerNode {
		return platform.ErrInvalid("结果数量超出上限").
			WithDetail("limit", MaxVariantsPerNode).WithDetail("actual", len(r.Variants))
	}
	for i, v := range r.Variants {
		if v.Kind == "" {
			return platform.ErrInvalid("结果变体缺少 kind").WithDetail("index", i)
		}
		switch v.Kind {
		case KindImage, KindVideo, KindAudio, KindText, KindJSON, KindFile:
		default:
			return platform.NewError(422, CodeInvalidSpec, "未知的结果类型: "+string(v.Kind)).WithDetail("index", i)
		}
		if v.AssetID == "" && v.Text == "" {
			return platform.ErrInvalid("结果变体既没有资产也没有文本").WithDetail("index", i)
		}
	}
	if len(r.Variants) > 0 && (r.Primary < 0 || r.Primary >= len(r.Variants)) {
		return platform.ErrInvalid("主结果指针越界").
			WithDetail("primary", r.Primary).WithDetail("variants", len(r.Variants))
	}
	return nil
}
