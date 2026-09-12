package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/plugin"
)

// GuardedFetcher 是带 SSRF 防护的通用出网抓取器。
//
// 它同时满足三处需求：
//   - plugin.ManifestFetcher：拉取第三方插件清单与 bundle（ATK-04/ATK-09 前置）；
//   - wiring.URLFetcher：把上游产物 URL 落成资产（执行引擎 StoreURL）；
//   - 统一的「拒绝内网目标 + 限制响应体大小」出口。
//
// 集中在一处是刻意的：出网是攻击面最大的地方，散落的 http.Get 会有漏网。
type GuardedFetcher struct {
	guard  *platform.NetGuard
	client platform.HTTPDoer
	// maxBytes 单次抓取上限（默认 64MB），防止「一个 10GB 的响应把内存吃光」。
	maxBytes int64
}

// NewGuardedFetcher 构造抓取器。
func NewGuardedFetcher(guard *platform.NetGuard, client platform.HTTPDoer) *GuardedFetcher {
	if guard == nil {
		guard = platform.NewNetGuard()
	}
	return &GuardedFetcher{guard: guard, client: client, maxBytes: 64 << 20}
}

// FetchManifest 见 plugin.ManifestFetcher。
func (f *GuardedFetcher) FetchManifest(ctx context.Context, url string) (*plugin.Manifest, []byte, error) {
	body, _, _, err := f.Fetch(ctx, url, nil)
	if err != nil {
		return nil, nil, err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, f.maxBytes))
	if err != nil {
		return nil, nil, platform.AsError(err)
	}
	var m plugin.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, platform.NewError(422, platform.CodeInvalidRequest, "插件清单不是合法 JSON").WithCause(err)
	}
	return &m, raw, nil
}

// FetchBundle 见 plugin.ManifestFetcher。
func (f *GuardedFetcher) FetchBundle(ctx context.Context, url string) ([]byte, string, error) {
	body, mime, _, err := f.Fetch(ctx, url, nil)
	if err != nil {
		return nil, "", err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, f.maxBytes))
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	return raw, mime, nil
}

// Fetch 见 wiring.URLFetcher：返回响应体、内容类型与长度。
func (f *GuardedFetcher) Fetch(ctx context.Context, url string, headers map[string]string) (io.ReadCloser, string, int64, error) {
	if err := f.guard.CheckURL(ctx, url); err != nil {
		return nil, "", 0, err
	}
	req, err := newGetRequest(ctx, url, headers)
	if err != nil {
		return nil, "", 0, platform.AsError(err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", 0, platform.AsError(err)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, "", 0, platform.NewError(502, platform.CodeUpstreamInvalid,
			fmt.Sprintf("上游返回 %d", resp.StatusCode))
	}
	size := resp.ContentLength
	if size <= 0 || size > f.maxBytes {
		size = -1 // 未知长度：下游按流式处理并使用自己的上限
	}
	return resp.Body, resp.Header.Get("Content-Type"), size, nil
}
