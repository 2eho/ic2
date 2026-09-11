// Package migrations 内嵌迁移脚本，供无文件系统的部署形态使用。
package migrations

import "embed"

// FS 是迁移脚本集合。
//
//go:embed *.sql
var FS embed.FS
