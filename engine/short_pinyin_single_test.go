package engine

import (
	"path/filepath"
	"testing"
)

// 单字兜底：任何多段输入都在列表末尾补首段全量单字（上限 singleSyllableExactLimit），
// 让用户任何时候都能逐字挑选（shipei 想打「适配」这类词库没有的词）。
// 整词/组合候选仍在前面，单字块收尾。

func TestShortPinyinSingleCharBackfill(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	all := allCandidates(s, mustComp(s, "shipei"))
	if len(all) == 0 || all[0].Text != "失陪" {
		t.Fatalf("shipei 首选应保持「失陪」（不改变原有首选），实际: %+v", firstTexts(all, 3))
	}
	found := -1
	for i, c := range all {
		if c.Text == "适" {
			found = i
			break
		}
	}
	if found < 0 {
		t.Fatalf("shipei 候选里应有首音节单字「适」（词频 30，同音字排 23 位），实际候选数 %d", len(all))
	}
	if found < 10 {
		t.Fatalf("「适」位次 %d 太靠前，未覆盖 top10 截断场景（候选数 %d）", found+1, len(all))
	}
	// 首音节同音字应接近全量（top10 之外的字必须可达）
	dictSingles := len(d.QueryTokensExact([]Token{{Text: "shi", IsExact: true}}, singleSyllableExactLimit))
	singles := 0
	for _, c := range all {
		if c.Syllables == "shi" {
			singles++
		}
	}
	if singles < dictSingles {
		t.Errorf("首音节单字召回 %d，应达到词典全量 %d（单字兜底）", singles, dictSingles)
	}
}

// TestShortPinyinSingleCharBackfillProgressiveSelection：选「适」后剩余 pei，
// 候选里应有「配」，完成 shipei → 适配 的逐字上屏。
func TestShortPinyinSingleCharBackfillProgressiveSelection(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	comp := mustComp(s, "shipei")
	for {
		idx := -1
		for i, c := range comp.Candidates {
			if c.Text == "适" {
				idx = i
				break
			}
		}
		if idx >= 0 {
			nextComp, committed, err := s.SelectPrefix(idx)
			if err != nil {
				t.Fatalf("选「适」失败: %v", err)
			}
			if committed != "适" {
				t.Fatalf("SelectPrefix 上屏 = %q, 期望「适」", committed)
			}
			if nextComp.Input != "pei" {
				t.Fatalf("选「适」后剩余输入 = %q, 期望 pei", nextComp.Input)
			}
			all := allCandidates(s, nextComp)
			found := false
			for _, c := range all {
				if c.Text == "配" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("剩余 pei 候选里应有「配」，实际: %+v", firstTexts(all, 8))
			}
			return
		}
		if comp.Page >= comp.PageCount-1 {
			break
		}
		next, err := s.NextPage()
		if err != nil {
			t.Fatalf("翻页失败: %v", err)
		}
		comp = next
	}
	t.Fatalf("shipei 候选里找不到「适」")
}

// TestStrongWordKeepsTopWithSingleFallback：有常用整词候选时首选仍是整词，
// 但列表末尾同样追加首音节全量单字（任何时候都能逐字挑选）。
func TestStrongWordKeepsTopWithSingleFallback(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input string
		word  string
	}{
		{"nihao", "你好"},      // 52
		{"shijie", "世界"},     // 73
		{"changjiang", "长江"}, // 48
		{"jisuanji", "计算机"},  // 54
	}
	for _, c := range cases {
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, c.input))
		if len(all) == 0 || all[0].Text != c.word {
			t.Fatalf("%s: 首选应保持「%s」，实际: %+v", c.input, c.word, firstTexts(all, 3))
		}
		firstSyl := all[0].Syllables
		if i := indexByte(firstSyl, '\''); i >= 0 {
			firstSyl = firstSyl[:i]
		}
		singles := 0
		for _, cand := range all {
			if cand.Syllables == firstSyl {
				singles++
			}
		}
		want := len(d.QueryTokensExact([]Token{{Text: firstSyl, IsExact: true}}, 500))
		if want > singleSyllableExactLimit {
			want = singleSyllableExactLimit
		}
		if singles < want {
			t.Errorf("%s: 首音节单字 %d，应至少达到 %d（单字兜底始终可用，上限 %d）",
				c.input, singles, want, singleSyllableExactLimit)
		}
	}
}

func firstTexts(cands []Candidate, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n && i < len(cands); i++ {
		out = append(out, cands[i].Text)
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// TestShortPinyinSingleCharBackfillIgnoresUserBoost：单字兜底不受用户学习加权
// 影响，学过「失陪」后仍能选到首音节单字（用户仍可能想打「适配」）。
func TestShortPinyinSingleCharBackfillIgnoresUserBoost(t *testing.T) {
	d := mustCommonDict(t)
	user, err := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	if err != nil {
		t.Fatalf("构造用户词库失败: %v", err)
	}
	if err := user.BumpWord("失陪", []string{"shi", "pei"}); err != nil {
		t.Fatalf("学习「失陪」失败: %v", err)
	}
	s := NewSession(d, user)
	all := allCandidates(s, mustComp(s, "shipei"))
	found := false
	for _, c := range all {
		if c.Text == "适" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("学习「失陪」后仍应补首音节单字「适」（单字兜底不受学习加权影响，候选数 %d）", len(all))
	}
}

// TestShortPinyinSingleCharTailIsPureSingles：单字兜底补全后，列表最末一段必须
// 是纯单字（组合候选 是配/是培… 不得再出现在单字块之后）。
func TestShortPinyinSingleCharTailIsPureSingles(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	all := allCandidates(s, mustComp(s, "shipei"))
	tailSingles := 0
	for i := len(all) - 1; i >= 0; i-- {
		if len([]rune(all[i].Text)) != 1 {
			break
		}
		tailSingles++
	}
	if tailSingles < 50 {
		start := len(all) - 3
		if start < 0 {
			start = 0
		}
		t.Fatalf("shipei 列表末尾连续单字只有 %d 个，期望单字块收尾（候选数 %d，尾部: %+v）",
			tailSingles, len(all), firstTexts(all[start:], 3))
	}
}
