package engine

import (
	"fmt"
	"strings"
	"testing"
)

// mustXiDict 构造含 12 个「xi」单字词的词典（词频递减）→ 恰好 2 页。
func mustXiDict(t *testing.T) *Dictionary {
	t.Helper()
	var b strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "词%02d\txi\t%d\n", i, 90-i)
	}
	dict, err := ParseDict(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	return dict
}

// 分页：候选多于 pageSize 时切成多页，翻页在边界钳制。
func TestPaginationPages(t *testing.T) {
	s := NewSession(mustXiDict(t), nil)
	comp, err := s.Input("xi")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if comp.PageCount != 2 || comp.Page != 0 {
		t.Fatalf("PageCount=%d Page=%d, 期望 2 页从第 0 页起", comp.PageCount, comp.Page)
	}
	if len(comp.Candidates) != pageSize {
		t.Fatalf("第一页候选数 = %d, 期望 %d", len(comp.Candidates), pageSize)
	}
	if comp.Candidates[0].Text != "词00" {
		t.Fatalf("第一页首选 = %q, 期望 词00", comp.Candidates[0].Text)
	}

	// 下一页：剩余 3 个候选
	comp, err = s.NextPage()
	if err != nil {
		t.Fatalf("NextPage 失败: %v", err)
	}
	if comp.Page != 1 || len(comp.Candidates) != 3 {
		t.Fatalf("Page=%d 候选数=%d, 期望 第 1 页 3 个候选", comp.Page, len(comp.Candidates))
	}
	if comp.Candidates[0].Text != "词09" {
		t.Fatalf("第二页首选 = %q, 期望 词09", comp.Candidates[0].Text)
	}

	// 末页再下翻：钳制不动
	if comp, _ = s.NextPage(); comp.Page != 1 {
		t.Fatalf("末页下翻应钳制, Page=%d", comp.Page)
	}
	// 上一页回首页；首页再上翻同样钳制
	if comp, _ = s.PrevPage(); comp.Page != 0 {
		t.Fatalf("上翻应回首页, Page=%d", comp.Page)
	}
	if comp, _ = s.PrevPage(); comp.Page != 0 {
		t.Fatalf("首页上翻应钳制, Page=%d", comp.Page)
	}
}

// 页内序号：Select 按当前页的页内序号选候选。
func TestPaginationSelectPageRelative(t *testing.T) {
	s := NewSession(mustXiDict(t), nil)
	s.Input("xi")
	if _, err := s.NextPage(); err != nil {
		t.Fatalf("NextPage 失败: %v", err)
	}
	text, err := s.Select(0)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if text != "词09" {
		t.Fatalf("第二页选 1 应得 词09, 得到 %q", text)
	}
	comp, _ := s.Input("")
	if comp.Input != "" {
		t.Fatalf("选词后组词应清空, 剩余 %q", comp.Input)
	}
}

// 输入变化回到第一页；空组词翻页为无操作。
func TestPaginationResetOnInput(t *testing.T) {
	s := NewSession(mustXiDict(t), nil)
	s.Input("xi")
	s.NextPage()

	comp, _ := s.Input("i") // 组词变化 → 回第一页
	if comp.Page != 0 {
		t.Fatalf("输入变化后 Page=%d, 期望 0", comp.Page)
	}

	// 空组词翻页为无操作（独立会话，避免追加输入语义干扰）
	empty := NewSession(mustXiDict(t), nil)
	if comp, err := empty.NextPage(); err != nil || comp.Page != 0 || comp.Input != "" {
		t.Fatalf("空组词翻页应为无操作: page=%d err=%v", comp.Page, err)
	}

	// Backspace 同样回到第一页
	s.Input("xi")
	s.NextPage()
	if comp, _ = s.Backspace(); comp.Page != 0 {
		t.Fatalf("退格后 Page=%d, 期望 0", comp.Page)
	}
}
