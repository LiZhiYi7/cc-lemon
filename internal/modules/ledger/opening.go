package ledger

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const PermOpeningWrite = "opening:write"

// Opening 末级科目的期初数据。Balance 按科目余额方向填列。
type Opening struct {
	AccountID int64 `json:"account_id"`
	Balance   Dec   `json:"balance"`
	YtdDebit  Dec   `json:"ytd_debit"`
	YtdCredit Dec   `json:"ytd_credit"`
}

func (s *Service) Openings(ctx context.Context, bookID int64) ([]Opening, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (Opening, error) {
		var o Opening
		return o, r.Scan(&o.AccountID, &o.Balance, &o.YtdDebit, &o.YtdCredit)
	}, `select account_id, balance, ytd_debit, ytd_credit from opening_balances where book_id=$1`, bookID)
}

// SaveOpenings 批量保存期初；启用期间结账后不可再改。
func (s *Service) SaveOpenings(ctx context.Context, b *book.Book, items []Opening) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		if _, closed, err := s.books.LastClosed(ctx, b.ID); err != nil {
			return err
		} else if closed {
			return httpx.Conflict("启用期间已结账，不能修改期初数据（请先反结账）")
		}
		accounts, err := s.accounts.List(ctx, b.ID)
		if err != nil {
			return err
		}
		cats, err := s.books.Categories(ctx, b.ID)
		if err != nil {
			return err
		}
		pl := map[string]bool{}
		for _, c := range cats {
			if c.IsPL {
				pl[c.Code] = true
			}
		}
		byID := map[int64]int{}
		for i, a := range accounts {
			byID[a.ID] = i
		}
		q := s.db.Q(ctx)
		for _, it := range items {
			i, ok := byID[it.AccountID]
			if !ok {
				return httpx.BadRequest("科目不存在")
			}
			a := accounts[i]
			if !a.IsLeaf {
				return httpx.BadRequest("只能在末级科目录入期初：" + a.Code + " " + a.Name)
			}
			if pl[a.Category] && !it.Balance.IsZero() {
				return httpx.BadRequest("损益（收支）类科目没有期初余额，只需录入本年累计发生额：" + a.Code + " " + a.Name)
			}
			if b.StartMonth == 1 && (!it.YtdDebit.IsZero() || !it.YtdCredit.IsZero()) {
				return httpx.BadRequest("年初启用的账套无需录入本年累计发生额")
			}
			if _, err := q.Exec(ctx, `insert into opening_balances(book_id, account_id, balance, ytd_debit, ytd_credit)
				values($1,$2,$3,$4,$5) on conflict (account_id) do update
				set balance=excluded.balance, ytd_debit=excluded.ytd_debit, ytd_credit=excluded.ytd_credit`,
				b.ID, it.AccountID, it.Balance.Round(2), it.YtdDebit.Round(2), it.YtdCredit.Round(2)); err != nil {
				return err
			}
		}
		audit.Log(ctx, s.db, b.ID, "opening.save", "opening", "", map[string]int{"count": len(items)})
		return nil
	})
}

// OpeningCheck 各账簿期初试算平衡。
type OpeningCheck struct {
	LedgerID int64 `json:"ledger_id"`
	Debit    Dec   `json:"debit"`  // 借方余额合计
	Credit   Dec   `json:"credit"` // 贷方余额合计
	Balanced bool  `json:"balanced"`
}

func (s *Service) CheckOpenings(ctx context.Context, b *book.Book) ([]OpeningCheck, error) {
	ledgers, err := s.books.Ledgers(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	out := make([]OpeningCheck, len(ledgers))
	idx := map[int64]int{}
	for i, l := range ledgers {
		out[i].LedgerID = l.ID
		idx[l.ID] = i
	}
	rows, err := s.db.Q(ctx).Query(ctx, `select a.ledger_id, a.direction, coalesce(sum(o.balance),0)
		from opening_balances o join accounts a on a.id=o.account_id where o.book_id=$1 group by 1,2`, b.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var lid int64
		var dir string
		var sum decimal.Decimal
		if err := rows.Scan(&lid, &dir, &sum); err != nil {
			return nil, err
		}
		if dir == kernel.Debit {
			out[idx[lid]].Debit = sum
		} else {
			out[idx[lid]].Credit = sum
		}
	}
	for i := range out {
		out[i].Balanced = out[i].Debit.Equal(out[i].Credit)
	}
	return out, rows.Err()
}
