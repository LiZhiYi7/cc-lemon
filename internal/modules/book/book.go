// Package book 账套：建账、设置、成员与权限、会计期间、账套级配置存储。
//
// 其他业务模块通过本包获取“账套上下文”（当前账套 + 当前用户在该账套的权限）。
package book

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/auth"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

// 账套级配置类别。
const (
	KindReport      = "report"
	KindClosingRule = "closing_rule"
)

// 平行记账校验模式。
const (
	ParallelOff    = "off"
	ParallelWarn   = "warn"
	ParallelStrict = "strict"
)

// Settings 账套可调参数（存于 books.settings）。新增参数在此加字段并给默认值即可。
type Settings struct {
	AccountCodeScheme []int `json:"account_code_scheme"`
	Voucher           struct {
		ReviewerMustDiffer   bool `json:"reviewer_must_differ"`    // 审核人与制单人不能相同
		RequireReviewToClose bool `json:"require_review_to_close"` // 结账前凭证须全部审核
	} `json:"voucher"`
	Parallel struct {
		// Mode 来源账簿分录涉及现金类科目而未录目标账簿分录时：off 不检查 / warn 提示 / strict 禁止保存
		Mode string `json:"mode"`
	} `json:"parallel"`
}

type Book struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Company      string    `json:"company"`
	StandardCode string    `json:"standard_code"`
	StartYear    int       `json:"start_year"`
	StartMonth   int       `json:"start_month"`
	Settings     Settings  `json:"settings"`
	CreatedAt    time.Time `json:"created_at"`
}

func (b *Book) Start() kernel.Period { return kernel.Period{Year: b.StartYear, Month: b.StartMonth} }

type Ledger struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsPrimary bool   `json:"is_primary"`
}

type Category struct {
	LedgerID int64  `json:"ledger_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsPL     bool   `json:"is_pl"`
}

type Role struct {
	Code        string   `yaml:"code" json:"code"`
	Name        string   `yaml:"name" json:"name"`
	Permissions []string `yaml:"permissions" json:"permissions"`
}

type Service struct {
	db        *db.DB
	standards *standard.Registry
	roles     []Role
}

func New(d *db.DB, standards *standard.Registry, rolesYAML []byte) (*Service, error) {
	var cfg struct {
		Roles []Role `yaml:"roles"`
	}
	if err := yaml.Unmarshal(rolesYAML, &cfg); err != nil {
		return nil, fmt.Errorf("解析角色配置: %w", err)
	}
	if len(cfg.Roles) == 0 {
		return nil, errors.New("角色配置为空")
	}
	return &Service{db: d, standards: standards, roles: cfg.Roles}, nil
}

func (s *Service) role(code string) (Role, bool) {
	for _, r := range s.roles {
		if r.Code == code {
			return r, true
		}
	}
	return Role{}, false
}

const bookCols = `b.id, b.name, b.company, b.standard_code, b.start_year, b.start_month, b.settings, b.created_at`

func scanBook(row pgx.Row, extra ...any) (*Book, error) {
	var b Book
	var raw []byte
	dst := append([]any{&b.ID, &b.Name, &b.Company, &b.StandardCode, &b.StartYear, &b.StartMonth, &raw, &b.CreatedAt}, extra...)
	if err := row.Scan(dst...); err != nil {
		return nil, err
	}
	return &b, json.Unmarshal(raw, &b.Settings)
}

type CreateInput struct {
	Name         string `json:"name"`
	Company      string `json:"company"`
	StandardCode string `json:"standard_code"`
	StartYear    int    `json:"start_year"`
	StartMonth   int    `json:"start_month"`
}

// Create 按会计准则模板建账：复制账簿、科目类别、科目表、凭证字、报表模板、结转规则、平行记账规则。
func (s *Service) Create(ctx context.Context, in CreateInput) (*Book, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, httpx.BadRequest("账套名称不能为空")
	}
	std, ok := s.standards.Get(in.StandardCode)
	if !ok {
		return nil, httpx.BadRequest("未知的会计准则: " + in.StandardCode)
	}
	if !(kernel.Period{Year: in.StartYear, Month: in.StartMonth}).Valid() {
		return nil, httpx.BadRequest("启用期间无效")
	}
	var settings Settings
	settings.AccountCodeScheme = std.AccountCodeScheme
	settings.Parallel.Mode = ParallelOff
	if len(std.Parallel) > 0 {
		settings.Parallel.Mode = ParallelWarn
	}
	settingsJSON, _ := json.Marshal(settings)
	user := auth.CurrentUser(ctx)

	var b *Book
	err := s.db.Tx(ctx, func(ctx context.Context) error {
		q := s.db.Q(ctx)
		var err error
		b, err = scanBook(q.QueryRow(ctx, `insert into books as b (name, company, standard_code, start_year, start_month, settings, created_by)
			values($1,$2,$3,$4,$5,$6,$7) returning `+bookCols,
			in.Name, in.Company, std.Code, in.StartYear, in.StartMonth, settingsJSON, user.ID))
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `insert into book_members(book_id, user_id, role) values($1,$2,$3)`, b.ID, user.ID, s.roles[0].Code); err != nil {
			return err
		}
		ledgerIDs := map[string]int64{}
		for i, l := range std.Ledgers {
			var id int64
			if err := q.QueryRow(ctx, `insert into ledgers(book_id, code, name, sort, is_primary) values($1,$2,$3,$4,$5) returning id`,
				b.ID, l.Code, l.Name, i, l.Primary).Scan(&id); err != nil {
				return err
			}
			ledgerIDs[l.Code] = id
		}
		accountIDs := map[string]int64{} // ledger/code → id
		for i, c := range std.Categories {
			lid := ledgerIDs[c.Ledger]
			if _, err := q.Exec(ctx, `insert into account_categories(book_id, ledger_id, code, name, sort, is_pl) values($1,$2,$3,$4,$5,$6)`,
				b.ID, lid, c.Code, c.Name, i, c.PL); err != nil {
				return err
			}
			for _, a := range c.Accounts {
				code := string(a.Code)
				parentCode, level, err := standard.ParentCode(std.AccountCodeScheme, code)
				if err != nil {
					return err
				}
				var parent *int64
				if parentCode != "" {
					pid := accountIDs[c.Ledger+"/"+parentCode]
					parent = &pid
					if _, err := q.Exec(ctx, `update accounts set is_leaf=false where id=$1`, pid); err != nil {
						return err
					}
				}
				var id int64
				if err := q.QueryRow(ctx, `insert into accounts(book_id, ledger_id, code, name, parent_id, level, category, direction, is_cash, is_system)
					values($1,$2,$3,$4,$5,$6,$7,$8,$9,true) returning id`,
					b.ID, lid, code, a.Name, parent, level, c.Code, a.Dir, a.Cash).Scan(&id); err != nil {
					return err
				}
				accountIDs[c.Ledger+"/"+code] = id
			}
		}
		for i, w := range std.VoucherWords {
			if _, err := q.Exec(ctx, `insert into voucher_words(book_id, word, title, is_default, sort) values($1,$2,$3,$4,$5)`,
				b.ID, w.Word, w.Title, w.Default, i); err != nil {
				return err
			}
		}
		for i, t := range std.AuxTypes {
			if _, err := q.Exec(ctx, `insert into aux_types(book_id, code, name, sort, is_system) values($1,$2,$3,$4,true)`,
				b.ID, t.Code, t.Name, i); err != nil {
				return err
			}
		}
		for i, r := range std.Reports {
			if err := s.PutConfig(ctx, b.ID, KindReport, r.Code, r.Name, i, r); err != nil {
				return err
			}
		}
		for i, r := range std.ClosingRules {
			if err := s.PutConfig(ctx, b.ID, KindClosingRule, r.Code, r.Name, i, r); err != nil {
				return err
			}
		}
		for _, g := range std.Parallel {
			for _, r := range g.Rules {
				if _, err := q.Exec(ctx, `insert into parallel_rules(book_id, source_ledger_id, source_code, target_ledger_id, target_code, side, note)
					values($1,$2,$3,$4,$5,$6,$7)`, b.ID, ledgerIDs[g.Source], string(r.From), ledgerIDs[g.Target], string(r.To), r.Side, r.Note); err != nil {
					return err
				}
			}
		}
		audit.Log(ctx, s.db, b.ID, "book.create", "book", b.ID, in)
		return nil
	})
	return b, err
}

func (s *Service) Get(ctx context.Context, id int64) (*Book, error) {
	b, err := scanBook(s.db.Q(ctx).QueryRow(ctx, `select `+bookCols+` from books b where b.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("账套不存在")
	}
	return b, err
}

type ListItem struct {
	*Book
	Role          string        `json:"role"`
	StandardName  string        `json:"standard_name"`
	CurrentPeriod kernel.Period `json:"current_period"`
}

// List 返回当前用户可访问的账套（系统管理员可见全部）。
func (s *Service) List(ctx context.Context) ([]*ListItem, error) {
	u := auth.CurrentUser(ctx)
	items, err := db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (*ListItem, error) {
		it := &ListItem{}
		var role *string
		b, err := scanBook(r, &role)
		if err != nil {
			return nil, err
		}
		it.Book = b
		if role != nil {
			it.Role = *role
		}
		return it, nil
	}, `select `+bookCols+`, m.role from books b
		left join book_members m on m.book_id=b.id and m.user_id=$1
		where m.user_id is not null or $2 order by b.id`, u.ID, u.IsAdmin)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if std, ok := s.standards.Get(it.StandardCode); ok {
			it.StandardName = std.Name
		}
		if it.CurrentPeriod, err = s.CurrentPeriod(ctx, it.Book); err != nil {
			return nil, err
		}
	}
	return items, nil
}

type UpdateInput struct {
	Name     *string   `json:"name"`
	Company  *string   `json:"company"`
	Settings *Settings `json:"settings"`
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (*Book, error) {
	var settings []byte
	if in.Settings != nil {
		st := in.Settings
		if len(st.AccountCodeScheme) == 0 {
			return nil, httpx.BadRequest("科目编码方案不能为空")
		}
		switch st.Parallel.Mode {
		case ParallelOff, ParallelWarn, ParallelStrict:
		default:
			return nil, httpx.BadRequest("平行记账校验模式无效")
		}
		settings, _ = json.Marshal(st)
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, httpx.BadRequest("账套名称不能为空")
	}
	b, err := scanBook(s.db.Q(ctx).QueryRow(ctx, `update books b set name=coalesce($2,name), company=coalesce($3,company),
		settings=coalesce($4,settings) where id=$1 returning `+bookCols, id, in.Name, in.Company, settings))
	if err == nil {
		audit.Log(ctx, s.db, id, "book.update", "book", id, in)
	}
	return b, err
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	_, err := s.db.Q(ctx).Exec(ctx, `delete from books where id=$1`, id)
	if err == nil {
		audit.Log(ctx, s.db, 0, "book.delete", "book", id, nil)
	}
	return err
}

func (s *Service) Ledgers(ctx context.Context, bookID int64) ([]Ledger, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (Ledger, error) {
		var l Ledger
		return l, r.Scan(&l.ID, &l.Code, &l.Name, &l.IsPrimary)
	}, `select id, code, name, is_primary from ledgers where book_id=$1 order by sort, id`, bookID)
}

// LedgerByCode 按编码找账簿。
func (s *Service) LedgerByCode(ctx context.Context, bookID int64, code string) (*Ledger, error) {
	ls, err := s.Ledgers(ctx, bookID)
	if err != nil {
		return nil, err
	}
	for i := range ls {
		if ls[i].Code == code {
			return &ls[i], nil
		}
	}
	return nil, httpx.BadRequest("账簿不存在: " + code)
}

func (s *Service) Categories(ctx context.Context, bookID int64) ([]Category, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (Category, error) {
		var c Category
		return c, r.Scan(&c.LedgerID, &c.Code, &c.Name, &c.IsPL)
	}, `select ledger_id, code, name, is_pl from account_categories where book_id=$1 order by sort`, bookID)
}

type VoucherWord struct {
	Word      string `json:"word"`
	Title     string `json:"title"`
	IsDefault bool   `json:"is_default"`
}

func (s *Service) VoucherWords(ctx context.Context, bookID int64) ([]VoucherWord, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (VoucherWord, error) {
		var w VoucherWord
		return w, r.Scan(&w.Word, &w.Title, &w.IsDefault)
	}, `select word, title, is_default from voucher_words where book_id=$1 order by sort`, bookID)
}

// ---- 会计期间 ----

// Lock 对账套加行锁，串行化同一账套内的记账类写操作（须在事务中调用）。
func (s *Service) Lock(ctx context.Context, bookID int64) error {
	_, err := s.db.Q(ctx).Exec(ctx, `select 1 from books where id=$1 for update`, bookID)
	return err
}

// LastClosed 返回最后一个已结账期间；没有则 ok=false。
func (s *Service) LastClosed(ctx context.Context, bookID int64) (p kernel.Period, ok bool, err error) {
	var ym *int
	err = s.db.Q(ctx).QueryRow(ctx, `select max(year*100+month) from periods where book_id=$1 and closed`, bookID).Scan(&ym)
	if err != nil || ym == nil {
		return p, false, err
	}
	return kernel.Period{Year: *ym / 100, Month: *ym % 100}, true, nil
}

// CurrentPeriod 当前期间 = 第一个未结账期间。
func (s *Service) CurrentPeriod(ctx context.Context, b *Book) (kernel.Period, error) {
	last, ok, err := s.LastClosed(ctx, b.ID)
	if err != nil || !ok {
		return b.Start(), err
	}
	return last.Next(), nil
}

// EnsureOpen 校验期间可记账（不早于启用期间且未结账）。
func (s *Service) EnsureOpen(ctx context.Context, b *Book, p kernel.Period) error {
	cur, err := s.CurrentPeriod(ctx, b)
	if err != nil {
		return err
	}
	if p.Before(b.Start()) {
		return httpx.BadRequest(fmt.Sprintf("期间 %s 早于账套启用期间 %s", p, b.Start()))
	}
	if p.Before(cur) {
		return httpx.Conflict(fmt.Sprintf("期间 %s 已结账，不能修改", p))
	}
	return nil
}

func (s *Service) SetClosed(ctx context.Context, bookID int64, p kernel.Period, closed bool) error {
	_, err := s.db.Q(ctx).Exec(ctx, `insert into periods(book_id, year, month, closed, closed_at, closed_by)
		values($1,$2,$3,$4, case when $4 then now() end, case when $4 then $5::bigint end)
		on conflict (book_id, year, month) do update set closed=excluded.closed, closed_at=excluded.closed_at, closed_by=excluded.closed_by`,
		bookID, p.Year, p.Month, closed, auth.CurrentUser(ctx).ID)
	return err
}

// ---- 账套级配置 ----

type ConfigItem struct {
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
}

func (s *Service) Configs(ctx context.Context, bookID int64, kind string) ([]ConfigItem, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (ConfigItem, error) {
		var c ConfigItem
		return c, r.Scan(&c.Code, &c.Name, &c.Definition)
	}, `select code, name, definition from book_configs where book_id=$1 and kind=$2 order by sort, code`, bookID, kind)
}

func (s *Service) Config(ctx context.Context, bookID int64, kind, code string, dst any) error {
	var raw []byte
	err := s.db.Q(ctx).QueryRow(ctx, `select definition from book_configs where book_id=$1 and kind=$2 and code=$3`, bookID, kind, code).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound("配置不存在: " + code)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// PutConfig 新增或覆盖一项配置；sort<0 表示保留原排序（新增时排在最后）。
func (s *Service) PutConfig(ctx context.Context, bookID int64, kind, code, name string, sort int, def any) error {
	raw, err := json.Marshal(def)
	if err != nil {
		return err
	}
	_, err = s.db.Q(ctx).Exec(ctx, `insert into book_configs(book_id, kind, code, name, sort, definition)
		values($1,$2,$3,$4, case when $5 < 0 then (select coalesce(max(sort),0)+1 from book_configs where book_id=$1 and kind=$2) else $5 end, $6)
		on conflict (book_id, kind, code) do update set name=excluded.name, definition=excluded.definition,
			sort = case when $5 < 0 then book_configs.sort else excluded.sort end`,
		bookID, kind, code, name, sort, raw)
	return err
}
