package provider

import (
	"context"
	"io"
	"time"
)

// Credential 是解密后的凭据（只在 exec 调用瞬间存在，不落日志，INV-5）。
type Credential struct {
	ID         string
	ProviderID string
	BaseURL    string
	AuthKind   string
	Secret     string
	Priority   int
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
