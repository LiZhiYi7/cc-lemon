// Package report 报表引擎：按账套级报表模板（kind=report）从余额引擎取数计算。
//
// 模板语法见 docs/architecture.md。新增报表 = 新增模板，无需改代码。
package report

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
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
	aux     *auxiliary.Service
	filter  ActivityFilter
}

func New(d *db.DB, books *book.Service, ledgers *ledger.Service, aux *auxiliary.Service, filter ActivityFilter) *Service {
	return &Service{db: d, books: books, ledgers: ledgers, aux: aux, filter: filter}
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

// 取数规则。前两组随所在列的口径取数；第三组为“绝对规则”，不看列，可用于任何列。
const (
	// 时点列
	ruleD  = "D"  // 借方净额
	ruleC  = "C"  // 贷方净额
	ruleDB = "DB" // 末级借方余额合计
	ruleCB = "CB" // 末级贷方余额合计
	// 区间列
	ruleAMT = "AMT" // 实际发生额（按科目方向）
	ruleDA  = "DA"  // 借方实际发生额
	ruleCA  = "CA"  // 贷方实际发生额
	// 绝对规则
	ruleYBD  = "YBD"  // 年初借方净额
	ruleYBC  = "YBC"  // 年初贷方净额
	ruleED   = "ED"   // 期末借方净额
	ruleEC   = "EC"   // 期末贷方净额
	ruleYAMT = "YAMT" // 本年累计实际发生额（按科目方向）
	ruleYDA  = "YDA"  // 本年累计借方实际发生额
	ruleYCA  = "YCA"  // 本年累计贷方实际发生额
)

func isPointRule(r string) bool { return r == ruleD || r == ruleC || r == ruleDB || r == ruleCB }
func isRangeRule(r string) bool { return r == ruleAMT || r == ruleDA || r == ruleCA }
func isAbsRule(r string) bool {
	switch r {
	case ruleYBD, ruleYBC, ruleED, ruleEC, ruleYAMT, ruleYDA, ruleYCA:
		return true
	}
	return false
}

type term struct {
	neg      bool
	code     string // 科目编码；category 为真时是科目类别编码
	category bool
	rule     string
	aux      string // 辅助核算过滤 "类别编码=项目编码"，空为不过滤
}

// parseTerm 解析 "[+|-](科目编码|@科目类别)[:规则][#辅助核算类别=项目编码]"。
func parseTerm(s string) (term, error) {
	var t term
	raw := s
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		t.neg, s = true, rest
	} else {
		s = strings.TrimPrefix(s, "+")
	}
	s, t.aux, _ = strings.Cut(s, "#")
	t.code, t.rule, _ = strings.Cut(s, ":")
	t.rule = strings.ToUpper(t.rule)
	if rest, ok := strings.CutPrefix(t.code, "@"); ok {
		t.category, t.code = true, rest
	}
	if t.code == "" || (t.rule != "" && !isPointRule(t.rule) && !isRangeRule(t.rule) && !isAbsRule(t.rule)) {
		return t, fmt.Errorf("无效的取数项 %q", raw)
	}
	if t.aux != "" {
		if tp, it, ok := strings.Cut(t.aux, "="); !ok || tp == "" || it == "" {
			return t, fmt.Errorf("取数项 %q 的辅助核算条件应写作 类别编码=项目编码", raw)
		}
	}
	return t, nil
}

// balanceValue 一个科目余额按规则在某列的取值。
func balanceValue(t term, col standard.ReportColumn, nat string, x *ledger.Balance) Dec {
	natural := func(d, c Dec) Dec {
		if x.Account.Direction == kernel.Debit {
			return d.Sub(c)
		}
		return c.Sub(d)
	}
	switch t.rule {
	case ruleYBD:
		return x.YearOpenNet
	case ruleYBC:
		return x.YearOpenNet.Neg()
	case ruleED:
		return x.EndNet()
	case ruleEC:
		return x.EndNet().Neg()
	case ruleYAMT:
		return natural(x.YtdActDebit, x.YtdActCredit)
	case ruleYDA:
		return x.YtdActDebit
	case ruleYCA:
		return x.YtdActCredit
	}
	switch {
	case col.At != "": // 时点列
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
			return net.Neg()
		case ruleDB:
			if yearBegin {
				return x.YearOpenDebitBal
			}
			return x.EndDebitBal
		case ruleCB:
			if yearBegin {
				return x.YearOpenCreditBal
			}
			return x.EndCreditBal
		}
		return net
	case col.Range != "": // 区间列
		d, c := x.ActDebit, x.ActCredit
		if col.Range == "ytd" {
			d, c = x.YtdActDebit, x.YtdActCredit
		}
		switch t.rule {
		case ruleDA:
			return d
		case ruleCA:
			return c
		}
		return natural(d, c)
	}
	return decimal.Zero // 计算列上未写绝对规则：Validate 已拦截
}

// Data 报表计算所需的余额数据。
type Data struct {
	LedgerID int64
	Set      *ledger.Set
	// ByAux 按辅助核算条件（"类别编码=项目编码"）过滤后的余额集。
	ByAux map[string]*ledger.Set
}

func (d *Data) termValue(t term, col standard.ReportColumn, nat string) Dec {
	set := d.Set
	if t.aux != "" {
		if set = d.ByAux[t.aux]; set == nil {
			return decimal.Zero
		}
	}
	v := decimal.Zero
	if t.category {
		// 科目类别：汇总该类别下全部一级科目
		for _, a := range set.Accounts {
			if a.LedgerID == d.LedgerID && a.Level == 1 && a.Category == t.code {
				v = v.Add(balanceValue(t, col, nat, set.ByID[a.ID]))
			}
		}
	} else if x := set.ByCode(d.LedgerID, t.code); x != nil {
		v = balanceValue(t, col, nat, x)
	}
	if t.neg {
		return v.Neg()
	}
	return v
}

// evalSum 计算 "1+2-3" 形式的公式。
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

// rowTerms 某行在某列使用的取数项。
func rowTerms(r *standard.ReportRow, colKey string) []standard.Str {
	if cells, ok := r.Cells[colKey]; ok {
		return cells
	}
	if r.Cells != nil {
		return nil // 矩阵行只在列出的列取数
	}
	return r.Terms
}

// Validate 校验模板定义（保存模板与计算前都会执行）。
func Validate(t *Template) error {
	if t.Name == "" || len(t.Columns) == 0 {
		return fmt.Errorf("报表名称与列定义不能为空")
	}
	cols := map[string]*standard.ReportColumn{}
	for i := range t.Columns {
		c := &t.Columns[i]
		if c.Key == "" || cols[c.Key] != nil {
			return fmt.Errorf("列标识为空或重复: %q", c.Name)
		}
		if n := btoi(c.At != "") + btoi(c.Range != "") + btoi(c.Sum != ""); n > 1 {
			return fmt.Errorf("列 %q 的 at、range、sum 只能指定一个", c.Name)
		}
		cols[c.Key] = c
	}
	for _, c := range t.Columns {
		if c.Sum == "" {
			continue
		}
		if _, err := evalSum(c.Sum, func(k string) (Dec, error) {
			if cols[k] == nil || cols[k].Sum != "" {
				return decimal.Zero, fmt.Errorf("列 %q 的公式引用了不存在或同为公式的列 %s", c.Name, k)
			}
			return decimal.Zero, nil
		}); err != nil {
			return err
		}
	}
	keys := map[string]bool{}
	for i := range t.Rows {
		r := &t.Rows[i]
		if r.Key != "" {
			if keys[string(r.Key)] {
				return fmt.Errorf("行标识重复: %s", r.Key)
			}
			keys[string(r.Key)] = true
		}
		if (len(r.Terms) > 0 || len(r.Cells) > 0) && r.Sum != "" {
			return fmt.Errorf("行 %q 不能同时定义取数项与 sum", r.Name)
		}
		for k := range r.Cells {
			if cols[k] == nil {
				return fmt.Errorf("行 %q 的 cells 引用了不存在的列 %s", r.Name, k)
			}
		}
		for _, c := range t.Columns {
			for _, s := range rowTerms(r, c.Key) {
				tm, err := parseTerm(string(s))
				if err != nil {
					return fmt.Errorf("行 %q: %w", r.Name, err)
				}
				if c.At == "" && c.Range == "" && c.Sum == "" && !isAbsRule(tm.rule) {
					return fmt.Errorf("行 %q: 计算列“%s”上的取数项 %q 须写明绝对规则（如 YBC、EC、YAMT）", r.Name, c.Name, s)
				}
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

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// auxFilters 模板中出现的全部辅助核算过滤条件。
func auxFilters(t *Template) []string {
	seen := map[string]bool{}
	var out []string
	for i := range t.Rows {
		all := append([]standard.Str{}, t.Rows[i].Terms...)
		for _, cells := range t.Rows[i].Cells {
			all = append(all, cells...)
		}
		for _, s := range all {
			if tm, err := parseTerm(string(s)); err == nil && tm.aux != "" && !seen[tm.aux] {
				seen[tm.aux] = true
				out = append(out, tm.aux)
			}
		}
	}
	return out
}

// Compute 用给定余额数据计算模板（纯函数，便于测试）。
func Compute(t *Template, data *Data) (*Result, error) {
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
	colByKey := map[string]standard.ReportColumn{}
	for _, c := range t.Columns {
		colByKey[c.Key] = c
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
		r := &t.Rows[i]
		if c.busy {
			return c.v, fmt.Errorf("行 %q 的公式存在循环引用", r.Name)
		}
		c.busy = true
		var err error
		switch {
		case col.Sum != "": // 列间公式：同一行其他列相加减
			c.v, err = evalSum(col.Sum, func(k string) (Dec, error) { return value(i, colByKey[k]) })
		case r.Sum != "": // 行间公式
			c.v, err = evalSum(r.Sum, func(k string) (Dec, error) { return value(byKey[k], col) })
		default:
			for _, s := range rowTerms(r, col.Key) {
				tm, _ := parseTerm(string(s))
				c.v = c.v.Add(data.termValue(tm, col, r.Nat))
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
	if err := Validate(&t); err != nil {
		return nil, httpx.BadRequest("报表模板有误：" + err.Error())
	}
	lg, err := s.books.LedgerByCode(ctx, b.ID, t.Ledger)
	if err != nil {
		return nil, err
	}
	exclude, err := s.filter.ActivityExcludedSources(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	data := &Data{LedgerID: lg.ID, ByAux: map[string]*ledger.Set{}}
	if data.Set, err = s.ledgers.Balances(ctx, b, p, p, exclude); err != nil {
		return nil, err
	}
	// 带辅助核算条件的取数项：每个条件取一份过滤后的余额。条件指向的项目不存在时按零处理。
	if filters := auxFilters(&t); len(filters) > 0 {
		types, err := s.aux.Types(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		items, err := s.aux.Items(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range filters {
			typeCode, itemCode, _ := strings.Cut(f, "=")
			for _, tp := range types {
				if tp.Code != typeCode {
					continue
				}
				for _, it := range items {
					if it.TypeID == tp.ID && it.Code == itemCode {
						if data.ByAux[f], err = s.ledgers.BalancesByAux(ctx, b, p, p, exclude, it.ID); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	res, err := Compute(&t, data)
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
