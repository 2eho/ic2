package prompt

import (
	"context"
	"net/http"
)

// newRequest 构造带上下文的请求。独立成函数便于测试注入。
func newRequest(ctx context.Context, method, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, nil)
}
