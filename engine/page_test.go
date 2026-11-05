package engine

import (
	"fmt"
	"strings"
	"testing"
)

// mustXiDict 构造含 12 个不同「xi」单字的词典（词频递减）→ 恰好 2 页。
func mustXiDict(t *testing.T) *Dictionary {
	t.Helper()
	chars := []string{"西", "希", "息", "喜", "系", "细", "习", "吸", "析", "嬉", "膝", "夕"}
	var b strings.Builder
	for i, ch := range chars {
		fmt.Fprintf(&b, "%s\txi\t%d\n", ch, 90-i)
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
	comp, err := s.Input("xi", false)
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if comp.PageCount != 2 || comp.Page != 0 {
		t.Fatalf("PageCount=%d Page=%d, 期望 2 页从第 0 页起", comp.PageCount, comp.Page)
	}
	if len(comp.Candidates) != pageSize {
		t.Fatalf("第一页候选数 = %d, 期望 %d", len(comp.Candidates), pageSize)
	}
	if comp.Candidates[0].Text != "西" {
		t.Fatalf("第一页首选 = %q, 期望 西", comp.Candidates[0].Text)
	}

	// 下一页：恰好剩余一整页候选
	comp, err = s.NextPage()
	if err != nil {
		t.Fatalf("NextPage 失败: %v", err)
	}
	if comp.Page != 1 || len(comp.Candidates) != pageSize {
		t.Fatalf("Page=%d 候选数=%d, 期望 第 1 页 %d 个候选", comp.Page, len(comp.Candidates), pageSize)
	}
	if comp.Candidates[0].Text != "习" {
		t.Fatalf("第二页首选 = %q, 期望 习", comp.Candidates[0].Text)
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
	s.Input("xi", false)
	if _, err := s.NextPage(); err != nil {
		t.Fatalf("NextPage 失败: %v", err)
	}
	text, err := s.Select(0)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if text != "习" {
		t.Fatalf("第二页选 1 应得 习, 得到 %q", text)
	}
	comp, _ := s.Input("", false)
	if comp.Input != "" {
		t.Fatalf("选词后组词应清空, 剩余 %q", comp.Input)
	}
}

// 输入变化回到第一页；空组词翻页为无操作。
func TestPaginationResetOnInput(t *testing.T) {
	s := NewSession(mustXiDict(t), nil)
	s.Input("xi", false)
	s.NextPage()

	comp, _ := s.Input("i", false) // 组词变化 → 回第一页
	if comp.Page != 0 {
		t.Fatalf("输入变化后 Page=%d, 期望 0", comp.Page)
	}

	// 空组词翻页为无操作（独立会话，避免追加输入语义干扰）
	empty := NewSession(mustXiDict(t), nil)
	if comp, err := empty.NextPage(); err != nil || comp.Page != 0 || comp.Input != "" {
		t.Fatalf("空组词翻页应为无操作: page=%d err=%v", comp.Page, err)
	}

	// Backspace 同样回到第一页
	s.Input("xi", false)
	s.NextPage()
	if comp, _ = s.Backspace(); comp.Page != 0 {
		t.Fatalf("退格后 Page=%d, 期望 0", comp.Page)
	}
}

// allCandidates 翻页收集全部候选。pageSize 缩小后，排序/回退类断言的
// 目标词可能落在后续页——这类断言应基于完整列表而非首页。
func allCandidates(s *Session, comp Composition) []Candidate {
	out := append([]Candidate(nil), comp.Candidates...)
	for comp.Page < comp.PageCount-1 {
		next, err := s.NextPage()
		if err != nil {
			break
		}
		comp = next
		out = append(out, comp.Candidates...)
	}
	return out
}

// mustComp 输入整串拼音并返回最终组词状态（测试 helper）。
func mustComp(s *Session, input string) Composition {
	comp, err := s.Input(input, false)
	if err != nil {
		panic(err)
	}
	return comp
}
