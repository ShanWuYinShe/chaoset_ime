package engine

import (
	"strings"
	"testing"
)

// fuzzyVariants：声母对（sh/s）、韵母对（an/ang）单替换。
func TestFuzzyVariantsBasic(t *testing.T) {
	got := fuzzyVariants("shi")
	want := map[string]bool{"shi": true, "si": true}
	if len(got) != len(want) {
		t.Fatalf("shi 变体 = %v, 期望 %v", got, want)
	}
	for _, v := range got {
		if !want[v] {
			t.Fatalf("shi 变体多余项 %q: %v", v, got)
		}
	}
	if got[0] != "shi" {
		t.Fatalf("变体首元素应为原音节, 得到 %v", got)
	}
}

// fuzzyVariants：声母+韵母双替换组合（zhang → zang/zhan/zan）。
func TestFuzzyVariantsCombined(t *testing.T) {
	got := map[string]bool{}
	for _, v := range fuzzyVariants("zhang") {
		got[v] = true
	}
	for _, want := range []string{"zhang", "zang", "zhan", "zan"} {
		if !got[want] {
			t.Fatalf("zhang 变体缺少 %q, 实际 %v", want, got)
		}
	}
	// 所有变体都是合法音节
	for v := range got {
		if !IsSyllable(v) {
			t.Fatalf("变体 %q 不是合法音节", v)
		}
	}
}

// fuzzyVariants：最长韵母后缀优先（liang 命中 iang 而非 ang）。
func TestFuzzyVariantsLongestSuffix(t *testing.T) {
	got := strings.Join(fuzzyVariants("liang"), ",")
	if !strings.Contains(got, "lian") {
		t.Fatalf("liang 应含变体 lian（iang→ian）, 实际 %s", got)
	}
	// ang→an 的普通替换对 liang 不再单独命中（否则会生成 "li" + an?）
	// l→n 声母替换给出 niang/nian：
	if !strings.Contains(got, "niang") || !strings.Contains(got, "nian") {
		t.Fatalf("liang 应含 n/l 声母变体 niang/nian, 实际 %s", got)
	}
}

// mustFuzzyDict 构造 n/l 与 z/zhu、an/ang 模糊验证词库。
func mustFuzzyDict(t *testing.T) *Dictionary {
	t.Helper()
	dictSrc := `
泥	ni	50
里	li	60
主	zhu	50
组	zu	60
半	ban	50
帮	bang	40
尼利	ni li	30
`
	dict, err := ParseDict(strings.NewReader(dictSrc))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	return dict
}

// hasCand 报告候选中是否包含指定词。
func hasCand(comp Composition, word string) bool {
	for _, c := range comp.Candidates {
		if c.Text == word {
			return true
		}
	}
	return false
}

// 模糊开关：关闭时精确匹配；开启后 n/l、z/zh、an/ang 互相命中。
// 两个阶段各用独立会话（Input 为追加语义，避免互相干扰）。
func TestFuzzySessionToggle(t *testing.T) {
	cases := []struct {
		input, fuzzyHit, exactHit string
	}{
		{"ni", "里", "泥"},
		{"zhu", "组", "主"},
		{"ban", "帮", "半"},
	}
	for _, tc := range cases {
		// 关闭：只有精确项
		s := NewSession(mustFuzzyDict(t), nil)
		comp, _ := s.Input(tc.input)
		if !hasCand(comp, tc.exactHit) || hasCand(comp, tc.fuzzyHit) {
			t.Fatalf("%q 模糊关闭: 期望含 %q 不含 %q, 得到 %v", tc.input, tc.exactHit, tc.fuzzyHit, comp.Candidates)
		}
		// 开启：两者都在
		s2 := NewSession(mustFuzzyDict(t), nil)
		s2.SetFuzzy(true)
		comp, _ = s2.Input(tc.input)
		if !hasCand(comp, tc.exactHit) || !hasCand(comp, tc.fuzzyHit) {
			t.Fatalf("%q 模糊开启: 期望同时含 %q 与 %q, 得到 %v", tc.input, tc.exactHit, tc.fuzzyHit, comp.Candidates)
		}
	}
}

// 多音节模糊：组合变体路径在封顶内正常命中二字词。
func TestFuzzyMultiSyllable(t *testing.T) {
	s := NewSession(mustFuzzyDict(t), nil)
	s.SetFuzzy(true)
	comp, err := s.Input("nili")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	// ni li 原路径命中「尼利」；l→n 变体 [ni ni] 无词但不得干扰
	if !hasCand(comp, "尼利") {
		t.Fatalf("多音节模糊应命中 尼利, 得到 %v", comp.Candidates)
	}
}
