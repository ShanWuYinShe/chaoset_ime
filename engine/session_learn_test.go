package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// 词组进化测试词库：故意不放「单位」，让学习环节自己造出来。
const evoDict = `
单	dan	60
位	wei	60
满	man	50
意	yi	50
满意	man yi	70
实施	shi shi	60
输入法	shu ru fa	70
`

func mustEvoDict(t *testing.T) *Dictionary {
	t.Helper()
	d, err := ParseDict(strings.NewReader(evoDict))
	if err != nil {
		t.Fatalf("解析词库失败: %v", err)
	}
	return d
}

func indexOfCandidate(t *testing.T, comp Composition, text string) int {
	t.Helper()
	for i, c := range comp.Candidates {
		if c.Text == text {
			return i
		}
	}
	t.Fatalf("候选中没有 %q: %+v", text, comp.Candidates)
	return -1
}

// 词组进化主流程：分字打「单位」两轮后固化为词组，第三轮整词直接出。
func TestPhraseEvolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	d := mustEvoDict(t)

	// 第一轮：分字上屏 单→位，共现 1 次，尚不固化
	s1 := NewSession(d, mustUserDict(t, path))
	s1.Select(indexOfCandidate(t, mustInput(t, s1, "dan"), "单"))
	s1.Select(indexOfCandidate(t, mustInput(t, s1, "wei"), "位"))
	u1 := mustUserDict(t, path)
	if got := u1.PhraseCount("单位", []string{"dan", "wei"}); got != 0 {
		t.Fatalf("共现 1 次不应固化词组，得到 %d", got)
	}

	// 第二轮：共现 2 次 → 固化
	s2 := NewSession(d, u1)
	s2.Select(indexOfCandidate(t, mustInput(t, s2, "dan"), "单"))
	s2.Select(indexOfCandidate(t, mustInput(t, s2, "wei"), "位"))
	u2 := mustUserDict(t, path)
	if got := u2.PhraseCount("单位", []string{"dan", "wei"}); got != 1 {
		t.Fatalf("共现 2 次应固化词组，得到 %d", got)
	}

	// 第三轮：直接输入 danwei，「单位」应作为整词排第一
	s3 := NewSession(d, u2)
	comp := mustInput(t, s3, "danwei")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "单位" || !comp.Candidates[0].FromUser {
		t.Fatalf("词组进化后 danwei 第一候选应为「单位」，得到 %+v", comp.Candidates)
	}
}

// 主词典已有的词组不应重复学习（避免用户数据膨胀）。
func TestPhraseNotLearnedWhenStaticExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	d := mustEvoDict(t)
	s := NewSession(d, mustUserDict(t, path))
	for i := 0; i < 2; i++ {
		s.Select(indexOfCandidate(t, mustInput(t, s, "man"), "满"))
		s.Select(indexOfCandidate(t, mustInput(t, s, "yi"), "意"))
	}
	u := mustUserDict(t, path)
	if got := u.PairCount("满", "意"); got != 2 {
		t.Fatalf("共现应记录为 2，得到 %d", got)
	}
	if got := u.PhraseCount("满意", []string{"man", "yi"}); got != 0 {
		t.Fatalf("主词典已有的词不应固化，得到 %d", got)
	}
	// 已有的「满意」依然在候选首位（从未整词上屏过，暂无用户加权）
	comp := mustInput(t, s, "manyi")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "满意" {
		t.Fatalf("manyi 第一候选应为「满意」，得到 %+v", comp.Candidates)
	}
}

// 超长组合（音节 > 4）不固化，避免学出无意义长串。
func TestPhraseSyllableCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	d := mustEvoDict(t)
	s := NewSession(d, mustUserDict(t, path))
	for i := 0; i < 2; i++ {
		s.Select(indexOfCandidate(t, mustInput(t, s, "shishi"), "实施"))
		s.Select(indexOfCandidate(t, mustInput(t, s, "shurufa"), "输入法"))
	}
	u := mustUserDict(t, path)
	if got := u.PhraseCount("实施输入法", []string{"shi", "shi", "shu", "ru", "fa"}); got != 0 {
		t.Fatalf("超过 4 音节的组合不应固化，得到 %d", got)
	}
}

// 上屏后清空（Reset）应同时清掉学习上下文。
func TestResetClearsContext(t *testing.T) {
	d := mustEvoDict(t)
	u := mustUserDict(t, "")
	s := NewSession(d, u)
	s.Select(indexOfCandidate(t, mustInput(t, s, "dan"), "单"))
	s.Reset()
	if s.prev != "" || len(s.history) != 0 {
		t.Fatalf("Reset 后学习上下文应清空，prev=%q history=%d", s.prev, len(s.history))
	}
}

func mustInput(t *testing.T, s *Session, text string) Composition {
	t.Helper()
	comp, err := s.Input(text)
	if err != nil {
		t.Fatalf("Input(%q) 失败: %v", text, err)
	}
	return comp
}
