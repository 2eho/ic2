package provider

import (
	"context"
	"io"
	"time"
)

// Credential 是解密后的凭据（只在 exec 调用瞬间存在，不落日志，INV-5）。
type Credential struct {
	ID string
	// ProviderID 是**协议名**（openai / gemini / script），用于在适配器注册表里查表。
	ProviderID string
	// SourceProviderID 是渠道行的 id（用户可自定义，如 `my-relay`）。
	//
	// 两个 id 必须分开：协议名决定「怎么发请求」，渠道 id 决定「用哪条配置」。
	// 合成一个会让自定义渠道永远找不到适配器 —— 而报错会是
	// 「没有可用凭据」，与真实原因无关。
	SourceProviderID string
	BaseURL          string
	AuthKind         string
	Secret           string
	Priority         int
	// Limits 是凭据级附加配置（明文 JSON），目前承载「自定义调用脚本」。
	//
	// 它必须随凭据一起解析出来，而不是只存库：脚本是**执行期**才需要的东西，
	// 而执行期唯一能拿到的就是这里的 Credential。曾经这里缺了它，
	// 症状是「脚本保存成功但从未生效」——而那时渠道照常返回 200 或
	// 「没有可用凭据」，看不出脚本没被读到（e2e 用例实测发现）。
	Limits map[string]any
}

// IntLimit 读一个整数型 limits 字段（缺失或类型不对时返回默认值）。
func (c Credential) IntLimit(key string, def int) int {
	v, ok := c.Limits[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return def
}

// Request 是一次上游调用请求。
type Request struct {
	Capability Capability
	Model      string
	Prompt     string
	// Inputs 是结构化的上游输入（不再依赖「文本N」这种脆弱约定，见 05 §2.3）。
	Inputs []ResolvedInput
	Params map[string]any
	Count  int
	// RequestID 作为幂等键传给上游（支持时），避免「服务端超时但已计费」重复扣费（INV-3）。
	RequestID string
	// RemoteTaskID 用于异步任务的续查（视频生成进程重启后继续轮询）。
	RemoteTaskID string
}

// ResolvedInput 是编译期解析出的输入快照。
type ResolvedInput struct {
	Kind    string
	Value   string
	AssetID string
	Label   string
	Origin  Origin
}

// Origin 记录输入来源，便于回溯。
type Origin struct {
	NodeID string `json:"nodeId"`
	PortID string `json:"portId"`
}

// Response 是一次上游调用的结果。
type Response struct {
	Assets     []AssetRef
	Text       string
	Usage      Usage
	RemoteTask *RemoteTask
	Raw        map[string]any
}

// AssetRef 是上游产出的资源引用（尚未入库）。
type AssetRef struct {
	Kind string
	// Bytes 为小体积内容（直接返回 base64 的图片）
	Bytes []byte
	MIME  string
	// URL 为大体积内容的下载地址
	URL string
}

// RemoteTask 描述异步任务（视频生成）。
type RemoteTask struct {
	ID       string
	Provider string
	Status   string
	Progress int
}

// Usage 是计量结果（整数微元，INV-9）。
type Usage struct {
	TextTokensIn  int64
	TextTokensOut int64
	Images        int64
	VideoMillis   int64
	AudioMillis   int64
	CostMicros    int64
}

// Add 累加计量。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		TextTokensIn:  u.TextTokensIn + o.TextTokensIn,
		TextTokensOut: u.TextTokensOut + o.TextTokensOut,
		Images:        u.Images + o.Images,
		VideoMillis:   u.VideoMillis + o.VideoMillis,
		AudioMillis:   u.AudioMillis + o.AudioMillis,
		CostMicros:    u.CostMicros + o.CostMicros,
	}
}

// Stream 是文本流式响应。
type Stream interface {
	Recv() (chunk string, done bool, err error)
	Close() error
}

// Adapter 是渠道适配器接口（解耦清单要求 ≥2 个实现：openai / gemini / script）。
type Adapter interface {
	ID() string
	Capabilities() []Capability
	Invoke(ctx context.Context, cred Credential, req Request) (Response, error)
	// Stream 用于文本生成（不支持则返回 ErrStreamUnsupported）。
	Stream(ctx context.Context, cred Credential, req Request) (Stream, error)
	// Poll 查询异步任务状态。
	Poll(ctx context.Context, cred Credential, taskID string) (RemoteTask, error)
	// ListModels 拉取模型列表。
	ListModels(ctx context.Context, cred Credential) ([]ModelInfo, error)
	// FetchAsset 下载上游产出的资源。
	FetchAsset(ctx context.Context, cred Credential, ref AssetRef) (io.ReadCloser, string, error)
}

// ModelInfo 是上游模型条目。
type ModelInfo struct {
	ID           string
	DisplayName  string
	Capabilities []Capability
}

// Pricing 是价格表（每百万 token / 每张图 / 每秒视频，单位微元）。
type Pricing struct {
	TextInputPerMTok  int64
	TextOutputPerMTok int64
	ImagePerUnit      int64
	VideoPerSecond    int64
	AudioPerSecond    int64
	Currency          string
	UpdatedAt         time.Time
}

// ErrStreamUnsupported 表示适配器不支持流式。
type ErrStreamUnsupported struct{ Adapter string }

// Error 实现 error。
func (e ErrStreamUnsupported) Error() string { return e.Adapter + " does not support streaming" }
