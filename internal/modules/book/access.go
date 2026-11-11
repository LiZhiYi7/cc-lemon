package book

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/auth"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

// PermAdmin 账套管理权限（设置、成员、删除）。
const PermAdmin = "book:admin"

// Access 一次请求的账套上下文。
type Access struct {
	Book  *Book
	Role  string
	perms []string
}

func (a *Access) Can(perm string) bool {
	for _, p := range a.perms {
		if p == "*" || p == perm {
			return true
		}
	}
	return false
}

type accessKey struct{}

// From 取出账套上下文；只能在 Middleware 之后的处理链中调用。
func From(ctx context.Context) *Access { return ctx.Value(accessKey{}).(*Access) }

// Middleware 解析 URL 中的 {bookID}，校验成员身份并注入账套上下文。
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, err := httpx.ParamInt64(r, "bookID")
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		b, err := s.Get(ctx, id)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		u := auth.CurrentUser(ctx)
		acc := &Access{Book: b}
		err = s.db.Q(ctx).QueryRow(ctx, `select role from book_members where book_id=$1 and user_id=$2`, id, u.ID).Scan(&acc.Role)
		switch {
		case err == nil:
			if role, ok := s.role(acc.Role); ok {
				acc.perms = role.Permissions
			}
		case errors.Is(err, pgx.ErrNoRows) && u.IsAdmin:
			acc.Role = s.roles[0].Code
		case errors.Is(err, pgx.ErrNoRows):
			httpx.WriteError(w, r, httpx.Forbidden("无权访问该账套"))
			return
		default:
			httpx.WriteError(w, r, err)
			return
		}
		if u.IsAdmin {
			acc.perms = []string{"*"}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, accessKey{}, acc)))
	})
}

// Require 声明路由所需权限点。
func Require(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !From(r.Context()).Can(perm) {
				httpx.WriteError(w, r, httpx.Forbidden("当前角色无此操作权限（"+perm+"）"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type Member struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

func (s *Service) Members(ctx context.Context, bookID int64) ([]Member, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (Member, error) {
		var m Member
		return m, r.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Role)
	}, `select u.id, u.username, u.display_name, m.role from book_members m join users u on u.id=m.user_id
		where m.book_id=$1 order by u.id`, bookID)
}

func (s *Service) ensureOwnerRemains(ctx context.Context, bookID int64) error {
	var n int
	if err := s.db.Q(ctx).QueryRow(ctx, `select count(*) from book_members where book_id=$1 and role=$2`, bookID, s.roles[0].Code).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return httpx.BadRequest("账套至少保留一名" + s.roles[0].Name)
	}
	return nil
}

func (s *Service) PutMember(ctx context.Context, bookID, userID int64, role string) error {
	if _, ok := s.role(role); !ok {
		return httpx.BadRequest("未知角色: " + role)
	}
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.Lock(ctx, bookID); err != nil {
			return err
		}
		if _, err := s.db.Q(ctx).Exec(ctx, `insert into book_members(book_id, user_id, role) values($1,$2,$3)
			on conflict (book_id, user_id) do update set role=excluded.role`, bookID, userID, role); err != nil {
			return httpx.BadRequest("用户不存在")
		}
		audit.Log(ctx, s.db, bookID, "member.put", "user", userID, map[string]string{"role": role})
		return s.ensureOwnerRemains(ctx, bookID)
	})
}

func (s *Service) RemoveMember(ctx context.Context, bookID, userID int64) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.Lock(ctx, bookID); err != nil {
			return err
		}
		if _, err := s.db.Q(ctx).Exec(ctx, `delete from book_members where book_id=$1 and user_id=$2`, bookID, userID); err != nil {
			return err
		}
		audit.Log(ctx, s.db, bookID, "member.remove", "user", userID, nil)
		return s.ensureOwnerRemains(ctx, bookID)
	})
}

// Detail 账套详情：前端进入账套后一次取齐的基础数据。
type Detail struct {
	*Book
	StandardName  string        `json:"standard_name"`
	Ledgers       []Ledger      `json:"ledgers"`
	Categories    []Category    `json:"categories"`
	VoucherWords  []VoucherWord `json:"voucher_words"`
	CurrentPeriod kernel.Period `json:"current_period"`
	Role          string        `json:"role"`
	Permissions   []string      `json:"permissions"`
}

func (s *Service) Detail(ctx context.Context) (*Detail, error) {
	acc := From(ctx)
	d := &Detail{Book: acc.Book, Role: acc.Role, Permissions: acc.perms}
	if d.Permissions == nil {
		d.Permissions = []string{}
	}
	if std, ok := s.standards.Get(acc.Book.StandardCode); ok {
		d.StandardName = std.Name
	}
	var err error
	if d.Ledgers, err = s.Ledgers(ctx, acc.Book.ID); err != nil {
		return nil, err
	}
	if d.Categories, err = s.Categories(ctx, acc.Book.ID); err != nil {
		return nil, err
	}
	if d.VoucherWords, err = s.VoucherWords(ctx, acc.Book.ID); err != nil {
		return nil, err
	}
	d.CurrentPeriod, err = s.CurrentPeriod(ctx, acc.Book)
	return d, err
}

// GlobalRoutes 不属于某个账套的接口。
func (s *Service) GlobalRoutes(r chi.Router) {
	r.Get("/standards", httpx.Handle(func(*http.Request) (any, error) {
		list := s.standards.List()
		if list == nil {
			list = []*standard.Standard{}
		}
		return list, nil
	}))
	r.Get("/roles", httpx.Handle(func(*http.Request) (any, error) { return s.roles, nil }))
	r.Get("/books", httpx.Handle(func(r *http.Request) (any, error) { return s.List(r.Context()) }))
	r.Post("/books", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[CreateInput](r)
		if err != nil {
			return nil, err
		}
		return s.Create(r.Context(), in)
	}))
}

// Routes 挂在 /books/{bookID} 下（已过 Middleware）。
func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(func(r *http.Request) (any, error) { return s.Detail(r.Context()) }))
	r.Group(func(r chi.Router) {
		r.Use(Require(PermAdmin))
		r.Patch("/", httpx.Handle(func(r *http.Request) (any, error) {
			in, err := httpx.Bind[UpdateInput](r)
			if err != nil {
				return nil, err
			}
			return s.Update(r.Context(), From(r.Context()).Book.ID, in)
		}))
		r.Delete("/", httpx.Handle(func(r *http.Request) (any, error) {
			b := From(r.Context()).Book
			if r.URL.Query().Get("confirm") != b.Name {
				return nil, httpx.BadRequest("请输入账套名称以确认删除")
			}
			return nil, s.Delete(r.Context(), b.ID)
		}))
		r.Get("/members", httpx.Handle(func(r *http.Request) (any, error) {
			return s.Members(r.Context(), From(r.Context()).Book.ID)
		}))
		r.Put("/members/{userID}", httpx.Handle(func(r *http.Request) (any, error) {
			uid, err := httpx.ParamInt64(r, "userID")
			if err != nil {
				return nil, err
			}
			in, err := httpx.Bind[struct {
				Role string `json:"role"`
			}](r)
			if err != nil {
				return nil, err
			}
			return nil, s.PutMember(r.Context(), From(r.Context()).Book.ID, uid, in.Role)
		}))
		r.Delete("/members/{userID}", httpx.Handle(func(r *http.Request) (any, error) {
			uid, err := httpx.ParamInt64(r, "userID")
			if err != nil {
				return nil, err
			}
			return nil, s.RemoveMember(r.Context(), From(r.Context()).Book.ID, uid)
		}))
	})
}
