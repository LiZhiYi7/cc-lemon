// Package configs 内嵌默认配置：会计准则模板与角色权限。
// 运行时可通过外部目录/文件覆盖，详见 docs/architecture.md。
package configs

import "embed"

//go:embed standards/*.yaml
var Standards embed.FS

//go:embed roles.yaml
var Roles []byte
