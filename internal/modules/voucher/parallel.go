package voucher

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/account"
	"github.com/lizhiyi7/cc-lemon/internal/modules/auxiliary"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/audit"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

const PermParallelWrite = "parallel:write"

// ParallelRule 平行记账规则：来源账簿科目（及其下级）发生时，在目标账簿生成同方向、同金额的分录。
type ParallelRule struct {
	ID             int64  `json:"id"`
	SourceLedgerID int64  `json:"source_ledger_id"`
	SourceCode     string `json:"source_code"`
	TargetLedgerID int64  `json:"target_ledger_id"`
	TargetCode     string `json:"target_code"`
	Side           string `json:"side"` // "" 借贷均适用 | D 仅借方 | C 仅贷方
	Note           string `json:"note"`
}

func (s *Service) ParallelRules(ctx context.Context, bookID int64) ([]ParallelRule, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) (ParallelRule, error) {
		var p ParallelRule
		return p, r.Scan(&p.ID, &p.SourceLedgerID, &p.SourceCode, &p.TargetLedgerID, &p.TargetCode, &p.Side, &p.Note)
	}, `select id, source_ledger_id, source_code, target_ledger_id, target_code, side, note
		from parallel_rules where book_id=$1 order by source_ledger_id, source_code, side`, bookID)
}

// parallelPairs 返回存在规则的（来源账簿, 目标账簿）组合。
func (s *Service) parallelPairs(ctx context.Context, bookID int64) ([][2]int64, error) {
	return db.Collect(ctx, s.db.Q(ctx), func(r pgx.Rows) ([2]int64, error) {
		var p [2]int64
		return p, r.Scan(&p[0], &p[1])
	}, `select distinct source_ledger_id, target_ledger_id from parallel_rules where book_id=$1`, bookID)
}

// SaveParallelRule 新增（ID=0）或修改规则。
func (s *Service) SaveParallelRule(ctx context.Context, b *book.Book, in ParallelRule) (*ParallelRule, error) {
	if in.Side != "" && in.Side != kernel.Debit && in.Side != kernel.Credit {
		return nil, httpx.BadRequest("适用方向无效")
	}
	if in.SourceLedgerID == in.TargetLedgerID {
		return nil, httpx.BadRequest("来源账簿与目标账簿不能相同")
	}
	accounts, err := s.accounts.List(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	has := func(ledgerID int64, code string) bool {
		for _, a := range accounts {
			if a.LedgerID == ledgerID && a.Code == code {
				return true
			}
		}
		return false
	}
	if !has(in.SourceLedgerID, in.SourceCode) || !has(in.TargetLedgerID, in.TargetCode) {
		return nil, httpx.BadRequest("来源科目或目标科目不存在")
	}
	q := s.db.Q(ctx)
	if in.ID == 0 {
		err = q.QueryRow(ctx, `insert into parallel_rules(book_id, source_ledger_id, source_code, target_ledger_id, target_code, side, note)
			values($1,$2,$3,$4,$5,$6,$7) returning id`, b.ID, in.SourceLedgerID, in.SourceCode, in.TargetLedgerID, in.TargetCode, in.Side, in.Note).Scan(&in.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = q.Exec(ctx, `update parallel_rules set source_ledger_id=$3, source_code=$4, target_ledger_id=$5, target_code=$6, side=$7, note=$8
			where book_id=$1 and id=$2`, b.ID, in.ID, in.SourceLedgerID, in.SourceCode, in.TargetLedgerID, in.TargetCode, in.Side, in.Note)
		if err == nil && tag.RowsAffected() == 0 {
			return nil, httpx.NotFound("规则不存在")
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, httpx.Conflict("该来源科目在相同方向上已有规则")
	}
	if err != nil {
		return nil, err
	}
	audit.Log(ctx, s.db, b.ID, "parallel_rule.save", "parallel_rule", in.ID, in)
	return &in, nil
}

func (s *Service) DeleteParallelRule(ctx context.Context, bookID, id int64) error {
	_, err := s.db.Q(ctx).Exec(ctx, `delete from parallel_rules where book_id=$1 and id=$2`, bookID, id)
	if err == nil {
		audit.Log(ctx, s.db, bookID, "parallel_rule.delete", "parallel_rule", id, nil)
	}
	return err
}

// Suggestion 平行记账分录建议。
type Suggestion struct {
	Lines    []Line   `json:"lines"`
	Warnings []string `json:"warnings"`
}

// SuggestParallel 根据来源账簿分录生成目标账簿分录建议（不落库，由用户确认后随凭证保存）。
//
// 触发条件：某来源账簿的分录中出现现金类科目（纳入预算管理的现金收支）。
// 生成方式：该账簿的每条分录按规则映射为同方向、同金额的目标账簿分录；
// 规则查找顺序为 科目本身 → 逐级上级，同级内“限定方向”的规则优先于通用规则。
func (s *Service) SuggestParallel(ctx context.Context, b *book.Book, lines []LineInput) (*Suggestion, error) {
	out := &Suggestion{Lines: []Line{}, Warnings: []string{}}
	rules, err := s.ParallelRules(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		out.Warnings = append(out.Warnings, "当前账套未配置平行记账规则")
		return out, nil
	}
	accounts, err := s.accounts.List(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	checker, err := s.aux.Checker(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*account.Account{}
	type codeKey struct {
		ledger int64
		code   string
	}
	byCode := map[codeKey]*account.Account{}
	for _, a := range accounts {
		byID[a.ID] = a
		byCode[codeKey{a.LedgerID, a.Code}] = a
	}
	type ruleKey struct {
		ledger     int64
		code, side string
	}
	ruleMap := map[ruleKey][]ParallelRule{}
	isSource := map[int64]bool{}
	for _, r := range rules {
		k := ruleKey{r.SourceLedgerID, r.SourceCode, r.Side}
		ruleMap[k] = append(ruleMap[k], r)
		isSource[r.SourceLedgerID] = true
	}
	cash := map[int64]bool{}
	for _, l := range lines {
		if a := byID[l.AccountID]; a != nil && a.IsCash {
			cash[a.LedgerID] = true
		}
	}
	triggered := false
	for _, l := range lines {
		a := byID[l.AccountID]
		if a == nil || !isSource[a.LedgerID] || !cash[a.LedgerID] {
			continue
		}
		triggered = true
		lineSide := kernel.Debit
		if l.Debit.IsZero() {
			lineSide = kernel.Credit
		}
		var matched []ParallelRule
		for x := a; x != nil && matched == nil; {
			if matched = ruleMap[ruleKey{x.LedgerID, x.Code, lineSide}]; matched == nil {
				matched = ruleMap[ruleKey{x.LedgerID, x.Code, ""}]
			}
			if x.ParentID == nil {
				break
			}
			x = byID[*x.ParentID]
		}
		if matched == nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("科目 %s %s 未配置平行记账规则，如需记入请手工补录", a.Code, a.Name))
			continue
		}
		for _, r := range matched {
			t := byCode[codeKey{r.TargetLedgerID, r.TargetCode}]
			if t == nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("规则 %s→%s 的目标科目不存在", r.SourceCode, r.TargetCode))
				continue
			}
			if !t.IsLeaf {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s %s 有下级科目，请改选末级科目", t.Code, t.Name))
			}
			line := Line{LedgerID: t.LedgerID, Summary: l.Summary, AccountID: t.ID,
				AccountCode: t.Code, AccountName: t.FullName, Debit: l.Debit, Credit: l.Credit}
			// 目标科目启用的辅助核算类别若来源分录也有，则沿用同一项目；其余留给用户补选
			for _, typeID := range checker.Required(t.ID) {
				for _, ref := range l.Aux {
					if ref.TypeID == typeID && ref.ItemID != 0 {
						line.Aux = append(line.Aux, auxiliary.Ref{TypeID: typeID, ItemID: ref.ItemID})
					}
				}
			}
			out.Lines = append(out.Lines, line)
		}
	}
	if !triggered {
		out.Warnings = append(out.Warnings, "分录未涉及现金类科目，无需平行记账")
	}
	return out, nil
}
