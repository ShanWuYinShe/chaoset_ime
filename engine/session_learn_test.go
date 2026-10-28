package engine

import (
	"fmt"
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

// TestAssociatePagingCap：联想上限翻页化为 30 条（壳本地分页展示），
// 返回仍按得分降序——条数上限与排序互不影响。
func TestAssociatePagingCap(t *testing.T) {
	// 40 个「ni + 合法音节」的续接词条（第二音节从音节表取样）
	tails := []string{"hao", "men", "de", "shuo", "kan", "xiang", "yao", "neng", "hui", "qu", "lai", "zuo", "xie", "gei", "zhao", "wen", "ting", "jiao", "dai", "ban", "pan", "wan", "tan", "man", "zhan", "guan", "chuan", "zhuan", "duan", "ruan", "suan", "nuan", "luan", "kuan", "huan", "tuan", "duo", "hai", "hei", "hun"}
	var src strings.Builder
	for i, tail := range tails {
		fmt.Fprintf(&src, "你%s\tni %s\t%d\n", tail, tail, 90-i)
	}
	d, err := ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	s := NewSession(d, nil)
	assoc := s.Associate([]string{"ni"})
	if len(assoc) != maxAssociate {
		t.Fatalf("联想应返回上限 %d 条，实际 %d", maxAssociate, len(assoc))
	}
	for i := 1; i < len(assoc); i++ {
		if assoc[i-1].Score < assoc[i].Score {
			t.Fatalf("联想候选应按得分降序，第 %d/%d 项逆序", i, i+1)
		}
	}
}

// TestAssociateFollowsWord：联想续接按**上屏词的字**走——同音不同字
// （测试/策士）上屏后的续接候选应当不同（2026-10-28 用户反馈）。
// 同时覆盖 Bigram 共现排序（打过的「词 + 续接」组合排最前）。
func TestAssociateFollowsWord(t *testing.T) {
	var src strings.Builder
	src.WriteString("测试\tce shi\t80\n测试题\tce shi ti\t70\n测试员\tce shi yuan\t60\n")
	src.WriteString("策士\tce shi\t75\n策士团\tce shi tuan\t50\n")
	d, err := ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	user, _ := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	s := NewSession(d, user)

	// 选「测试」上屏 → 联想首条应是「测试」开头的续接词（测试题），
	// 而非同音节的「策士团」
	s.Input("ceshi")
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	assoc := s.Associate([]string{"ce", "shi"})
	if len(assoc) == 0 {
		t.Fatal("应有联想候选")
	}
	if !strings.HasPrefix(assoc[0].Text, "测试") {
		t.Fatalf("上屏「测试」后联想首选应是「测试」开头，实际 %q（%+v）", assoc[0].Text, assoc)
	}

	// 选「策士」上屏 → 联想首条应是「策士」开头（策士团）——同音节
	// 不同字，续接跟着字走
	s.Reset()
	s.Input("ceshi")
	for i, c := range s.Compose().Candidates {
		if c.Text == "策士" {
			if _, err := s.Select(i); err != nil {
				t.Fatalf("Select 策士失败: %v", err)
			}
			break
		}
		_ = i
	}
	assoc = s.Associate([]string{"ce", "shi"})
	if len(assoc) == 0 {
		t.Fatal("应有联想候选")
	}
	if !strings.HasPrefix(assoc[0].Text, "策士") {
		t.Fatalf("上屏「策士」后联想首选应是「策士」开头，实际 %q（%+v）", assoc[0].Text, assoc)
	}
}

// TestAssociateBigramBoostWithWordPrefix：Bigram 共现加成必须在字前缀
// 分层下仍然生效（「好家伙」freq 仅 41、被 30 个「好」开头词条包夹，
// 共现加成后的 500+ 分必须把它顶到首位——收集上限放宽前它被内部截断
// 排 31+，2026-10-28 探针实测）。
func TestAssociateBigramBoostWithWordPrefix(t *testing.T) {
	d := mustCommonDict(t)
	user, err := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	if err != nil {
		t.Fatalf("NewUserDict 失败: %v", err)
	}
	user.BumpBigram("好", "好家伙")
	user.BumpBigram("好", "好家伙")
	s := NewSession(d, user)
	s.prev = "好"
	out := s.Associate([]string{"hao"})
	if len(out) == 0 || out[0].Text != "好家伙" {
		t.Fatalf("有 Bigram 共现的「好家伙」应排首，实际首选 %+v", out[:min(3, len(out))])
	}
}
