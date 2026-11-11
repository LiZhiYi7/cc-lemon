package closing

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/modules/ledger"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

// piece 一笔待结转的余额：某科目（或其某个辅助核算组合）的期末净额，借方为正。
type piece struct {
	accountID int64
	net       Dec
	aux       []auxiliary.Ref
}

// resolver 把结转规则的“来源选择器”解析为具体的待结转余额。一个期间构建一次，各规则复用。
type resolver struct {
	set        *ledger.Set
	bindings   map[int64][]int64
	combos     map[int64][]ledger.ComboBalance
	typeByCode map[string]int64
	itemByCode map[int64]map[string]int64 // 类别 ID → 项目编码 → 项目 ID
}

func (s *Service) newResolver(ctx context.Context, b *book.Book, p kernel.Period) (*resolver, error) {
	rv := &resolver{typeByCode: map[string]int64{}, itemByCode: map[int64]map[string]int64{}}
	var err error
	if rv.set, err = s.ledgers.Balances(ctx, b, p, p, nil); err != nil {
		return nil, err
	}
	if rv.bindings, err = s.aux.Bindings(ctx, b.ID); err != nil {
		return nil, err
	}
	if rv.combos, err = s.ledgers.ComboBalances(ctx, b.ID, p); err != nil {
		return nil, err
	}
	types, err := s.aux.Types(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		rv.typeByCode[t.Code] = t.ID
		rv.itemByCode[t.ID] = map[string]int64{}
	}
	items, err := s.aux.Items(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		rv.itemByCode[it.TypeID][it.Code] = it.ID
	}
	return rv, nil
}

// selectors 规则的全部来源选择器（兼容旧的 source_categories / source_accounts 写法）。
func selectors(r *Rule) []standard.ClosingSource {
	out := slices.Clone(r.Sources)
	if len(r.SourceCategories)+len(r.SourceAccounts) > 0 {
		out = append(out, standard.ClosingSource{Categories: r.SourceCategories, Accounts: r.SourceAccounts})
	}
	return out
}

func matches(sel *standard.ClosingSource, a *account.Account) bool {
	if slices.Contains(sel.Categories, a.Category) {
		return true
	}
	for _, code := range sel.Accounts {
		if strings.HasPrefix(a.Code, string(code)) {
			return true
		}
	}
	return false
}

// pieces 解析规则在指定账簿下的待结转余额（不含结转目标科目自身）。
//
// 选择器带辅助核算条件时，只取包含该项目的组合；科目未启用该类别时，
// 仅当选择器声明 unbound 才整体纳入（用于“未区分资金性质的一律视为其他资金”）。
func (rv *resolver) pieces(r *Rule, ledgerID, targetID int64) ([]piece, error) {
	var out []piece
	sels := selectors(r)
	for _, a := range rv.set.Accounts {
		if a.LedgerID != ledgerID || !a.IsLeaf || a.ID == targetID {
			continue
		}
		bound := rv.bindings[a.ID]
		whole := false
		seen := map[string]bool{}
		for i := range sels {
			sel := &sels[i]
			if !matches(sel, a) {
				continue
			}
			var want []int64 // 组合中必须包含的项目
			applies := true
			for typeCode, itemCode := range sel.Aux {
				typeID, ok := rv.typeByCode[typeCode]
				if !ok {
					return nil, httpx.BadRequest(fmt.Sprintf("结转规则“%s”引用了不存在的辅助核算类别 %s", r.Name, typeCode))
				}
				itemID, ok := rv.itemByCode[typeID][itemCode]
				if !ok {
					return nil, httpx.BadRequest(fmt.Sprintf("结转规则“%s”引用了不存在的辅助核算项目 %s/%s", r.Name, typeCode, itemCode))
				}
				if !slices.Contains(bound, typeID) {
					applies = false
				}
				want = append(want, itemID)
			}
			if !applies {
				if !sel.Unbound {
					continue
				}
				want = nil
			}
			if len(bound) == 0 {
				whole = true
				continue
			}
			for _, c := range rv.combos[a.ID] {
				ok := true
				for _, itemID := range want {
					ok = ok && slices.ContainsFunc(c.Aux, func(ref auxiliary.Ref) bool { return ref.ItemID == itemID })
				}
				key := auxiliary.Key(c.Aux)
				if ok && !seen[key] && !c.Net.IsZero() {
					seen[key] = true
					out = append(out, piece{accountID: a.ID, net: c.Net, aux: c.Aux})
				}
			}
		}
		if whole {
			if n := rv.set.ByID[a.ID].EndNet(); !n.IsZero() {
				out = append(out, piece{accountID: a.ID, net: n})
			}
		}
	}
	// only_if：仅当来源合计为贷方（或借方）余额时才结转，例如经营结余为亏损时不转出
	total := decimal.Zero
	for _, pc := range out {
		total = total.Add(pc.net)
	}
	if (r.OnlyIf == "credit" && total.Sign() >= 0) || (r.OnlyIf == "debit" && total.Sign() <= 0) {
		return nil, nil
	}
	return out, nil
}

func pending(pieces []piece) (bool, Dec) {
	total := decimal.Zero
	for _, pc := range pieces {
		total = total.Add(pc.net)
	}
	return len(pieces) > 0, total.Abs()
}
