package platform

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// NetGuard 实现出网目标校验：默认禁止私网 / 回环 / 链路本地 / 元数据地址（ATK-04）。
// 自部署可配置白名单放开内网（例如接自建中转站）。
type NetGuard struct {
	// AllowPrivate 放开私网（自部署场景）。
	AllowPrivate bool
	// ExtraAllow 允许的额外主机（精确匹配 host:port 或 host）。
	ExtraAllow []string
	// Resolver 便于测试注入。
	Resolver *net.Resolver
}

// NewNetGuard 默认策略：禁私网。
func NewNetGuard() *NetGuard { return &NetGuard{Resolver: net.DefaultResolver} }

func (g *NetGuard) allowedHost(host string) bool {
	h := strings.ToLower(host)
	for _, a := range g.ExtraAllow {
		if strings.ToLower(strings.TrimSpace(a)) == h {
			return true
		}
	}
	return false
}

// CheckURL 校验目标 URL 是否可以访问。
func (g *NetGuard) CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return NewError(400, CodeInvalidRequest, "url is not parseable").WithCause(err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return NewError(400, CodeInvalidRequest, "only http/https is allowed").WithDetail("scheme", u.Scheme)
	}
	if u.User != nil {
		return NewError(400, CodeInvalidRequest, "userinfo in url is not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return NewError(400, CodeInvalidRequest, "url has no host")
	}
	if g.allowedHost(host) || g.allowedHost(u.Host) {
		return nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		if g.AllowPrivate {
			return nil
		}
		return ssrfErr("localhost")
	}
	ips, err := g.lookup(ctx, host)
	if err != nil {
		return NewError(400, CodeInvalidRequest, "dns lookup failed").WithDetail("host", host).WithCause(err)
	}
	if len(ips) == 0 {
		return NewError(400, CodeInvalidRequest, "dns lookup returned no address").WithDetail("host", host)
	}
	for _, ip := range ips {
		if err := g.checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

func (g *NetGuard) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	r := g.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := r.LookupNetIP(cctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Unmap())
	}
	return out, nil
}

func (g *NetGuard) checkIP(ip netip.Addr) error {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		if g.AllowPrivate && ip.IsLoopback() {
			return nil
		}
		return ssrfErr(ip.String())
	}
	if ip.IsPrivate() && !g.AllowPrivate {
		return ssrfErr(ip.String())
	}
	// 云元数据地址显式兜底（IPv4 链路本地已被 IsLinkLocalUnicast 覆盖，
	// 但 100.100.100.200 等运营商/云厂商元数据段并非标准私网段）。
	if isCloudMetadata(ip) && !g.AllowPrivate {
		return ssrfErr(ip.String())
	}
	return nil
}

// metadataAddrs 覆盖常见云厂商元数据地址（阿里云/腾讯云 100.100.100.200、
// AWS/GCP/Azure 169.254.169.254、GCP 的 metadata.google.internal 解析段）。
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("169.254.170.2"),
}

// metadataPrefixes 是元数据地址所在的网段。
var metadataPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.100.100.0/24"),
	netip.MustParsePrefix("169.254.0.0/16"),
}

func isCloudMetadata(ip netip.Addr) bool {
	for _, a := range metadataAddrs {
		if ip == a {
			return true
		}
	}
	for _, p := range metadataPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func ssrfErr(target string) *DomainError {
	return NewError(http.StatusForbidden, CodeSSRFBlocked, "target address is blocked by SSRF policy").WithDetail("target", target)
}

// NewGuardedClient 返回一个禁止自动跟随重定向到私网的 HTTP 客户端。
// 每跳重新校验，避免「白名单域名 302 到内网」绕过（见 11 §2.6）。
func NewGuardedClient(g *NetGuard, base *http.Client, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	if base != nil {
		cp := *base
		c = &cp
		if c.Timeout == 0 {
			c.Timeout = timeout
		}
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		if err := g.CheckURL(req.Context(), req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	return c
}
