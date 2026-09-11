package exec

import "encoding/json"

// jsonUnmarshal 收敛到一处：便于将来替换为严格解析（拒绝未知字段）而不影响调用方。
func jsonUnmarshal(raw string, out any) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}
