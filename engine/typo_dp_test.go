package engine

import (
	"strings"
	"testing"
)

// 错拼修正后的分词 DP 回归（2026-11-01 用户要求）：
//   chagnshuru（多打/颠倒）→ 期望「长输入」= 长 + 输入
//   tuzhogng（多打一个 g）→ 期望「图中」= 图 + 中
// 同时锁定边界：正常（未打错）输入仍不拼合造词，单字始终保留。

func TestTypoPhraseDPComposesLongInput(t *testing.T) {
	d := mustJoinedDict(t,
		"差\tcha\t66",
		"茶\tcha\t50",
		"馆\tguan\t45",
		"长\tchang\t40",
		"常\tchang\t57",
		"输入\tshu ru\t54",
	)
	all := collectAllCandidates(t, NewSession(d, nil), "chagnshuru")
	idx := -1
	var got *Candidate
	for i := range all {
		if all[i].Text == "长输入" {
			idx = i
			got = &all[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("chagnshuru 纠错后应给出「长输入」候选，实际 %+v", all)
	}
	if len(got.Segments) != 2 || got.Segments[0] != "长" || got.Segments[1] != "输入" {
		t.Fatalf("「长输入」应是 长+输入 的分段候选，实际 Segments=%v", got.Segments)
	}
	if !got.typoFix {
		t.Fatalf("纠错拼出的「长输入」必须带 typoFix 标记（选中后整串消费），实际 %+v", *got)
	}
	if idx > 5 {
		t.Fatalf("「长输入」应在前 6 个候选内（快速可及），实际第 %d 个：%+v", idx+1, all)
	}
}

func TestTypoPhraseDPComposesTuZhong(t *testing.T) {
	d := mustJoinedDict(t,
		"图\ttu\t51",
		"途\ttu\t38",
		"土\ttu\t49",
		"中\tzhong\t81",
		"钟\tzhong\t43",
		"种\tzhong\t65",
	)
	all := collectAllCandidates(t, NewSession(d, nil), "tuzhogng")
	var got *Candidate
	for i := range all {
		if all[i].Text == "图中" {
			got = &all[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("tuzhogng 纠错后应给出「图中」候选（图+中），实际 %+v", all)
	}
	if len(got.Segments) != 2 || got.Segments[0] != "图" || got.Segments[1] != "中" {
		t.Fatalf("「图中」应是 图+中 的分段候选，实际 Segments=%v", got.Segments)
	}
	if got.Syllables != "tu'zhong" {
		t.Fatalf("「图中」音节应为 tu'zhong，实际 %q", got.Syllables)
	}
}

// TestTypoPhraseDPSelectionConsumesWholeInput：选中纠错词组后应整串消费，
// 不能把修正掉的碎片留在输入框里。
func TestTypoPhraseDPSelectionConsumesWholeInput(t *testing.T) {
	d := mustJoinedDict(t,
		"差\tcha\t66",
		"长\tchang\t40",
		"常\tchang\t57",
		"输入\tshu ru\t54",
	)
	s := NewSession(d, nil)
	comp := mustComp(s, "chagnshuru")
	idx := -1
	for i, c := range comp.Candidates {
		if c.Text == "长输入" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("首页应能直接看到「长输入」，实际 %+v", comp.Candidates)
	}
	after, committed, err := s.SelectPrefix(idx)
	if err != nil {
		t.Fatalf("选中失败: %v", err)
	}
	if committed != "长输入" {
		t.Fatalf("上屏应为「长输入」，实际 %q", committed)
	}
	if s.input != "" {
		t.Fatalf("整串消费后输入应为空，实际 %q", s.input)
	}
	if after.Input != "" {
		t.Fatalf("选中后组合态输入应为空，实际 %q", after.Input)
	}
}

// TestCorrectInputCompositionsStayLowPriority：正常（未打错）输入也会给出
// 「已有词组合」候选（2026-11-01 用户确认放开），但只能出现在列表末尾——
// 真实词条必须保持首选，组合不得挤进首屏；组合每一段都必须是词库已有词。
func TestCorrectInputCompositionsStayLowPriority(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input    string
		composed []string
	}{
		{"womende", []string{"我们的"}},
		{"nihaoma", []string{"你好吗"}},
		{"changshuru", []string{"长输入"}},
		{"tuzhong", []string{"图中"}},
	}
	for _, tc := range cases {
		all := collectAllCandidates(t, NewSession(d, nil), tc.input)
		if len(all) == 0 {
			t.Fatalf("%s: 应有候选", tc.input)
		}
		for _, word := range tc.composed {
			for i, c := range all {
				if c.Text != word {
					continue
				}
				if i < pageSize {
					t.Fatalf("%s: 组合候选 %q 出现在首屏第 %d 位——真实词条必须优先", tc.input, word, i+1)
				}
				if len(c.Segments) < 2 || !allSegmentsInDict(d, c.Segments) {
					t.Fatalf("%s: 组合候选 %q 的分段 %v 不全是词库已有词", tc.input, word, c.Segments)
				}
			}
		}
	}
}

// TestCorrectInputCompositionsExist：用户确认「正确拼写也允许组合候选」后，
// 这些组合必须真的出现在候选里（只在列表末尾），防止后续重构时被整体删掉。
func TestCorrectInputCompositionsExist(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct{ input, word string }{
		{"womende", "我们的"},
		{"changshuru", "长输入"},
		{"tuzhong", "图中"},
	}
	for _, tc := range cases {
		all := collectAllCandidates(t, NewSession(d, nil), tc.input)
		found := false
		for _, c := range all {
			if c.Text != tc.word {
				continue
			}
			found = true
			if len(c.Segments) < 2 || !allSegmentsInDict(d, c.Segments) {
				t.Fatalf("%s: 组合候选 %q 的分段 %v 不全是词库已有词", tc.input, tc.word, c.Segments)
			}
		}
		if !found {
			t.Fatalf("%s: 正确输入应给出已有词组合候选「%s」（用户确认放开）", tc.input, tc.word)
		}
	}
}

// TestTypoPhraseDPSegmentsAllExist：纠错拼出的每一段都必须是词库里真实存在的词。
func TestTypoPhraseDPSegmentsAllExist(t *testing.T) {
	d := mustJoinedDict(t,
		"差\tcha\t66",
		"长\tchang\t40",
		"常\tchang\t57",
		"输入\tshu ru\t54",
		"图\ttu\t51",
		"中\tzhong\t81",
	)
	for _, input := range []string{"chagnshuru", "tuzhogng"} {
		all := collectAllCandidates(t, NewSession(d, nil), input)
		hasPhrase := false
		for _, c := range all {
			if len(c.Segments) >= 2 {
				hasPhrase = true
				for _, seg := range c.Segments {
					if !dictHasAny(d, seg) {
						t.Fatalf("%s: 纠错组合候选 %q 的分段 %q 不在词库中", input, c.Text, seg)
					}
				}
			}
		}
		if !hasPhrase {
			t.Fatalf("%s: 应有纠错组合候选（分段 ≥2）", input)
		}
	}
}

// TestTypoPhraseDPKeepsSingleChars：纠错候选不得妨碍单字逐个匹配。
func TestTypoPhraseDPKeepsSingleChars(t *testing.T) {
	d := mustJoinedDict(t,
		"差\tcha\t66",
		"长\tchang\t40",
		"常\tchang\t57",
		"输入\tshu ru\t54",
	)
	all := collectAllCandidates(t, NewSession(d, nil), "chagnshuru")
	singles := 0
	for _, c := range all {
		if len([]rune(c.Text)) == 1 {
			singles++
		}
	}
	if singles == 0 {
		t.Fatalf("chagnshuru 候选里必须保留单字（逐字挑选路径），实际 %+v", all)
	}
}

func dictHasAny(d *Dictionary, word string) bool {
	for _, e := range d.Entries() {
		if strings.EqualFold(e.Word, word) {
			return true
		}
	}
	return false
}
