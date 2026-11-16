package ledger

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

// 分录/期初的辅助核算组合标识："类别ID:项目ID,类别ID:项目ID"（按类别 ID 升序）。
const (
	lineComboSQL    = `coalesce((select string_agg(x.type_id || ':' || x.item_id, ',' order by x.type_id) from voucher_line_aux x where x.line_id = l.id), '')`
	openingComboSQL = `coalesce((select string_agg(x.type_id || ':' || x.item_id, ',' order by x.type_id) from opening_aux_items x where x.opening_id = oa.id), '')`
)

func parseCombo(key string) []auxiliary.Ref {
	if key == "" {
		return nil
	}
	var refs []auxiliary.Ref
	for _, part := range strings.Split(key, ",") {
		t, i, _ := strings.Cut(part, ":")
		var r auxiliary.Ref
		r.TypeID, _ = strconv.ParseInt(t, 10, 64)
		r.ItemID, _ = strconv.ParseInt(i, 10, 64)
		refs = append(refs, r)
	}
	return refs
}

// AuxBindings 科目 ID → 启用的辅助核算类别。
func (s *Service) AuxBindings(ctx context.Context, bookID int64) (map[int64][]int64, error) {
	return s.aux.Bindings(ctx, bookID)
}

// ---- 期初（按辅助核算项目） ----

type OpeningAuxRow struct {
	Aux       []auxiliary.Ref `json:"aux"`
	Balance   Dec             `json:"balance"`
	YtdDebit  Dec             `json:"ytd_debit"`
	YtdCredit Dec             `json:"ytd_credit"`
}

func (s *Service) OpeningAux(ctx context.Context, bookID, accountID int64) ([]OpeningAuxRow, error) {
	q := s.db.Q(ctx)
	rows, err := q.Query(ctx, `select oa.id, oa.balance, oa.ytd_debit, oa.ytd_credit from opening_aux oa
		where oa.book_id=$1 and oa.account_id=$2 order by oa.id`, bookID, accountID)
	if err != nil {
		return nil, err
	}
	out := []OpeningAuxRow{}
	idx := map[int64]int{}
	for rows.Next() {
		var id int64
		var r OpeningAuxRow
		if err := rows.Scan(&id, &r.Balance, &r.YtdDebit, &r.YtdCredit); err != nil {
			rows.Close()
			return nil, err
		}
		idx[id] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `select oi.opening_id, oi.type_id, oi.item_id, i.code, i.name
		from opening_aux_items oi join opening_aux oa on oa.id=oi.opening_id
		join aux_items i on i.id=oi.item_id join aux_types t on t.id=oi.type_id
		where oa.book_id=$1 and oa.account_id=$2 order by t.sort, t.id`, bookID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var ref auxiliary.Ref
		if err := rows.Scan(&id, &ref.TypeID, &ref.ItemID, &ref.ItemCode, &ref.ItemName); err != nil {
			return nil, err
		}
		out[idx[id]].Aux = append(out[idx[id]].Aux, ref)
	}
	return out, rows.Err()
}

// SaveOpeningAux 整体替换一个科目按辅助核算项目的期初，并同步该科目的期初合计。
func (s *Service) SaveOpeningAux(ctx context.Context, b *book.Book, accountID int64, items []OpeningAuxRow) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		if _, closed, err := s.books.LastClosed(ctx, b.ID); err != nil {
			return err
		} else if closed {
			return httpx.Conflict("启用期间已结账，不能修改期初数据（请先反结账）")
		}
		a, err := s.accounts.Get(ctx, b.ID, accountID)
		if err != nil {
			return err
		}
		checker, err := s.aux.Checker(ctx, b.ID)
		if err != nil {
			return err
		}
		if len(checker.Required(accountID)) == 0 {
			return httpx.BadRequest("科目未启用辅助核算")
		}
		cats, err := s.books.Categories(ctx, b.ID)
		if err != nil {
			return err
		}
		isPL := false
		for _, c := range cats {
			isPL = isPL || (c.LedgerID == a.LedgerID && c.Code == a.Category && c.IsPL)
		}
		q := s.db.Q(ctx)
		if _, err := q.Exec(ctx, `delete from opening_aux where account_id=$1`, accountID); err != nil {
			return err
		}
		seen := map[string]bool{}
		var total OpeningAuxRow
		for n, it := range items {
			if it.Balance.IsZero() && it.YtdDebit.IsZero() && it.YtdCredit.IsZero() {
				continue
			}
			refs, err := checker.Normalize(accountID, it.Aux, true)
			if err != nil {
				return httpx.BadRequest(fmt.Sprintf("第 %d 行：%s", n+1, err.Error()))
			}
			if key := auxiliary.Key(refs); seen[key] {
				return httpx.BadRequest(fmt.Sprintf("第 %d 行：辅助核算项目组合重复", n+1))
			} else {
				seen[key] = true
			}
			if isPL && !it.Balance.IsZero() {
				return httpx.BadRequest("损益（收支）类科目没有期初余额，只需录入本年累计发生额")
			}
			if b.StartMonth == 1 && (!it.YtdDebit.IsZero() || !it.YtdCredit.IsZero()) {
				return httpx.BadRequest("年初启用的账套无需录入本年累计发生额")
			}
			var id int64
			if err := q.QueryRow(ctx, `insert into opening_aux(book_id, account_id, balance, ytd_debit, ytd_credit)
				values($1,$2,$3,$4,$5) returning id`, b.ID, accountID, it.Balance.Round(2), it.YtdDebit.Round(2), it.YtdCredit.Round(2)).Scan(&id); err != nil {
				return err
			}
			for _, ref := range refs {
				if _, err := q.Exec(ctx, `insert into opening_aux_items(opening_id, type_id, item_id) values($1,$2,$3)`, id, ref.TypeID, ref.ItemID); err != nil {
					return err
				}
			}
			total.Balance = total.Balance.Add(it.Balance.Round(2))
			total.YtdDebit = total.YtdDebit.Add(it.YtdDebit.Round(2))
			total.YtdCredit = total.YtdCredit.Add(it.YtdCredit.Round(2))
		}
		if _, err := q.Exec(ctx, `insert into opening_balances(book_id, account_id, balance, ytd_debit, ytd_credit)
			values($1,$2,$3,$4,$5) on conflict (account_id) do update
			set balance=excluded.balance, ytd_debit=excluded.ytd_debit, ytd_credit=excluded.ytd_credit`,
			b.ID, accountID, total.Balance, total.YtdDebit, total.YtdCredit); err != nil {
			return err
		}
		audit.Log(ctx, s.db, b.ID, "opening.save_aux", "account", accountID, map[string]int{"count": len(items)})
		return nil
	})
}

// ---- 按组合的余额（期末结转用） ----

// ComboBalance 某科目一个辅助核算组合的期末净额（借方为正）。
type ComboBalance struct {
	Aux []auxiliary.Ref
	Net Dec
}

// ComboBalances 返回启用辅助核算的科目截至 to 期末、按项目组合的净额。
func (s *Service) ComboBalances(ctx context.Context, bookID int64, to kernel.Period) (map[int64][]ComboBalance, error) {
	rows, err := s.db.Q(ctx).Query(ctx, `
		select account_id, combo, sum(net) from (
			select oa.account_id, `+openingComboSQL+` as combo,
				case when a.direction='D' then oa.balance else -oa.balance end as net
			from opening_aux oa join accounts a on a.id=oa.account_id where oa.book_id=$1
			union all
			select l.account_id, `+lineComboSQL+`, l.debit - l.credit
			from voucher_lines l join vouchers v on v.id=l.voucher_id
			where l.book_id=$1 and v.year*100+v.month <= $2
				and exists(select 1 from account_aux_types b where b.account_id=l.account_id)
		) t group by account_id, combo order by account_id, combo`, bookID, to.YM())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]ComboBalance{}
	for rows.Next() {
		var id int64
		var key string
		var net Dec
		if err := rows.Scan(&id, &key, &net); err != nil {
			return nil, err
		}
		out[id] = append(out[id], ComboBalance{Aux: parseCombo(key), Net: net})
	}
	return out, rows.Err()
}

// ---- 辅助核算余额表 ----

type AuxBalanceRow struct {
	ItemID      int64  `json:"item_id"`
	ItemCode    string `json:"item_code"`
	ItemName    string `json:"item_name"`
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	OpenDebit   Dec    `json:"open_debit"`
	OpenCredit  Dec    `json:"open_credit"`
	Debit       Dec    `json:"debit"`
	Credit      Dec    `json:"credit"`
	EndDebit    Dec    `json:"end_debit"`
	EndCredit   Dec    `json:"end_credit"`
}

type AuxBalance struct {
	Rows  []AuxBalanceRow `json:"rows"`
	Total AuxBalanceRow   `json:"total"`
}

// AuxBalance 某辅助核算类别下，各项目在各科目的期初、发生额与期末。accountID=0 表示全部科目。
func (s *Service) AuxBalance(ctx context.Context, b *book.Book, typeID, accountID int64, from, to kernel.Period) (*AuxBalance, error) {
	rows, err := s.db.Q(ctx).Query(ctx, `
		with ob as (
			select oa.account_id, oi.item_id, sum(oa.balance) as bal
			from opening_aux oa join opening_aux_items oi on oi.opening_id=oa.id
			where oa.book_id=$1 and oi.type_id=$2 group by 1,2),
		mv as (
			select l.account_id, la.item_id,
				coalesce(sum(l.debit - l.credit) filter (where v.year*100+v.month < $3), 0) as pre,
				coalesce(sum(l.debit)  filter (where v.year*100+v.month >= $3), 0) as d,
				coalesce(sum(l.credit) filter (where v.year*100+v.month >= $3), 0) as c
			from voucher_lines l join vouchers v on v.id=l.voucher_id
			join voucher_line_aux la on la.line_id=l.id and la.type_id=$2
			where l.book_id=$1 and v.year*100+v.month <= $4 group by 1,2)
		select i.id, i.code, i.name, a.id, a.code, a.name, a.direction,
			coalesce(ob.bal,0), coalesce(mv.pre,0), coalesce(mv.d,0), coalesce(mv.c,0)
		from (select account_id, item_id from ob union select account_id, item_id from mv) k
		join accounts a on a.id=k.account_id join aux_items i on i.id=k.item_id
		left join ob on ob.account_id=k.account_id and ob.item_id=k.item_id
		left join mv on mv.account_id=k.account_id and mv.item_id=k.item_id
		where ($5=0 or a.id=$5)
		order by i.code, a.code`, b.ID, typeID, from.YM(), to.YM(), accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &AuxBalance{Rows: []AuxBalanceRow{}}
	for rows.Next() {
		var r AuxBalanceRow
		var dir string
		var bal, pre Dec
		if err := rows.Scan(&r.ItemID, &r.ItemCode, &r.ItemName, &r.AccountID, &r.AccountCode, &r.AccountName, &dir, &bal, &pre, &r.Debit, &r.Credit); err != nil {
			return nil, err
		}
		open := pre.Add(bal)
		end := open.Add(r.Debit).Sub(r.Credit)
		if dir == kernel.Debit {
			r.OpenDebit, r.EndDebit = open, end
		} else {
			open = pre.Sub(bal)
			end = open.Add(r.Debit).Sub(r.Credit)
			r.OpenCredit, r.EndCredit = open.Neg(), end.Neg()
		}
		if r.OpenDebit.IsZero() && r.OpenCredit.IsZero() && r.Debit.IsZero() && r.Credit.IsZero() {
			continue
		}
		t := &out.Total
		t.OpenDebit, t.OpenCredit = t.OpenDebit.Add(r.OpenDebit), t.OpenCredit.Add(r.OpenCredit)
		t.Debit, t.Credit = t.Debit.Add(r.Debit), t.Credit.Add(r.Credit)
		t.EndDebit, t.EndCredit = t.EndDebit.Add(r.EndDebit), t.EndCredit.Add(r.EndCredit)
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}

// AuxDetail 辅助核算明细账：某项目（可限定科目）在区间内的逐笔分录。
func (s *Service) AuxDetail(ctx context.Context, b *book.Book, itemID, accountID int64, from, to kernel.Period) ([]DetailRow, error) {
	if to.Before(from) {
		return nil, httpx.BadRequest("结束期间不能早于开始期间")
	}
	q := s.db.Q(ctx)
	var net Dec
	if err := q.QueryRow(ctx, `select
		coalesce((select sum(case when a.direction='D' then oa.balance else -oa.balance end)
			from opening_aux oa join opening_aux_items oi on oi.opening_id=oa.id join accounts a on a.id=oa.account_id
			where oa.book_id=$1 and oi.item_id=$2 and ($3=0 or oa.account_id=$3)), 0)
		+ coalesce((select sum(l.debit - l.credit)
			from voucher_lines l join vouchers v on v.id=l.voucher_id join voucher_line_aux la on la.line_id=l.id
			where l.book_id=$1 and la.item_id=$2 and ($3=0 or l.account_id=$3) and v.year*100+v.month < $4), 0)`,
		b.ID, itemID, accountID, from.YM()).Scan(&net); err != nil {
		return nil, err
	}
	out := []DetailRow{}
	push := func(kind, summary string, d, c Dec) {
		dir, bal := dirText(net)
		out = append(out, DetailRow{Kind: kind, Summary: summary, Debit: d, Credit: c, Dir: dir, Balance: bal})
	}
	push("open", "期初余额", decimal.Zero, decimal.Zero)
	rows, err := q.Query(ctx, `select v.year, v.month, v.vdate, v.id, v.word, v.number, a.code, a.name, l.summary, l.debit, l.credit
		from voucher_lines l join vouchers v on v.id=l.voucher_id join voucher_line_aux la on la.line_id=l.id
		join accounts a on a.id=l.account_id
		where l.book_id=$1 and la.item_id=$2 and ($3=0 or l.account_id=$3) and v.year*100+v.month between $4 and $5
		order by v.year, v.month, v.vdate, v.word, v.number, l.line_no`, b.ID, itemID, accountID, from.YM(), to.YM())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cur := from
	var monD, monC Dec
	closeMonth := func() {
		push("month", cur.String()+" 本期合计", monD, monC)
		monD, monC = decimal.Zero, decimal.Zero
		cur = cur.Next()
	}
	for rows.Next() {
		var y, m, number int
		var date time.Time
		var vid int64
		var word, code, name, summary string
		var d, c Dec
		if err := rows.Scan(&y, &m, &date, &vid, &word, &number, &code, &name, &summary, &d, &c); err != nil {
			return nil, err
		}
		for p := (kernel.Period{Year: y, Month: m}); cur.Before(p); {
			closeMonth()
		}
		net = net.Add(d).Sub(c)
		monD, monC = monD.Add(d), monC.Add(c)
		dir, bal := dirText(net)
		out = append(out, DetailRow{Kind: "line", Date: date.Format(time.DateOnly), VoucherID: vid,
			VoucherNo: fmt.Sprintf("%s-%d", word, number), Account: code + " " + name, Summary: summary,
			Debit: d, Credit: c, Dir: dir, Balance: bal})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for !to.Before(cur) {
		closeMonth()
	}
	return out, nil
}

func (s *Service) auxRoutes(r chi.Router) {
	bk := func(r *http.Request) *book.Book { return book.From(r.Context()).Book }
	r.Get("/opening/aux/{accountID}", httpx.Handle(func(r *http.Request) (any, error) {
		id, err := httpx.ParamInt64(r, "accountID")
		if err != nil {
			return nil, err
		}
		return s.OpeningAux(r.Context(), bk(r).ID, id)
	}))
	r.With(book.Require(PermOpeningWrite)).Put("/opening/aux/{accountID}", httpx.Handle(func(r *http.Request) (any, error) {
		id, err := httpx.ParamInt64(r, "accountID")
		if err != nil {
			return nil, err
		}
		in, err := httpx.Bind[struct {
			Items []OpeningAuxRow `json:"items"`
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.SaveOpeningAux(r.Context(), bk(r), id, in.Items)
	}))
	r.Get("/ledger/aux-balance", httpx.Handle(func(r *http.Request) (any, error) {
		from, to, err := periodRange(r)
		if err != nil {
			return nil, err
		}
		return s.AuxBalance(r.Context(), bk(r), httpx.QueryInt64(r, "type_id"), httpx.QueryInt64(r, "account_id"), from, to)
	}))
	r.Get("/ledger/aux-detail", httpx.Handle(func(r *http.Request) (any, error) {
		from, to, err := periodRange(r)
		if err != nil {
			return nil, err
		}
		return s.AuxDetail(r.Context(), bk(r), httpx.QueryInt64(r, "item_id"), httpx.QueryInt64(r, "account_id"), from, to)
	}))
}
