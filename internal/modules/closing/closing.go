// Package closing 期末处理：按规则生成结转凭证、结账检查、结账与反结账。
//
// 结转规则来自账套级配置（kind=closing_rule），建账时由准则模板复制，可按账套调整。
package closing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/modules/ledger"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/modules/voucher"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const (
	PermRun   = "closing:run"
	PermClose = "period:close"

	FreqMonthly = "monthly"
	FreqYearly  = "yearly"
)

type Dec = decimal.Decimal

type Rule = standard.ClosingRule

// SourceOf 规则生成凭证的来源标识。
func SourceOf(ruleCode string) string { return "closing:" + ruleCode }

type Service struct {
	db       *db.DB
	books    *book.Service
	ledgers  *ledger.Service
	vouchers *voucher.Service
}

func New(d *db.DB, books *book.Service, ledgers *ledger.Service, vouchers *voucher.Service) *Service {
	return &Service{db: d, books: books, ledgers: ledgers, vouchers: vouchers}
}

func (s *Service) Rules(ctx context.Context, bookID int64) ([]Rule, error) {
	items, err := s.books.Configs(ctx, bookID, book.KindClosingRule)
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, len(items))
	for i, it := range items {
		if err := json.Unmarshal(it.Definition, &rules[i]); err != nil {
			return nil, fmt.Errorf("结转规则 %s 定义损坏: %w", it.Code, err)
		}
	}
	return rules, nil
}

// ActivityExcludedSources 不计入“实际发生额”的凭证来源（结转损益类凭证）。
func (s *Service) ActivityExcludedSources(ctx context.Context, bookID int64) ([]string, error) {
	rules, err := s.Rules(ctx, bookID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range rules {
		if r.ExcludeFromActivity {
			out = append(out, SourceOf(r.Code))
		}
	}
	return out, nil
}

func applicable(r *Rule, p kernel.Period) bool {
	return r.Frequency != FreqYearly || p.Month == 12
}

// sources 规则涉及的末级来源科目。
func sources(r *Rule, ledgerID int64, set *ledger.Set) []*ledger.Balance {
	var out []*ledger.Balance
	for _, a := range set.Accounts {
		if a.LedgerID != ledgerID || !a.IsLeaf {
			continue
		}
		hit := slices.Contains(r.SourceCategories, a.Category)
		for _, code := range r.SourceAccounts {
			hit = hit || strings.HasPrefix(a.Code, string(code))
		}
		if hit {
			out = append(out, set.ByID[a.ID])
		}
	}
	return out
}

func pending(bals []*ledger.Balance) (bool, Dec) {
	total := decimal.Zero
	any := false
	for _, x := range bals {
		if n := x.EndNet(); !n.IsZero() {
			any = true
			total = total.Add(n)
		}
	}
	return any, total.Abs()
}

type RuleStatus struct {
	Rule
	Applicable bool    `json:"applicable"`
	Pending    bool    `json:"pending"` // 来源科目仍有余额待结转
	Amount     Dec     `json:"amount"`  // 待转入目标科目的净额
	VoucherIDs []int64 `json:"voucher_ids"`
}

// Status 某期间各结转规则的执行情况。
func (s *Service) Status(ctx context.Context, b *book.Book, p kernel.Period) ([]RuleStatus, error) {
	rules, err := s.Rules(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	set, err := s.ledgers.Balances(ctx, b, p, p, nil)
	if err != nil {
		return nil, err
	}
	out := make([]RuleStatus, len(rules))
	for i := range rules {
		st := RuleStatus{Rule: rules[i], Applicable: applicable(&rules[i], p)}
		lg, err := s.books.LedgerByCode(ctx, b.ID, st.Ledger)
		if err != nil {
			return nil, err
		}
		if st.Applicable {
			st.Pending, st.Amount = pending(sources(&st.Rule, lg.ID, set))
		}
		if st.VoucherIDs, err = s.vouchers.IDsBySource(ctx, b.ID, p, SourceOf(st.Code)); err != nil {
			return nil, err
		}
		out[i] = st
	}
	return out, nil
}

// Generate 执行一条结转规则：重新生成该期间的结转凭证（已有的同规则凭证先删除）。
// 无余额可结转时返回 nil。
func (s *Service) Generate(ctx context.Context, b *book.Book, p kernel.Period, code string) (*voucher.Voucher, error) {
	var rule Rule
	if err := s.books.Config(ctx, b.ID, book.KindClosingRule, code, &rule); err != nil {
		return nil, err
	}
	if !applicable(&rule, p) {
		return nil, httpx.BadRequest(rule.Name + "仅在年末（12 月）执行")
	}
	var out *voucher.Voucher
	err := s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		if err := s.books.EnsureOpen(ctx, b, p); err != nil {
			return err
		}
		old, err := s.vouchers.IDsBySource(ctx, b.ID, p, SourceOf(code))
		if err != nil {
			return err
		}
		for _, id := range old {
			if err := s.vouchers.Delete(ctx, b, id); err != nil {
				return err
			}
		}
		lg, err := s.books.LedgerByCode(ctx, b.ID, rule.Ledger)
		if err != nil {
			return err
		}
		set, err := s.ledgers.Balances(ctx, b, p, p, nil)
		if err != nil {
			return err
		}
		target := set.ByCode(lg.ID, string(rule.Target))
		if target == nil {
			return httpx.BadRequest("结转目标科目 " + string(rule.Target) + " 不存在，请调整结转规则")
		}
		if !target.Account.IsLeaf {
			return httpx.BadRequest(fmt.Sprintf("结转目标科目 %s %s 有下级科目，请在结转规则中指定末级科目", target.Account.Code, target.Account.Name))
		}
		bindings, err := s.ledgers.AuxBindings(ctx, b.ID)
		if err != nil {
			return err
		}
		if len(bindings[target.Account.ID]) > 0 {
			return httpx.BadRequest(fmt.Sprintf("结转目标科目 %s %s 启用了辅助核算，无法自动结转", target.Account.Code, target.Account.Name))
		}
		combos, err := s.ledgers.ComboBalances(ctx, b.ID, p)
		if err != nil {
			return err
		}
		var debits, credits []voucher.LineInput
		net := decimal.Zero
		transfer := func(accountID int64, n Dec, aux []auxiliary.Ref) {
			if n.IsZero() {
				return
			}
			net = net.Add(n)
			if n.IsPositive() { // 借方余额 → 贷记转出
				credits = append(credits, voucher.LineInput{Summary: rule.Summary, AccountID: accountID, Credit: n, Aux: aux})
			} else {
				debits = append(debits, voucher.LineInput{Summary: rule.Summary, AccountID: accountID, Debit: n.Neg(), Aux: aux})
			}
		}
		for _, x := range sources(&rule, lg.ID, set) {
			if x.Account.ID == target.Account.ID {
				continue
			}
			if len(bindings[x.Account.ID]) == 0 {
				transfer(x.Account.ID, x.EndNet(), nil)
				continue
			}
			// 启用辅助核算的科目按项目组合逐一转平
			for _, c := range combos[x.Account.ID] {
				transfer(x.Account.ID, c.Net, c.Aux)
			}
		}
		if len(debits)+len(credits) == 0 {
			return nil
		}
		switch net.Sign() {
		case 1:
			debits = append(debits, voucher.LineInput{Summary: rule.Summary, AccountID: target.Account.ID, Debit: net})
		case -1:
			credits = append(credits, voucher.LineInput{Summary: rule.Summary, AccountID: target.Account.ID, Credit: net.Neg()})
		}
		out, err = s.vouchers.Save(ctx, b, 0, voucher.Input{
			Date:  p.LastDay().Format(time.DateOnly),
			Lines: append(debits, credits...),
		}, SourceOf(code))
		return err
	})
	return out, err
}

// SaveRule 调整账套的结转规则。
func (s *Service) SaveRule(ctx context.Context, b *book.Book, code string, r Rule) error {
	r.Code = code
	if r.Name == "" || r.Target == "" {
		return httpx.BadRequest("规则名称与目标科目不能为空")
	}
	if r.Frequency != FreqMonthly && r.Frequency != FreqYearly {
		return httpx.BadRequest("执行频率无效")
	}
	if len(r.SourceCategories)+len(r.SourceAccounts) == 0 {
		return httpx.BadRequest("请指定来源科目或科目类别")
	}
	if _, err := s.books.LedgerByCode(ctx, b.ID, r.Ledger); err != nil {
		return err
	}
	if err := s.books.PutConfig(ctx, b.ID, book.KindClosingRule, code, r.Name, -1, r); err != nil {
		return err
	}
	audit.Log(ctx, s.db, b.ID, "closing_rule.save", "closing_rule", code, r)
	return nil
}

// CheckItem 结账检查项。Blocking 为真且未通过时不能结账。
type CheckItem struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Blocking bool   `json:"blocking"`
	Message  string `json:"message"`
}

// Check 结账前检查。
func (s *Service) Check(ctx context.Context, b *book.Book, p kernel.Period) ([]CheckItem, error) {
	var items []CheckItem
	add := func(name string, ok, blocking bool, msg string) {
		if ok {
			msg = "通过"
		}
		items = append(items, CheckItem{Name: name, OK: ok, Blocking: blocking, Message: msg})
	}
	cur, err := s.books.CurrentPeriod(ctx, b)
	if err != nil {
		return nil, err
	}
	add("结账期间", p == cur, true, fmt.Sprintf("只能结账当前期间 %s", cur))

	if p == b.Start() {
		checks, err := s.ledgers.CheckOpenings(ctx, b)
		if err != nil {
			return nil, err
		}
		ok := true
		for _, c := range checks {
			ok = ok && c.Balanced
		}
		add("期初试算平衡", ok, true, "期初余额借贷不平衡，请检查期初数据")
	}

	q := s.db.Q(ctx)
	var unbalanced int
	if err := q.QueryRow(ctx, `select count(*) from (
			select l.voucher_id from voucher_lines l join vouchers v on v.id=l.voucher_id
			where v.book_id=$1 and v.year=$2 and v.month=$3
			group by l.voucher_id, l.ledger_id having sum(l.debit) <> sum(l.credit)) t`, b.ID, p.Year, p.Month).Scan(&unbalanced); err != nil {
		return nil, err
	}
	add("凭证借贷平衡", unbalanced == 0, true, fmt.Sprintf("有 %d 张凭证借贷不平衡", unbalanced))

	status, err := s.Status(ctx, b, p)
	if err != nil {
		return nil, err
	}
	for _, st := range status {
		if st.Applicable && st.Required {
			add(st.Name, !st.Pending, true, "来源科目仍有余额，请先在“期末结转”中生成凭证")
		}
	}

	var total, unreviewed, gaps int
	if err := q.QueryRow(ctx, `select count(*), count(*) filter (where status <> 'reviewed') from vouchers
		where book_id=$1 and year=$2 and month=$3`, b.ID, p.Year, p.Month).Scan(&total, &unreviewed); err != nil {
		return nil, err
	}
	add("凭证审核", unreviewed == 0, b.Settings.Voucher.RequireReviewToClose, fmt.Sprintf("有 %d 张凭证未审核", unreviewed))

	if err := q.QueryRow(ctx, `select coalesce(sum(mx - n), 0) from (
			select max(number) mx, count(*) n from vouchers where book_id=$1 and year=$2 and month=$3 group by word) t`,
		b.ID, p.Year, p.Month).Scan(&gaps); err != nil {
		return nil, err
	}
	add("凭证号连续", gaps == 0, false, "凭证断号，可在凭证列表中“整理凭证号”")
	return items, nil
}

// Close 结账。
func (s *Service) Close(ctx context.Context, b *book.Book, p kernel.Period) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		items, err := s.Check(ctx, b, p)
		if err != nil {
			return err
		}
		for _, it := range items {
			if !it.OK && it.Blocking {
				e := httpx.Conflict("结账检查未通过：" + it.Name + "——" + it.Message)
				e.Details = items
				return e
			}
		}
		if err := s.books.SetClosed(ctx, b.ID, p, true); err != nil {
			return err
		}
		audit.Log(ctx, s.db, b.ID, "period.close", "period", p.String(), nil)
		return nil
	})
}

// Reopen 反结账：只能从最后一个已结账期间开始逐期反结。
func (s *Service) Reopen(ctx context.Context, b *book.Book, p kernel.Period) error {
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		last, ok, err := s.books.LastClosed(ctx, b.ID)
		if err != nil {
			return err
		}
		if !ok || last != p {
			return httpx.Conflict("只能反结账最后一个已结账期间")
		}
		if err := s.books.SetClosed(ctx, b.ID, p, false); err != nil {
			return err
		}
		audit.Log(ctx, s.db, b.ID, "period.reopen", "period", p.String(), nil)
		return nil
	})
}

func periodParam(r *http.Request) (kernel.Period, error) {
	p, err := kernel.ParsePeriod(chi.URLParam(r, "period"))
	if err != nil {
		return p, httpx.BadRequest(err.Error())
	}
	return p, nil
}

func (s *Service) Routes(r chi.Router) {
	bk := func(r *http.Request) *book.Book { return book.From(r.Context()).Book }

	r.Get("/closing/{period}", httpx.Handle(func(r *http.Request) (any, error) {
		p, err := periodParam(r)
		if err != nil {
			return nil, err
		}
		return s.Status(r.Context(), bk(r), p)
	}))
	r.With(book.Require(PermRun)).Post("/closing/{period}/{code}", httpx.Handle(func(r *http.Request) (any, error) {
		p, err := periodParam(r)
		if err != nil {
			return nil, err
		}
		v, err := s.Generate(r.Context(), bk(r), p, chi.URLParam(r, "code"))
		return map[string]any{"voucher": v}, err
	}))
	r.With(book.Require(book.PermAdmin)).Put("/closing-rules/{code}", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[Rule](r)
		if err != nil {
			return nil, err
		}
		return nil, s.SaveRule(r.Context(), bk(r), chi.URLParam(r, "code"), in)
	}))
	r.Get("/periods/{period}/check", httpx.Handle(func(r *http.Request) (any, error) {
		p, err := periodParam(r)
		if err != nil {
			return nil, err
		}
		return s.Check(r.Context(), bk(r), p)
	}))
	r.Group(func(r chi.Router) {
		r.Use(book.Require(PermClose))
		r.Post("/periods/{period}/close", httpx.Handle(func(r *http.Request) (any, error) {
			p, err := periodParam(r)
			if err != nil {
				return nil, err
			}
			return nil, s.Close(r.Context(), bk(r), p)
		}))
		r.Post("/periods/{period}/reopen", httpx.Handle(func(r *http.Request) (any, error) {
			p, err := periodParam(r)
			if err != nil {
				return nil, err
			}
			return nil, s.Reopen(r.Context(), bk(r), p)
		}))
	})
}
