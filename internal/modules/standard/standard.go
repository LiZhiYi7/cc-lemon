// Package standard 加载“会计准则模板”。
//
// 模板以 YAML 描述一套准则的全部预置内容：核算账簿、科目类别、科目表、凭证字、
// 平行记账规则、期末结转规则、报表模板。建账时复制进账套，此后账套独立演化。
// 新增一套准则 = 新增一个 YAML 文件，无需改代码。
package standard

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Standard struct {
	Code              string           `yaml:"code" json:"code"`
	Name              string           `yaml:"name" json:"name"`
	Description       string           `yaml:"description" json:"description"`
	AccountCodeScheme []int            `yaml:"account_code_scheme" json:"account_code_scheme"`
	VoucherWords      []VoucherWord    `yaml:"voucher_words" json:"voucher_words"`
	Ledgers           []Ledger         `yaml:"ledgers" json:"ledgers"`
	AuxTypes          []AuxType        `yaml:"aux_types" json:"-"`
	Categories        []Category       `yaml:"categories" json:"-"`
	Parallel          []ParallelGroup  `yaml:"parallel" json:"-"`
	ClosingRules      []ClosingRule    `yaml:"closing_rules" json:"-"`
	Reports           []ReportTemplate `yaml:"reports" json:"-"`
}

type VoucherWord struct {
	Word    string `yaml:"word" json:"word"`
	Title   string `yaml:"title" json:"title"`
	Default bool   `yaml:"default" json:"default"`
}

type Ledger struct {
	Code    string `yaml:"code" json:"code"`
	Name    string `yaml:"name" json:"name"`
	Primary bool   `yaml:"primary" json:"primary"`
}

// AuxType 预置的辅助核算类别。
type AuxType struct {
	Code  string `yaml:"code"`
	Name  string `yaml:"name"`
	Items []struct {
		Code Str    `yaml:"code"`
		Name string `yaml:"name"`
	} `yaml:"items"`
}

type Category struct {
	Ledger   string    `yaml:"ledger"`
	Code     string    `yaml:"code"`
	Name     string    `yaml:"name"`
	PL       bool      `yaml:"pl"`
	Accounts []Account `yaml:"accounts"`
}

type Account struct {
	Code Str      `yaml:"code"`
	Name string   `yaml:"name"`
	Dir  string   `yaml:"dir"`
	Cash bool     `yaml:"cash"`
	Aux  []string `yaml:"aux"` // 预先启用的辅助核算类别编码（仅末级科目）
}

// ParallelGroup 一组平行记账映射：来源账簿科目 → 目标账簿科目。
type ParallelGroup struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	Rules  []struct {
		From Str    `yaml:"from"`
		To   Str    `yaml:"to"`
		Side string `yaml:"side"` // "" | D | C
		Note string `yaml:"note"`
	} `yaml:"rules"`
}

// ClosingRule 期末结转规则：把一批来源科目的余额转入目标科目。
type ClosingRule struct {
	Code             string   `yaml:"code" json:"code"`
	Name             string   `yaml:"name" json:"name"`
	Ledger           string   `yaml:"ledger" json:"ledger"`
	Frequency        string   `yaml:"frequency" json:"frequency"` // monthly | yearly
	SourceCategories []string `yaml:"source_categories" json:"source_categories,omitempty"`
	SourceAccounts   []Str    `yaml:"source_accounts" json:"source_accounts,omitempty"`
	// Sources 来源选择器，可按辅助核算项目筛选；与上面两个简写字段可并用。
	Sources []ClosingSource `yaml:"sources" json:"sources,omitempty"`
	// OnlyIf 仅当来源合计为 credit（贷方）或 debit（借方）余额时才结转；留空不限。
	OnlyIf  string `yaml:"only_if" json:"only_if,omitempty"`
	Target  Str    `yaml:"target" json:"target"`
	Summary string `yaml:"summary" json:"summary"`
	// ExcludeFromActivity 为真时，该规则生成的凭证不计入损益类“实际发生额”（利润表取数用）。
	ExcludeFromActivity bool `yaml:"exclude_from_activity" json:"exclude_from_activity"`
	// Required 为真时，结账前来源科目余额必须为零。
	Required bool `yaml:"required" json:"required"`
}

// ClosingSource 一个来源选择器：科目类别或科目编码（含下级）命中即入选。
type ClosingSource struct {
	Categories []string `yaml:"categories" json:"categories,omitempty"`
	Accounts   []Str    `yaml:"accounts" json:"accounts,omitempty"`
	// Aux 辅助核算条件：类别编码 → 项目编码。只结转包含这些项目的余额。
	Aux map[string]string `yaml:"aux" json:"aux,omitempty"`
	// Unbound 科目未启用 Aux 中的类别时是否整体纳入。
	Unbound bool `yaml:"unbound" json:"unbound,omitempty"`
}

// ReportTemplate 报表模板。
type ReportTemplate struct {
	Code    string         `yaml:"code" json:"code"`
	Name    string         `yaml:"name" json:"name"`
	Ledger  string         `yaml:"ledger" json:"ledger"`
	Layout  string         `yaml:"layout" json:"layout"` // list | two_side
	Columns []ReportColumn `yaml:"columns" json:"columns"`
	Rows    []ReportRow    `yaml:"rows" json:"rows"`
	Checks  []ReportCheck  `yaml:"checks" json:"checks,omitempty"`
}

// ReportColumn 取数口径：At 为时点（period_end | year_begin），Range 为区间（period | ytd）。
// 两者都不写的是“计算列”：取数项必须自带绝对规则（如 YBC、YAMT），或用 Sum 由同行其他列计算。
type ReportColumn struct {
	Key   string `yaml:"key" json:"key"`
	Name  string `yaml:"name" json:"name"`
	At    string `yaml:"at" json:"at,omitempty"`
	Range string `yaml:"range" json:"range,omitempty"`
	Sum   string `yaml:"sum" json:"sum,omitempty"` // 列间公式，如 "a+b+c"
}

// ReportRow 报表行。Terms 与 Sum 二选一：
//
//	Terms: ["1001", "-1407", "1122:DB"]  科目取数，可带符号与取数规则
//	Sum:   "1+2-3"                       引用其他行（按 Key）
//
// 取数规则：D 借方净额 / C 贷方净额 / DB 明细借方余额合计 / CB 明细贷方余额合计 /
//
//	AMT 实际发生额(按科目方向) / DA 借方发生额 / CA 贷方发生额
type ReportRow struct {
	Key   Str    `yaml:"key" json:"key,omitempty"`
	Name  string `yaml:"name" json:"name"`
	Side  string `yaml:"side" json:"side,omitempty"` // two_side 布局：left | right
	Nat   string `yaml:"nat" json:"nat,omitempty"`   // 时点列默认方向 D | C
	Terms []Str  `yaml:"terms" json:"terms,omitempty"`
	Sum   string `yaml:"sum" json:"sum,omitempty"`
	// Cells 矩阵式报表：按列分别指定取数项（列 key → terms），优先于 Terms。
	Cells  map[string][]Str `yaml:"cells" json:"cells,omitempty"`
	Indent int              `yaml:"indent" json:"indent,omitempty"`
	Bold   bool             `yaml:"bold" json:"bold,omitempty"`
}

type ReportCheck struct {
	Name  string `yaml:"name" json:"name"`
	Left  Str    `yaml:"left" json:"left"`
	Right Str    `yaml:"right" json:"right"`
}

// Str 允许 YAML 中把科目编码写成裸数字（1001）而仍按字符串处理。
type Str string

func (s *Str) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("第 %d 行: 需要标量值", n.Line)
	}
	*s = Str(n.Value)
	return nil
}

func (s *Str) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case string:
		*s = Str(x)
	case nil:
		*s = ""
	default:
		*s = Str(strings.Trim(string(b), `"`))
	}
	return nil
}

// Registry 保存全部已加载的准则模板。
type Registry struct{ items map[string]*Standard }

// Load 先读内置模板，再读外部目录（同 code 覆盖）。
func Load(embedded fs.FS, extraDir string) (*Registry, error) {
	r := &Registry{items: map[string]*Standard{}}
	if err := r.loadFS(embedded, "standards"); err != nil {
		return nil, err
	}
	if extraDir != "" {
		if err := r.loadFS(os.DirFS(extraDir), "."); err != nil {
			return nil, fmt.Errorf("加载外部准则模板: %w", err)
		}
	}
	return r, nil
}

func (r *Registry) loadFS(fsys fs.FS, dir string) error {
	names, err := fs.Glob(fsys, strings.TrimPrefix(dir+"/*.yaml", "./"))
	if err != nil {
		return err
	}
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		var s Standard
		if err := yaml.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := s.validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		r.items[s.Code] = &s
	}
	return nil
}

func (r *Registry) Get(code string) (*Standard, bool) {
	s, ok := r.items[code]
	return s, ok
}

func (r *Registry) List() []*Standard {
	out := make([]*Standard, 0, len(r.items))
	for _, s := range r.items {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ParentCode 按编码方案求上级科目编码；一级科目返回 ""。
func ParentCode(scheme []int, code string) (parent string, level int, err error) {
	n := 0
	for i, seg := range scheme {
		n += seg
		if len(code) == n {
			if i == 0 {
				return "", 1, nil
			}
			return code[:n-seg], i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("科目编码 %s 不符合编码方案 %v", code, scheme)
}

func (s *Standard) validate() error {
	if s.Code == "" || s.Name == "" {
		return fmt.Errorf("缺少 code/name")
	}
	if len(s.AccountCodeScheme) == 0 || len(s.Ledgers) == 0 {
		return fmt.Errorf("缺少 account_code_scheme 或 ledgers")
	}
	ledgers := map[string]bool{}
	for _, l := range s.Ledgers {
		ledgers[l.Code] = true
	}
	auxItems := map[string]bool{} // 类别编码 或 类别编码/项目编码
	for _, t := range s.AuxTypes {
		auxItems[t.Code] = true
		for _, it := range t.Items {
			auxItems[t.Code+"/"+string(it.Code)] = true
		}
	}
	codes := map[string]bool{} // ledger/code
	for _, c := range s.Categories {
		if !ledgers[c.Ledger] {
			return fmt.Errorf("科目类别 %s 引用了未定义的账簿 %s", c.Code, c.Ledger)
		}
		for _, a := range c.Accounts {
			code := string(a.Code)
			if a.Dir != "D" && a.Dir != "C" {
				return fmt.Errorf("科目 %s 的方向必须为 D 或 C", code)
			}
			parent, _, err := ParentCode(s.AccountCodeScheme, code)
			if err != nil {
				return err
			}
			if parent != "" && !codes[c.Ledger+"/"+parent] {
				return fmt.Errorf("科目 %s 的上级 %s 未定义（须先于下级出现）", code, parent)
			}
			for _, t := range a.Aux {
				if !auxItems[t] {
					return fmt.Errorf("科目 %s 引用了未定义的辅助核算类别 %s", code, t)
				}
			}
			if codes[c.Ledger+"/"+code] {
				return fmt.Errorf("科目编码重复: %s", code)
			}
			codes[c.Ledger+"/"+code] = true
		}
	}
	for _, g := range s.Parallel {
		for _, r := range g.Rules {
			if !codes[g.Source+"/"+string(r.From)] || !codes[g.Target+"/"+string(r.To)] {
				return fmt.Errorf("平行记账规则 %s→%s 引用了不存在的科目", r.From, r.To)
			}
		}
	}
	for _, c := range s.ClosingRules {
		if !ledgers[c.Ledger] || !codes[c.Ledger+"/"+string(c.Target)] {
			return fmt.Errorf("结转规则 %s 的账簿或目标科目无效", c.Code)
		}
		if c.OnlyIf != "" && c.OnlyIf != "credit" && c.OnlyIf != "debit" {
			return fmt.Errorf("结转规则 %s 的 only_if 只能是 credit 或 debit", c.Code)
		}
		for _, src := range c.Sources {
			for t, it := range src.Aux {
				if !auxItems[t+"/"+it] {
					return fmt.Errorf("结转规则 %s 引用了未定义的辅助核算项目 %s/%s", c.Code, t, it)
				}
			}
		}
	}
	for _, rp := range s.Reports {
		if !ledgers[rp.Ledger] {
			return fmt.Errorf("报表 %s 引用了未定义的账簿", rp.Code)
		}
	}
	return nil
}
