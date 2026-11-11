// Package migrations 内嵌数据库迁移脚本，按文件名顺序执行。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
