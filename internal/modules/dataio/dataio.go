// Package dataio 批量导入：科目、辅助核算项目、期初余额。
//
// 文件解析在前端完成（Excel → 行数据），本包只接收结构化的行，
// 逐行校验并调用各业务模块的正常写入路径，因此导入与手工录入遵循完全相同的规则。
// 一次导入在同一事务内执行：只要有一行出错，全部不生效，并返回每一行的错误。
package dataio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/modules/ledger"
	"github.com/lizhiyi7/cc-lemon/internal/modules/standard"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const maxRows = 5000

type RowError struct {
	Row     int    `json:"row"` // Excel 行号；0 表示与具体行无关
	Message string `json:"message"`
}

// Result 导入结果。Applied 为假时数据库未做任何修改。
type Result struct {
	Applied   bool       `json:"applied"`
	Created   int        `json:"created"`
	Updated   int        `json:"updated"`
	Unchanged int        `json:"unchanged"`
	Errors    []RowError `json:"errors"`
}

func (r *Result) fail(row int, err error) error {
	var he *httpx.Error
	if !errors.As(err, &he) {
		return err // 非业务错误（如数据库故障）：立即中止
	}
	r.Errors = append(r.Errors, RowError{Row: row, Message: he.Message})
	return nil
}

func (r *Result) failf(row int, format string, args ...any) {
	r.Errors = append(r.Errors, RowError{Row: row, Message: fmt.Sprintf(format, args...)})
}

var errRollback = errors.New("rollback")

type Service struct {
	db       *db.DB
	books    *book.Service
	accounts *account.Service
	aux      *auxiliary.Service
	ledgers  *ledger.Service
}

func New(d *db.DB, books *book.Service, accounts *account.Service, aux *auxiliary.Service, ledgers *ledger.Service) *Service {
	return &Service{db: d, books: books, accounts: accounts, aux: aux, ledgers: ledgers}
}

// run 在事务内执行 fn；有行错误时回滚并返回 Applied=false。
func (s *Service) run(ctx context.Context, bookID int64, kind string, n int, fn func(ctx context.Context, res *Result) error) (*Result, error) {
	res := &Result{Errors: []RowError{}}
	if n == 0 {
		return nil, httpx.BadRequest("文件中没有可导入的数据行")
	}
	if n > maxRows {
		return nil, httpx.BadRequest(fmt.Sprintf("一次最多导入 %d 行", maxRows))
	}
	err := s.db.Tx(ctx, func(ctx context.Context) error {
		if err := fn(ctx, res); err != nil {
			return err
		}
		if len(res.Errors) > 0 {
			return errRollback
		}
		audit.Log(ctx, s.db, bookID, "import."+kind, kind, "", map[string]int{"created": res.Created, "updated": res.Updated})
		return nil
	})
	if errors.Is(err, errRollback) {
		res.Created, res.Updated, res.Unchanged = 0, 0, 0
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	res.Applied = true
	return res, nil
}

func parseBool(s string) bool {
	switch strings.TrimSpace(s) {
	case "是", "Y", "y", "1", "true", "TRUE", "√":
		return true
	}
	return false
}

// ---- 科目 ----

type AccountRow struct {
	Row       int    `json:"row"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Category  string `json:"category"`  // 类别名称或编码；新增一级科目时必填
	Direction string `json:"direction"` // 借 / 贷（或 D / C）；下级科目留空则同上级
	IsCash    string `json:"is_cash"`   // 是 / 否
	Aux       string `json:"aux"`       // 辅助核算类别名称，多个用顿号或逗号分隔
}

func splitList(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune("、,，;； /", r) })
	return parts
}

// ImportAccounts 导入科目：编码不存在则新增，已存在则只更新名称。上级科目须已存在或在同一文件中。
func (s *Service) ImportAccounts(ctx context.Context, b *book.Book, ledgerID int64, rows []AccountRow) (*Result, error) {
	return s.run(ctx, b.ID, "accounts", len(rows), func(ctx context.Context, res *Result) error {
		existing, err := s.accounts.List(ctx, b.ID)
		if err != nil {
			return err
		}
		byCode := map[string]*account.Account{}
		for _, a := range existing {
			if a.LedgerID == ledgerID {
				byCode[a.Code] = a
			}
		}
		cats, err := s.books.Categories(ctx, b.ID)
		if err != nil {
			return err
		}
		catCode := map[string]string{} // 名称或编码 → 编码
		for _, c := range cats {
			if c.LedgerID == ledgerID {
				catCode[c.Name], catCode[c.Code] = c.Code, c.Code
			}
		}
		if len(catCode) == 0 {
			return httpx.BadRequest("核算账簿不存在")
		}
		types, err := s.aux.Types(ctx, b.ID)
		if err != nil {
			return err
		}
		typeID := map[string]int64{}
		for _, t := range types {
			typeID[t.Name] = t.ID
		}
		// 按编码排序，保证上级先于下级创建
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
		seen := map[string]bool{}
		for _, r := range rows {
			r.Code, r.Name = strings.TrimSpace(r.Code), strings.TrimSpace(r.Name)
			if r.Code == "" || r.Name == "" {
				res.failf(r.Row, "科目编码和名称不能为空")
				continue
			}
			if seen[r.Code] {
				res.failf(r.Row, "科目编码 %s 在文件中重复", r.Code)
				continue
			}
			seen[r.Code] = true
			var auxIDs []int64
			auxOK := true
			for _, name := range splitList(r.Aux) {
				id, ok := typeID[name]
				if !ok {
					res.failf(r.Row, "辅助核算类别“%s”不存在", name)
					auxOK = false
				}
				auxIDs = append(auxIDs, id)
			}
			if !auxOK {
				continue
			}
			if a := byCode[r.Code]; a != nil {
				if a.Name == r.Name {
					res.Unchanged++
					continue
				}
				if _, err := s.accounts.Update(ctx, b, a.ID, account.UpdateInput{Name: &r.Name}); err != nil {
					if err := res.fail(r.Row, err); err != nil {
						return err
					}
					continue
				}
				res.Updated++
				continue
			}
			in := account.CreateInput{Code: r.Code, Name: r.Name, LedgerID: ledgerID}
			switch strings.TrimSpace(r.Direction) {
			case "借", "D", "d":
				in.Direction = kernel.Debit
			case "贷", "C", "c":
				in.Direction = kernel.Credit
			case "":
			default:
				res.failf(r.Row, "余额方向应为“借”或“贷”")
				continue
			}
			if strings.TrimSpace(r.IsCash) != "" {
				v := parseBool(r.IsCash)
				in.IsCash = &v
			}
			parentCode, _, err := standard.ParentCode(b.Settings.AccountCodeScheme, r.Code)
			if err != nil {
				res.failf(r.Row, "%s", err.Error())
				continue
			}
			if parentCode != "" {
				parent := byCode[parentCode]
				if parent == nil {
					res.failf(r.Row, "上级科目 %s 不存在", parentCode)
					continue
				}
				in.ParentID = &parent.ID
			} else {
				code, ok := catCode[strings.TrimSpace(r.Category)]
				if !ok {
					res.failf(r.Row, "一级科目须填写有效的科目类别")
					continue
				}
				in.Category = code
			}
			a, err := s.accounts.Create(ctx, b, in)
			if err != nil {
				if err := res.fail(r.Row, err); err != nil {
					return err
				}
				continue
			}
			byCode[a.Code] = a
			if len(auxIDs) > 0 {
				if err := s.aux.SetBindings(ctx, b, a.ID, auxIDs); err != nil {
					if err := res.fail(r.Row, err); err != nil {
						return err
					}
					continue
				}
			}
			res.Created++
		}
		return nil
	})
}

// ---- 辅助核算项目 ----

type AuxItemRow struct {
	Row    int    `json:"row"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Remark string `json:"remark"`
}

// ImportAuxItems 导入某类别的项目：编码不存在则新增，已存在则更新名称与备注。
func (s *Service) ImportAuxItems(ctx context.Context, b *book.Book, typeID int64, rows []AuxItemRow) (*Result, error) {
	return s.run(ctx, b.ID, "aux_items", len(rows), func(ctx context.Context, res *Result) error {
		items, err := s.aux.Items(ctx, b.ID)
		if err != nil {
			return err
		}
		byCode := map[string]auxiliary.Item{}
		for _, it := range items {
			if it.TypeID == typeID {
				byCode[it.Code] = it
			}
		}
		seen := map[string]bool{}
		for _, r := range rows {
			r.Code, r.Name, r.Remark = strings.TrimSpace(r.Code), strings.TrimSpace(r.Name), strings.TrimSpace(r.Remark)
			if r.Code == "" || r.Name == "" {
				res.failf(r.Row, "编码和名称不能为空")
				continue
			}
			if seen[r.Code] {
				res.failf(r.Row, "编码 %s 在文件中重复", r.Code)
				continue
			}
			seen[r.Code] = true
			old, exists := byCode[r.Code]
			if exists && old.Name == r.Name && old.Remark == r.Remark {
				res.Unchanged++
				continue
			}
			if _, err := s.aux.SaveItem(ctx, b.ID, old.ID, auxiliary.ItemInput{TypeID: typeID, Code: &r.Code, Name: &r.Name, Remark: &r.Remark}); err != nil {
				if err := res.fail(r.Row, err); err != nil {
					return err
				}
				continue
			}
			if exists {
				res.Updated++
			} else {
				res.Created++
			}
		}
		return nil
	})
}

// ---- 期初余额 ----

type OpeningRow struct {
	Row       int               `json:"row"`
	Code      string            `json:"code"`
	Balance   string            `json:"balance"`
	YtdDebit  string            `json:"ytd_debit"`
	YtdCredit string            `json:"ytd_credit"`
	Aux       map[string]string `json:"aux"` // 辅助核算类别名称 → 项目编码
}

func parseAmount(s string) (decimal.Decimal, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// ImportOpenings 导入期初。文件中出现的科目以文件为准（启用辅助核算的科目整体替换其项目明细），未出现的科目不变。
func (s *Service) ImportOpenings(ctx context.Context, b *book.Book, ledgerID int64, rows []OpeningRow) (*Result, error) {
	return s.run(ctx, b.ID, "openings", len(rows), func(ctx context.Context, res *Result) error {
		accounts, err := s.accounts.List(ctx, b.ID)
		if err != nil {
			return err
		}
		byCode := map[string]*account.Account{}
		for _, a := range accounts {
			if a.LedgerID == ledgerID {
				byCode[a.Code] = a
			}
		}
		bindings, err := s.aux.Bindings(ctx, b.ID)
		if err != nil {
			return err
		}
		types, err := s.aux.Types(ctx, b.ID)
		if err != nil {
			return err
		}
		typeName := map[int64]string{}
		for _, t := range types {
			typeName[t.ID] = t.Name
		}
		items, err := s.aux.Items(ctx, b.ID)
		if err != nil {
			return err
		}
		itemID := map[string]int64{} // "类别ID/项目编码"
		for _, it := range items {
			itemID[fmt.Sprintf("%d/%s", it.TypeID, it.Code)] = it.ID
		}

		var plain []ledger.Opening
		auxRows := map[int64][]ledger.OpeningAuxRow{}
		var auxOrder []int64
		seenPlain := map[int64]bool{}
		for _, r := range rows {
			code := strings.TrimSpace(r.Code)
			a := byCode[code]
			if a == nil {
				res.failf(r.Row, "科目 %s 不存在", code)
				continue
			}
			if !a.IsLeaf {
				continue // 上级科目的合计行：忽略
			}
			bal, e1 := parseAmount(r.Balance)
			yd, e2 := parseAmount(r.YtdDebit)
			yc, e3 := parseAmount(r.YtdCredit)
			if e1 != nil || e2 != nil || e3 != nil {
				res.failf(r.Row, "金额格式不正确")
				continue
			}
			required := bindings[a.ID]
			if len(required) == 0 {
				if seenPlain[a.ID] {
					res.failf(r.Row, "科目 %s 在文件中重复", code)
					continue
				}
				seenPlain[a.ID] = true
				plain = append(plain, ledger.Opening{AccountID: a.ID, Balance: bal, YtdDebit: yd, YtdCredit: yc})
				continue
			}
			if _, ok := auxRows[a.ID]; !ok {
				auxRows[a.ID] = nil
				auxOrder = append(auxOrder, a.ID)
			}
			if bal.IsZero() && yd.IsZero() && yc.IsZero() {
				continue
			}
			row := ledger.OpeningAuxRow{Balance: bal, YtdDebit: yd, YtdCredit: yc}
			ok := true
			for _, tid := range required {
				// 单元格可写“编码”或“编码 名称”
				itemCode, _, _ := strings.Cut(strings.TrimSpace(r.Aux[typeName[tid]]), " ")
				id, found := itemID[fmt.Sprintf("%d/%s", tid, itemCode)]
				if !found {
					res.failf(r.Row, "科目 %s 需要填写%s（项目编码“%s”不存在）", code, typeName[tid], itemCode)
					ok = false
					continue
				}
				row.Aux = append(row.Aux, auxiliary.Ref{TypeID: tid, ItemID: id})
			}
			if ok {
				auxRows[a.ID] = append(auxRows[a.ID], row)
			}
		}
		if len(res.Errors) > 0 {
			return nil
		}
		if len(plain) > 0 {
			if err := s.ledgers.SaveOpenings(ctx, b, plain); err != nil {
				return res.fail(0, err)
			}
		}
		for _, id := range auxOrder {
			if err := s.ledgers.SaveOpeningAux(ctx, b, id, auxRows[id]); err != nil {
				if err := res.fail(0, err); err != nil {
					return err
				}
			}
		}
		res.Updated = len(plain) + len(auxOrder)
		return nil
	})
}

func (s *Service) Routes(r chi.Router) {
	bk := func(r *http.Request) *book.Book { return book.From(r.Context()).Book }
	r.With(book.Require(account.PermWrite)).Post("/import/accounts", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			LedgerID int64        `json:"ledger_id"`
			Rows     []AccountRow `json:"rows"`
		}](r)
		if err != nil {
			return nil, err
		}
		return s.ImportAccounts(r.Context(), bk(r), in.LedgerID, in.Rows)
	}))
	r.With(book.Require(auxiliary.PermWrite)).Post("/import/aux-items", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			TypeID int64        `json:"type_id"`
			Rows   []AuxItemRow `json:"rows"`
		}](r)
		if err != nil {
			return nil, err
		}
		return s.ImportAuxItems(r.Context(), bk(r), in.TypeID, in.Rows)
	}))
	r.With(book.Require(ledger.PermOpeningWrite)).Post("/import/openings", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			LedgerID int64        `json:"ledger_id"`
			Rows     []OpeningRow `json:"rows"`
		}](r)
		if err != nil {
			return nil, err
		}
		return s.ImportOpenings(r.Context(), bk(r), in.LedgerID, in.Rows)
	}))
}
