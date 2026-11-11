package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lizhiyi7/cc-lemon/internal/app"
	"github.com/lizhiyi7/cc-lemon/internal/platform/config"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
)

// 端到端测试：经真实路由与 PostgreSQL 走通核心账务流程。
// 需设置 LEMON_TEST_DSN 指向一个可清空的测试库，否则跳过。

type client struct {
	t     *testing.T
	h     http.Handler
	token string
}

func newClient(t *testing.T) *client {
	dsn := os.Getenv("LEMON_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 LEMON_TEST_DSN")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if _, err := d.Pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Auth.TokenTTL = time.Hour
	h, err := app.New(ctx, cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: h}
	c.must("POST", "/setup", map[string]any{"username": "admin", "password": "secret1"}, nil)
	c.login("admin", "secret1")
	return c
}

func (c *client) login(user, pw string) {
	var res struct{ Token string }
	c.token = ""
	c.must("POST", "/auth/login", map[string]any{"username": user, "password": pw}, &res)
	c.token = res.Token
}

// do 发起请求，返回状态码；out 非空时解析响应体。
func (c *client) do(method, path string, body, out any) (int, string) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, &buf)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			c.t.Fatalf("%s %s: 解析响应失败: %v\n%s", method, path, err, rec.Body.String())
		}
	}
	return rec.Code, rec.Body.String()
}

func (c *client) must(method, path string, body, out any) {
	c.t.Helper()
	if code, resp := c.do(method, path, body, out); code >= 300 {
		c.t.Fatalf("%s %s → %d: %s", method, path, code, resp)
	}
}

func (c *client) expect(want int, method, path string, body any) {
	c.t.Helper()
	if code, resp := c.do(method, path, body, nil); code != want {
		c.t.Fatalf("%s %s → %d（期望 %d）: %s", method, path, code, want, resp)
	}
}

type acct struct {
	ID       int64
	LedgerID int64 `json:"ledger_id"`
	Code     string
}

type env struct {
	*client
	base     string
	accounts map[string]int64 // 编码 → ID
	ledgers  map[string]int64
}

func (c *client) newBook(standard string, year, month int) *env {
	c.t.Helper()
	var b struct{ ID int64 }
	c.must("POST", "/books", map[string]any{"name": "测试账套-" + standard, "standard_code": standard, "start_year": year, "start_month": month}, &b)
	e := &env{client: c, base: fmt.Sprintf("/books/%d", b.ID), ledgers: map[string]int64{}}
	var d struct {
		Ledgers []struct {
			ID   int64
			Code string
		}
	}
	c.must("GET", e.base, nil, &d)
	for _, l := range d.Ledgers {
		e.ledgers[l.Code] = l.ID
	}
	e.reload()
	return e
}

func (e *env) reload() {
	var list []acct
	e.must("GET", e.base+"/accounts", nil, &list)
	e.accounts = map[string]int64{}
	for _, a := range list {
		e.accounts[a.Code] = a.ID
	}
}

type line struct {
	code          string
	debit, credit string
}

func (e *env) lines(ls []line) []map[string]any {
	out := []map[string]any{}
	for _, l := range ls {
		m := map[string]any{"summary": "测试", "account_id": e.accounts[l.code], "debit": "0", "credit": "0"}
		if l.debit != "" {
			m["debit"] = l.debit
		}
		if l.credit != "" {
			m["credit"] = l.credit
		}
		out = append(out, m)
	}
	return out
}

type voucherResp struct {
	ID       int64
	Number   int
	Warnings []string
}

func (e *env) post(date string, ls ...line) voucherResp {
	e.t.Helper()
	var v voucherResp
	e.must("POST", e.base+"/vouchers", map[string]any{"date": date, "lines": e.lines(ls)}, &v)
	return v
}

type reportResp struct {
	Rows []struct {
		Key    string
		Values map[string]string
	}
	Checks []struct {
		Name string
		OK   bool
	}
}

func (r *reportResp) val(t *testing.T, key, col string) string {
	t.Helper()
	for _, row := range r.Rows {
		if row.Key == key {
			return row.Values[col]
		}
	}
	t.Fatalf("报表无行 %s", key)
	return ""
}

func eq(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %s，期望 %s", what, got, want)
	}
}

func TestSmallEnterpriseFlow(t *testing.T) {
	e := newClient(t).newBook("small_enterprise", 2026, 1)

	// 期初：银行存款 10 万 / 实收资本 10 万
	e.must("PUT", e.base+"/opening", map[string]any{"items": []map[string]any{
		{"account_id": e.accounts["1002"], "balance": "100000"},
		{"account_id": e.accounts["3001"], "balance": "100000"},
	}}, nil)
	var oc []struct{ Balanced bool }
	e.must("GET", e.base+"/opening/check", nil, &oc)
	if !oc[0].Balanced {
		t.Fatal("期初应平衡")
	}
	// 损益类不允许期初余额
	e.expect(400, "PUT", e.base+"/opening", map[string]any{"items": []map[string]any{{"account_id": e.accounts["5001"], "balance": "1"}}})

	v1 := e.post("2026-01-05", line{"1002", "50000", ""}, line{"5001", "", "50000"})
	eq(t, "首张凭证号", fmt.Sprint(v1.Number), "1")
	e.post("2026-01-10", line{"5602", "8000", ""}, line{"1002", "", "8000"})

	// 借贷不平、非末级科目、启用前期间 均应被拒绝
	e.expect(400, "POST", e.base+"/vouchers", map[string]any{"date": "2026-01-11", "lines": e.lines([]line{{"1002", "1", ""}, {"5001", "", "2"}})})
	e.expect(400, "POST", e.base+"/vouchers", map[string]any{"date": "2026-01-11", "lines": e.lines([]line{{"2221", "1", ""}, {"1002", "", "1"}})})
	e.expect(400, "POST", e.base+"/vouchers", map[string]any{"date": "2025-12-31", "lines": e.lines([]line{{"1001", "1", ""}, {"1002", "", "1"}})})

	// 应收账款先记账，再增加下级：数据应下沉到新科目
	e.post("2026-01-15", line{"1122", "2000", ""}, line{"5001", "", "2000"})
	e.must("POST", e.base+"/accounts", map[string]any{"parent_id": e.accounts["1122"], "code": "112201", "name": "甲公司"}, nil)
	e.reload()
	e.expect(400, "POST", e.base+"/vouchers", map[string]any{"date": "2026-01-16", "lines": e.lines([]line{{"1122", "1", ""}, {"5001", "", "1"}})})

	var tb struct {
		Rows []struct {
			Code                    string
			EndDebit                string `json:"end_debit"`
			EndCredit, OpenDebit    string
			Debit, Credit, YtdDebit string
		}
		Total struct {
			EndDebit  string `json:"end_debit"`
			EndCredit string `json:"end_credit"`
			Debit     string
			Credit    string
		}
	}
	e.must("GET", fmt.Sprintf("%s/ledger/trial-balance?from=2026-01&to=2026-01&ledger_id=%d", e.base, e.ledgers["MAIN"]), nil, &tb)
	eq(t, "发生额合计", tb.Total.Debit, tb.Total.Credit)
	eq(t, "期末余额合计", tb.Total.EndDebit, tb.Total.EndCredit)
	for _, r := range tb.Rows {
		switch r.Code {
		case "1002":
			eq(t, "银行存款期末", r.EndDebit, "142000")
		case "1122", "112201":
			eq(t, r.Code+" 期末", r.EndDebit, "2000")
		}
	}

	// 未结转损益不能结账
	e.expect(409, "POST", e.base+"/periods/2026-01/close", nil)
	var gen struct{ Voucher *voucherResp }
	e.must("POST", e.base+"/closing/2026-01/pl_transfer", nil, &gen)
	if gen.Voucher == nil {
		t.Fatal("应生成结转凭证")
	}
	// 重复执行应替换而非叠加
	e.must("POST", e.base+"/closing/2026-01/pl_transfer", nil, &gen)
	var st []struct {
		Code       string
		Pending    bool
		VoucherIDs []int64 `json:"voucher_ids"`
	}
	e.must("GET", e.base+"/closing/2026-01", nil, &st)
	if st[0].Pending || len(st[0].VoucherIDs) != 1 {
		t.Fatalf("结转状态异常: %+v", st[0])
	}

	var is reportResp
	e.must("GET", e.base+"/reports/income_statement?period=2026-01", nil, &is)
	eq(t, "营业收入", is.val(t, "1", "month"), "52000")
	eq(t, "管理费用", is.val(t, "14", "month"), "8000")
	eq(t, "净利润", is.val(t, "32", "ytd"), "44000")

	var bs reportResp
	e.must("GET", e.base+"/reports/balance_sheet?period=2026-01", nil, &bs)
	eq(t, "资产总计", bs.val(t, "30", "end"), "144000")
	eq(t, "年初资产总计", bs.val(t, "30", "begin"), "100000")
	eq(t, "未分配利润", bs.val(t, "51", "end"), "44000")
	if !bs.Checks[0].OK {
		t.Fatal("资产负债表应平衡")
	}

	// 结账后期间锁定；反结账后恢复
	e.must("POST", e.base+"/periods/2026-01/close", nil, nil)
	e.expect(409, "POST", e.base+"/vouchers", map[string]any{"date": "2026-01-20", "lines": e.lines([]line{{"1001", "1", ""}, {"1002", "", "1"}})})
	e.expect(409, "DELETE", fmt.Sprintf("%s/vouchers/%d", e.base, v1.ID), nil)
	e.expect(409, "PUT", e.base+"/opening", map[string]any{"items": []map[string]any{}})

	// 二月：累计数与年初数
	e.post("2026-02-03", line{"1002", "1000", ""}, line{"5001", "", "1000"})
	e.must("GET", e.base+"/reports/income_statement?period=2026-02", nil, &is)
	eq(t, "二月营业收入", is.val(t, "1", "month"), "1000")
	eq(t, "累计营业收入", is.val(t, "1", "ytd"), "53000")

	var detail []struct {
		Kind    string
		Balance string
	}
	e.must("GET", fmt.Sprintf("%s/ledger/detail?account_id=%d&from=2026-01&to=2026-02", e.base, e.accounts["1002"]), nil, &detail)
	eq(t, "明细账期初", detail[0].Balance, "100000")
	eq(t, "明细账期末", detail[len(detail)-1].Balance, "143000")

	e.expect(409, "POST", e.base+"/periods/2026-02/reopen", nil)
	e.must("POST", e.base+"/periods/2026-01/reopen", nil, nil)
	e.must("DELETE", fmt.Sprintf("%s/vouchers/%d", e.base, v1.ID), nil, nil)
	// 删除后出现断号，整理凭证号
	e.must("POST", e.base+"/vouchers/renumber", map[string]any{"year": 2026, "month": 1}, nil)
	var page struct {
		Items []struct{ Number int }
		Total int
	}
	e.must("GET", e.base+"/vouchers?from=2026-01&to=2026-01", nil, &page)
	for i, v := range page.Items {
		eq(t, "整理后凭证号", fmt.Sprint(v.Number), fmt.Sprint(i+1))
	}
}

func TestGovParallelFlow(t *testing.T) {
	c := newClient(t)
	e := c.newBook("gov_accounting", 2026, 1)

	type sug struct {
		Lines []struct {
			AccountCode string `json:"account_code"`
			Debit       string
			Credit      string
		}
		Warnings []string
	}
	suggest := func(ls ...line) sug {
		t.Helper()
		var s sug
		e.must("POST", e.base+"/vouchers/parallel-suggest", map[string]any{"lines": e.lines(ls)}, &s)
		return s
	}

	// 收到财政拨款：财务 借银行存款/贷财政拨款收入 → 预算 借资金结存-货币资金/贷财政拨款预算收入
	s := suggest(line{"1002", "10000", ""}, line{"4001", "", "10000"})
	if len(s.Lines) != 2 || s.Lines[0].AccountCode != "800102" || s.Lines[0].Debit != "10000" ||
		s.Lines[1].AccountCode != "6001" || s.Lines[1].Credit != "10000" {
		t.Fatalf("平行分录建议不符: %+v", s)
	}
	// 方向限定规则：借入 → 债务预算收入；还本 → 债务还本支出
	if s := suggest(line{"1002", "500", ""}, line{"2001", "", "500"}); s.Lines[1].AccountCode != "6501" {
		t.Fatalf("借入应映射 6501: %+v", s)
	}
	if s := suggest(line{"2001", "500", ""}, line{"1002", "", "500"}); s.Lines[0].AccountCode != "7701" {
		t.Fatalf("还本应映射 7701: %+v", s)
	}
	// 不涉及现金（计提折旧）不生成
	if s := suggest(line{"5001", "100", ""}, line{"1602", "", "100"}); len(s.Lines) != 0 {
		t.Fatalf("非现金业务不应生成预算分录: %+v", s)
	}

	// 只录财务会计 → 提示；两套都录 → 无提示
	v := e.post("2026-01-05", line{"1002", "10000", ""}, line{"4001", "", "10000"})
	if len(v.Warnings) != 1 {
		t.Fatalf("应有平行记账提示: %+v", v)
	}
	e.must("DELETE", fmt.Sprintf("%s/vouchers/%d", e.base, v.ID), nil, nil)
	v = e.post("2026-01-05", line{"1002", "10000", ""}, line{"4001", "", "10000"},
		line{"800102", "10000", ""}, line{"6001", "", "10000"})
	if len(v.Warnings) != 0 {
		t.Fatalf("不应有提示: %+v", v)
	}
	e.post("2026-01-08", line{"5001", "3000", ""}, line{"1011", "", "3000"},
		line{"7201", "3000", ""}, line{"800101", "", "3000"})
	// 预算会计分录自身不平衡应被拒绝
	e.expect(400, "POST", e.base+"/vouchers", map[string]any{"date": "2026-01-09", "lines": e.lines([]line{
		{"1002", "1", ""}, {"4001", "", "1"}, {"800102", "2", ""}, {"6001", "", "1"}})})

	// strict 模式下缺少预算分录直接拒绝
	var d struct{ Settings map[string]any }
	e.must("GET", e.base, nil, &d)
	d.Settings["parallel"] = map[string]any{"mode": "strict"}
	e.must("PATCH", e.base, map[string]any{"settings": d.Settings}, nil)
	e.expect(400, "POST", e.base+"/vouchers", map[string]any{"date": "2026-01-09", "lines": e.lines([]line{{"1002", "1", ""}, {"4001", "", "1"}})})

	for _, lg := range []string{"FIN", "BUD"} {
		var tb struct {
			Total struct {
				EndDebit  string `json:"end_debit"`
				EndCredit string `json:"end_credit"`
			}
		}
		e.must("GET", fmt.Sprintf("%s/ledger/trial-balance?from=2026-01&to=2026-01&ledger_id=%d", e.base, e.ledgers[lg]), nil, &tb)
		eq(t, lg+" 期末余额合计", tb.Total.EndDebit, tb.Total.EndCredit)
	}

	var bud reportResp
	e.must("GET", e.base+"/reports/budget_revenue_expense?period=2026-01", nil, &bud)
	eq(t, "预算收入", bud.val(t, "1", "month"), "10000")
	eq(t, "预算收支差额", bud.val(t, "20", "ytd"), "7000")

	e.must("POST", e.base+"/closing/2026-01/fin_surplus", nil, nil)
	var re, bs reportResp
	e.must("GET", e.base+"/reports/revenue_expense?period=2026-01", nil, &re)
	eq(t, "本期盈余", re.val(t, "22", "month"), "7000")
	e.must("GET", e.base+"/reports/balance_sheet?period=2026-01", nil, &bs)
	eq(t, "资产总计", bs.val(t, "39", "end"), "7000")
	if !bs.Checks[0].OK {
		t.Fatal("资产负债表应平衡")
	}
	e.must("POST", e.base+"/periods/2026-01/close", nil, nil)

	// 权限：查看角色不能记账，非成员不能访问
	var u struct{ ID int64 }
	c.must("POST", "/users", map[string]any{"username": "viewer", "password": "secret1"}, &u)
	c.must("POST", "/users", map[string]any{"username": "outsider", "password": "secret1"}, nil)
	e.must("PUT", fmt.Sprintf("%s/members/%d", e.base, u.ID), map[string]any{"role": "viewer"}, nil)
	c.login("viewer", "secret1")
	e.must("GET", e.base+"/accounts", nil, nil)
	e.expect(403, "POST", e.base+"/vouchers", map[string]any{"date": "2026-02-01", "lines": e.lines([]line{{"1001", "1", ""}, {"1002", "", "1"}})})
	c.login("outsider", "secret1")
	e.expect(403, "GET", e.base+"/accounts", nil)
	c.token = ""
	e.expect(401, "GET", e.base+"/accounts", nil)
}

func TestAuxiliaryFlow(t *testing.T) {
	e := newClient(t).newBook("small_enterprise", 2026, 1)

	var types []struct {
		ID   int64
		Code string
	}
	e.must("GET", e.base+"/aux/types", nil, &types)
	typeID := map[string]int64{}
	for _, tp := range types {
		typeID[tp.Code] = tp.ID
	}
	if typeID["customer"] == 0 || typeID["department"] == 0 {
		t.Fatalf("建账应预置辅助核算类别: %+v", types)
	}
	item := func(tp, code, name string) int64 {
		t.Helper()
		var it struct{ ID int64 }
		e.must("POST", e.base+"/aux/items", map[string]any{"type_id": typeID[tp], "code": code, "name": name}, &it)
		return it.ID
	}
	custA, custB, dept := item("customer", "001", "甲公司"), item("customer", "002", "乙公司"), item("department", "01", "行政部")
	e.expect(409, "POST", e.base+"/aux/items", map[string]any{"type_id": typeID["customer"], "code": "001", "name": "重复"})

	bind := func(code string, tps ...string) (int, string) {
		ids := []int64{}
		for _, tp := range tps {
			ids = append(ids, typeID[tp])
		}
		return e.do("PUT", fmt.Sprintf("%s/accounts/%d/aux", e.base, e.accounts[code]), map[string]any{"type_ids": ids}, nil)
	}
	if code, resp := bind("1122", "customer"); code >= 300 {
		t.Fatal(resp)
	}
	if code, resp := bind("5602", "department"); code >= 300 {
		t.Fatal(resp)
	}
	// 启用辅助核算后不能再拆下级
	e.expect(409, "POST", e.base+"/accounts", map[string]any{"parent_id": e.accounts["1122"], "code": "112201", "name": "x"})

	aux := func(tp string, id int64) []map[string]any {
		return []map[string]any{{"type_id": typeID[tp], "item_id": id}}
	}
	// 期初：应收账款按客户录入，科目期初自动汇总；不能绕过项目直接录科目期初
	e.must("PUT", fmt.Sprintf("%s/opening/aux/%d", e.base, e.accounts["1122"]), map[string]any{"items": []map[string]any{
		{"aux": aux("customer", custA), "balance": "2000"},
	}}, nil)
	e.expect(400, "PUT", e.base+"/opening", map[string]any{"items": []map[string]any{{"account_id": e.accounts["1122"], "balance": "1"}}})
	e.must("PUT", e.base+"/opening", map[string]any{"items": []map[string]any{{"account_id": e.accounts["3001"], "balance": "2000"}}}, nil)
	var oc []struct{ Balanced bool }
	e.must("GET", e.base+"/opening/check", nil, &oc)
	if !oc[0].Balanced {
		t.Fatal("期初应平衡")
	}

	post := func(date string, lines ...map[string]any) (int, string, voucherResp) {
		var v voucherResp
		code, resp := e.do("POST", e.base+"/vouchers", map[string]any{"date": date, "lines": lines}, &v)
		return code, resp, v
	}
	ln := func(code, debit, credit string, a []map[string]any) map[string]any {
		m := e.lines([]line{{code, debit, credit}})[0]
		if a != nil {
			m["aux"] = a
		}
		return m
	}
	// 缺少辅助核算项目、项目类别不符 均拒绝
	if code, _, _ := post("2026-01-05", ln("1122", "1000", "", nil), ln("5001", "", "1000", nil)); code != 400 {
		t.Fatalf("缺少客户应被拒绝，得到 %d", code)
	}
	if code, _, _ := post("2026-01-05", ln("1122", "1000", "", aux("customer", dept)), ln("5001", "", "1000", nil)); code != 400 {
		t.Fatalf("项目类别不符应被拒绝，得到 %d", code)
	}
	for _, c := range []struct {
		lines []map[string]any
	}{
		{[]map[string]any{ln("1122", "1000", "", aux("customer", custA)), ln("5001", "", "1000", nil)}},
		{[]map[string]any{ln("1122", "500", "", aux("customer", custB)), ln("5001", "", "500", nil)}},
		{[]map[string]any{ln("5602", "300", "", aux("department", dept)), ln("1122", "", "300", aux("customer", custA))}},
	} {
		if code, resp, _ := post("2026-01-06", c.lines...); code >= 300 {
			t.Fatal(resp)
		}
	}
	// 已有数据后不能调整绑定；已使用的项目不能删除但可停用
	if code, _ := bind("1122"); code != 409 {
		t.Fatalf("已有数据的科目不应允许调整辅助核算，得到 %d", code)
	}
	e.expect(409, "DELETE", fmt.Sprintf("%s/aux/items/%d", e.base, custA), nil)
	e.must("PATCH", fmt.Sprintf("%s/aux/items/%d", e.base, dept), map[string]any{"enabled": false}, nil)

	var ab struct {
		Rows []struct {
			ItemCode  string `json:"item_code"`
			OpenDebit string `json:"open_debit"`
			Debit     string
			Credit    string
			EndDebit  string `json:"end_debit"`
		}
		Total struct {
			EndDebit string `json:"end_debit"`
		}
	}
	e.must("GET", fmt.Sprintf("%s/ledger/aux-balance?type_id=%d&from=2026-01&to=2026-01", e.base, typeID["customer"]), nil, &ab)
	if len(ab.Rows) != 2 {
		t.Fatalf("辅助核算余额表应有两行: %+v", ab)
	}
	eq(t, "甲公司期初", ab.Rows[0].OpenDebit, "2000")
	eq(t, "甲公司借方", ab.Rows[0].Debit, "1000")
	eq(t, "甲公司贷方", ab.Rows[0].Credit, "300")
	eq(t, "甲公司期末", ab.Rows[0].EndDebit, "2700")
	eq(t, "乙公司期末", ab.Rows[1].EndDebit, "500")
	eq(t, "合计期末", ab.Total.EndDebit, "3200")

	var detail []struct {
		Kind    string
		Balance string
	}
	e.must("GET", fmt.Sprintf("%s/ledger/aux-detail?item_id=%d&from=2026-01&to=2026-01", e.base, custA), nil, &detail)
	eq(t, "辅助明细账期初", detail[0].Balance, "2000")
	eq(t, "辅助明细账期末", detail[len(detail)-1].Balance, "2700")

	// 结转损益：带辅助核算的损益科目按项目转平（即使项目已停用）
	var gen struct {
		Voucher *struct {
			Lines []struct {
				AccountCode string `json:"account_code"`
				Credit      string
				Aux         []struct {
					ItemName string `json:"item_name"`
				}
			}
		}
	}
	e.must("POST", e.base+"/closing/2026-01/pl_transfer", nil, &gen)
	found := false
	for _, l := range gen.Voucher.Lines {
		if l.AccountCode == "5602" {
			found = len(l.Aux) == 1 && l.Aux[0].ItemName == "行政部" && l.Credit == "300"
		}
	}
	if !found {
		t.Fatalf("结转凭证应包含带部门的管理费用分录: %+v", gen.Voucher)
	}
	e.must("POST", e.base+"/periods/2026-01/close", nil, nil)
	var bs reportResp
	e.must("GET", e.base+"/reports/balance_sheet?period=2026-01", nil, &bs)
	eq(t, "应收账款", bs.val(t, "4", "end"), "3200")
	if !bs.Checks[0].OK {
		t.Fatal("资产负债表应平衡")
	}
}
