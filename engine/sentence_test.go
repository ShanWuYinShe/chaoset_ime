package engine

import (
	"path/filepath"
	"testing"
)

// 测试常见 3-6 音节词组与短句在无单条词库词条时，通过动态规划整句分词成功拼合上屏
func TestComposeMultiSyllableSentences(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	cases := []struct {
		input string
		want  string
	}{
		{"womende", "我们的"},
		{"chifanle", "吃饭了"},
		{"diannaoshang", "电脑上"},
		{"tadeche", "他的车"},
		{"kanyixia", "看一下"},
		{"zaima", "在吗"},
		{"wolaile", "我来了"},
		{"jintiantianqizhenhao", "今天天气真好"},
		{"wobuxiangquchifan", "我不想去吃饭"},
	}

	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Fatalf("%s: 应出候选，实际无候选", c.input)
		}
		if got := comp.Candidates[0].Text; got != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望 %q（全部候选: %+v）", c.input, got, c.want, comp.Candidates)
		}
	}
}

// 常见疑问短句：如 nihaoma，首选或高频候选必须包含「你好吗」
func TestComposeNihaoma(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, err := s.Input("nihaoma")
	if err != nil {
		t.Fatalf("nihaoma: Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatal("nihaoma: 应有候选")
	}
	found := false
	for i, cand := range comp.Candidates {
		if cand.Text == "你好吗" {
			found = true
			if i > 2 {
				t.Errorf("你好吗 顺位偏低（第 %d 位）", i+1)
			}
			break
		}
	}
	if !found {
		t.Fatalf("nihaoma 候选应包含「你好吗」，实际: %+v", comp.Candidates)
	}
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
		t.Errorf("CommitRaw 返回 = %q, 期望 \"nizaiganma\"", raw)
	}
	comp := s.Compose()
	if comp.Input != "" || comp.Preedit != "" || len(comp.Candidates) != 0 {
		t.Errorf("CommitRaw 后组词应已重置，实际: %+v", comp)
	}
}

// 用户学习整句候选：选中整句候选后，调频能提升后续输入权重
func TestSentenceCandidateUserLearning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u := mustUserDict(t, path)
	s := NewSession(mustCommonDict(t), u)

	// 输入 womende 选中「我们的」
	comp, _ := s.Input("womende")
	targetIdx := -1
	for i, c := range comp.Candidates {
		if c.Text == "我们的" {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		t.Fatal("未在候选列表中找到「我们的」")
	}
	committed, err := s.Select(targetIdx)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if committed != "我们的" {
		t.Fatalf("上屏词 = %q, 期望「我们的」", committed)
	}

	// 再次输入 womende，「我们的」必须是带用户标志的首选
	s.Reset()
	comp, _ = s.Input("womende")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "我们的" || !comp.Candidates[0].FromUser {
		t.Fatalf("二次输入 womende 首选应为带 FromUser 的「我们的」，得到 %+v", comp.Candidates)
	}
}
