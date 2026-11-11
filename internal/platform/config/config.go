// Package config 负责加载运行配置：内置默认值 → YAML 文件 → 环境变量（LEMON_ 前缀）。
package config

import (
	"fmt"
	"os"
	"strconv"
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
	} `yaml:"standards"`
}

func Load() (*Config, error) {
	c := &Config{}
	c.Server.Addr = ":8080"
	c.Database.DSN = "postgres://lemon:lemon@localhost:5432/lemon?sslmode=disable"
	c.Database.MaxConns = 10
	c.Auth.TokenTTL = 12 * time.Hour

	path := os.Getenv("LEMON_CONFIG")
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
	str("LEMON_SERVER_ADDR", &c.Server.Addr)
	str("LEMON_SERVER_STATIC_DIR", &c.Server.StaticDir)
	str("LEMON_DATABASE_DSN", &c.Database.DSN)
	str("LEMON_AUTH_JWT_SECRET", &c.Auth.JWTSecret)
	str("LEMON_STANDARDS_DIR", &c.Standards.Dir)
	str("LEMON_AUTH_ROLES_FILE", &c.Auth.RolesFile)
	if v := os.Getenv("LEMON_DATABASE_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("LEMON_DATABASE_MAX_CONNS: %w", err)
		}
		c.Database.MaxConns = n
	}
	if v := os.Getenv("LEMON_AUTH_TOKEN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("LEMON_AUTH_TOKEN_TTL: %w", err)
		}
		c.Auth.TokenTTL = d
	}
	return c, nil
}
