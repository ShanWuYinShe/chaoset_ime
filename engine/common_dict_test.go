package engine

import (
	"path/filepath"
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

// 用户调频必须压过「完全匹配优先」：反复选过长词后，长词应排到前面。
func TestUserBoostBeatsExactMatch(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user.json")
	d := mustCommonDict(t)
	s := NewSession(d, mustUserDict(t, udPath))
	// 打 jingchang 时选「经常化」（长词，非完全匹配；可能落在后续页，
	// 翻页定位后按页内序号选择）
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
	// 新会话加载同一份用户词典：学习过的长词应排到第一
	s2 := NewSession(d, mustUserDict(t, udPath))
	comp2, _ := s2.Input("jingchang")
	if len(comp2.Candidates) == 0 || comp2.Candidates[0].Text != "经常化" {
		t.Fatalf("学习后「经常化」应排第一，得到 %+v", comp2.Candidates)
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
