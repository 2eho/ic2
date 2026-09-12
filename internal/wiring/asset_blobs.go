// Package wiring 把各领域服务装配成可运行的应用。
//
// 为什么单独一层：上一轮的问题是「服务写好了但没接线」，API 层全是 501。
// 把装配集中到一个包，并让 `internal/wiring/app_test.go` 对**真实装配结果**做端到端断言，
// 可以让「忘了接线」在测试阶段就暴露，而不是等用户点上按钮才发现。
package wiring

import (
	"context"
	"io"

	"github.com/context-flow/ic/internal/asset"
	"github.com/context-flow/ic/internal/exec"
	"github.com/context-flow/ic/internal/graph"
)

// AssetBlobs 把 asset.Service 适配成 exec.BlobWriter：
// 执行引擎只关心「把字节/URL 变成资产 id」，不关心存储布局、去重或引用计数。
type AssetBlobs struct {
	assets *asset.Service
	// fetch 用于 StoreURL（下载上游产物），必须是带 SSRF 防护的客户端。
	fetch URLFetcher
}

// URLFetcher 抽象「按 URL 取字节」，便于注入带防护的实现与测试替身。
type URLFetcher interface {
	Fetch(ctx context.Context, url string, headers map[string]string) (io.ReadCloser, string, int64, error)
}

// NewAssetBlobs 构造适配器。
func NewAssetBlobs(assets *asset.Service, fetch URLFetcher) *AssetBlobs {
	return &AssetBlobs{assets: assets, fetch: fetch}
}

// StoreBytes 见 exec.BlobWriter。
func (a *AssetBlobs) StoreBytes(ctx context.Context, wsID, kind, mime, name string, r io.Reader, size int64, origin string, source map[string]string) (string, error) {
	dto, err := a.assets.Upload(ctx, wsID, name, mime, r, size)
	if err != nil {
		return "", err
	}
	return dto.ID, nil
}

// StoreURL 见 exec.BlobWriter：下载上游产物再入库。
// 走 URLFetcher（带 SSRF 防护）而不是 http.Get：上游返回的 URL 也可能指向内网。
func (a *AssetBlobs) StoreURL(ctx context.Context, wsID, kind, mime, name, url string, headers map[string]string, origin string, source map[string]string) (string, error) {
	if a.fetch == nil {
		return "", exec.ErrCanceled
	}
	body, contentType, size, err := a.fetch.Fetch(ctx, url, headers)
	if err != nil {
		return "", err
	}
	defer body.Close()
	if mime == "" {
		mime = contentType
	}
	dto, err := a.assets.Upload(ctx, wsID, name, mime, body, size)
	if err != nil {
		return "", err
	}
	return dto.ID, nil
}

// AssetReaderImpl 把 asset.Service 适配成 exec.AssetReader。
type AssetReaderImpl struct{ assets *asset.Service }

// NewAssetReader 构造读取适配器。
func NewAssetReader(a *asset.Service) *AssetReaderImpl { return &AssetReaderImpl{assets: a} }

// ReadAsset 见 exec.AssetReader。
func (r *AssetReaderImpl) ReadAsset(ctx context.Context, wsID, assetID string) ([]byte, string, error) {
	rc, _, mime, err := r.assets.Open(ctx, wsID, assetID)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, asset.MaxUploadBytes))
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

// CanvasWriterImpl 把 graph.Service 适配成 exec.CanvasWriter：
// 结果回写必须走标准 op 路径，从而天然带上校验、版本推进与实时广播。
type CanvasWriterImpl struct{ graph *graph.Service }

// NewCanvasWriter 构造回写适配器。
func NewCanvasWriter(g *graph.Service) *CanvasWriterImpl { return &CanvasWriterImpl{graph: g} }

// WriteBack 见 exec.CanvasWriter。
func (w *CanvasWriterImpl) WriteBack(ctx context.Context, canvasID, nodeID string, result graph.NodeResult, state graph.NodeState, actor string) error {
	ops, err := graph.EncodeWriteBackOps(nodeID, result, state)
	if err != nil {
		return err
	}
	doc, err := w.graph.Get(ctx, canvasID)
	if err != nil {
		return err
	}
	_, _, err = w.graph.AppendOps(ctx, canvasID, doc.Version, ops, actor)
	return err
}

// Document 让执行引擎在重放时能读到权威画布文档。
// 之所以由写入侧提供读取能力：两者都需要「同一份权威文档」，
// 分两个依赖注入容易出现「一个指向缓存、一个指向数据库」的不一致。
func (w *CanvasWriterImpl) Document(ctx context.Context, canvasID string) (*graph.CanvasDocument, error) {
	return w.graph.Get(ctx, canvasID)
}
