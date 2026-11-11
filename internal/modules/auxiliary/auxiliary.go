// Package auxiliary 辅助核算：类别（客户、供应商、部门…及自定义）、项目、科目与类别的绑定。
//
// 科目启用某类别后，该科目的每条分录、每笔期初都必须指定该类别下的一个项目。
// 本包只管“档案与绑定”；分录/期初上的项目由 voucher、ledger 各自保存，并通过本包校验。
package auxiliary

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const PermWrite = "aux:write"

type Type struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsSystem bool   `json:"is_system"`
}

type Item struct {
	ID      int64  `json:"id"`
	TypeID  int64  `json:"type_id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Remark  string `json:"remark"`
}

// Ref 一条分录或期初上的辅助核算取值。读取时附带项目编码与名称。
type Ref struct {
	TypeID   int64  `json:"type_id"`
	ItemID   int64  `json:"item_id"`
	ItemCode string `json:"item_code,omitempty"`
	ItemName string `json:"item_name,omitempty"`
}

type Service struct {
	db       *db.DB
	books    *book.Service
	accounts *account.Service
}

func New(d *db.DB, books *book.Service, accounts *account.Service) *Service {
	s := &Service{db: d, books: books, accounts: accounts}
	// 启用辅助核算的科目按项目记账，不能再拆下级。
	accounts.OnAddChild(func(ctx context.Context, parent *account.Account) error {
		var bound bool
		if err := d.Q(ctx).QueryRow(ctx, `select exists(select 1 from account_aux_types where account_id=$1)`, parent.ID).Scan(&bound); err != nil {
			return err
		}
		if bound {
			return httpx.Conflict(fmt.Sprintf("科目 %s %s 已启用辅助核算，不能增加下级科目", parent.Code, parent.Name))
		}
		return nil
	})
	return s
}

// SeedMissing 为还没有任何辅助核算类别的账套补上准则模板中的预置类别（升级旧账套用）。
func (s *Service) SeedMissing(ctx context.Context, standards *standard.Registry) error {
	type bk struct {
		id  int64
		std string
	}
	list, err := db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (bk, error) {
		var b bk
		return b, r.Scan(&b.id, &b.std)
	}, `select id, standard_code from books b where not exists(select 1 from aux_types t where t.book_id=b.id)`)
	if err != nil {
		return err
	}
	for _, b := range list {
		std, ok := standards.Get(b.std)
		if !ok {
			continue
		}
		for i, t := range std.AuxTypes {
			if _, err := s.db.Q(ctx).Exec(ctx, `insert into aux_types(book_id, code, name, sort, is_system) values($1,$2,$3,$4,true)
				on conflict do nothing`, b.id, t.Code, t.Name, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) Types(ctx context.Context, bookID int64) ([]Type, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (Type, error) {
		var t Type
		return t, r.Scan(&t.ID, &t.Code, &t.Name, &t.IsSystem)
	}, `select id, code, name, is_system from aux_types where book_id=$1 order by sort, id`, bookID)
}

func (s *Service) Items(ctx context.Context, bookID int64) ([]Item, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (Item, error) {
		var it Item
		return it, r.Scan(&it.ID, &it.TypeID, &it.Code, &it.Name, &it.Enabled, &it.Remark)
	}, `select id, type_id, code, name, enabled, remark from aux_items where book_id=$1 order by type_id, code`, bookID)
}

// Bindings 科目 ID → 启用的类别 ID（按类别排序）。
func (s *Service) Bindings(ctx context.Context, bookID int64) (map[int64][]int64, error) {
	rows, err := s.db.Q(ctx).Query(ctx, `select b.account_id, b.type_id from account_aux_types b
		join aux_types t on t.id=b.type_id where t.book_id=$1 order by t.sort, t.id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var a, t int64
		if err := rows.Scan(&a, &t); err != nil {
			return nil, err
		}
		out[a] = append(out[a], t)
	}
	return out, rows.Err()
}

// Checker 校验分录/期初上的辅助核算取值。一次构建，多行复用。
type Checker struct {
	bindings map[int64][]int64
	items    map[int64]*Item
	types    map[int64]string
}

func (s *Service) Checker(ctx context.Context, bookID int64) (*Checker, error) {
	c := &Checker{items: map[int64]*Item{}, types: map[int64]string{}}
	var err error
	if c.bindings, err = s.Bindings(ctx, bookID); err != nil {
		return nil, err
	}
	items, err := s.Items(ctx, bookID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		c.items[items[i].ID] = &items[i]
	}
	types, err := s.Types(ctx, bookID)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		c.types[t.ID] = t.Name
	}
	return c, nil
}

// Required 科目启用的类别。
func (c *Checker) Required(accountID int64) []int64 { return c.bindings[accountID] }

// Normalize 校验 refs 恰好覆盖科目启用的类别，返回按类别顺序整理后的取值（多余的类别被丢弃）。
// allowDisabled 为真时允许引用已停用的项目（修改历史数据时）。
func (c *Checker) Normalize(accountID int64, refs []Ref, allowDisabled bool) ([]Ref, error) {
	required := c.bindings[accountID]
	if len(required) == 0 {
		return nil, nil
	}
	out := make([]Ref, 0, len(required))
	for _, typeID := range required {
		i := slices.IndexFunc(refs, func(r Ref) bool { return r.TypeID == typeID })
		if i < 0 || refs[i].ItemID == 0 {
			return nil, fmt.Errorf("请选择%s", c.types[typeID])
		}
		it := c.items[refs[i].ItemID]
		if it == nil || it.TypeID != typeID {
			return nil, fmt.Errorf("%s项目无效", c.types[typeID])
		}
		if !it.Enabled && !allowDisabled {
			return nil, fmt.Errorf("%s“%s”已停用", c.types[typeID], it.Name)
		}
		out = append(out, Ref{TypeID: typeID, ItemID: it.ID, ItemCode: it.Code, ItemName: it.Name})
	}
	return out, nil
}

// Key 取值组合的规范化标识（用于判重与分组）。
func Key(refs []Ref) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = fmt.Sprintf("%d:%d", r.TypeID, r.ItemID)
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func conflict(err error, msg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23503") {
		return httpx.Conflict(msg)
	}
	return err
}

// ---- 类别 ----

func (s *Service) SaveType(ctx context.Context, bookID, id int64, name string) (*Type, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, httpx.BadRequest("类别名称不能为空")
	}
	t := Type{ID: id, Name: name}
	q := s.db.Q(ctx)
	var err error
	if id == 0 {
		// 自定义类别的编码自动生成，仅作内部标识
		err = q.QueryRow(ctx, `insert into aux_types(book_id, code, name, sort)
			select $1, 'custom_' || (coalesce(max(id),0)+1), $2, coalesce(max(sort),0)+1 from aux_types where book_id=$1
			returning id, code, is_system`, bookID, name).Scan(&t.ID, &t.Code, &t.IsSystem)
	} else {
		err = q.QueryRow(ctx, `update aux_types set name=$3 where book_id=$1 and id=$2 returning code, is_system`, bookID, id, name).Scan(&t.Code, &t.IsSystem)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NotFound("类别不存在")
		}
	}
	if err != nil {
		return nil, err
	}
	audit.Log(ctx, s.db, bookID, "aux_type.save", "aux_type", t.ID, t)
	return &t, nil
}

func (s *Service) DeleteType(ctx context.Context, bookID, id int64) error {
	var used bool
	if err := s.db.Q(ctx).QueryRow(ctx, `select exists(select 1 from aux_items where type_id=$1)
		or exists(select 1 from account_aux_types where type_id=$1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return httpx.Conflict("类别下已有项目或已被科目启用，不能删除")
	}
	if _, err := s.db.Q(ctx).Exec(ctx, `delete from aux_types where book_id=$1 and id=$2`, bookID, id); err != nil {
		return conflict(err, "类别已被使用，不能删除")
	}
	audit.Log(ctx, s.db, bookID, "aux_type.delete", "aux_type", id, nil)
	return nil
}

// ---- 项目 ----

type ItemInput struct {
	TypeID  int64   `json:"type_id"`
	Code    *string `json:"code"`
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
	Remark  *string `json:"remark"`
}

func trimmed(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

func (s *Service) SaveItem(ctx context.Context, bookID, id int64, in ItemInput) (*Item, error) {
	in.Code, in.Name = trimmed(in.Code), trimmed(in.Name)
	if (id == 0 && (in.Code == nil || in.Name == nil)) || (in.Code != nil && *in.Code == "") || (in.Name != nil && *in.Name == "") {
		return nil, httpx.BadRequest("编码和名称不能为空")
	}
	var it Item
	q := s.db.Q(ctx)
	var err error
	const ret = ` returning id, type_id, code, name, enabled, remark`
	if id == 0 {
		err = q.QueryRow(ctx, `insert into aux_items(book_id, type_id, code, name, remark)
			select $1, t.id, $3, $4, coalesce($5,'') from aux_types t where t.id=$2 and t.book_id=$1`+ret,
			bookID, in.TypeID, in.Code, in.Name, in.Remark).Scan(&it.ID, &it.TypeID, &it.Code, &it.Name, &it.Enabled, &it.Remark)
	} else {
		err = q.QueryRow(ctx, `update aux_items set code=coalesce($3,code), name=coalesce($4,name),
			enabled=coalesce($5,enabled), remark=coalesce($6,remark) where book_id=$1 and id=$2`+ret,
			bookID, id, in.Code, in.Name, in.Enabled, in.Remark).Scan(&it.ID, &it.TypeID, &it.Code, &it.Name, &it.Enabled, &it.Remark)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("类别或项目不存在")
	}
	if err != nil {
		return nil, conflict(err, "编码已存在")
	}
	audit.Log(ctx, s.db, bookID, "aux_item.save", "aux_item", it.ID, it)
	return &it, nil
}

func (s *Service) DeleteItem(ctx context.Context, bookID, id int64) error {
	if _, err := s.db.Q(ctx).Exec(ctx, `delete from aux_items where book_id=$1 and id=$2`, bookID, id); err != nil {
		return conflict(err, "项目已被凭证或期初使用，不能删除（可改为停用）")
	}
	audit.Log(ctx, s.db, bookID, "aux_item.delete", "aux_item", id, nil)
	return nil
}

// ---- 科目绑定 ----

// SetBindings 设置科目启用的辅助核算类别。科目已有数据时不允许变更，避免历史分录缺项目。
func (s *Service) SetBindings(ctx context.Context, b *book.Book, accountID int64, typeIDs []int64) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		a, err := s.accounts.Get(ctx, b.ID, accountID)
		if err != nil {
			return err
		}
		slices.Sort(typeIDs)
		typeIDs = slices.Compact(typeIDs)
		bindings, err := s.Bindings(ctx, b.ID)
		if err != nil {
			return err
		}
		current := slices.Clone(bindings[accountID])
		slices.Sort(current)
		if slices.Equal(current, typeIDs) {
			return nil
		}
		if !a.IsLeaf {
			return httpx.BadRequest("只有末级科目可以启用辅助核算")
		}
		if used, err := s.accounts.Used(ctx, accountID); err != nil {
			return err
		} else if used {
			return httpx.Conflict("科目已有凭证或期初数据，不能调整辅助核算")
		}
		types, err := s.Types(ctx, b.ID)
		if err != nil {
			return err
		}
		q := s.db.Q(ctx)
		if _, err := q.Exec(ctx, `delete from account_aux_types where account_id=$1`, accountID); err != nil {
			return err
		}
		for _, id := range typeIDs {
			if !slices.ContainsFunc(types, func(t Type) bool { return t.ID == id }) {
				return httpx.BadRequest("辅助核算类别无效")
			}
			if _, err := q.Exec(ctx, `insert into account_aux_types(account_id, type_id) values($1,$2)`, accountID, id); err != nil {
				return err
			}
		}
		audit.Log(ctx, s.db, b.ID, "account.aux", "account", accountID, map[string]any{"type_ids": typeIDs})
		return nil
	})
}

func (s *Service) Routes(r chi.Router) {
	bid := func(r *http.Request) int64 { return book.From(r.Context()).Book.ID }

	r.Get("/aux/types", httpx.Handle(func(r *http.Request) (any, error) { return s.Types(r.Context(), bid(r)) }))
	r.Get("/aux/items", httpx.Handle(func(r *http.Request) (any, error) { return s.Items(r.Context(), bid(r)) }))
	r.Get("/aux/bindings", httpx.Handle(func(r *http.Request) (any, error) { return s.Bindings(r.Context(), bid(r)) }))

	r.Group(func(r chi.Router) {
		r.Use(book.Require(PermWrite))
		saveType := func(r *http.Request, id int64) (any, error) {
			in, err := httpx.Bind[struct {
				Name string `json:"name"`
			}](r)
			if err != nil {
				return nil, err
			}
			return s.SaveType(r.Context(), bid(r), id, in.Name)
		}
		saveItem := func(r *http.Request, id int64) (any, error) {
			in, err := httpx.Bind[ItemInput](r)
			if err != nil {
				return nil, err
			}
			return s.SaveItem(r.Context(), bid(r), id, in)
		}
		r.Post("/aux/types", httpx.Handle(func(r *http.Request) (any, error) { return saveType(r, 0) }))
		r.Patch("/aux/types/{id}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "id")
			if err != nil {
				return nil, err
			}
			return saveType(r, id)
		}))
		r.Delete("/aux/types/{id}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "id")
			if err != nil {
				return nil, err
			}
			return nil, s.DeleteType(r.Context(), bid(r), id)
		}))
		r.Post("/aux/items", httpx.Handle(func(r *http.Request) (any, error) { return saveItem(r, 0) }))
		r.Patch("/aux/items/{id}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "id")
			if err != nil {
				return nil, err
			}
			return saveItem(r, id)
		}))
		r.Delete("/aux/items/{id}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "id")
			if err != nil {
				return nil, err
			}
			return nil, s.DeleteItem(r.Context(), bid(r), id)
		}))
	})

	r.With(book.Require(account.PermWrite)).Put("/accounts/{accountID}/aux", httpx.Handle(func(r *http.Request) (any, error) {
		id, err := httpx.ParamInt64(r, "accountID")
		if err != nil {
			return nil, err
		}
		in, err := httpx.Bind[struct {
			TypeIDs []int64 `json:"type_ids"`
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.SetBindings(r.Context(), book.From(r.Context()).Book, id, in.TypeIDs)
	}))
}
