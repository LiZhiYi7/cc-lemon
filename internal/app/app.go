// Package app 负责装配：创建各模块并注册路由。模块间依赖只在这里连线。
package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/lizhiyi7/cc-lemon/configs"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/modules/closing"
	"github.com/lizhiyi7/cc-lemon/internal/modules/iam"
	"github.com/lizhiyi7/cc-lemon/internal/modules/ledger"
	"github.com/lizhiyi7/cc-lemon/internal/modules/report"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/modules/voucher"
	"github.com/lizhiyi7/cc-lemon/internal/platform/config"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
	"github.com/lizhiyi7/cc-lemon/migrations"
)

// New 执行数据库迁移、装配模块并返回 HTTP 处理器。
func New(ctx context.Context, cfg *config.Config, d *db.DB) (http.Handler, error) {
	if err := d.Migrate(ctx, migrations.FS); err != nil {
		return nil, err
	}
	standards, err := standard.Load(configs.Standards, cfg.Standards.Dir)
	if err != nil {
		return nil, err
	}
	users, err := iam.New(ctx, d, cfg.Auth.JWTSecret, cfg.Auth.TokenTTL)
	if err != nil {
		return nil, err
	}
	roles := configs.Roles
	if cfg.Auth.RolesFile != "" {
		if roles, err = os.ReadFile(cfg.Auth.RolesFile); err != nil {
			return nil, fmt.Errorf("读取角色配置: %w", err)
		}
	}
	books, err := book.New(d, standards, roles)
	if err != nil {
		return nil, err
	}
	accounts := account.New(d, books)
	aux := auxiliary.New(d, books, accounts)
	if err := aux.SeedMissing(ctx, standards); err != nil {
		return nil, err
	}
	ledgers := ledger.New(d, books, accounts, aux)
	vouchers := voucher.New(d, books, accounts, aux)
	closings := closing.New(d, books, ledgers, vouchers)
	reports := report.New(d, books, ledgers, closings)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	r.Route("/api/v1", func(r chi.Router) {
		users.PublicRoutes(r)
		r.Group(func(r chi.Router) {
			r.Use(users.Authenticate)
			users.Routes(r)
			books.GlobalRoutes(r)
			// 账套内的业务模块：新增模块在此挂载其 Routes 即可。
			r.Route("/books/{bookID}", func(r chi.Router) {
				r.Use(books.Middleware)
				books.Routes(r)
				accounts.Routes(r)
				aux.Routes(r)
				ledgers.Routes(r)
				vouchers.Routes(r)
				closings.Routes(r)
				reports.Routes(r)
			})
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteError(w, r, httpx.NotFound("接口不存在"))
		})
	})
	if cfg.Server.StaticDir != "" {
		r.NotFound(spa(cfg.Server.StaticDir))
	}
	return r, nil
}

// spa 提供前端静态文件；未命中的路径回退到 index.html（前端路由）。
func spa(dir string) http.HandlerFunc {
	files := http.FileServer(http.Dir(dir))
	return func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	}
}
