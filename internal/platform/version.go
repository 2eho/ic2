// Package platform 提供跨模块的基础设施：配置、日志、ID、时钟、错误、HTTP 客户端、脱敏。
package platform

// 构建期由 -ldflags 注入。
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// BuildInfo 是 /api/v1/meta 的返回结构。
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Mode      string `json:"mode"`
}
