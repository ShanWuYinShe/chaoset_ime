package engine

import (
	"strings"
	"testing"
)

// 空组词：标点键直接映射为中文标点（完整直映表）。
func TestPunctIdleMapping(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	cases := map[string]string{
		",": "，", ".": "。", ";": "；", ":": "：", "?": "？", "!": "！",
		"(": "（", ")": "）", "[": "【", "]": "】", "<": "《", ">": "》",
		"\\": "、", "~": "～", "^": "……",
	}
	for in, want := range cases {
		got, err := s.Punct(in, false)
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
	if _, err := s.Input("nihao", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	got, err := s.Punct(",", false)
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if got != "你好，" {
		t.Fatalf("Punct = %q, 期望 你好，", got)
	}
	comp, _ := s.Input("", false)
	if comp.Input != "" {
		t.Fatalf("标点提交后组词应清空，剩余 %q", comp.Input)
	}
}

// 残缺输入（无候选）打标点：丢弃输入，只出标点。
func TestPunctDiscardsDeadInput(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Input("ea", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	got, err := s.Punct(".", false)
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if got != "。" {
		t.Fatalf("Punct = %q, 期望 。", got)
	}
	comp, _ := s.Input("", false)
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
		got, err := s.Punct(st.key, false)
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
	out += must(s.Punct("\"", false)) // “
	s.Input("nihao", false)
	out += must(s.Select(0))          // 你好
	out += must(s.Punct("\"", false)) // ”
	if out != "“你好”" {
		t.Fatalf("跨上屏引号配对 = %q, 期望 “你好”", out)
	}
}

// 组词中的 ' 是音节分隔符：作为标点提交必须报错。
func TestPunctApostropheWhileComposing(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Input("xi", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := s.Punct("'", false); err == nil {
		t.Fatal("组词中 ' 作为标点应报错")
	}
}

// 同键连按循环：句号 。/. 交替并回绕，逗号同理；引号不受影响。
func TestPunctRepeatCycle(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	steps := []struct{ key, want string }{
		{".", "。"}, {".", "."}, {".", "。"}, // 回绕
		{",", "，"}, {",", ","}, // 逗号独立循环
		{".", "。"}, // 换键后回到首项
	}
	for i, st := range steps {
		got, err := s.Punct(st.key, false)
		if err != nil {
			t.Fatalf("第 %d 次 Punct(%q) 失败: %v", i+1, st.key, err)
		}
		if got != st.want {
			t.Errorf("第 %d 次 Punct(%q) = %q, 期望 %q", i+1, st.key, got, st.want)
		}
	}
}

// 其他输入/上屏打断循环：下一次同键回到序列首项。
func TestPunctCycleReset(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.Punct(".", false)             // 。
	s.Input("nihao", false)         // 打断循环并进入组词
	got, err := s.Punct(",", false) // 组词中：首选 + 逗号首项
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if !strings.HasSuffix(got, "，") {
		t.Fatalf("组词中标点应合并上屏，得到 %q", got)
	}
	got, err = s.Punct(".", false) // Select/Input 路径已重置 → 首项 。
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if got != "。" {
		t.Fatalf("打断后应回首项，得到 %q", got)
	}
}

// 退格同样打断循环。
func TestPunctCycleResetByBackspace(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.Punct(".", false) // 。
	s.Backspace()       // 退格打断
	got, _ := s.Punct(".", false)
	if got != "。" {
		t.Fatalf("退格打断后应回首项，得到 %q", got)
	}
}

// 未知标点与多字符键报错；组词状态不受影响。
func TestPunctRejectsInvalid(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if _, err := s.Punct("ab", false); err == nil {
		t.Fatal("多字符键应报错")
	}
	if _, err := s.Punct("@", false); err == nil {
		t.Fatal("未知标点应报错")
	}
	if _, err := s.Input("nihao", false); err != nil {
		t.Fatalf("报错后 Input 失败: %v", err)
	}
	comp, _ := s.Input("", false)
	if comp.Input != "nihao" {
		t.Fatalf("报错不应影响组词状态，Input = %q", comp.Input)
	}
}

// 半角模式：标点原样输出、引号不配对；切回全角恢复映射。
//
// 2026-11-05：宽度改为**请求参数**（壳是唯一真源），不再是会话状态。
// 因此这里用 Punct(key, halfWidth) 显式传宽度，引擎不保存该状态。
func TestHalfWidthMode(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if got, _ := s.Punct(".", false); got != "。" {
		t.Fatalf("全角首按应得中文句号, 得到 %q", got)
	}
	if got, _ := s.Punct(".", true); got != "." {
		t.Fatalf("半角句号应原样输出, 得到 %q", got)
	}
	if got, _ := s.Punct(",", true); got != "," {
		t.Fatalf("半角逗号应原样输出, 得到 %q", got)
	}
	// 引号不配对：两次输出相同（半角）
	g1, _ := s.Punct("\"", true)
	g2, _ := s.Punct("\"", true)
	if g1 != "\"" || g2 != "\"" {
		t.Fatalf("半角引号不应配对, 得到 %q %q", g1, g2)
	}
	// 同一次会话里换回全角：宽度只影响本次调用，与请求顺序无关
	if got, _ := s.Punct(".", false); got != "。" {
		t.Fatalf("切回全角应得中文句号, 得到 %q", got)
	}
}

// 半角模式组词中：首选候选 + 半角标点合并上屏；半角下无循环。
func TestHalfWidthComposing(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.Input("nihao", false)
	got, err := s.Punct(",", true)
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if got != "你好," {
		t.Fatalf("半角组词合并 = %q, 期望 你好,", got)
	}
	got2, _ := s.Punct(",", true)
	if got2 != "," {
		t.Fatalf("半角逗号应固定原样, 得到 %q", got2)
	}
}

// 半角宽度不得跨请求残留（2026-11-05 纯传参改造的回归锁）。
//
// 旧实现把宽度存进会话字段、靠后续请求“同步”：一旦某次请求没携带该字段
// （旧壳、或只调 Punct 的路径），daemon 会沿用上一次的残留值产出标点——
// 即 2026-10-30 记录的「刚切全/半角就单按 ';' 按旧宽度出标点」瞬态。
// 改为纯传参后，宽度只对本次调用生效，结构上不可能残留。
func TestHalfWidthNoResidual(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if got, _ := s.Punct(".", true); got != "." {
		t.Fatalf("半角句号 = %q, 期望 .", got)
	}
	// 紧接着全角请求：必须立即回到「。」，不允许残留上次的半角
	if got, _ := s.Punct(".", false); got != "。" {
		t.Fatalf("全角句号 = %q（半角状态残留了）", got)
	}
	if got, _ := s.Punct(".", true); got != "." {
		t.Fatalf("半角句号 = %q, 期望 .", got)
	}
}
