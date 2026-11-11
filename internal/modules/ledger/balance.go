// Package ledger 账簿（读模型）：余额引擎、期初余额、科目余额表、明细账、总账。
//
// 余额不落表，始终由“期初 + 凭证分录”实时汇总，天然一致。
// 若将来数据量需要，可在 Balances 内部引入按期间的快照缓存，对调用方透明。
package ledger

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
)

type Dec = decimal.Decimal

// Balance 一个科目在查询区间 [From, To] 的余额与发生额。
// 所有“净额”均为借方为正（借 − 贷）；上级科目为下级之和。
type Balance struct {
	Account *account.Account

	OpenNet     Dec // From 期初净额
	Debit       Dec // 区间借方发生额
	Credit      Dec // 区间贷方发生额
	YearOpenNet Dec // To 所在年度的年初净额
	YtdDebit    Dec // To 所在年度年初至 To 的累计借方
	YtdCredit   Dec

	// 实际发生额：剔除指定来源凭证（如结转损益）后的发生额，供利润表类报表取数。
	ActDebit, ActCredit       Dec
	YtdActDebit, YtdActCredit Dec

	// 末级科目按余额方向分别汇总（用于“应收/预收”等按明细重分类的报表项目）。
	EndDebitBal, EndCreditBal           Dec
	YearOpenDebitBal, YearOpenCreditBal Dec
}

// EndNet To 期末净额。
func (b *Balance) EndNet() Dec { return b.OpenNet.Add(b.Debit).Sub(b.Credit) }

func (b *Balance) add(o *Balance) {
	b.OpenNet = b.OpenNet.Add(o.OpenNet)
	b.Debit = b.Debit.Add(o.Debit)
	b.Credit = b.Credit.Add(o.Credit)
	b.YearOpenNet = b.YearOpenNet.Add(o.YearOpenNet)
	b.YtdDebit = b.YtdDebit.Add(o.YtdDebit)
	b.YtdCredit = b.YtdCredit.Add(o.YtdCredit)
	b.ActDebit = b.ActDebit.Add(o.ActDebit)
	b.ActCredit = b.ActCredit.Add(o.ActCredit)
	b.YtdActDebit = b.YtdActDebit.Add(o.YtdActDebit)
	b.YtdActCredit = b.YtdActCredit.Add(o.YtdActCredit)
	b.EndDebitBal = b.EndDebitBal.Add(o.EndDebitBal)
	b.EndCreditBal = b.EndCreditBal.Add(o.EndCreditBal)
	b.YearOpenDebitBal = b.YearOpenDebitBal.Add(o.YearOpenDebitBal)
	b.YearOpenCreditBal = b.YearOpenCreditBal.Add(o.YearOpenCreditBal)
}

// Set 一次余额查询的结果集，覆盖账套全部科目。
type Set struct {
	Accounts []*account.Account // 按账簿、编码排序
	ByID     map[int64]*Balance
}

// ByCode 在指定账簿内按编码查找。
func (s *Set) ByCode(ledgerID int64, code string) *Balance {
	for _, a := range s.Accounts {
		if a.LedgerID == ledgerID && a.Code == code {
			return s.ByID[a.ID]
		}
	}
	return nil
}

type Service struct {
	db       *db.DB
	books    *book.Service
	accounts *account.Service
	aux      *auxiliary.Service
}

func New(d *db.DB, books *book.Service, accounts *account.Service, aux *auxiliary.Service) *Service {
	return &Service{db: d, books: books, accounts: accounts, aux: aux}
}

func splitSign(net Dec) (debit, credit Dec) {
	if net.IsPositive() {
		return net, decimal.Zero
	}
	return decimal.Zero, net.Neg()
}

// Balances 计算区间 [from, to] 的全科目余额。excludeSources 为计算“实际发生额”时要剔除的凭证来源。
func (s *Service) Balances(ctx context.Context, b *book.Book, from, to kernel.Period, excludeSources []string) (*Set, error) {
	accounts, err := s.accounts.List(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	cats, err := s.books.Categories(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	type catKey struct {
		ledger int64
		code   string
	}
	isPL := map[catKey]bool{}
	for _, c := range cats {
		isPL[catKey{c.LedgerID, c.Code}] = c.IsPL
	}
	set := &Set{Accounts: accounts, ByID: make(map[int64]*Balance, len(accounts))}
	for _, a := range accounts {
		set.ByID[a.ID] = &Balance{Account: a}
	}
	q := s.db.Q(ctx)
	inStartYear := to.Year == b.StartYear

	// 1) 期初数据
	rows, err := q.Query(ctx, `select account_id, balance, ytd_debit, ytd_credit from opening_balances where book_id=$1`, b.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var bal, yd, yc Dec
		if err := rows.Scan(&id, &bal, &yd, &yc); err != nil {
			rows.Close()
			return nil, err
		}
		x := set.ByID[id]
		if x == nil {
			continue
		}
		net := bal
		if x.Account.Direction == kernel.Credit {
			net = bal.Neg()
		}
		x.OpenNet = net
		x.YearOpenNet = net
		if inStartYear {
			x.YtdDebit, x.YtdCredit = yd, yc
			x.YtdActDebit, x.YtdActCredit = yd, yc
			// 损益类期初只有累计发生额、无余额，年初为零；其余科目倒推年初余额。
			if !isPL[catKey{x.Account.LedgerID, x.Account.Category}] {
				x.YearOpenNet = net.Sub(yd).Add(yc)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2) 凭证分录汇总
	if excludeSources == nil {
		excludeSources = []string{}
	}
	rows, err = q.Query(ctx, `
		select l.account_id,
			coalesce(sum(l.debit - l.credit) filter (where v.year*100+v.month < $2), 0),
			coalesce(sum(l.debit)  filter (where v.year*100+v.month >= $2), 0),
			coalesce(sum(l.credit) filter (where v.year*100+v.month >= $2), 0),
			coalesce(sum(l.debit - l.credit) filter (where v.year < $4), 0),
			coalesce(sum(l.debit)  filter (where v.year = $4), 0),
			coalesce(sum(l.credit) filter (where v.year = $4), 0),
			coalesce(sum(l.debit)  filter (where v.year*100+v.month >= $2 and v.source <> all($5)), 0),
			coalesce(sum(l.credit) filter (where v.year*100+v.month >= $2 and v.source <> all($5)), 0),
			coalesce(sum(l.debit)  filter (where v.year = $4 and v.source <> all($5)), 0),
			coalesce(sum(l.credit) filter (where v.year = $4 and v.source <> all($5)), 0)
		from voucher_lines l join vouchers v on v.id = l.voucher_id
		where l.book_id = $1 and v.year*100+v.month <= $3
		group by l.account_id`, b.ID, from.YM(), to.YM(), to.Year, excludeSources)
	if err != nil {
		return nil, err
	}
	if err := scanAgg(rows, set, inStartYear); err != nil {
		return nil, err
	}

	// 3) 末级 → 上级逐级汇总（按编码倒序保证先处理下级）
	for i := len(accounts) - 1; i >= 0; i-- {
		a := accounts[i]
		x := set.ByID[a.ID]
		if a.IsLeaf {
			x.EndDebitBal, x.EndCreditBal = splitSign(x.EndNet())
			x.YearOpenDebitBal, x.YearOpenCreditBal = splitSign(x.YearOpenNet)
		}
		if a.ParentID != nil {
			if p := set.ByID[*a.ParentID]; p != nil {
				p.add(x)
			}
		}
	}
	return set, nil
}

func scanAgg(rows pgx.Rows, set *Set, inStartYear bool) error {
	defer rows.Close()
	for rows.Next() {
		var id int64
		var preNet, d, c, preYearNet, yd, yc, ad, ac, yad, yac Dec
		if err := rows.Scan(&id, &preNet, &d, &c, &preYearNet, &yd, &yc, &ad, &ac, &yad, &yac); err != nil {
			return err
		}
		x := set.ByID[id]
		if x == nil {
			continue
		}
		if !inStartYear {
			// 非启用年度：年初 = 期初 + 以前年度全部分录
			x.YearOpenNet = x.OpenNet.Add(preYearNet)
		}
		x.OpenNet = x.OpenNet.Add(preNet)
		x.Debit, x.Credit = d, c
		x.YtdDebit, x.YtdCredit = x.YtdDebit.Add(yd), x.YtdCredit.Add(yc)
		x.ActDebit, x.ActCredit = ad, ac
		x.YtdActDebit, x.YtdActCredit = x.YtdActDebit.Add(yad), x.YtdActCredit.Add(yac)
	}
	return rows.Err()
}
