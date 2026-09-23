package engine

import "testing"

// 空组词：标点键直接映射为中文标点（完整直映表）。
func TestPunctIdleMapping(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	cases := map[string]string{
		",": "，", ".": "。", ";": "；", ":": "：", "?": "？", "!": "！",
		"(": "（", ")": "）", "[": "【", "]": "】", "<": "《", ">": "》",
		"\\": "、", "~": "～", "^": "……",
	}
	for in, want := range cases {
		got, err := s.Punct(in)
		if err != nil {
			t.Fatalf("Punct(%q) 失败: %v", in, err)
		}
		if got != want {
			t.Errorf("Punct(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// 组词中打标点：先上屏首选候选，再输出中文标点，随后组词清空。
func TestPunctCommitsFirstCandidate(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Input("nihao"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	got, err := s.Punct(",")
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if got != "你好，" {
		t.Fatalf("Punct = %q, 期望 你好，", got)
	}
	comp, _ := s.Input("")
	if comp.Input != "" {
		t.Fatalf("标点提交后组词应清空，剩余 %q", comp.Input)
	}
}

// 残缺输入（无候选）打标点：丢弃输入，只出标点。
func TestPunctDiscardsDeadInput(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Input("ea"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	got, err := s.Punct(".")
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if got != "。" {
		t.Fatalf("Punct = %q, 期望 。", got)
	}
	comp, _ := s.Input("")
	if comp.Input != "" {
		t.Fatalf("残缺输入应被丢弃，剩余 %q", comp.Input)
	}
}

// 双/单引号各自开闭交替，互不干扰。
func TestPunctQuotePairing(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	steps := []struct{ key, want string }{
		{"\"", "“"}, {"\"", "”"}, {"'", "‘"}, {"'", "’"}, {"\"", "“"},
	}
	for i, st := range steps {
		got, err := s.Punct(st.key)
		if err != nil {
			t.Fatalf("第 %d 次 Punct(%q) 失败: %v", i+1, st.key, err)
		}
		if got != st.want {
			t.Errorf("第 %d 次 Punct(%q) = %q, 期望 %q", i+1, st.key, got, st.want)
		}
	}
}

// 引号配对跨组词上屏保持：他说“你好”再见。
func TestPunctQuoteAcrossCommits(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	must := func(got string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("失败: %v", err)
		}
		return got
	}
	var out string
	out += must(s.Punct("\"")) // “
	s.Input("nihao")
	out += must(s.Select(0))   // 你好
	out += must(s.Punct("\"")) // ”
	if out != "“你好”" {
		t.Fatalf("跨上屏引号配对 = %q, 期望 “你好”", out)
	}
}

// 组词中的 ' 是音节分隔符：作为标点提交必须报错。
func TestPunctApostropheWhileComposing(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Input("xi"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := s.Punct("'"); err == nil {
		t.Fatal("组词中 ' 作为标点应报错")
	}
}

// 未知标点与多字符键报错；组词状态不受影响。
func TestPunctRejectsInvalid(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Punct("ab"); err == nil {
		t.Fatal("多字符键应报错")
	}
	if _, err := s.Punct("@"); err == nil {
		t.Fatal("未知标点应报错")
	}
	if _, err := s.Input("nihao"); err != nil {
		t.Fatalf("报错后 Input 失败: %v", err)
	}
	comp, _ := s.Input("")
	if comp.Input != "nihao" {
		t.Fatalf("报错不应影响组词状态，Input = %q", comp.Input)
	}
}
