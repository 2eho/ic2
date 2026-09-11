package api

import (
	"context"
	"encoding/json"
)

// MCPService 是 MCP 协议处理接口（工具表与 REST 工具同源生成，见 07 §5）。
type MCPService interface {
	Handle(ctx context.Context, method string, params json.RawMessage, id json.RawMessage) any
}
