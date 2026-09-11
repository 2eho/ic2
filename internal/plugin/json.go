package plugin

import "encoding/json"

// 集中 json 依赖，便于测试辅助函数复用。
func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
