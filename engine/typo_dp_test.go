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

// TestTypoPhraseDPDisabledForCorrectInput：正常输入仍不允许拼合造词
// （用户此前明确要求：词库里没有的复合词不要猜）。纠错 DP 只在错拼路径生效。
func TestTypoPhraseDPDisabledForCorrectInput(t *testing.T) {
	d := mustJoinedDict(t,
		"我们\two men\t85",
		"的\tde\t90",
		"你好\tni hao\t80",
		"吗\tma\t50",
		"长\tchang\t40",
		"输入\tshu ru\t54",
		"图\ttu\t51",
		"中\tzhong\t81",
	)
	for _, tc := range []struct{ input, banned string }{
		{"womende", "我们的"},
		{"nihaoma", "你好吗"},
		{"changshuru", "长输入"}, // 正确拼写：不纠错、不拼合
		{"tuzhong", "图中"},
	} {
		all := collectAllCandidates(t, NewSession(d, nil), tc.input)
		for _, c := range all {
			if c.Text == tc.banned {
				t.Fatalf("%s: 正常输入不得拼出「%s」（这是错拼修正路径才允许的组合）", tc.input, tc.banned)
			}
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
