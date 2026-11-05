package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func resetCompDict(t *testing.T) *Dictionary {
	t.Helper()
	f, err := os.Open("../data/dict_common.tsv")
	if err != nil {
		t.Skip("需要真实词库")
	}
	defer f.Close()
	d, err := ParseDict(f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// ResetComposition 必须保留学习上下文：这是切中英/透传等「用户还会继续
// 打字」场景的正确收尾，清掉 prev 会让紧接着的候选失去 bigram 加成。
func TestResetCompositionKeepsLearningContext(t *testing.T) {
	dict := resetCompDict(t)
	dir := t.TempDir()
	ud, err := NewUserDict(filepath.Join(dir, "u.json"))
	if err != nil {
		t.Fatal(err)
	}

	s := NewSession(dict, ud)
	if _, err := s.Input("nihao", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Select(0); err != nil {
		t.Fatal(err)
	}
	if s.prev == "" {
		t.Fatal("上屏后 prev 应已设置（bigram 上下文来源）")
	}
	prevBefore := s.prev

	if _, err := s.ResetComposition(); err != nil {
		t.Fatal(err)
	}

	if s.prev != prevBefore {
		t.Fatalf("ResetComposition 后 prev = %q, 期望保留 %q", s.prev, prevBefore)
	}
	if s.input != "" {
		t.Fatalf("ResetComposition 后 input 应清空, 得到 %q", s.input)
	}
}

// 反向：Reset 仍必须清学习上下文（Esc/失焦/新会话语义不能被削弱）。
func TestResetStillClearsLearningContext(t *testing.T) {
	dict := resetCompDict(t)
	dir := t.TempDir()
	ud, err := NewUserDict(filepath.Join(dir, "u.json"))
	if err != nil {
		t.Fatal(err)
	}

	s := NewSession(dict, ud)
	if _, err := s.Input("nihao", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Select(0); err != nil {
		t.Fatal(err)
	}
	if s.prev == "" {
		t.Fatal("上屏后 prev 应已设置")
	}

	if _, err := s.Reset(); err != nil {
		t.Fatal(err)
	}

	if s.prev != "" {
		t.Fatalf("Reset 后 prev 应清空, 得到 %q", s.prev)
	}
	if len(s.history) != 0 {
		t.Fatalf("Reset 后 history 应清空, 得到 %d 条", len(s.history))
	}
}

// 双向：ResetComposition 保留的 prev 必须**真的影响**候选打分——
// 否则「保留上下文」只是形式，不构成有效修复。
func TestResetCompositionPreservesPrevBoost(t *testing.T) {
	dict := resetCompDict(t)
	dir := t.TempDir()
	ud, _ := NewUserDict(filepath.Join(dir, "u.json"))
	// 让「我们→需要」形成强 bigram
	for i := 0; i < 20; i++ {
		ud.BumpBigram("我们", "需要")
	}

	withCtx := NewSession(dict, ud)
	withCtx.prev = "我们"
	ca, _ := withCtx.Input("xuyao", false)

	withoutCtx := NewSession(dict, ud)
	cb, _ := withoutCtx.Input("xuyao", false)

	if len(ca.Candidates) == 0 || len(cb.Candidates) == 0 {
		t.Skip()
	}
	if ca.Candidates[0].Score <= cb.Candidates[0].Score {
		t.Fatalf("带 prev 的首候选分数 (%d) 应高于不带 (%d)，说明上下文确实参与打分",
			ca.Candidates[0].Score, cb.Candidates[0].Score)
	}
}
