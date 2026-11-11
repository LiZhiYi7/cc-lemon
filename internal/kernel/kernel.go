// Package kernel 放置各业务模块共享的最小领域概念（期间、方向等），不依赖任何其他包。
package kernel

import (
	"fmt"
	"time"
)

// Period 会计期间（自然月）。
type Period struct {
	Year  int `json:"year"`
	Month int `json:"month"`
}

func (p Period) YM() int { return p.Year*100 + p.Month }
func (p Period) Valid() bool {
	return p.Year >= 1900 && p.Year <= 9999 && p.Month >= 1 && p.Month <= 12
}
func (p Period) Before(o Period) bool { return p.YM() < o.YM() }
func (p Period) String() string       { return fmt.Sprintf("%04d-%02d", p.Year, p.Month) }

func (p Period) Next() Period {
	if p.Month == 12 {
		return Period{p.Year + 1, 1}
	}
	return Period{p.Year, p.Month + 1}
}

func (p Period) Prev() Period {
	if p.Month == 1 {
		return Period{p.Year - 1, 12}
	}
	return Period{p.Year, p.Month - 1}
}

// LastDay 期间最后一天。
func (p Period) LastDay() time.Time {
	return time.Date(p.Year, time.Month(p.Month)+1, 0, 0, 0, 0, 0, time.UTC)
}

// ParsePeriod 解析 "2026-03"。
func ParsePeriod(s string) (Period, error) {
	var p Period
	if _, err := fmt.Sscanf(s, "%d-%d", &p.Year, &p.Month); err != nil || !p.Valid() {
		return p, fmt.Errorf("无效的会计期间: %q", s)
	}
	return p, nil
}

// 科目余额方向。
const (
	Debit  = "D"
	Credit = "C"
)
