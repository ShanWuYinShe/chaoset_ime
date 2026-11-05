package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// 用户历史优先（2026-11-01 用户要求补回归测试锁住）：
// 用户真正上屏过的词/单字，在冲突场景里必须胜出——静态词频更高的词典词
// 不能被顶掉。Lead 实测已满足，本文件只做双向锁定；若不满足再最小修复。
//
// 覆盖：冲突词（知到 vs 知道）、冲突单字（石 vs 是）、简拼（zg）、
// 混拼（wmd，用户词「我们的」由渐进上屏真实学习得到）。

func TestUserHistoryPriorityConflictWord(t *testing.T) {
	d := mustCommonDict(t)
	// 对照组：未学习时 zhidao 首选静态词频更高的「知道」。
	base := NewSession(d, nil)
	comp := mustComp(base, "zhidao")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "知道" {
		t.Fatalf("未学习时 zhidao 首选应为「知道」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}

	u := mustUserDict(t, filepath.Join(t.TempDir(), "u.json"))
	if err := u.BumpWord("知到", []string{"zhi", "dao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	s := NewSession(d, u)
	comp = mustComp(s, "zhidao")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "知到" || !comp.Candidates[0].FromUser {
		t.Fatalf("学过「知到」后 zhidao 首选应为带 FromUser 的「知到」，实际 %+v",
			comp.Candidates[:min(3, len(comp.Candidates))])
	}
}

func TestUserHistoryPriorityConflictChar(t *testing.T) {
	d := mustCommonDict(t)
	base := NewSession(d, nil)
	comp := mustComp(base, "shi")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "是" {
		t.Fatalf("未学习时 shi 首选应为「是」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}

	u := mustUserDict(t, filepath.Join(t.TempDir(), "u.json"))
	if err := u.BumpWord("石", []string{"shi"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	s := NewSession(d, u)
	comp = mustComp(s, "shi")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "石" || !comp.Candidates[0].FromUser {
		t.Fatalf("学过「石」后 shi 首选应为带 FromUser 的「石」，实际 %+v",
			comp.Candidates[:min(3, len(comp.Candidates))])
	}
}

func TestUserHistoryPriorityInitials(t *testing.T) {
	d := mustCommonDict(t)
	base := NewSession(d, nil)
	comp := mustComp(base, "zg")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "中国" {
		t.Fatalf("未学习时 zg 首选应为词频最高的「中国」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}

	u := mustUserDict(t, filepath.Join(t.TempDir(), "u.json"))
	if err := u.BumpWord("这个", []string{"zhe", "ge"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	s := NewSession(d, u)
	comp = mustComp(s, "zg")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "这个" || !comp.Candidates[0].FromUser {
		t.Fatalf("学过「这个」后 zg 首选应为带 FromUser 的「这个」，实际 %+v",
			comp.Candidates[:min(3, len(comp.Candidates))])
	}
}

// TestUserHistoryPriorityMixed：womende 逐段渐进上屏（我们 + 的）后，
// 学习记录写出带分段的用户词「我们的」；之后简拼/混拼 wmd 里用户确认过的
// 「我们的」必须压过词典里完整匹配简拼序列的低频词（无目地）。
func TestUserHistoryPriorityMixed(t *testing.T) {
	d := mustCommonDict(t)
	u := mustUserDict(t, filepath.Join(t.TempDir(), "u.json"))

	learn := NewSession(d, u)
	if _, err := learn.Input("womende", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, _, err := learn.SelectPrefix(findCandidate(t, learn, "我们")); err != nil {
		t.Fatalf("渐进选「我们」失败: %v", err)
	}
	if _, _, err := learn.SelectPrefix(findCandidate(t, learn, "的")); err != nil {
		t.Fatalf("渐进选「的」失败: %v", err)
	}
	if got := learn.Compose().Input; got != "" {
		t.Fatalf("逐段拼完后组词应为空，实际 %q", got)
	}
	// 学习记录：整句「我们的」带分段 [我们 的]（Words 表）
	rec := u.file.Words[userKey("我们的", []string{"wo", "men", "de"})]
	if rec == nil || len(rec.Segs) != 2 || rec.Segs[0] != "我们" || rec.Segs[1] != "的" {
		t.Fatalf("逐段上屏应写入带分段的「我们的」学习记录，实际 %+v", rec)
	}

	s := NewSession(d, u)
	comp := mustComp(s, "wmd")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "我们的" || !comp.Candidates[0].FromUser {
		t.Fatalf("学过「我们的」后 wmd 首选应为带 FromUser 的「我们的」，实际 %+v",
			comp.Candidates[:min(3, len(comp.Candidates))])
	}
}

// associateSourceExists 判断联想候选是否有真实出处：词典词条 prev+候选
// （音节也拼得上），或用户词库里的同一条记录。联想是「剥掉已上屏前缀」的
// 续接展示，所以校验要按完整词（prev + 增量）做。
func associateSourceExists(d *Dictionary, u *UserDict, prev string, prevSylls []string, c Candidate) bool {
	sylls := strings.Split(c.Syllables, "'")
	full := append(append([]string(nil), prevSylls...), sylls...)
	if d.Contains(prev+c.Text, full) {
		return true
	}
	if u == nil {
		return false
	}
	e := &Entry{Word: prev + c.Text, Syllables: full}
	if got := u.QueryPhrasesByWordPrefix(prev); hasEntryWithWordAndSylls(got, e) {
		return true
	}
	return hasEntryWithWordAndSylls(u.QueryWordsByWordPrefix(prev), e)
}

func hasEntryWithWordAndSylls(entries []*Entry, want *Entry) bool {
	for _, e := range entries {
		if e.Word != want.Word || len(e.Syllables) != len(want.Syllables) {
			continue
		}
		same := true
		for i := range e.Syllables {
			if e.Syllables[i] != want.Syllables[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// TestAssociateCandidatesNoComposition：联想候选只允许来自词典词条或用户
// 词库记录，不允许任何拼接造词（2026-11-01 用户要求补回归）。
func TestAssociateCandidatesNoComposition(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	if _, err := s.Input("hao", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	assoc := s.Associate([]string{"hao"})
	if len(assoc) == 0 {
		t.Fatalf("上屏「好」后应有联想候选")
	}
	for _, c := range assoc {
		if !associateSourceExists(d, nil, "好", []string{"hao"}, c) {
			t.Errorf("联想候选 %q（%s）没有词典出处——联想不得拼合造词", c.Text, c.Syllables)
		}
	}
}

// TestAssociateUserLearnedTailIsAllowed：用户词库是联想的合法来源——
// 用户学过的「打滴」不在词典里，但联想给出续接「滴」必须被接受。
func TestAssociateUserLearnedTailIsAllowed(t *testing.T) {
	d, err := ParseDict(strings.NewReader("打\tda\t60\n车\tche\t50\n"))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	u := mustUserDict(t, filepath.Join(t.TempDir(), "u.json"))
	if err := u.BumpWord("打滴", []string{"da", "di"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	s := NewSession(d, u)
	if _, err := s.Input("da", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	assoc := s.Associate([]string{"da"})
	if len(assoc) == 0 {
		t.Fatalf("上屏「打」后应有联想候选")
	}
	foundTail := false
	for _, c := range assoc {
		if c.Text == "滴" {
			foundTail = true
		}
		if !associateSourceExists(d, u, "打", []string{"da"}, c) {
			t.Errorf("联想候选 %q（%s）既不在词典也不是用户词库记录", c.Text, c.Syllables)
		}
	}
	if !foundTail {
		t.Fatalf("用户学过的「打滴」应在联想中给出续接「滴」，实际 %+v", assoc)
	}
}
