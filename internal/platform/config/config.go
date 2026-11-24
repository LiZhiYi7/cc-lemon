// Package config 负责加载运行配置：内置默认值 → YAML 文件 → 环境变量（YANGHE_FINANCE_ 前缀）。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Addr      string `yaml:"addr"`
		StaticDir string `yaml:"static_dir"` // 前端静态文件目录，空则只提供 API
	} `yaml:"server"`
	Database struct {
		DSN      string `yaml:"dsn"`
		MaxConns int    `yaml:"max_conns"`
	} `yaml:"database"`
	Auth struct {
		JWTSecret string        `yaml:"jwt_secret"` // 留空则首次启动自动生成并保存在数据库
		TokenTTL  time.Duration `yaml:"token_ttl"`
		RolesFile string        `yaml:"roles_file"` // 自定义角色权限文件，留空用内置 configs/roles.yaml
	} `yaml:"auth"`
	Standards struct {
		Dir string `yaml:"dir"` // 额外的会计准则模板目录（覆盖/追加内置模板）
		// Allow 限定可用的准则编码（留空=全部）。交付给只使用某一种准则的客户时，
		// 用它把建账选项收敛到实际需要的范围，避免误选。
		Allow []string `yaml:"allow"`
	} `yaml:"standards"`
}

func Load() (*Config, error) {
	c := &Config{}
	c.Server.Addr = ":8080"
	c.Database.DSN = "postgres://yanghe_finance:yanghe_finance@localhost:5432/yanghe_finance?sslmode=disable"
	c.Database.MaxConns = 10
	c.Auth.TokenTTL = 12 * time.Hour

	path := os.Getenv("YANGHE_FINANCE_CONFIG")
	if path == "" {
		if _, err := os.Stat("config.yaml"); err == nil {
			path = "config.yaml"
		}
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件: %w", err)
		}
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
		}
	}

	str := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	str("YANGHE_FINANCE_SERVER_ADDR", &c.Server.Addr)
	str("YANGHE_FINANCE_SERVER_STATIC_DIR", &c.Server.StaticDir)
	str("YANGHE_FINANCE_DATABASE_DSN", &c.Database.DSN)
	str("YANGHE_FINANCE_AUTH_JWT_SECRET", &c.Auth.JWTSecret)
	str("YANGHE_FINANCE_STANDARDS_DIR", &c.Standards.Dir)
	if v := os.Getenv("YANGHE_FINANCE_STANDARDS_ALLOW"); v != "" {
		allowed := c.Standards.Allow[:0]
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				allowed = append(allowed, part)
			}
		}
		c.Standards.Allow = allowed
	}
	str("YANGHE_FINANCE_AUTH_ROLES_FILE", &c.Auth.RolesFile)
	if v := os.Getenv("YANGHE_FINANCE_DATABASE_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("YANGHE_FINANCE_DATABASE_MAX_CONNS: %w", err)
		}
		c.Database.MaxConns = n
	}
	if v := os.Getenv("YANGHE_FINANCE_AUTH_TOKEN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("YANGHE_FINANCE_AUTH_TOKEN_TTL: %w", err)
		}
		c.Auth.TokenTTL = d
	}
	return c, nil
}
