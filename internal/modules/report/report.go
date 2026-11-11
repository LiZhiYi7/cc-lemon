// Package report 报表引擎：按账套级报表模板（kind=report）从余额引擎取数计算。
//
// 模板语法见 standard.ReportRow。新增报表 = 新增模板，无需改代码。
package report

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/modules/ledger"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

type Dec = decimal.Decimal

type Template = standard.ReportTemplate

// ActivityFilter 提供“实际发生额”需剔除的凭证来源（由期末结转模块实现）。
type ActivityFilter interface {
	ActivityExcludedSources(ctx context.Context, bookID int64) ([]string, error)
}

type Service struct {
	db      *db.DB
	books   *book.Service
	ledgers *ledger.Service
	filter  ActivityFilter
}

func New(d *db.DB, books *book.Service, ledgers *ledger.Service, filter ActivityFilter) *Service {
	return &Service{db: d, books: books, ledgers: ledgers, filter: filter}
}

type Row struct {
	Key    string         `json:"key,omitempty"`
	Name   string         `json:"name"`
	Side   string         `json:"side,omitempty"`
	Indent int            `json:"indent,omitempty"`
	Bold   bool           `json:"bold,omitempty"`
	Values map[string]Dec `json:"values,omitempty"` // 列 key → 金额；标题行无值
}

type CheckResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}

type Result struct {
	Code    string                  `json:"code"`
	Name    string                  `json:"name"`
	Layout  string                  `json:"layout"`
	Period  kernel.Period           `json:"period"`
	Columns []standard.ReportColumn `json:"columns"`
	Rows    []Row                   `json:"rows"`
	Checks  []CheckResult           `json:"checks"`
}

// 取数规则。
const (
	ruleD   = "D"   // 借方净额
	ruleC   = "C"   // 贷方净额
	ruleDB  = "DB"  // 末级借方余额合计
	ruleCB  = "CB"  // 末级贷方余额合计
	ruleAMT = "AMT" // 实际发生额（按科目方向）
	ruleDA  = "DA"  // 借方实际发生额
	ruleCA  = "CA"  // 贷方实际发生额
)

func isPointRule(r string) bool { return r == ruleD || r == ruleC || r == ruleDB || r == ruleCB }
func isRangeRule(r string) bool { return r == ruleAMT || r == ruleDA || r == ruleCA }

type term struct {
	neg  bool
	code string
	rule string
}

// parseTerm 解析 "[+|-]科目编码[:规则]"。
func parseTerm(s string) (term, error) {
	var t term
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		t.neg, s = true, rest
	} else {
		s = strings.TrimPrefix(s, "+")
	}
	t.code, t.rule, _ = strings.Cut(s, ":")
	t.rule = strings.ToUpper(t.rule)
	if t.code == "" || (t.rule != "" && !isPointRule(t.rule) && !isRangeRule(t.rule)) {
		return t, fmt.Errorf("无效的取数项 %q", s)
	}
	return t, nil
}

// termValue 计算一个取数项在某列的值。
func termValue(t term, col standard.ReportColumn, nat string, x *ledger.Balance) Dec {
	if x == nil {
		return decimal.Zero
	}
	var v Dec
	if col.At != "" { // 时点列
		rule := t.rule
		if !isPointRule(rule) {
			rule = nat
		}
		yearBegin := col.At == "year_begin"
		net := x.EndNet()
		if yearBegin {
			net = x.YearOpenNet
		}
		switch rule {
		case ruleC:
			v = net.Neg()
		case ruleDB:
			v = x.EndDebitBal
			if yearBegin {
				v = x.YearOpenDebitBal
			}
		case ruleCB:
			v = x.EndCreditBal
			if yearBegin {
				v = x.YearOpenCreditBal
			}
		default:
			v = net
		}
	} else { // 区间列
		d, c := x.ActDebit, x.ActCredit
		if col.Range == "ytd" {
			d, c = x.YtdActDebit, x.YtdActCredit
		}
		switch t.rule {
		case ruleDA:
			v = d
		case ruleCA:
			v = c
		default:
			if x.Account.Direction == kernel.Debit {
				v = d.Sub(c)
			} else {
				v = c.Sub(d)
			}
		}
	}
	if t.neg {
		return v.Neg()
	}
	return v
}

// evalSum 计算 "1+2-3" 形式的行间公式。
func evalSum(expr string, get func(key string) (Dec, error)) (Dec, error) {
	total := decimal.Zero
	sign, start := 1, 0
	flush := func(end int) error {
		key := strings.TrimSpace(expr[start:end])
		if key == "" {
			if start == 0 { // 允许以符号开头
				return nil
			}
			return fmt.Errorf("公式 %q 语法错误", expr)
		}
		v, err := get(key)
		if err != nil {
			return err
		}
		if sign < 0 {
			v = v.Neg()
		}
		total = total.Add(v)
		return nil
	}
	for i, ch := range expr {
		if ch == '+' || ch == '-' {
			if err := flush(i); err != nil {
				return total, err
			}
			sign = 1
			if ch == '-' {
				sign = -1
			}
			start = i + 1
		}
	}
	if err := flush(len(expr)); err != nil {
		return total, err
	}
	return total, nil
}

// Validate 校验模板定义（保存模板与计算前都会执行）。
func Validate(t *Template) error {
	if t.Name == "" || len(t.Columns) == 0 {
		return fmt.Errorf("报表名称与列定义不能为空")
	}
	for _, c := range t.Columns {
		if c.Key == "" || (c.At == "") == (c.Range == "") {
			return fmt.Errorf("列 %q 须且只能指定 at 或 range", c.Name)
		}
	}
	keys := map[string]bool{}
	for _, r := range t.Rows {
		if r.Key != "" {
			if keys[string(r.Key)] {
				return fmt.Errorf("行标识重复: %s", r.Key)
			}
			keys[string(r.Key)] = true
		}
		if len(r.Terms) > 0 && r.Sum != "" {
			return fmt.Errorf("行 %q 不能同时定义 terms 与 sum", r.Name)
		}
		for _, s := range r.Terms {
			if _, err := parseTerm(string(s)); err != nil {
				return fmt.Errorf("行 %q: %w", r.Name, err)
			}
		}
	}
	for _, r := range t.Rows {
		if r.Sum == "" {
			continue
		}
		if _, err := evalSum(r.Sum, func(k string) (Dec, error) {
			if !keys[k] {
				return decimal.Zero, fmt.Errorf("行 %q 引用了不存在的行 %s", r.Name, k)
			}
			return decimal.Zero, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// Compute 用给定余额集计算模板（纯函数，便于测试）。
func Compute(t *Template, ledgerID int64, set *ledger.Set) (*Result, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	res := &Result{Code: t.Code, Name: t.Name, Layout: t.Layout, Columns: t.Columns, Checks: []CheckResult{}}
	byKey := map[string]int{}
	for i, r := range t.Rows {
		if r.Key != "" {
			byKey[string(r.Key)] = i
		}
	}
	type cell struct {
		v    Dec
		done bool
		busy bool
	}
	cells := make([]map[string]*cell, len(t.Rows))
	var value func(i int, col standard.ReportColumn) (Dec, error)
	value = func(i int, col standard.ReportColumn) (Dec, error) {
		if cells[i] == nil {
			cells[i] = map[string]*cell{}
		}
		c := cells[i][col.Key]
		if c == nil {
			c = &cell{}
			cells[i][col.Key] = c
		}
		if c.done {
			return c.v, nil
		}
		r := t.Rows[i]
		if c.busy {
			return c.v, fmt.Errorf("行 %q 的公式存在循环引用", r.Name)
		}
		c.busy = true
		var err error
		if r.Sum != "" {
			c.v, err = evalSum(r.Sum, func(k string) (Dec, error) { return value(byKey[k], col) })
		} else {
			for _, s := range r.Terms {
				tm, _ := parseTerm(string(s))
				c.v = c.v.Add(termValue(tm, col, r.Nat, set.ByCode(ledgerID, tm.code)))
			}
		}
		c.busy, c.done = false, err == nil
		return c.v, err
	}
	for i, r := range t.Rows {
		row := Row{Key: string(r.Key), Name: r.Name, Side: r.Side, Indent: r.Indent, Bold: r.Bold}
		if r.Key != "" {
			row.Values = map[string]Dec{}
			for _, col := range t.Columns {
				v, err := value(i, col)
				if err != nil {
					return nil, err
				}
				row.Values[col.Key] = v
			}
		}
		res.Rows = append(res.Rows, row)
	}
	for _, ck := range t.Checks {
		li, lok := byKey[string(ck.Left)]
		ri, rok := byKey[string(ck.Right)]
		if !lok || !rok {
			return nil, fmt.Errorf("校验 %q 引用了不存在的行", ck.Name)
		}
		ok := true
		for _, col := range t.Columns {
			l, _ := value(li, col)
			r, _ := value(ri, col)
			ok = ok && l.Equal(r)
		}
		res.Checks = append(res.Checks, CheckResult{Name: ck.Name, OK: ok})
	}
	return res, nil
}

// Run 计算指定期间的报表。
func (s *Service) Run(ctx context.Context, b *book.Book, code string, p kernel.Period) (*Result, error) {
	var t Template
	if err := s.books.Config(ctx, b.ID, book.KindReport, code, &t); err != nil {
		return nil, err
	}
	lg, err := s.books.LedgerByCode(ctx, b.ID, t.Ledger)
	if err != nil {
		return nil, err
	}
	exclude, err := s.filter.ActivityExcludedSources(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	set, err := s.ledgers.Balances(ctx, b, p, p, exclude)
	if err != nil {
		return nil, err
	}
	res, err := Compute(&t, lg.ID, set)
	if err != nil {
		return nil, httpx.BadRequest("报表模板有误：" + err.Error())
	}
	res.Period = p
	return res, nil
}

// SaveTemplate 新增或修改账套的报表模板。
func (s *Service) SaveTemplate(ctx context.Context, b *book.Book, code string, t Template) error {
	t.Code = code
	if err := Validate(&t); err != nil {
		return httpx.BadRequest(err.Error())
	}
	if _, err := s.books.LedgerByCode(ctx, b.ID, t.Ledger); err != nil {
		return err
	}
	if err := s.books.PutConfig(ctx, b.ID, book.KindReport, code, t.Name, -1, t); err != nil {
		return err
	}
	audit.Log(ctx, s.db, b.ID, "report_template.save", "report", code, nil)
	return nil
}

func (s *Service) Routes(r chi.Router) {
	bk := func(r *http.Request) *book.Book { return book.From(r.Context()).Book }
	// 报表目录（含模板定义，供前端渲染菜单与公式查看）
	r.Get("/reports", httpx.Handle(func(r *http.Request) (any, error) {
		return s.books.Configs(r.Context(), bk(r).ID, book.KindReport)
	}))
	r.Get("/reports/{code}", httpx.Handle(func(r *http.Request) (any, error) {
		p, err := kernel.ParsePeriod(r.URL.Query().Get("period"))
		if err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		return s.Run(r.Context(), bk(r), chi.URLParam(r, "code"), p)
	}))
	r.With(book.Require(book.PermAdmin)).Put("/reports/{code}", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[Template](r)
		if err != nil {
			return nil, err
		}
		return nil, s.SaveTemplate(r.Context(), bk(r), chi.URLParam(r, "code"), in)
	}))
}
