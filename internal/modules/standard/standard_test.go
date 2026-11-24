package standard_test

import (
	"strings"
	"testing"

	"github.com/lizhiyi7/yanghe-finance/configs"
	"github.com/lizhiyi7/yanghe-finance/internal/modules/standard"
)

// 默认（不配置 allow）应加载全部内置准则。
func TestLoadAll(t *testing.T) {
	r, err := standard.Load(configs.Standards, "", nil)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if _, ok := r.Get("gov_accounting"); !ok {
		t.Fatal("缺少 gov_accounting")
	}
	if _, ok := r.Get("small_enterprise"); !ok {
		t.Fatal("缺少 small_enterprise")
	}
	if len(r.List()) != 2 {
		t.Fatalf("期望 2 套准则，实际 %d", len(r.List()))
	}
}

// 配置白名单后，只保留指定准则，其余在建账时不可选也不可用。
func TestLoadWithAllow(t *testing.T) {
	r, err := standard.Load(configs.Standards, "", []string{"gov_accounting"})
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	list := r.List()
	if len(list) != 1 || list[0].Code != "gov_accounting" {
		t.Fatalf("期望只剩 gov_accounting，实际 %d 套", len(list))
	}
	if _, ok := r.Get("small_enterprise"); ok {
		t.Fatal("small_enterprise 应被过滤掉")
	}
}

// 白名单里写了不存在的编码要直接报错，而不是静默筛空。
func TestLoadWithUnknownAllow(t *testing.T) {
	_, err := standard.Load(configs.Standards, "", []string{"not_exist"})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !strings.Contains(err.Error(), "not_exist") {
		t.Fatalf("错误信息应包含错误的编码，实际: %v", err)
	}
}

// 空白项应被忽略，不会把模板全删光。
func TestLoadWithBlankAllow(t *testing.T) {
	r, err := standard.Load(configs.Standards, "", []string{"  ", ""})
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(r.List()) != 2 {
		t.Fatalf("空白白名单不应过滤，实际剩 %d 套", len(r.List()))
	}
}
