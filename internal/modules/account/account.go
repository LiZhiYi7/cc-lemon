// Package account 会计科目。
package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const PermWrite = "account:write"

type Account struct {
	ID        int64  `json:"id"`
	LedgerID  int64  `json:"ledger_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	FullName  string `json:"full_name"` // 含各级上级名称
	ParentID  *int64 `json:"parent_id"`
	Level     int    `json:"level"`
	Category  string `json:"category"`
	Direction string `json:"direction"`
	IsLeaf    bool   `json:"is_leaf"`
	IsCash    bool   `json:"is_cash"`
	Enabled   bool   `json:"enabled"`
	IsSystem  bool   `json:"is_system"`
}

type Service struct {
	db         *db.DB
	books      *book.Service
	onAddChild []func(ctx context.Context, parent *Account) error
}

// OnAddChild 注册“给某科目增加下级”前的检查（其他模块借此声明自己的约束，避免反向依赖）。
func (s *Service) OnAddChild(fn func(ctx context.Context, parent *Account) error) {
	s.onAddChild = append(s.onAddChild, fn)
}

func New(d *db.DB, books *book.Service) *Service { return &Service{db: d, books: books} }

const cols = `id, ledger_id, code, name, parent_id, level, category, direction, is_leaf, is_cash, enabled, is_system`

func scan(row pgx.Row) (*Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.LedgerID, &a.Code, &a.Name, &a.ParentID, &a.Level, &a.Category, &a.Direction,
		&a.IsLeaf, &a.IsCash, &a.Enabled, &a.IsSystem)
	return &a, err
}

// List 返回账套全部科目，按账簿、编码排序（上级必在下级之前）。
func (s *Service) List(ctx context.Context, bookID int64) ([]*Account, error) {
	list, err := db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (*Account, error) { return scan(r) },
		`select `+cols+` from accounts where book_id=$1 order by ledger_id, code`, bookID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Account, len(list))
	for _, a := range list {
		byID[a.ID] = a
		a.FullName = a.Name
		if a.ParentID != nil {
			if p := byID[*a.ParentID]; p != nil {
				a.FullName = p.FullName + "-" + a.Name
			}
		}
	}
	return list, nil
}

// Get 按 ID 取科目。
func (s *Service) Get(ctx context.Context, bookID, id int64) (*Account, error) {
	a, err := scan(s.db.Q(ctx).QueryRow(ctx, `select `+cols+` from accounts where book_id=$1 and id=$2`, bookID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("科目不存在")
	}
	return a, err
}

// Used 科目是否已有凭证分录或期初数据。
func (s *Service) Used(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := s.db.Q(ctx).QueryRow(ctx, `select exists(select 1 from voucher_lines where account_id=$1)
		or exists(select 1 from opening_balances where account_id=$1 and (balance<>0 or ytd_debit<>0 or ytd_credit<>0))`, id).Scan(&ok)
	return ok, err
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

type CreateInput struct {
	ParentID  *int64 `json:"parent_id"`
	LedgerID  int64  `json:"ledger_id"` // 新增一级科目时必填
	Category  string `json:"category"`  // 新增一级科目时必填
	Code      string `json:"code"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
	IsCash    *bool  `json:"is_cash"`
}

func (s *Service) Create(ctx context.Context, b *book.Book, in CreateInput) (*Account, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, httpx.BadRequest("科目名称不能为空")
	}
	scheme := b.Settings.AccountCodeScheme
	var out *Account
	err := s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		q := s.db.Q(ctx)
		a := Account{Code: in.Code, Name: in.Name, Direction: in.Direction, Level: 1}
		var parent *Account
		if in.ParentID != nil {
			var err error
			if parent, err = s.Get(ctx, b.ID, *in.ParentID); err != nil {
				return err
			}
			a.ParentID, a.LedgerID, a.Category, a.Level, a.IsCash = &parent.ID, parent.LedgerID, parent.Category, parent.Level+1, parent.IsCash
			if a.Direction == "" {
				a.Direction = parent.Direction
			}
			if a.Level > len(scheme) {
				return httpx.BadRequest(fmt.Sprintf("科目最多 %d 级", len(scheme)))
			}
			for _, check := range s.onAddChild {
				if err := check(ctx, parent); err != nil {
					return err
				}
			}
			if !strings.HasPrefix(a.Code, parent.Code) {
				return httpx.BadRequest("下级科目编码必须以上级编码 " + parent.Code + " 开头")
			}
		} else {
			a.LedgerID, a.Category = in.LedgerID, in.Category
			var ok bool
			if err := q.QueryRow(ctx, `select exists(select 1 from account_categories where book_id=$1 and ledger_id=$2 and code=$3)`,
				b.ID, in.LedgerID, in.Category).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return httpx.BadRequest("科目类别无效")
			}
		}
		if a.Direction != kernel.Debit && a.Direction != kernel.Credit {
			return httpx.BadRequest("请选择余额方向")
		}
		want := 0
		for _, seg := range scheme[:a.Level] {
			want += seg
		}
		if !isDigits(a.Code) || len(a.Code) != want {
			return httpx.BadRequest(fmt.Sprintf("%d 级科目编码应为 %d 位数字（编码方案 %v）", a.Level, want, scheme))
		}
		if in.IsCash != nil {
			a.IsCash = *in.IsCash
		}
		err := q.QueryRow(ctx, `insert into accounts(book_id, ledger_id, code, name, parent_id, level, category, direction, is_cash)
			values($1,$2,$3,$4,$5,$6,$7,$8,$9) returning id`,
			b.ID, a.LedgerID, a.Code, a.Name, a.ParentID, a.Level, a.Category, a.Direction, a.IsCash).Scan(&a.ID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httpx.Conflict("科目编码 " + a.Code + " 已存在")
		}
		if err != nil {
			return err
		}
		if parent != nil && parent.IsLeaf {
			// 上级首次增加下级：已有的分录与期初数据下沉到新科目，保持“只有末级科目记账”。
			if _, err := q.Exec(ctx, `update voucher_lines set account_id=$2 where account_id=$1`, parent.ID, a.ID); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `update opening_balances set account_id=$2 where account_id=$1`, parent.ID, a.ID); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `update accounts set is_leaf=false where id=$1`, parent.ID); err != nil {
				return err
			}
		}
		audit.Log(ctx, s.db, b.ID, "account.create", "account", a.ID, in)
		out, err = s.Get(ctx, b.ID, a.ID)
		return err
	})
	return out, err
}

type UpdateInput struct {
	Name      *string `json:"name"`
	Direction *string `json:"direction"`
	IsCash    *bool   `json:"is_cash"`
	Enabled   *bool   `json:"enabled"`
}

func (s *Service) Update(ctx context.Context, b *book.Book, id int64, in UpdateInput) (*Account, error) {
	var out *Account
	err := s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		a, err := s.Get(ctx, b.ID, id)
		if err != nil {
			return err
		}
		if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
			return httpx.BadRequest("科目名称不能为空")
		}
		if in.Direction != nil && *in.Direction != a.Direction {
			if *in.Direction != kernel.Debit && *in.Direction != kernel.Credit {
				return httpx.BadRequest("余额方向无效")
			}
			if !a.IsLeaf {
				return httpx.BadRequest("非末级科目不能修改余额方向")
			}
			if used, err := s.Used(ctx, id); err != nil {
				return err
			} else if used {
				return httpx.Conflict("科目已有数据，不能修改余额方向")
			}
		}
		if _, err := s.db.Q(ctx).Exec(ctx, `update accounts set name=coalesce($2,name), direction=coalesce($3,direction),
			is_cash=coalesce($4,is_cash), enabled=coalesce($5,enabled) where id=$1`, id, in.Name, in.Direction, in.IsCash, in.Enabled); err != nil {
			return err
		}
		audit.Log(ctx, s.db, b.ID, "account.update", "account", id, in)
		out, err = s.Get(ctx, b.ID, id)
		return err
	})
	return out, err
}

func (s *Service) Delete(ctx context.Context, b *book.Book, id int64) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		a, err := s.Get(ctx, b.ID, id)
		if err != nil {
			return err
		}
		if !a.IsLeaf {
			return httpx.Conflict("请先删除下级科目")
		}
		if used, err := s.Used(ctx, id); err != nil {
			return err
		} else if used {
			return httpx.Conflict("科目已有凭证或期初数据，不能删除（可改为停用）")
		}
		q := s.db.Q(ctx)
		if _, err := q.Exec(ctx, `delete from accounts where id=$1`, id); err != nil {
			return err
		}
		if a.ParentID != nil {
			if _, err := q.Exec(ctx, `update accounts set is_leaf = not exists(select 1 from accounts c where c.parent_id=$1) where id=$1`, *a.ParentID); err != nil {
				return err
			}
		}
		audit.Log(ctx, s.db, b.ID, "account.delete", "account", id, map[string]string{"code": a.Code, "name": a.Name})
		return nil
	})
}

func (s *Service) Routes(r chi.Router) {
	r.Get("/accounts", httpx.Handle(func(r *http.Request) (any, error) {
		return s.List(r.Context(), book.From(r.Context()).Book.ID)
	}))
	r.Group(func(r chi.Router) {
		r.Use(book.Require(PermWrite))
		r.Post("/accounts", httpx.Handle(func(r *http.Request) (any, error) {
			in, err := httpx.Bind[CreateInput](r)
			if err != nil {
				return nil, err
			}
			return s.Create(r.Context(), book.From(r.Context()).Book, in)
		}))
		r.Patch("/accounts/{accountID}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "accountID")
			if err != nil {
				return nil, err
			}
			in, err := httpx.Bind[UpdateInput](r)
			if err != nil {
				return nil, err
			}
			return s.Update(r.Context(), book.From(r.Context()).Book, id, in)
		}))
		r.Delete("/accounts/{accountID}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "accountID")
			if err != nil {
				return nil, err
			}
			return nil, s.Delete(r.Context(), book.From(r.Context()).Book, id)
		}))
	})
}
