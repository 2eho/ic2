package platform

import (
	"net/http"
	"time"
)

// HTTPDoer 便于测试替换（解耦清单）。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// DefaultHTTPClient 是出网默认客户端：全局超时 + 受 Guard 保护的重定向。
func DefaultHTTPClient(g *NetGuard) *http.Client {
	return NewGuardedClient(g, &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		},
	}, 120*time.Second)
}
