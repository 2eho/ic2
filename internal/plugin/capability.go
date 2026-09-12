package plugin

import (
	"errors"

	"github.com/context-flow/ic/internal/platform"
)

// HostMethod 是宿主暴露给插件的能力方法。
type HostMethod string

// 宿主方法枚举（见 docs/design/06 §5）。
const (
	MethodNodeGet         HostMethod = "node.get"
	MethodNodePatch       HostMethod = "node.patch"
	MethodNodeResize      HostMethod = "node.resize"
	MethodNodeEmit        HostMethod = "node.emit"
	MethodGraphUpstream   HostMethod = "graph.upstream"
	MethodGraphDownstream HostMethod = "graph.downstream"
	MethodGraphQuery      HostMethod = "graph.query"
	MethodAssetGetURL     HostMethod = "asset.getUrl"
	MethodAssetUpload     HostMethod = "asset.upload"
	MethodAIGenerate      HostMethod = "ai.generate"
	MethodStorageGet      HostMethod = "storage.get"
	MethodStorageSet      HostMethod = "storage.set"
	MethodStorageRemove   HostMethod = "storage.remove"
	MethodToast           HostMethod = "host.toast"
)

// methodPermission 把方法映射到所需权限。未知方法一律拒绝（INV-6）。
var methodPermission = map[HostMethod]string{
	MethodNodeGet:         PermNodeRead,
	MethodNodePatch:       PermNodeWrite,
	MethodNodeResize:      PermNodeWrite,
	MethodNodeEmit:        PermNodeWrite,
	MethodGraphUpstream:   PermNodeRead,
	MethodGraphDownstream: PermNodeRead,
	MethodGraphQuery:      PermGraphQuery,
	MethodAssetGetURL:     PermAssetRead,
	MethodAssetUpload:     PermAssetWrite,
	MethodAIGenerate:      PermAIGenerate,
	MethodStorageGet:      PermStorage,
	MethodStorageSet:      PermStorage,
	MethodStorageRemove:   PermStorage,
	MethodToast:           "", // 提示不需要权限
}

// ErrUnknownMethod 表示插件调用了未定义的宿主方法。
var ErrUnknownMethod = errors.New("unknown host method")

// CapabilityChecker 校验插件是否有权调用某方法。
type CapabilityChecker struct {
	Permissions []string
	// AllowedHosts 是网络白名单（仅 network 权限下生效）。
	AllowedHosts []string
	// MaxStorageBytes 是插件存储配额。
	MaxStorageBytes int64
}

// NewChecker 由清单构造校验器。
func NewChecker(m *Manifest) *CapabilityChecker {
	return &CapabilityChecker{
		Permissions:     m.Permissions,
		AllowedHosts:    m.Network,
		MaxStorageBytes: 1 << 20, // 1MB / 插件
	}
}

// Check 校验一次能力调用。返回值包含应记录的审计字段。
func (c *CapabilityChecker) Check(method HostMethod, capability string) error {
	need, known := methodPermission[method]
	if !known {
		return platform.NewError(403, platform.CodePluginPermission, "unknown host method").
			WithDetail("method", string(method))
	}
	if need == "" {
		return nil
	}
	// 带参数的能力（如 ai.generate:image）需要前缀匹配
	if need == PermAIGenerate && capability != "" {
		want := PermAIGenerate + ":" + capability
		for _, p := range c.Permissions {
			if p == want || p == PermAIGenerate {
				return nil
			}
		}
		return platform.NewError(403, platform.CodePluginPermission,
			"capability not declared in manifest").WithDetail("required", want)
	}
	for _, p := range c.Permissions {
		if p == need {
			return nil
		}
	}
	return platform.NewError(403, platform.CodePluginPermission,
		"permission not declared in manifest").WithDetail("required", need)
}

// CheckHost 校验插件是否可访问某主机（默认无网络；声明后仍需在 allowlist 内）。
func (c *CapabilityChecker) CheckHost(host string) error {
	hasNetwork := false
	for _, p := range c.Permissions {
		if p == PermNetwork {
			hasNetwork = true
		}
	}
	if !hasNetwork {
		return platform.NewError(403, platform.CodePluginPermission,
			"plugin has no network permission").WithDetail("host", host)
	}
	for _, h := range c.AllowedHosts {
		if h == host {
			return nil
		}
	}
	return platform.NewError(403, platform.CodePluginPermission,
		"host is not in the manifest allowlist").WithDetail("host", host)
}

// SandboxAttrs 返回 iframe 的 sandbox 属性。
//
// 关键：**绝不**加 allow-same-origin。否则插件与宿主同源，可读 Cookie/存储（INV-6）。
func SandboxAttrs() string {
	return "allow-scripts"
}

// CSPForPlugin 返回插件 iframe 的 CSP（默认无外部连接）。
func CSPForPlugin(allowedHosts []string) string {
	connect := "'none'"
	if len(allowedHosts) > 0 {
		parts := make([]string, 0, len(allowedHosts))
		for _, h := range allowedHosts {
			parts = append(parts, "https://"+h)
		}
		connect = joinSpace(parts)
	}
	// 注意：不放开 'unsafe-eval'；插件若需动态代码应改为构建期完成。
	return "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src " + connect
}

func joinSpace(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
