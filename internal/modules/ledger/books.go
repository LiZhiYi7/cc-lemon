package ledger

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

// side 把净额按科目余额方向拆到借/贷列（反方向余额以负数列示）。
func side(a *account.Account, net Dec) (debit, credit Dec) {
	if a.Direction == kernel.Debit {
		return net, decimal.Zero
	}
	return decimal.Zero, net.Neg()
}

// dirText 余额方向与绝对值。
func dirText(net Dec) (string, Dec) {
	switch net.Sign() {
	case 1:
		return "借", net
	case -1:
		return "贷", net.Neg()
	}
	return "平", decimal.Zero
}

// TrialRow 科目余额表一行。
type TrialRow struct {
	AccountID  int64  `json:"account_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Level      int    `json:"level"`
	IsLeaf     bool   `json:"is_leaf"`
	OpenDebit  Dec    `json:"open_debit"`
	OpenCredit Dec    `json:"open_credit"`
	Debit      Dec    `json:"debit"`
	Credit     Dec    `json:"credit"`
	YtdDebit   Dec    `json:"ytd_debit"`
	YtdCredit  Dec    `json:"ytd_credit"`
	EndDebit   Dec    `json:"end_debit"`
	EndCredit  Dec    `json:"end_credit"`
}

func (t *TrialRow) accumulate(o *TrialRow) {
	t.OpenDebit, t.OpenCredit = t.OpenDebit.Add(o.OpenDebit), t.OpenCredit.Add(o.OpenCredit)
	t.Debit, t.Credit = t.Debit.Add(o.Debit), t.Credit.Add(o.Credit)
	t.YtdDebit, t.YtdCredit = t.YtdDebit.Add(o.YtdDebit), t.YtdCredit.Add(o.YtdCredit)
	t.EndDebit, t.EndCredit = t.EndDebit.Add(o.EndDebit), t.EndCredit.Add(o.EndCredit)
}

type TrialBalance struct {
	Rows  []TrialRow `json:"rows"`
	Total TrialRow   `json:"total"`
}

// TrialBalance 科目余额表。showZero=false 时隐藏无余额且无发生额的科目。
func (s *Service) TrialBalance(ctx context.Context, b *book.Book, ledgerID int64, from, to kernel.Period, showZero bool) (*TrialBalance, error) {
	set, err := s.Balances(ctx, b, from, to, nil)
	if err != nil {
		return nil, err
	}
	out := &TrialBalance{Rows: []TrialRow{}}
	out.Total.Name = "合计"
	for _, a := range set.Accounts {
		if a.LedgerID != ledgerID {
			continue
		}
		x := set.ByID[a.ID]
		row := TrialRow{AccountID: a.ID, Code: a.Code, Name: a.Name, Level: a.Level, IsLeaf: a.IsLeaf,
			Debit: x.Debit, Credit: x.Credit, YtdDebit: x.YtdDebit, YtdCredit: x.YtdCredit}
		row.OpenDebit, row.OpenCredit = side(a, x.OpenNet)
		row.EndDebit, row.EndCredit = side(a, x.EndNet())
		if a.Level == 1 {
			out.Total.accumulate(&row)
		}
		if !showZero && x.OpenNet.IsZero() && x.EndNet().IsZero() && x.Debit.IsZero() && x.Credit.IsZero() &&
			x.YtdDebit.IsZero() && x.YtdCredit.IsZero() {
			continue
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// DetailRow 明细账一行。Kind: open 期初 / line 分录 / month 本期合计 / year 本年累计。
type DetailRow struct {
	Kind      string `json:"kind"`
	Date      string `json:"date,omitempty"`
	VoucherID int64  `json:"voucher_id,omitempty"`
	VoucherNo string `json:"voucher_no,omitempty"`
	Account   string `json:"account,omitempty"` // 查询上级科目时显示实际记账科目
	Summary   string `json:"summary"`
	Debit     Dec    `json:"debit"`
	Credit    Dec    `json:"credit"`
	Dir       string `json:"dir"`
	Balance   Dec    `json:"balance"`
}

// Detail 明细账：指定科目（含全部下级）在区间内的逐笔分录与期间小计。
func (s *Service) Detail(ctx context.Context, b *book.Book, accountID int64, from, to kernel.Period) ([]DetailRow, error) {
	if to.Before(from) {
		return nil, httpx.BadRequest("结束期间不能早于开始期间")
	}
	// 期初 = 上一期间的期末；本年累计起点 = 上一期间（同年）的累计数。
	prev := from.Prev()
	set, err := s.Balances(ctx, b, prev, prev, nil)
	if err != nil {
		return nil, err
	}
	x := set.ByID[accountID]
	if x == nil {
		return nil, httpx.NotFound("科目不存在")
	}
	net := x.EndNet()
	var ytdD, ytdC Dec
	if prev.Year == from.Year {
		ytdD, ytdC = x.YtdDebit, x.YtdCredit
	}
	prefix := x.Account.Code
	names := map[int64]string{}
	var ids []int64
	for _, a := range set.Accounts {
		if a.LedgerID == x.Account.LedgerID && strings.HasPrefix(a.Code, prefix) && a.IsLeaf {
			ids = append(ids, a.ID)
			names[a.ID] = a.Code + " " + a.FullName
		}
	}

	rows := []DetailRow{}
	push := func(kind, summary string, d, c Dec) {
		dir, bal := dirText(net)
		rows = append(rows, DetailRow{Kind: kind, Summary: summary, Debit: d, Credit: c, Dir: dir, Balance: bal})
	}
	push("open", "期初余额", decimal.Zero, decimal.Zero)

	q, err := s.db.Q(ctx).Query(ctx, `select v.year, v.month, v.vdate, v.id, v.word, v.number, l.account_id, l.summary, l.debit, l.credit
		from voucher_lines l join vouchers v on v.id=l.voucher_id
		where l.book_id=$1 and l.account_id = any($2) and v.year*100+v.month between $3 and $4
		order by v.year, v.month, v.vdate, v.word, v.number, l.line_no`, b.ID, ids, from.YM(), to.YM())
	if err != nil {
		return nil, err
	}
	defer q.Close()

	cur := from
	var monD, monC Dec
	closeMonth := func() {
		ytdD, ytdC = ytdD.Add(monD), ytdC.Add(monC)
		push("month", "本期合计", monD, monC)
		push("year", "本年累计", ytdD, ytdC)
		monD, monC = decimal.Zero, decimal.Zero
		cur = cur.Next()
		if cur.Month == 1 {
			ytdD, ytdC = decimal.Zero, decimal.Zero
		}
	}
	for q.Next() {
		var y, m, number int
		var date time.Time
		var vid, aid int64
		var word, summary string
		var d, c Dec
		if err := q.Scan(&y, &m, &date, &vid, &word, &number, &aid, &summary, &d, &c); err != nil {
			return nil, err
		}
		for p := (kernel.Period{Year: y, Month: m}); cur.Before(p); {
			closeMonth()
		}
		net = net.Add(d).Sub(c)
		monD, monC = monD.Add(d), monC.Add(c)
		dir, bal := dirText(net)
		row := DetailRow{Kind: "line", Date: date.Format(time.DateOnly), VoucherID: vid,
			VoucherNo: fmt.Sprintf("%s-%d", word, number), Summary: summary, Debit: d, Credit: c, Dir: dir, Balance: bal}
		if !x.Account.IsLeaf {
			row.Account = names[aid]
		}
		rows = append(rows, row)
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	for !to.Before(cur) {
		closeMonth()
	}
	return rows, nil
}

// GeneralRow 总账一行。
type GeneralRow struct {
	Period  string `json:"period"`
	Summary string `json:"summary"`
	Debit   Dec    `json:"debit"`
	Credit  Dec    `json:"credit"`
	Dir     string `json:"dir"`
	Balance Dec    `json:"balance"`
}

type GeneralAccount struct {
	AccountID int64        `json:"account_id"`
	Code      string       `json:"code"`
	Name      string       `json:"name"`
	Rows      []GeneralRow `json:"rows"`
}

// General 总账：一级科目逐月的期初、本期合计、本年累计。
func (s *Service) General(ctx context.Context, b *book.Book, ledgerID int64, from, to kernel.Period) ([]*GeneralAccount, error) {
	if to.Before(from) {
		return nil, httpx.BadRequest("结束期间不能早于开始期间")
	}
	if to.YM()-from.YM() > 300 {
		return nil, httpx.BadRequest("查询区间过长")
	}
	out := []*GeneralAccount{}
	byID := map[int64]*GeneralAccount{}
	active := map[int64]bool{}
	for p := from; !to.Before(p); p = p.Next() {
		set, err := s.Balances(ctx, b, p, p, nil)
		if err != nil {
			return nil, err
		}
		for _, a := range set.Accounts {
			if a.LedgerID != ledgerID || a.Level != 1 {
				continue
			}
			x := set.ByID[a.ID]
			g := byID[a.ID]
			if g == nil {
				g = &GeneralAccount{AccountID: a.ID, Code: a.Code, Name: a.Name}
				byID[a.ID] = g
				out = append(out, g)
				dir, bal := dirText(x.OpenNet)
				g.Rows = append(g.Rows, GeneralRow{Period: p.String(), Summary: "期初余额", Dir: dir, Balance: bal})
			}
			if !x.OpenNet.IsZero() || !x.Debit.IsZero() || !x.Credit.IsZero() || !x.YtdDebit.IsZero() || !x.YtdCredit.IsZero() {
				active[a.ID] = true
			}
			dir, bal := dirText(x.EndNet())
			g.Rows = append(g.Rows,
				GeneralRow{Period: p.String(), Summary: "本期合计", Debit: x.Debit, Credit: x.Credit, Dir: dir, Balance: bal},
				GeneralRow{Period: p.String(), Summary: "本年累计", Debit: x.YtdDebit, Credit: x.YtdCredit, Dir: dir, Balance: bal})
		}
	}
	filtered := out[:0]
	for _, g := range out {
		if active[g.AccountID] {
			filtered = append(filtered, g)
		}
	}
	return filtered, nil
}

// periodRange 解析 ?from=2026-01&to=2026-03。
func periodRange(r *http.Request) (from, to kernel.Period, err error) {
	if from, err = kernel.ParsePeriod(r.URL.Query().Get("from")); err != nil {
		return from, to, httpx.BadRequest(err.Error())
	}
	if to, err = kernel.ParsePeriod(r.URL.Query().Get("to")); err != nil {
		return from, to, httpx.BadRequest(err.Error())
	}
	return from, to, nil
}

func (s *Service) Routes(r chi.Router) {
	r.Get("/opening", httpx.Handle(func(r *http.Request) (any, error) {
		return s.Openings(r.Context(), book.From(r.Context()).Book.ID)
	}))
	r.Get("/opening/check", httpx.Handle(func(r *http.Request) (any, error) {
		return s.CheckOpenings(r.Context(), book.From(r.Context()).Book)
	}))
	r.With(book.Require(PermOpeningWrite)).Put("/opening", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			Items []Opening `json:"items"`
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.SaveOpenings(r.Context(), book.From(r.Context()).Book, in.Items)
	}))
	r.Get("/ledger/trial-balance", httpx.Handle(func(r *http.Request) (any, error) {
		from, to, err := periodRange(r)
		if err != nil {
			return nil, err
		}
		return s.TrialBalance(r.Context(), book.From(r.Context()).Book, httpx.QueryInt64(r, "ledger_id"), from, to,
			r.URL.Query().Get("show_zero") == "1")
	}))
	r.Get("/ledger/detail", httpx.Handle(func(r *http.Request) (any, error) {
		from, to, err := periodRange(r)
		if err != nil {
			return nil, err
		}
		return s.Detail(r.Context(), book.From(r.Context()).Book, httpx.QueryInt64(r, "account_id"), from, to)
	}))
	r.Get("/ledger/general", httpx.Handle(func(r *http.Request) (any, error) {
		from, to, err := periodRange(r)
		if err != nil {
			return nil, err
		}
		return s.General(r.Context(), book.From(r.Context()).Book, httpx.QueryInt64(r, "ledger_id"), from, to)
	}))
}
