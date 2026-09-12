package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// strictExceptKind 解析并拒绝未知字段（additionalProperties:false），
// 但始终忽略顶层 "kind"（op 分发字段，不属于任何 payload 结构）。
func strictExceptKind(raw []byte, v any) error {
	target := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &target); err != nil {
		return err
	}
	delete(target, "kind")
	body, err := json.Marshal(target)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty json")
		}
		return err
	}
	return nil
}

// lenientUnmarshal 解析但允许 struct 未声明的字段（如 spec.patch 的动态键）。
func lenientUnmarshal(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty json")
		}
		return err
	}
	return nil
}
