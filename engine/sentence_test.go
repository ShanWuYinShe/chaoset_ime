package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// 多音节输入候选的边界（2026-11-01 用户要求）：引擎**不再**用 DP 把词典里的
// 片段拼成并不存在的复合词（nihaoma → 你号码、womende → 我们的、
// zhonghuarenmingongheguo → 中华人民共和国、woxiangqubeijingdaxue →
// 我相去北京大学）。候选只允许来自「已存在的词」：
//   - 词典词条（含前缀续接、末段续接、逐级回退得到的前缀词）；
//   - 用户词库与学习记录（用户真正输入/确认过的整句分段记录）；
//   - 没有词组时回退到首音节单字，靠 SelectPrefix 逐字/逐段挑选。
//
// 本文件锁定这条边界，双向覆盖：正向证明长输入能给出「最长已有词前缀」并
// 可逐段拼出整句；反向证明所有候选都能在词典里找到出处、不再拼接造词。

// assertAllCandidatesInDict 断言候选全部能在主词典找到出处。
// 无用户词库时候选只能来自词典，任何拼合造词（DP 拼接）都会在这里暴露。
func assertAllCandidatesInDict(t *testing.T, d *Dictionary, input string, all []Candidate) {
	t.Helper()
	for _, c := range all {
		sylls := strings.Split(c.Syllables, "'")
		if d.Contains(c.Text, sylls) {
			continue
		}
		// 错拼修正的组合候选（2026-11-01 用户要求）：只允许「已有词的序列」，
		// 每个分段都必须在词典里真实存在——引擎仍然不造词，只是把修正后的
		// 音节按已有词切开。正常（非错拼）输入不允许出现这类候选。
		if c.typoFix && len(c.Segments) >= 2 && allSegmentsInDict(d, c.Segments) {
			continue
		}
		t.Errorf("%s: 候选 %q（%s）不在词典中——引擎不得拼合造词", input, c.Text, c.Syllables)
	}
}

// allSegmentsInDict 报告每个分段是否都能在词典里找到（任一读音即可）。
func allSegmentsInDict(d *Dictionary, segs []string) bool {
	for _, seg := range segs {
		found := false
		for _, e := range d.Entries() {
			if e.Word == seg {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestComposeCandidatesOnlyFromDict：常见长输入与短输入的**全部**候选都必须在
// 内置词库里存在（无用户词库时）。这是「禁止 DP 拼合造词」的总闸门。
func TestComposeCandidatesOnlyFromDict(t *testing.T) {
	d := mustCommonDict(t)
	inputs := []string{
		// 用户实际反馈过的长输入（此前首选全是 DP 造词）
		"nihaoma", "womende", "chifanle", "kanyixia", "diannaoshang",
		"tadeche", "zaima", "wolaile", "jintiantianqizhenhao",
		"wobuxiangquchifan", "zhonghuarenmingongheguo",
		"woxiangqubeijingdaxue",
		// 简拼 / 混拼形态
		"wmd", "womd", "chifanl", "zg", "bj", "jsj", "zhongg",
		// 短输入（回归守门：造词禁令不得把正常候选也砍掉）
		"nihao", "jingchang", "shijie", "shi", "ba", "zhongguo", "xian",
	}
	s := NewSession(d, nil)
	for _, in := range inputs {
		s.Reset()
		comp, err := s.Input(in)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", in, err)
		}
		all := allCandidates(s, comp)
		if len(all) == 0 {
			t.Errorf("%s: 应至少给出逐字兜底候选，实际无候选", in)
			continue
		}
		assertAllCandidatesInDict(t, d, in, all)
	}
}

// TestNoComposedSentenceCandidates：命名锁定用户反馈的具体造词不再出现。
// 同时断言首选是「最长已有词前缀」——多音节长输入命中前缀词时由它领跑，
// 词库里没有前缀词组时退回首音节单字（woxiangqubeijingdaxue 的「我」）。
func TestNoComposedSentenceCandidates(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	cases := []struct {
		input     string
		wantFirst string // 期望首选（最长已有词前缀；无前缀词时为首音节单字）
		banned    []string
	}{
		{"nihaoma", "你好", []string{"你号码", "你好吗", "你好马", "尼号码"}},
		{"womende", "我们", []string{"我们的", "我们得", "我们德"}},
		{"chifanle", "吃饭", []string{"吃饭了", "吃饭乐", "吃饭勒"}},
		{"kanyixia", "看", []string{"看一下", "看以下", "喊一下"}},
		{"diannaoshang", "电脑", []string{"电脑上", "电脑商", "电脑伤"}},
		{"zhonghuarenmingongheguo", "中华", []string{"中华人民共和国", "中华人民共和", "种花人民共和国"}},
		{"woxiangqubeijingdaxue", "我", []string{"我相去北京大学", "我相去北京大", "握相去北京大学"}},
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		all := allCandidates(s, comp)
		if len(all) == 0 {
			t.Fatalf("%s: 应有候选", c.input)
		}
		if got := all[0].Text; got != c.wantFirst {
			t.Errorf("%s: 首选 = %q, 期望「%s」（候选: %+v）", c.input, got, c.wantFirst, all[:min(6, len(all))])
		}
		for _, banned := range c.banned {
			for _, cand := range all {
				if cand.Text == banned {
					t.Errorf("%s: 候选不应包含拼接造词 %q（候选: %+v）", c.input, banned, all[:min(6, len(all))])
				}
			}
		}
	}
}

// TestLongInputLongestExistingPrefixFirst：长输入候选按「最长已有词前缀 →
// 更短前缀 → 首音节单字」排序，保证逐字挑选时首选就是最有信息量的已有词组。
func TestLongInputLongestExistingPrefixFirst(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input     string
		wantFirst string
		wantLater string // 必须排在 wantFirst 之后（首音节单字），空 = 不检查
	}{
		{"womende", "我们", "我"},
		{"chifanle", "吃饭", "吃"},
		{"diannaoshang", "电脑", "点"},
		{"jintiantianqizhenhao", "今天", "进"},
		{"zhonghuarenmingongheguo", "中华", "中"},
		{"changjiangdadaodexingqi", "长江", "长"},
		// 整串没有任何多音节前缀词组：回到首音节单字兜底（用户明确要的降级）
		{"woxiangqubeijingdaxue", "我", ""},
	}
	for _, c := range cases {
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, c.input))
		if len(all) == 0 {
			t.Errorf("%s: 应有候选", c.input)
			continue
		}
		if got := all[0].Text; got != c.wantFirst {
			t.Errorf("%s: 首选 = %q, 期望「%s」（候选: %+v）", c.input, got, c.wantFirst, all[:min(6, len(all))])
			continue
		}
		if c.wantLater == "" {
			continue
		}
		pos := -1
		for i, cand := range all {
			if cand.Text == c.wantLater {
				pos = i
				break
			}
		}
		if pos < 0 {
			t.Errorf("%s: 候选里找不到首音节单字「%s」", c.input, c.wantLater)
		} else if pos == 0 {
			t.Errorf("%s: 首音节单字「%s」不应排在已有前缀词「%s」之前", c.input, c.wantLater, c.wantFirst)
		}
	}
}

// TestProgressiveSelectionReconstructsLongInput：长输入没有整词候选时，
// 用户按「最长已有词前缀」逐段 SelectPrefix，就能把整串拼出来。
// 这替代了以前 DP 直接给出「中华人民共和国」的一步到位路径。
func TestProgressiveSelectionReconstructsLongInput(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input string
		segs  []string
	}{
		{"zhonghuarenmingongheguo", []string{"中华", "人民", "共和国"}},
		{"woxiangqubeijingdaxue", []string{"我", "想", "去", "北京大学"}},
	}
	for _, c := range cases {
		s := NewSession(d, nil)
		if _, err := s.Input(c.input); err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		var got []string
		for _, seg := range c.segs {
			idx := findCandidate(t, s, seg)
			_, committed, err := s.SelectPrefix(idx)
			if err != nil {
				t.Fatalf("%s: 渐进上屏「%s」失败: %v", c.input, seg, err)
			}
			if committed != seg {
				t.Fatalf("%s: 渐进上屏得到 %q, 期望「%s」", c.input, committed, seg)
			}
			got = append(got, committed)
			// 每段都必须是「已有词」：词典里能查到该词条
			if !d.Contains(committed, entrySylls(t, d, committed)) {
				t.Fatalf("%s: 上屏段「%s」不是词典词条", c.input, committed)
			}
		}
		if joined := strings.Join(got, ""); joined != strings.Join(c.segs, "") {
			t.Fatalf("%s: 逐段拼出 %q, 期望 %q", c.input, joined, strings.Join(c.segs, ""))
		}
		if rest := s.Compose(); rest.Input != "" || len(rest.Candidates) != 0 {
			t.Fatalf("%s: 整串耗尽后组词应为空，实际 %+v", c.input, rest)
		}
	}
}

// entrySylls 返回词典词条 text 的音节序列（用于 Contains 校验）。
func entrySylls(t *testing.T, d *Dictionary, text string) []string {
	t.Helper()
	for _, e := range d.Entries() {
		if e.Word == text {
			return e.Syllables
		}
	}
	t.Fatalf("词典里没有词条 %q", text)
	return nil
}

// 单条静态词库词条优先于零散字词拼装：
// jingchang 必须出「经常」（不能拆成「经」+「常」）；
// jisuanji 必须出「计算机」（不能拆成「计算」+「机」）。
func TestSingleWordBeatsCompoundSplit(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	comp, _ := s.Input("jingchang")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "经常" {
		t.Fatalf("jingchang 第一候选应为「经常」，实际 %+v", comp.Candidates)
	}

	s.Reset()
	comp, _ = s.Input("jisuanji")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "计算机" {
		t.Fatalf("jisuanji 第一候选应为「计算机」，实际 %+v", comp.Candidates)
	}
}

// 用户针对反馈的核心痛点：输入过程中（如 nizaig、nizaiganm、taibangl、womendeh）
// 绝不能出现候选列表瞬间变空（0 个候选）导致候选窗闪烁消失。
func TestProgressiveTypingNeverProducesZeroCandidates(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	streams := [][]string{
		{"n", "ni", "niz", "niza", "nizai", "nizaig", "nizaiga", "nizaigan", "nizaiganm", "nizaiganma"},
		{"t", "ta", "tai", "taib", "taiba", "taiban", "taibang", "taibangl", "taibangle"},
		{"w", "wo", "wom", "wome", "women", "womend", "womende", "womendeh", "womendeha", "womendehao"},
		{"c", "ch", "chi", "chif", "chifa", "chifan", "chifanl", "chifanle"},
	}

	for _, seq := range streams {
		s.Reset()
		for _, in := range seq {
			comp, err := s.Input(in)
			if err != nil {
				t.Fatalf("输入 %q 报错: %v", in, err)
			}
			if len(comp.Candidates) == 0 {
				t.Fatalf("渐进输入序列中 %q 产生 0 候选！这会导致候选窗闪烁消失", in)
			}
			s.Reset()
		}
	}
}

// 回车上屏原始拼音：CommitRaw 清空组词并返回原始拼音字符串
func TestCommitRaw(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	s.Input("nizaiganma")
	raw, err := s.CommitRaw()
	if err != nil {
		t.Fatalf("CommitRaw 失败: %v", err)
	}
	if raw != "nizaiganma" {
		t.Errorf("CommitRaw 返回 = %q, 期望 nizaiganma", raw)
	}
	comp := s.Compose()
	if comp.Input != "" || comp.Preedit != "" || len(comp.Candidates) != 0 {
		t.Errorf("CommitRaw 后组词应已重置，实际: %+v", comp)
	}
}

// 选中「已有词」后调频生效：再次输入同一串时该词带 FromUser 标记排前
// （用户历史优先，2026-11-01 要求补回归测试）。
func TestExistingWordSelectionBoostsUserHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u := mustUserDict(t, path)
	s := NewSession(mustCommonDict(t), u)

	comp, _ := s.Input("womende")
	idx := findCandidate(t, s, "我们")
	if _, _, err := s.SelectPrefix(idx); err != nil {
		t.Fatalf("渐进选「我们」失败: %v", err)
	}
	if got := s.Compose().Input; got != "de" {
		t.Fatalf("选「我们」后剩余输入应为 de，实际 %q", got)
	}
	s.Reset()

	comp, _ = s.Input("womende")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "我们" || !comp.Candidates[0].FromUser {
		t.Fatalf("二次输入 womende 首选应为带 FromUser 的「我们」，得到 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}
}
