// Package voucher 记账凭证：录入、修改、审核、整理凭证号，以及平行记账分录生成。
//
// 一张凭证的分录可分属多个核算账簿（平行记账），每个账簿内借贷必须各自平衡。
package voucher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/auth"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const (
	PermWrite  = "voucher:write"
	PermReview = "voucher:review"

	StatusSaved    = "saved"
	StatusReviewed = "reviewed"

	SourceManual = "manual"
)

type Dec = decimal.Decimal

type Line struct {
	ID          int64  `json:"id"`
	LedgerID    int64  `json:"ledger_id"`
	Summary     string `json:"summary"`
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	Debit       Dec    `json:"debit"`
	Credit      Dec    `json:"credit"`
}

type Voucher struct {
	ID             int64    `json:"id"`
	Year           int      `json:"year"`
	Month          int      `json:"month"`
	Word           string   `json:"word"`
	Number         int      `json:"number"`
	Date           string   `json:"date"`
	Attachments    int      `json:"attachments"`
	Status         string   `json:"status"`
	Source         string   `json:"source"`
	Remark         string   `json:"remark"`
	CreatedBy      int64    `json:"created_by"`
	CreatedByName  string   `json:"created_by_name"`
	ReviewedByName string   `json:"reviewed_by_name"`
	Total          Dec      `json:"total"` // 主账簿借方合计
	Lines          []Line   `json:"lines"`
	Warnings       []string `json:"warnings,omitempty"`
}

type LineInput struct {
	Summary   string `json:"summary"`
	AccountID int64  `json:"account_id"`
	Debit     Dec    `json:"debit"`
	Credit    Dec    `json:"credit"`
}

type Input struct {
	Word        string      `json:"word"`
	Number      int         `json:"number"` // 0 = 自动取号
	Date        string      `json:"date"`
	Attachments int         `json:"attachments"`
	Remark      string      `json:"remark"`
	Lines       []LineInput `json:"lines"`
}

type Service struct {
	db       *db.DB
	books    *book.Service
	accounts *account.Service
}

func New(d *db.DB, books *book.Service, accounts *account.Service) *Service {
	return &Service{db: d, books: books, accounts: accounts}
}

type header struct {
	id     int64
	period kernel.Period
	word   string
	number int
	status string
}

func (s *Service) header(ctx context.Context, bookID, id int64) (*header, error) {
	var h header
	err := s.db.Q(ctx).QueryRow(ctx, `select id, year, month, word, number, status from vouchers where book_id=$1 and id=$2`, bookID, id).
		Scan(&h.id, &h.period.Year, &h.period.Month, &h.word, &h.number, &h.status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("凭证不存在")
	}
	return &h, err
}

func (s *Service) nextNumber(ctx context.Context, bookID int64, p kernel.Period, word string) (int, error) {
	var n int
	err := s.db.Q(ctx).QueryRow(ctx, `select coalesce(max(number),0)+1 from vouchers where book_id=$1 and year=$2 and month=$3 and word=$4`,
		bookID, p.Year, p.Month, word).Scan(&n)
	return n, err
}

// NextNumber 供界面预显示下一个凭证号。
func (s *Service) NextNumber(ctx context.Context, bookID int64, p kernel.Period, word string) (int, error) {
	return s.nextNumber(ctx, bookID, p, word)
}

// validate 校验分录并返回每行所属账簿、保存时的提示信息。
func (s *Service) validate(ctx context.Context, b *book.Book, in *Input) (ledgerOf []int64, warnings []string, err error) {
	if len(in.Lines) < 2 {
		return nil, nil, httpx.BadRequest("凭证至少需要两行分录")
	}
	accounts, err := s.accounts.List(ctx, b.ID)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int64]*account.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	type sum struct {
		debit, credit Dec
		lines         int
		cash          bool
	}
	sums := map[int64]*sum{}
	ledgerOf = make([]int64, len(in.Lines))
	for i := range in.Lines {
		l := &in.Lines[i]
		n := i + 1
		a := byID[l.AccountID]
		if a == nil {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("第 %d 行：请选择科目", n))
		}
		if !a.IsLeaf {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("第 %d 行：%s %s 不是末级科目", n, a.Code, a.Name))
		}
		if !a.Enabled {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("第 %d 行：科目 %s %s 已停用", n, a.Code, a.Name))
		}
		if l.Debit.IsZero() == l.Credit.IsZero() {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("第 %d 行：借方、贷方金额须且只能填一个", n))
		}
		if !l.Debit.Equal(l.Debit.Round(2)) || !l.Credit.Equal(l.Credit.Round(2)) {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("第 %d 行：金额最多两位小数", n))
		}
		l.Summary = strings.TrimSpace(l.Summary)
		if l.Summary == "" {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("第 %d 行：请填写摘要", n))
		}
		ledgerOf[i] = a.LedgerID
		x := sums[a.LedgerID]
		if x == nil {
			x = &sum{}
			sums[a.LedgerID] = x
		}
		x.debit, x.credit = x.debit.Add(l.Debit), x.credit.Add(l.Credit)
		x.lines++
		x.cash = x.cash || a.IsCash
	}
	ledgers, err := s.books.Ledgers(ctx, b.ID)
	if err != nil {
		return nil, nil, err
	}
	name := map[int64]string{}
	for _, l := range ledgers {
		name[l.ID] = l.Name
	}
	for lid, x := range sums {
		if x.lines < 2 {
			return nil, nil, httpx.BadRequest(name[lid] + "至少需要两行分录")
		}
		if !x.debit.Equal(x.credit) {
			return nil, nil, httpx.BadRequest(fmt.Sprintf("%s借贷不平衡：借方 %s，贷方 %s", name[lid], x.debit.StringFixed(2), x.credit.StringFixed(2)))
		}
	}
	// 平行记账完整性：来源账簿动用了现金类科目，目标账簿却没有分录。
	if mode := b.Settings.Parallel.Mode; mode == book.ParallelWarn || mode == book.ParallelStrict {
		pairs, err := s.parallelPairs(ctx, b.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range pairs {
			if src := sums[p[0]]; src != nil && src.cash && sums[p[1]] == nil {
				msg := fmt.Sprintf("%s分录涉及现金类科目，但未录入%s分录", name[p[0]], name[p[1]])
				if mode == book.ParallelStrict {
					return nil, nil, httpx.BadRequest(msg)
				}
				warnings = append(warnings, msg)
			}
		}
	}
	return ledgerOf, warnings, nil
}

// Save 新增（id=0）或修改凭证。source 标识凭证来源，手工录入为 SourceManual。
func (s *Service) Save(ctx context.Context, b *book.Book, id int64, in Input, source string) (*Voucher, error) {
	date, err := time.Parse(time.DateOnly, in.Date)
	if err != nil {
		return nil, httpx.BadRequest("凭证日期无效")
	}
	if in.Attachments < 0 {
		return nil, httpx.BadRequest("附件数不能为负")
	}
	period := kernel.Period{Year: date.Year(), Month: int(date.Month())}
	var warnings []string
	err = s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		if err := s.books.EnsureOpen(ctx, b, period); err != nil {
			return err
		}
		words, err := s.books.VoucherWords(ctx, b.ID)
		if err != nil {
			return err
		}
		wordOK := false
		for _, w := range words {
			if in.Word == "" && w.IsDefault {
				in.Word = w.Word
			}
			wordOK = wordOK || w.Word == in.Word
		}
		if !wordOK {
			return httpx.BadRequest("凭证字无效")
		}
		ledgerOf, w, err := s.validate(ctx, b, &in)
		if err != nil {
			return err
		}
		warnings = w
		q := s.db.Q(ctx)
		user := auth.CurrentUser(ctx)

		if id == 0 {
			if in.Number <= 0 {
				if in.Number, err = s.nextNumber(ctx, b.ID, period, in.Word); err != nil {
					return err
				}
			}
			err = q.QueryRow(ctx, `insert into vouchers(book_id, year, month, word, number, vdate, attachments, source, remark, created_by)
				values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) returning id`,
				b.ID, period.Year, period.Month, in.Word, in.Number, date, in.Attachments, source, in.Remark, user.ID).Scan(&id)
		} else {
			var old *header
			if old, err = s.header(ctx, b.ID, id); err != nil {
				return err
			}
			if old.status == StatusReviewed {
				return httpx.Conflict("凭证已审核，请先取消审核")
			}
			if err := s.books.EnsureOpen(ctx, b, old.period); err != nil {
				return err
			}
			if in.Number <= 0 {
				in.Number = old.number
				if old.period != period || old.word != in.Word {
					if in.Number, err = s.nextNumber(ctx, b.ID, period, in.Word); err != nil {
						return err
					}
				}
			}
			if _, err = q.Exec(ctx, `delete from voucher_lines where voucher_id=$1`, id); err != nil {
				return err
			}
			_, err = q.Exec(ctx, `update vouchers set year=$2, month=$3, word=$4, number=$5, vdate=$6, attachments=$7, remark=$8, updated_at=now()
				where id=$1`, id, period.Year, period.Month, in.Word, in.Number, date, in.Attachments, in.Remark)
		}
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			if _, err := q.Exec(ctx, `insert into voucher_lines(voucher_id, book_id, ledger_id, line_no, summary, account_id, debit, credit)
				values($1,$2,$3,$4,$5,$6,$7,$8)`, id, b.ID, ledgerOf[i], i+1, l.Summary, l.AccountID, l.Debit, l.Credit); err != nil {
				return err
			}
		}
		// 唯一约束为延迟校验，这里主动触发以便返回友好提示。
		if _, err := q.Exec(ctx, `set constraints all immediate`); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return httpx.Conflict(fmt.Sprintf("凭证号 %s-%d 已存在", in.Word, in.Number))
			}
			return err
		}
		audit.Log(ctx, s.db, b.ID, "voucher.save", "voucher", id, map[string]any{"no": fmt.Sprintf("%s %s-%d", period, in.Word, in.Number), "source": source})
		return nil
	})
	if err != nil {
		return nil, err
	}
	v, err := s.Get(ctx, b.ID, id)
	if err == nil {
		v.Warnings = warnings
	}
	return v, err
}

type Filter struct {
	From, To  kernel.Period
	Word      string
	Status    string
	Keyword   string
	AccountID int64
	Page      int
	PageSize  int
}

type Page struct {
	Items []*Voucher `json:"items"`
	Total int        `json:"total"`
}

func (s *Service) List(ctx context.Context, bookID int64, f Filter) (*Page, error) {
	if f.PageSize <= 0 || f.PageSize > 500 {
		f.PageSize = 50
	}
	if f.Page < 1 {
		f.Page = 1
	}
	where := `v.book_id=$1 and v.year*100+v.month between $2 and $3
		and ($4='' or v.word=$4) and ($5='' or v.status=$5)
		and ($6='' or exists(select 1 from voucher_lines l where l.voucher_id=v.id and l.summary ilike '%'||$6||'%'))
		and ($7=0 or exists(select 1 from voucher_lines l where l.voucher_id=v.id and l.account_id=$7))`
	args := []any{bookID, f.From.YM(), f.To.YM(), f.Word, f.Status, f.Keyword, f.AccountID}
	p := &Page{}
	if err := s.db.Q(ctx).QueryRow(ctx, `select count(*) from vouchers v where `+where, args...).Scan(&p.Total); err != nil {
		return nil, err
	}
	var err error
	p.Items, err = s.query(ctx, where+` order by v.year, v.month, v.word, v.number limit $8 offset $9`,
		append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	return p, err
}

func (s *Service) Get(ctx context.Context, bookID, id int64) (*Voucher, error) {
	list, err := s.query(ctx, `v.book_id=$1 and v.id=$2`, bookID, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, httpx.NotFound("凭证不存在")
	}
	return list[0], nil
}

func (s *Service) query(ctx context.Context, where string, args ...any) ([]*Voucher, error) {
	q := s.db.Q(ctx)
	list, err := db.Collect(ctx, q, func(r pgx.Rows) (*Voucher, error) {
		v := &Voucher{Lines: []Line{}}
		var date time.Time
		err := r.Scan(&v.ID, &v.Year, &v.Month, &v.Word, &v.Number, &date, &v.Attachments, &v.Status, &v.Source, &v.Remark,
			&v.CreatedBy, &v.CreatedByName, &v.ReviewedByName)
		v.Date = date.Format(time.DateOnly)
		return v, err
	}, `select v.id, v.year, v.month, v.word, v.number, v.vdate, v.attachments, v.status, v.source, v.remark,
			coalesce(v.created_by,0), coalesce(cu.display_name,''), coalesce(ru.display_name,'')
		from vouchers v left join users cu on cu.id=v.created_by left join users ru on ru.id=v.reviewed_by
		where `+where, args...)
	if err != nil || len(list) == 0 {
		return list, err
	}
	byID := make(map[int64]*Voucher, len(list))
	ids := make([]int64, len(list))
	for i, v := range list {
		byID[v.ID] = v
		ids[i] = v.ID
	}
	rows, err := q.Query(ctx, `select l.voucher_id, l.id, l.ledger_id, l.summary, l.account_id, a.code,
			(with recursive up as (
				select x.id, x.parent_id, x.name, 1 as depth from accounts x where x.id=a.id
				union all select p.id, p.parent_id, p.name, up.depth+1 from accounts p join up on p.id=up.parent_id)
			 select string_agg(name, '-' order by depth desc) from up),
			l.debit, l.credit, g.is_primary
		from voucher_lines l join accounts a on a.id=l.account_id join ledgers g on g.id=l.ledger_id
		where l.voucher_id = any($1) order by l.voucher_id, l.line_no`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var vid int64
		var l Line
		var primary bool
		if err := rows.Scan(&vid, &l.ID, &l.LedgerID, &l.Summary, &l.AccountID, &l.AccountCode, &l.AccountName, &l.Debit, &l.Credit, &primary); err != nil {
			return nil, err
		}
		v := byID[vid]
		v.Lines = append(v.Lines, l)
		if primary {
			v.Total = v.Total.Add(l.Debit)
		}
	}
	return list, rows.Err()
}

func (s *Service) Delete(ctx context.Context, b *book.Book, id int64) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		h, err := s.header(ctx, b.ID, id)
		if err != nil {
			return err
		}
		if h.status == StatusReviewed {
			return httpx.Conflict("凭证已审核，请先取消审核")
		}
		if err := s.books.EnsureOpen(ctx, b, h.period); err != nil {
			return err
		}
		if _, err := s.db.Q(ctx).Exec(ctx, `delete from vouchers where id=$1`, id); err != nil {
			return err
		}
		audit.Log(ctx, s.db, b.ID, "voucher.delete", "voucher", id, map[string]any{"no": fmt.Sprintf("%s %s-%d", h.period, h.word, h.number)})
		return nil
	})
}

// IDsBySource 返回某期间内指定来源的凭证（供期末结转等业务模块定位自己生成的凭证）。
func (s *Service) IDsBySource(ctx context.Context, bookID int64, p kernel.Period, source string) ([]int64, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (int64, error) {
		var id int64
		return id, r.Scan(&id)
	}, `select id from vouchers where book_id=$1 and year=$2 and month=$3 and source=$4 order by number`, bookID, p.Year, p.Month, source)
}

// SetReviewed 批量审核 / 取消审核。
func (s *Service) SetReviewed(ctx context.Context, b *book.Book, ids []int64, reviewed bool) error {
	user := auth.CurrentUser(ctx)
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		q := s.db.Q(ctx)
		for _, id := range ids {
			h, err := s.header(ctx, b.ID, id)
			if err != nil {
				return err
			}
			if err := s.books.EnsureOpen(ctx, b, h.period); err != nil {
				return err
			}
			if reviewed {
				var creator int64
				if err := q.QueryRow(ctx, `select coalesce(created_by,0) from vouchers where id=$1`, id).Scan(&creator); err != nil {
					return err
				}
				if b.Settings.Voucher.ReviewerMustDiffer && creator == user.ID {
					return httpx.BadRequest(fmt.Sprintf("凭证 %s-%d：审核人不能与制单人相同", h.word, h.number))
				}
				_, err = q.Exec(ctx, `update vouchers set status=$2, reviewed_by=$3, reviewed_at=now() where id=$1`, id, StatusReviewed, user.ID)
			} else {
				_, err = q.Exec(ctx, `update vouchers set status=$2, reviewed_by=null, reviewed_at=null where id=$1`, id, StatusSaved)
			}
			if err != nil {
				return err
			}
		}
		audit.Log(ctx, s.db, b.ID, "voucher.review", "voucher", "", map[string]any{"ids": ids, "reviewed": reviewed})
		return nil
	})
}

// Renumber 整理凭证号：期间内每个凭证字按日期、原凭证号重排为连续号。
func (s *Service) Renumber(ctx context.Context, b *book.Book, p kernel.Period) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		if err := s.books.EnsureOpen(ctx, b, p); err != nil {
			return err
		}
		_, err := s.db.Q(ctx).Exec(ctx, `update vouchers v set number=t.n from (
				select id, row_number() over (partition by word order by vdate, number) as n
				from vouchers where book_id=$1 and year=$2 and month=$3) t
			where v.id=t.id and v.number<>t.n`, b.ID, p.Year, p.Month)
		if err == nil {
			audit.Log(ctx, s.db, b.ID, "voucher.renumber", "period", p.String(), nil)
		}
		return err
	})
}
