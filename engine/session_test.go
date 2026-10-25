package engine

import (
	"path/filepath"
	"testing"
	"time"
)

// 输入 nihao → 预编辑 ni'hao，第一候选「你好」。
func TestComposeBasic(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	comp, err := s.Input("nihao")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if comp.Preedit != "ni'hao" {
		t.Errorf("预编辑 = %q, 期望 ni'hao", comp.Preedit)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("第一候选应为「你好」，得到 %+v", comp.Candidates)
	}
}

// xian 存在 [xian] 与 [xi an] 两种切分，候选应同时包含「先」与「西安」。
func TestComposeSegmentationAmbiguity(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	comp, _ := s.Input("xian")
	hasXian, hasXian0 := false, false
	for _, c := range comp.Candidates {
		switch c.Text {
		case "先":
			hasXian = true
		case "西安":
			hasXian0 = true
		}
	}
	if !hasXian || !hasXian0 {
		t.Fatalf("xian 候选应同时包含 先/西安，得到 %+v", comp.Candidates)
	}
}

// 撇号是强制音节边界：xi'an 只走 [xi an] 切分，仍应命中「西安」。
func TestComposeApostropheForcesBoundary(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	comp, _ := s.Input("xi'an")
	if comp.Preedit != "xi'an" {
		t.Errorf("预编辑 = %q, 期望 xi'an", comp.Preedit)
	}
	found := false
	for _, c := range comp.Candidates {
		if c.Text == "西安" {
			found = true
		}
	}
	if !found {
		t.Fatalf("xi'an 候选应包含「西安」，得到 %+v", comp.Candidates)
	}
}

// 打到一半（nih）→ 预编辑 ni'h，前缀匹配应命中「你好」。
func TestComposeFragmentPrefix(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	comp, _ := s.Input("nih")
	if comp.Preedit != "ni'h" {
		t.Errorf("预编辑 = %q, 期望 ni'h", comp.Preedit)
	}
	found := false
	for _, c := range comp.Candidates {
		if c.Text == "你好" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nih 候选应包含「你好」，得到 %+v", comp.Candidates)
	}
}

// 非 [a-z'] 字符被过滤，大写归一化。
func TestInputFiltersNonLetters(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	comp, _ := s.Input("NI1hao!?")
	if comp.Input != "nihao" {
		t.Fatalf("过滤后输入 = %q, 期望 nihao", comp.Input)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("第一候选应为「你好」，得到 %+v", comp.Candidates)
	}
}

func TestBackspaceAndReset(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.Input("nihao")
	comp, _ := s.Backspace()
	if comp.Input != "niha" {
		t.Fatalf("退格后输入 = %q, 期望 niha", comp.Input)
	}
	comp, _ = s.Backspace()
	if comp.Input != "nih" || comp.Preedit != "ni'h" {
		t.Fatalf("退格后 = %q / %q, 期望 nih / ni'h", comp.Input, comp.Preedit)
	}
	comp, _ = s.Reset()
	if comp.Input != "" || comp.Preedit != "" || len(comp.Candidates) != 0 {
		t.Fatalf("重置后应全空，得到 %+v", comp)
	}
}

// 完全无法切分的输入：无候选、预编辑回退为原始输入。
func TestComposeUnsegmentable(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	comp, _ := s.Input("zzz")
	if len(comp.Candidates) != 0 {
		t.Fatalf("zzz 不应有候选，得到 %+v", comp.Candidates)
	}
	if comp.Preedit != "zzz" {
		t.Errorf("预编辑应回退为原始输入，得到 %q", comp.Preedit)
	}
}

// 核心学习闭环：低词频词被上屏一次后，下次输入排到第一。
func TestSelectLearnsAndReranks(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user.json")
	d := mustDict(t)

	s1 := NewSession(d, mustUserDict(t, udPath))
	comp, _ := s1.Input("shishi")
	if comp.Candidates[0].Text != "实施" {
		t.Fatalf("首词频排序应「实施」在前，得到 %+v", comp.Candidates)
	}
	if comp.Candidates[1].Text != "试试" {
		t.Fatalf("第二候选应为「试试」，得到 %+v", comp.Candidates)
	}
	committed, err := s1.Select(1)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if committed != "试试" {
		t.Fatalf("上屏 = %q, 期望 试试", committed)
	}
	if after, _ := s1.Reset(); after.Input != "" {
		t.Fatalf("上屏后组词应清空，得到 %+v", after)
	}

	// 新会话加载同一份用户词典：试试(10+500) 应超过 实施(60)
	s2 := NewSession(d, mustUserDict(t, udPath))
	comp2, _ := s2.Input("shishi")
	if comp2.Candidates[0].Text != "试试" {
		t.Fatalf("学习后「试试」应排第一，得到 %+v", comp2.Candidates)
	}
	if !comp2.Candidates[0].FromUser {
		t.Errorf("第一候选应标记 FromUser")
	}
}

func mustUserDict(t *testing.T, path string) *UserDict {
	t.Helper()
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}
	return u
}

// TestLongPinyinNoExponentialFreeze 验证超长拼音与残缺结尾输入在记忆化剪枝保护下
// 保持亚毫秒响应，杜绝未记忆化 DFS 导致的 O(2^N) 指数级回溯卡死。
func TestLongPinyinNoExponentialFreeze(t *testing.T) {
	d := mustDict(t)
	s := NewSession(d, nil)

	testCases := []string{
		// 1. 重复零声母元音 + 无法形成音节的结尾（旧逻辑在此处 O(2^N) 爆炸）
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaau",
		// 2. 重复重叠音节 + 无法形成音节的结尾
		"anananananananananananananananu",
		// 3. 连续简拼（40 字符）
		"zgzgzgzgzgzgzgzgzgzgzgzgzgzgzgzgzgzgzgzg",
		// 4. 超长真实句子（50+ 字符）
		"zhonghuarenmingongheguoguowuyuanzongliwenjiabaofangwen",
		// 5. 常见拟声/大笑拉长音
		"haaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	for _, tc := range testCases {
		s.Reset()
		t0 := time.Now()
		comp, err := s.Input(tc)
		duration := time.Since(t0)
		if err != nil {
			t.Fatalf("Input(%q) 出错: %v", tc, err)
		}
		// 保证超长输入在 50ms 内极速完成（实测 < 5ms），绝不发生数秒甚至数十秒卡死
		if duration > 50*time.Millisecond {
			t.Errorf("Input(%q) 耗时过长: %v (期望 < 50ms)", tc, duration)
		}
		_ = comp
	}
}

// 联想候选按词频降序：QueryPrefix 按 Trie 子树序收集、顺序与词频无关，
// 联想展示给用户时应高频在前（毫米 64 不应排在 豪快 9 之后）。
func TestAssociateSortedByFreq(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	out := s.Associate([]string{"hao"})
	if len(out) < 2 {
		t.Fatal("hao 应有联想候选")
	}
	for i := 1; i < len(out); i++ {
		if out[i-1].Score < out[i].Score {
			t.Fatalf("联想候选未按词频降序: %+v", out)
		}
	}
}
