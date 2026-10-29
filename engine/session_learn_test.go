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
	src.WriteString("你\tni\t95\n") // 单字「你」须是 ni 首选，上屏后按字查续接
	for i, tail := range tails {
		fmt.Fprintf(&src, "你%s\tni %s\t%d\n", tail, tail, 90-i)
	}
	d, err := ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	s := NewSession(d, nil)
	// 严格按字：先上屏「你」（s.prev），再按 ni 查续接
	s.Input("ni")
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
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
	// 2026-10-29 剥前缀语义：联想候选只含续接部分——测试题 → 「题」
	if assoc[0].Text != "题" {
		t.Fatalf("上屏「测试」后联想首选应是续接「题」（测试题剥前缀），实际 %q（%+v）", assoc[0].Text, assoc)
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
	if assoc[0].Text != "团" {
		t.Fatalf("上屏「策士」后联想首选应是续接「团」（策士团剥前缀），实际 %q（%+v）", assoc[0].Text, assoc)
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
	user.BumpBigram("好", "家伙")
	user.BumpBigram("好", "家伙")
	s := NewSession(d, user)
	s.prev = "好"
	out := s.Associate([]string{"hao"})
	if len(out) == 0 || out[0].Text != "家伙" {
		t.Fatalf("有 Bigram 共现的「家伙」（好家伙续接）应排首，实际首选 %+v", out[:min(3, len(out))])
	}
}

// TestAssociateShowsUserLearnedWords：用户上屏过的词（调频词条，含整句
// 学习的长句）必须出现在联想续接候选中——2026-10-28 用户反馈「之前输入
// 过的词联想的时候都没有显示」的根因就是 Associate 只查 Phrases（合成
// 词组），漏了 Words 调频表。
func TestAssociateShowsUserLearnedWords(t *testing.T) {
	d := mustCommonDict(t)
	user, err := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	if err != nil {
		t.Fatalf("NewUserDict 失败: %v", err)
	}
	s := NewSession(d, user)

	// 用户上屏「不行」多次（调频），再上屏「不行的话」作为一条整句词条
	for i := 0; i < 3; i++ {
		s.Input("buxing")
		if _, err := s.Select(0); err != nil {
			t.Fatalf("Select 失败: %v", err)
		}
	}
	s.Input("buxingdehua")
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 整句失败: %v", err)
	}

	// 新会话（模拟跨应用/重启）：上屏「不行」→ 联想应含「不行的话」
	s2 := NewSession(d, user)
	s2.Input("buxing")
	if _, err := s2.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	assoc := s2.Associate([]string{"bu", "xing"})
	found := false
	for _, c := range assoc {
		// 2026-10-29 剥前缀语义：「不行的话」的续接部分是「的话」
		if c.Text == "的话" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("用户上屏过「不行的话」，联想候选中应包含续接「的话」（共 %d 条：%+v）", len(assoc), assoc)
	}
}

// TestAssociateStripsCommittedPrefix：联想候选只含续接部分——2026-10-29
// 用户反馈：先上屏「打字测试」，之后再上屏「打字」，联想弹的是
// 「打字测试」而不是「测试」，选了会把已上屏的「打字」重复一遍。
// 正确语义：以上一个确认词为基，只返回补全部分（文本与音节都剥前缀）。
func TestAssociateStripsCommittedPrefix(t *testing.T) {
	var src strings.Builder
	src.WriteString("打字\tda zi\t80\n打字测试\tda zi ce shi\t70\n打字机\tda zi ji\t60\n")
	d, err := ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	user, err := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	if err != nil {
		t.Fatalf("NewUserDict 失败: %v", err)
	}
	s := NewSession(d, user)
	// 先上屏「打字测试」（整句学习进用户词库），再上屏「打字」
	s.Input("daziceshi")
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	s.Reset()
	s.Input("dazi")
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	assoc := s.Associate([]string{"da", "zi"})
	if len(assoc) == 0 {
		t.Fatal("应有联想候选")
	}
	for _, c := range assoc {
		if strings.HasPrefix(c.Text, "打字") {
			t.Fatalf("联想候选不应含已上屏前缀「打字」: %+v", assoc)
		}
	}
	found := false
	for _, c := range assoc {
		if c.Text == "测试" && c.Syllables == "ce'shi" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("联想应含「测试」(ce'shi)（用户词库打字测试剥前缀），实际 %+v", assoc)
	}
}

// TestAssociateChainContinues：续接词上屏后链式联想继续——上屏「打字」
// 选续接「测试」后，基变为「测试」，再联想应出「题」（测试题剥前缀）。
func TestAssociateChainContinues(t *testing.T) {
	var src strings.Builder
	src.WriteString("打字\tda zi\t80\n打字测试\tda zi ce shi\t70\n测试题\tce shi ti\t75\n")
	d, err := ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	user, _ := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	s := NewSession(d, user)
	s.Input("dazi")
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	assoc := s.Associate([]string{"da", "zi"})
	if len(assoc) == 0 || assoc[0].Text != "测试" {
		t.Fatalf("上屏「打字」后联想首选应是续接「测试」，实际 %+v", assoc)
	}
	// 模拟壳的联想选择路径：associateSelect 以剥离后的文本/音节学习并设 prev
	if _, err := s.AssociateSelect(assoc[0].Text, []string{"ce", "shi"}); err != nil {
		t.Fatalf("AssociateSelect 失败: %v", err)
	}
	assoc = s.Associate([]string{"ce", "shi"})
	if len(assoc) == 0 || assoc[0].Text != "题" {
		t.Fatalf("上屏「测试」后联想首选应是续接「题」，实际 %+v", assoc)
	}
}

// TestSelectPrefixProgressive：渐进上屏——打长串拼音后选中「今天」，
// 只消耗 jin'tian 两音节，剩余 wo'chi'le'yi'da'wan 回灌继续组词；
// 逐段渐进上屏，每段进用户词库学习（2026-10-29 用户场景）。
func TestSelectPrefixProgressive(t *testing.T) {
	var src strings.Builder
	src.WriteString("今天\tjin tian\t90\n我\two\t88\n吃\tchi\t85\n")
	src.WriteString("了\tle\t84\n一\tyi\t83\n大\tda\t82\n碗\twan\t81\n")
	src.WriteString("吃了一碗\tchi le yi wan\t70\n")
	d, err := ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	user, _ := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	s := NewSession(d, user)

	// 打长串，选「今天」（ jin'tian 词条，两音节）
	s.Input("jintianwochi leyidawan")
	comp, committed, err := s.SelectPrefix(0)
	if err != nil {
		t.Fatalf("SelectPrefix 失败: %v", err)
	}
	if committed != "今天" {
		t.Fatalf("committed 应为「今天」，实际 %q", committed)
	}
	if comp.Preedit != "wo'chi'le'yi'da'wan" {
		t.Fatalf("剩余拼音应为 wochi leyidawan，实际 %q", comp.Preedit)
	}

	// 剩余串选「吃了一碗」（词组路径或逐段）
	s.Reset()
	s.Input("chi leyidawan")
	comp, _ = s.Input("")
	if len(comp.Candidates) == 0 {
		t.Fatal("剩余串应有候选")
	}
}
