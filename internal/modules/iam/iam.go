// Package iam 用户与登录认证。
package iam

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/auth"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const minPasswordLen = 6

type Service struct {
	db     *db.DB
	tokens *auth.Tokens
}

// New 创建服务。secret 为空时使用数据库中保存的密钥（首次启动自动生成）。
func New(ctx context.Context, d *db.DB, secret string, ttl time.Duration) (*Service, error) {
	if secret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if _, err := d.Pool.Exec(ctx, `insert into system_kv(key, value) values('jwt_secret', $1) on conflict do nothing`,
			hex.EncodeToString(b)); err != nil {
			return nil, err
		}
		if err := d.Pool.QueryRow(ctx, `select value from system_kv where key='jwt_secret'`).Scan(&secret); err != nil {
			return nil, err
		}
	}
	return &Service{db: d, tokens: auth.NewTokens(secret, ttl)}, nil
}

const userCols = `id, username, display_name, is_admin, enabled`

func scanUser(row pgx.Row) (*auth.User, error) {
	var u auth.User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.Enabled)
	return &u, err
}

func (s *Service) Initialized(ctx context.Context) (bool, error) {
	var ok bool
	err := s.db.Q(ctx).QueryRow(ctx, `select exists(select 1 from users)`).Scan(&ok)
	return ok, err
}

type UserInput struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
	Enabled     *bool  `json:"enabled"`
}

func (s *Service) CreateUser(ctx context.Context, in UserInput) (*auth.User, error) {
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" {
		return nil, httpx.BadRequest("用户名不能为空")
	}
	if len(in.Password) < minPasswordLen {
		return nil, httpx.BadRequest("密码至少 6 位")
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u, err := scanUser(s.db.Q(ctx).QueryRow(ctx,
		`insert into users(username, password_hash, display_name, is_admin) values($1,$2,$3,$4)
		 on conflict (username) do nothing returning `+userCols,
		in.Username, hash, in.DisplayName, in.IsAdmin))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.Conflict("用户名已存在")
	}
	return u, err
}

// Setup 创建首个管理员；系统已初始化后拒绝。
func (s *Service) Setup(ctx context.Context, in UserInput) (*auth.User, error) {
	var u *auth.User
	err := s.db.Tx(ctx, func(ctx context.Context) error {
		if _, err := s.db.Q(ctx).Exec(ctx, `lock table users in exclusive mode`); err != nil {
			return err
		}
		if ok, err := s.Initialized(ctx); err != nil {
			return err
		} else if ok {
			return httpx.Conflict("系统已初始化")
		}
		in.IsAdmin = true
		var err error
		u, err = s.CreateUser(ctx, in)
		return err
	})
	return u, err
}

type LoginResult struct {
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
	User      *auth.User `json:"user"`
}

func (s *Service) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	var hash string
	var u auth.User
	err := s.db.Q(ctx).QueryRow(ctx, `select `+userCols+`, password_hash from users where username=$1`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.Enabled, &hash)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !auth.CheckPassword(hash, password)) {
		return nil, httpx.Unauthorized("用户名或密码错误")
	}
	if err != nil {
		return nil, err
	}
	if !u.Enabled {
		return nil, httpx.Forbidden("账号已停用")
	}
	tok, exp, err := s.tokens.Issue(u.ID)
	if err != nil {
		return nil, err
	}
	audit.Log(auth.WithUser(ctx, &u), s.db, 0, "login", "user", u.ID, nil)
	return &LoginResult{Token: tok, ExpiresAt: exp, User: &u}, nil
}

func (s *Service) ListUsers(ctx context.Context) ([]*auth.User, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (*auth.User, error) { return scanUser(r) },
		`select `+userCols+` from users order by id`)
}

type UserPatch struct {
	DisplayName *string `json:"display_name"`
	IsAdmin     *bool   `json:"is_admin"`
	Enabled     *bool   `json:"enabled"`
	Password    *string `json:"password"`
}

func (s *Service) UpdateUser(ctx context.Context, id int64, p UserPatch) (*auth.User, error) {
	me := auth.CurrentUser(ctx)
	if id == me.ID && ((p.IsAdmin != nil && !*p.IsAdmin) || (p.Enabled != nil && !*p.Enabled)) {
		return nil, httpx.BadRequest("不能取消自己的管理员身份或停用自己")
	}
	var hash *string
	if p.Password != nil {
		if len(*p.Password) < minPasswordLen {
			return nil, httpx.BadRequest("密码至少 6 位")
		}
		h, err := auth.HashPassword(*p.Password)
		if err != nil {
			return nil, err
		}
		hash = &h
	}
	u, err := scanUser(s.db.Q(ctx).QueryRow(ctx, `update users set
		display_name = coalesce($2, display_name), is_admin = coalesce($3, is_admin),
		enabled = coalesce($4, enabled), password_hash = coalesce($5, password_hash)
		where id=$1 returning `+userCols, id, p.DisplayName, p.IsAdmin, p.Enabled, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("用户不存在")
	}
	if err == nil {
		audit.Log(ctx, s.db, 0, "user.update", "user", id, map[string]any{"password_reset": hash != nil})
	}
	return u, err
}

func (s *Service) ChangePassword(ctx context.Context, oldPw, newPw string) error {
	me := auth.CurrentUser(ctx)
	var hash string
	if err := s.db.Q(ctx).QueryRow(ctx, `select password_hash from users where id=$1`, me.ID).Scan(&hash); err != nil {
		return err
	}
	if !auth.CheckPassword(hash, oldPw) {
		return httpx.BadRequest("原密码不正确")
	}
	_, err := s.UpdateUser(ctx, me.ID, UserPatch{Password: &newPw})
	return err
}

// Authenticate 校验 Bearer 令牌并把用户放入上下文。
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			httpx.WriteError(w, r, httpx.Unauthorized("请先登录"))
			return
		}
		id, err := s.tokens.Verify(tok)
		if err != nil {
			httpx.WriteError(w, r, httpx.Unauthorized("登录已过期，请重新登录"))
			return
		}
		u, err := scanUser(s.db.Pool.QueryRow(r.Context(), `select `+userCols+` from users where id=$1`, id))
		if err != nil || !u.Enabled {
			httpx.WriteError(w, r, httpx.Unauthorized("账号不可用"))
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.CurrentUser(r.Context()).IsAdmin {
			httpx.WriteError(w, r, httpx.Forbidden("需要系统管理员权限"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PublicRoutes 无需登录的接口。
func (s *Service) PublicRoutes(r chi.Router) {
	r.Get("/setup", httpx.Handle(func(r *http.Request) (any, error) {
		ok, err := s.Initialized(r.Context())
		return map[string]bool{"initialized": ok}, err
	}))
	r.Post("/setup", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[UserInput](r)
		if err != nil {
			return nil, err
		}
		return s.Setup(r.Context(), in)
	}))
	r.Post("/auth/login", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[UserInput](r)
		if err != nil {
			return nil, err
		}
		return s.Login(r.Context(), in.Username, in.Password)
	}))
}

// Routes 需登录的接口。
func (s *Service) Routes(r chi.Router) {
	r.Get("/auth/me", httpx.Handle(func(r *http.Request) (any, error) {
		return auth.CurrentUser(r.Context()), nil
	}))
	r.Post("/auth/password", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			Old string `json:"old"`
			New string `json:"new"`
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.ChangePassword(r.Context(), in.Old, in.New)
	}))
	// 用户列表供账套成员选择，任何登录用户可读；增改仅管理员。
	r.Get("/users", httpx.Handle(func(r *http.Request) (any, error) { return s.ListUsers(r.Context()) }))
	r.Group(func(r chi.Router) {
		r.Use(RequireAdmin)
		r.Post("/users", httpx.Handle(func(r *http.Request) (any, error) {
			in, err := httpx.Bind[UserInput](r)
			if err != nil {
				return nil, err
			}
			return s.CreateUser(r.Context(), in)
		}))
		r.Patch("/users/{userID}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "userID")
			if err != nil {
				return nil, err
			}
			p, err := httpx.Bind[UserPatch](r)
			if err != nil {
				return nil, err
			}
			return s.UpdateUser(r.Context(), id, p)
		}))
	})
}
