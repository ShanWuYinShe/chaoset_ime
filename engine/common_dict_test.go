package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// mustCommonDict 加载仓库内置常用词库（data/dict_common.tsv），
// 用于对真实词库规模做回归断言。
func mustCommonDict(t *testing.T) *Dictionary {
	t.Helper()
	d, err := LoadDictFiles([]string{filepath.Join("..", "data", "dict_common.tsv")})
	if err != nil {
		t.Fatalf("加载内置常用词库失败: %v", err)
	}
	return d
}

// 用户反馈的真实场景：输入 jingchang 必须能打出「经常」。
// 旧词库（terra 单字表）里没有这个词，导致该输入完全无候选。
func TestCommonDictJingchang(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, err := s.Input("jingchang")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatal("jingchang 应有候选（内置词库缺「经常」会导致无候选）")
	}
	if got := comp.Candidates[0].Text; got != "经常" {
		t.Fatalf("jingchang 第一候选 = %q, 期望「经常」；候选 = %+v", got, comp.Candidates)
	}
}

// 单音节输入不能被生僻字淹没：jing 的第一候选应是常用字（词频最高者）。
func TestCommonDictSingleSyllableRanksCommonChar(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, _ := s.Input("jing")
	if len(comp.Candidates) == 0 {
		t.Fatal("jing 应有候选")
	}
	if got := comp.Candidates[0].Text; got != "经" {
		t.Fatalf("jing 第一候选 = %q, 期望「经」（词频最高）", got)
	}
}

// 音节数完全匹配优先：打满两个音节时，二字词排在「以该序列开头的更长词」前面。
func TestExactSyllableCountPreferred(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp := allCandidates(s, mustComp(s, "jingchang"))
	var idxExact, idxLonger = -1, -1
	for i, c := range comp {
		switch c.Text {
		case "经常":
			idxExact = i
		case "经常化":
			idxLonger = i
		}
	}
	if idxExact < 0 || idxLonger < 0 {
		t.Fatalf("候选应同时含「经常」与「经常化」: %+v", comp)
	}
	if idxExact > idxLonger {
		t.Fatalf("完全匹配的「经常」应排在「经常化」前: 经常@%d 经常化@%d", idxExact, idxLonger)
	}
}

// 调频与「全消耗优先」的关系（2026-10-25 语义演进）：
// 学习过的长词（经常化，3 音节）不再压过「输入完全消耗」的短词（经常，
// 2 音节）——音节超段仲裁位于调频之前，准确匹配用户输入是硬约束。
// 学习效果体现为：「经常化」仍进入候选列表（此前根本查不到），排在
// 全消耗词之后；打满更多音节（jingchangh）时它回到首位。
func TestUserBoostBeatsExactMatch(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user.json")
	d := mustCommonDict(t)
	s := NewSession(d, mustUserDict(t, udPath))
	// 打 jingchang 时选「经常化」（长词；可能落在后续页，翻页定位后按
	// 页内序号选择，Select 的 index 是页内相对序号）
	comp := mustComp(s, "jingchang")
	idx := -1
	for {
		for i, c := range comp.Candidates {
			if c.Text == "经常化" {
				idx = i
				break
			}
		}
		if idx >= 0 || comp.Page >= comp.PageCount-1 {
			break
		}
		next, err := s.NextPage()
		if err != nil {
			break
		}
		comp = next
	}
	if idx < 0 {
		t.Fatalf("候选应含「经常化」: %+v", comp.Candidates)
	}
	if _, err := s.Select(idx); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	// 新会话加载同一份用户词典：
	// ① 全消耗的「经常」仍是第一（音节超段仲裁位于调频之前）
	s2 := NewSession(d, mustUserDict(t, udPath))
	comp2 := mustComp(s2, "jingchang")
	if len(comp2.Candidates) == 0 || comp2.Candidates[0].Text != "经常" {
		t.Fatalf("学习后全消耗词「经常」仍应第一，得到 %+v", comp2.Candidates)
	}
	// ② 「经常化」必须进入候选列表（学习生效；在本页或后续页）
	found := false
	for _, c := range allCandidates(s2, comp2) {
		if c.Text == "经常化" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("学习过的「经常化」应进入候选列表: %+v", comp2.Candidates)
	}
}

// 内置词库规模与词频区间回归：防止生成工具被改坏后词库退化。
func TestCommonDictScale(t *testing.T) {
	d := mustCommonDict(t)
	if d.Len() < 100000 {
		t.Fatalf("内置词库仅 %d 条，少于预期的 10 万条", d.Len())
	}
	// 常用词必须存在（缺词是旧词库最致命的问题）
	for _, w := range []string{"经常", "经济", "时间", "现在", "我们", "中国"} {
		found := false
		for _, e := range d.Entries() {
			if e.Word == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("内置词库缺少常用词 %q", w)
		}
	}
	// 词频区间：全部落在 1..99
	for _, e := range d.Entries() {
		if e.Freq < 1 || e.Freq > 99 {
			t.Fatalf("词条 %q 词频 %d 超出 1..99", e.Word, e.Freq)
		}
	}
}

// TestCommonDictSingleSyllableFullRecall：单音节必须**全量召回**同音字。
// 2026-10-28 用户反馈：输入 shi 打不出「示」——词频排 33 位的常用字被
// 精确匹配的 top20 裁剪挡在候选池外，翻页也不存在。修后同音字全部进入
// 候选池（落到后面的页），同时首选项与词组数量不受影响（双向回归）。
func TestCommonDictSingleSyllableFullRecall(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, err := s.Input("shi")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if got := comp.Candidates[0].Text; got != "是" {
		t.Fatalf("shi 第一候选 = %q, 期望「是」（召回扩大不得冲乱首选）", got)
	}

	// 遍历全部页收集候选
	var all []Candidate
	page := comp
	for {
		all = append(all, page.Candidates...)
		if page.Page+1 >= page.PageCount {
			break
		}
		page, err = s.NextPage()
		if err != nil {
			t.Fatalf("NextPage 失败: %v", err)
		}
	}
	if comp.PageCount <= 1 {
		t.Fatalf("shi 应为多页（同音字 100+），实际 pageCount=%d", comp.PageCount)
	}

	// 正向：「示」必须在候选池中（此前不存在）
	findText := func(text string) (int, bool) {
		for i, c := range all {
			if c.Text == text {
				return i + 1, true
			}
		}
		return 0, false
	}
	pos, ok := findText("示")
	if !ok {
		t.Fatalf("shi 的全部 %d 条候选中找不到「示」——单音节召回仍被裁剪", len(all))
	}
	if pos > 60 {
		t.Fatalf("「示」位次 %d 过深（期望 ≤60，词频 50 属常用字）", pos)
	}

	// 反向：prefix 词组不被 exactLimit 放大——单字召回数 = 词典中 shi
	// 单音节词条数（107），词组保持在原 top20 量级而非洪水。
	singles := 0
	for _, c := range all {
		if !strings.Contains(c.Syllables, "'") {
			singles++
		}
	}
	if singles != 107 {
		t.Fatalf("shi 单字召回应恰好为词典全部 107 个同音字，实际 %d", singles)
	}
	phrases := len(all) - singles
	if phrases > 25 {
		t.Fatalf("shi 衍生词组 %d 条超出 prefix 上限（20±5），单字页位被词组洪水稀释", phrases)
	}
}
