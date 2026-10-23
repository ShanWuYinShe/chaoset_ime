package engine

import (
	"testing"
)

// 用户反馈的场景 1：多打一个对不上的字母时，已完整的部分仍应出候选。
// 修复前 nihaox / nihaoz / womenx 全部「无候选」，给人的观感是输入法坏了。
func TestPartialTailFallsBackToCompletePrefix(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	cases := []struct{ input, want string }{
		{"nihaox", "你好"},
		{"nihaoz", "你好"},
		{"womenx", "我们"},
		{"zhongguob", "中国"},
		{"zhongguoo", "中国"}, // 末段 'o' 既成句（「中国哦」），候选列表中也必须保留已完整的前缀「中国」
		{"jingchangx", "经常"},
	}
	for _, c := range cases {
		s.Reset()
		comp, err := s.Input(c.input)
		if err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		if len(comp.Candidates) == 0 {
			t.Errorf("%s: 应有候选（期望能打出「%s」），实际无候选", c.input, c.want)
			continue
		}
		hasWant := false
		for _, cand := range comp.Candidates {
			if cand.Text == c.want {
				hasWant = true
				break
			}
		}
		if !hasWant {
			t.Errorf("%s: 候选列表中应包含「%s」，实际候选: %+v", c.input, c.want, comp.Candidates)
		}
		// 末段为无法组词或续接的字母时，首选直接回退到已完整前缀
		if c.input != "zhongguoo" && comp.Candidates[0].Text != c.want {
			t.Errorf("%s: 第一候选 = %q, 期望「%s」", c.input, comp.Candidates[0].Text, c.want)
		}
	}
}

// 回退候选不能压过正常匹配：完整输入时首选仍是精确匹配的词。
func TestBackoffDoesNotOutrankExactMatch(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, _ := s.Input("jingchang")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "经常" {
		t.Fatalf("jingchang 第一候选应为「经常」，实际 %+v", comp.Candidates)
	}
	// 单音节输入不应被回退逻辑影响
	s.Reset()
	comp, _ = s.Input("jing")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "经" {
		t.Fatalf("jing 第一候选应为「经」，实际 %+v", comp.Candidates)
	}
}

// 用户反馈的场景 2：退格逐字删拼音，不应出现「整段消失」。
// 引擎侧逐字符回退（一次退一个字母），壳侧据 input 是否为空决定放行与否。
func TestBackspaceDeletesOneCharAtATime(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	s.Input("jingchang")
	want := []string{"jingchan", "jingcha", "jingch", "jingc", "jing", "jin", "ji", "j"}
	for i, exp := range want {
		comp, err := s.Backspace()
		if err != nil {
			t.Fatalf("第 %d 次退格失败: %v", i+1, err)
		}
		if comp.Input != exp {
			t.Fatalf("第 %d 次退格后 input = %q, 期望 %q", i+1, comp.Input, exp)
		}
	}
	// 继续退格直到空
	for i := 0; i < 3; i++ {
		s.Backspace()
	}
	comp, _ := s.Backspace()
	if comp.Input != "" {
		t.Fatalf("退格到底后 input 应为空，实际 %q", comp.Input)
	}
}

// 无候选状态下的组词仍可正常退格（不应残留"冻结"的会话）。
func TestBackspaceWorksWhenNoCandidates(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	s.Input("zhzxq") // 无候选
	comp, _ := s.Backspace()
	if comp.Input != "zhzx" {
		t.Fatalf("退格后 input = %q, 期望 zhzx", comp.Input)
	}
	if comp.Preedit == "" {
		t.Fatal("组词中预编辑不应为空")
	}
}

// 测试 BackspaceWord 逐音节/逐词回退（对应 Option+Backspace）。
func TestBackspaceWordSyllableRollback(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)

	// 1. 无撇号多音节：nihaoma -> nihao -> ni -> 空
	s.Input("nihaoma")
	comp, err := s.BackspaceWord()
	if err != nil || comp.Input != "nihao" {
		t.Fatalf("BackspaceWord 1 期望 nihao, 得到 %q (err=%v)", comp.Input, err)
	}
	comp, err = s.BackspaceWord()
	if err != nil || comp.Input != "ni" {
		t.Fatalf("BackspaceWord 2 期望 ni, 得到 %q (err=%v)", comp.Input, err)
	}
	comp, err = s.BackspaceWord()
	if err != nil || comp.Input != "" {
		t.Fatalf("BackspaceWord 3 期望空, 得到 %q (err=%v)", comp.Input, err)
	}

	// 2. 显式撇号多音节：wo'men'de -> wo'men -> wo -> 空
	s.Reset()
	s.Input("wo'men'de")
	comp, _ = s.BackspaceWord()
	if comp.Input != "wo'men" {
		t.Fatalf("BackspaceWord 带撇号 1 期望 wo'men, 得到 %q", comp.Input)
	}
	comp, _ = s.BackspaceWord()
	if comp.Input != "wo" {
		t.Fatalf("BackspaceWord 带撇号 2 期望 wo, 得到 %q", comp.Input)
	}
	comp, _ = s.BackspaceWord()
	if comp.Input != "" {
		t.Fatalf("BackspaceWord 带撇号 3 期望空, 得到 %q", comp.Input)
	}

	// 3. 末段残缺声母：nihaox -> nihao
	s.Reset()
	s.Input("nihaox")
	comp, _ = s.BackspaceWord()
	if comp.Input != "nihao" {
		t.Fatalf("BackspaceWord 末段残缺 期望 nihao, 得到 %q", comp.Input)
	}
}
