package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 整句式输入习惯（2026-10-30 用户需求）回归测试：
//
//  1. 多字拼音没有整句候选时逐段选择可用，且逐段选择的过程进入学习；
//  2. 整句学习只在**完整输入**时作为整句候选（下次打同一串更符合习惯）；
//  3. 不能整词贪婪匹配：打 今 不给 天星期一，打 jintian 不出现长词候选，
//     联想只在用户确认过的分段边界剥前缀——既不反噬「只想输入今天」，
//     也保留「今天→星期一」「不行→的话」这类合理续接。
//
// 测试词库刻意用最小集合，便于精确断言候选与学习数据。

const seqDict = `
今天	jin tian	90
今	jin	60
星期一	xing qi yi	80
天气	tian qi	75
`

func mustSeqDict(t *testing.T) *Dictionary {
	t.Helper()
	d, err := ParseDict(strings.NewReader(seqDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	return d
}

// findCandidate 在所有页里找候选文本，返回选它所需的页起点下标。
func findCandidate(t *testing.T, s *Session, text string) int {
	t.Helper()
	for page := 0; ; page++ {
		comp := s.Compose()
		for i, c := range comp.Candidates {
			if c.Text == text {
				return i
			}
		}
		if page >= comp.PageCount-1 {
			t.Fatalf("所有页都没有候选 %q（input=%q）", text, comp.Input)
		}
		s.NextPage()
	}
}

// collectCandidates 翻遍所有页收集候选文本（不改 input，只改 page）。
func collectCandidates(s *Session) []Candidate {
	var out []Candidate
	for page := 0; ; page++ {
		comp := s.Compose()
		out = append(out, comp.Candidates...)
		if page >= comp.PageCount-1 {
			break
		}
		s.NextPage()
	}
	return out
}

func hasCandidate(cands []Candidate, text string) bool {
	for _, c := range cands {
		if c.Text == text {
			return true
		}
	}
	return false
}

func mustSelectText(t *testing.T, s *Session, text string) string {
	t.Helper()
	idx := findCandidate(t, s, text)
	committed, err := s.Select(idx)
	if err != nil {
		t.Fatalf("Select(%q) 失败: %v", text, err)
	}
	return committed
}

// TestProgressiveSequenceLearned 覆盖：逐段拼出的 5 音节整句进入学习，
// 完整输入时整句候选被用户调频加权（>4 音节此前学不到）。
func TestProgressiveSequenceLearned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	d := mustSeqDict(t)
	s := NewSession(d, mustUserDict(t, path))

	// 输入条件：整句 DP 候选「今天星期一」与回退前缀「今天」都能选。
	s.Input("jintianxingqiyi")
	idx := findCandidate(t, s, "今天")
	comp, committed, err := s.SelectPrefix(idx)
	if err != nil || committed != "今天" {
		t.Fatalf("渐进选「今天」失败: committed=%q err=%v", committed, err)
	}
	if comp.Preedit != "xing'qi'yi" {
		t.Fatalf("剩余拼音应为 xing'qi'yi，实际 %q", comp.Preedit)
	}
	// 剩余串直接出「星期一」
	if comp.Candidates[0].Text != "星期一" {
		t.Fatalf("剩余串首选应为「星期一」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}
	comp, committed, err = s.SelectPrefix(0)
	if err != nil || committed != "星期一" {
		t.Fatalf("渐进选「星期一」失败: committed=%q err=%v", committed, err)
	}
	if comp.Input != "" || len(comp.Candidates) != 0 {
		t.Fatalf("整串耗尽后应为空组词: %+v", comp)
	}

	// 学习落盘：整句带分段记录；每段也各自有普通词调频。
	u := mustUserDict(t, path)
	seqKey := userKey("今天星期一", []string{"jin", "tian", "xing", "qi", "yi"})
	rec := u.file.Words[seqKey]
	if rec == nil {
		t.Fatalf("逐段拼出的「今天星期一」应写入 Words 表")
	}
	if len(rec.Segs) != 2 || rec.Segs[0] != "今天" || rec.Segs[1] != "星期一" {
		t.Fatalf("整句学习记录应带分段 [今天 星期一]，实际 %#v", rec.Segs)
	}
	if got := u.file.Words[userKey("今天", []string{"jin", "tian"})]; got == nil || got.Count != 1 {
		t.Fatalf("分段「今天」应各记一次调频，实际 %+v", got)
	}

	// 下一轮完整输入：整句候选带学习标记排第一（用户习惯被利用）。
	s2 := NewSession(d, u)
	comp, _ = s2.Input("jintianxingqiyi")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "今天星期一" || !comp.Candidates[0].FromUser {
		t.Fatalf("学习后完整输入首选应为带 FromUser 的「今天星期一」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}

	// 反噬防护 1：部分输入不出现整句候选（不能整词贪婪匹配）。
	s2.Reset()
	comp, _ = s2.Input("jin")
	if hasCandidate(collectCandidates(s2), "今天星期一") {
		t.Fatalf("输入 jin 不应出现整句候选「今天星期一」: %+v", collectCandidates(s2))
	}
	s2.Reset()
	comp, _ = s2.Input("jintian")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "今天" {
		t.Fatalf("输入 jintian 首选仍应是「今天」，实际 %+v", comp.Candidates)
	}
	if hasCandidate(collectCandidates(s2), "今天星期一") {
		t.Fatalf("输入 jintian 不应出现整句候选「今天星期一」: %+v", collectCandidates(s2))
	}
}

// TestOneShotSentenceLearnsSegments 覆盖：一次选中整句候选（干货路径）
// 按逐段确认的等价路径学习——整句带分段、分段词各自调频。
func TestOneShotSentenceLearnsSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	d := mustSeqDict(t)
	s := NewSession(d, mustUserDict(t, path))

	s.Input("jintianxingqiyi")
	comp := s.Compose()
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "今天星期一" {
		t.Fatalf("整句候选应为首页第一条，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}
	if got := comp.Candidates[0].Segments; len(got) != 2 || got[0] != "今天" || got[1] != "星期一" {
		t.Fatalf("整句候选应携带分段 [今天 星期一]，实际 %#v", got)
	}
	if _, _, err := s.SelectPrefix(0); err != nil {
		t.Fatalf("SelectPrefix 失败: %v", err)
	}

	u := mustUserDict(t, path)
	rec := u.file.Words[userKey("今天星期一", []string{"jin", "tian", "xing", "qi", "yi"})]
	if rec == nil || len(rec.Segs) != 2 {
		t.Fatalf("整句一键上屏应写入带分段的整句记录，实际 %+v", rec)
	}
	for _, seg := range []struct {
		word  string
		sylls []string
	}{{"今天", []string{"jin", "tian"}}, {"星期一", []string{"xing", "qi", "yi"}}} {
		if got := u.file.Words[userKey(seg.word, seg.sylls)]; got == nil || got.Count != 1 {
			t.Fatalf("整句一键上屏应等价学习分段词 %q，实际 %+v", seg.word, got)
		}
	}

	// 联想安全：上屏「今」不得剥出整句尾巴；上屏「今天」才续接「星期一」。
	s2 := NewSession(d, u)
	s2.Input("jin")
	mustSelectText(t, s2, "今")
	assoc := s2.Associate([]string{"jin"})
	if hasCandidate(assoc, "天星期一") {
		t.Fatalf("上屏「今」后联想不应出现整句尾巴「天星期一」: %+v", assoc)
	}
	s2.Reset()
	s2.Input("jintian")
	mustSelectText(t, s2, "今天")
	assoc = s2.Associate([]string{"jin", "tian"})
	if !hasCandidate(assoc, "星期一") {
		t.Fatalf("上屏「今天」后联想应在分段边界给出「星期一」，实际 %+v", assoc)
	}
}

// TestPhraseEvolutionSegmentGating 覆盖：词组进化产物同样只在分段边界续接，
// 避免学过「今天天气」后上屏「今」弹出「天天气」。
func TestPhraseEvolutionSegmentGating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	d := mustSeqDict(t)
	for round := 0; round < 2; round++ {
		s := NewSession(d, mustUserDict(t, path))
		s.Input("jintian")
		mustSelectText(t, s, "今天")
		s.Input("tianqi")
		mustSelectText(t, s, "天气")
	}
	u := mustUserDict(t, path)
	rec := u.file.Phrases[userKey("今天天气", []string{"jin", "tian", "tian", "qi"})]
	if rec == nil || len(rec.Segs) != 2 || rec.Segs[0] != "今天" || rec.Segs[1] != "天气" {
		t.Fatalf("词组进化应记录分段 [今天 天气]，实际 %+v", rec)
	}

	// 完整输入：整词候选照常出现（学到的词组要能用）。
	s := NewSession(d, u)
	comp, _ := s.Input("jintiantianqi")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "今天天气" {
		t.Fatalf("完整输入 jintiantianqi 首选应为「今天天气」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}
	// 部分输入：不出现整词候选（只在完整输入时使用）。
	s.Reset()
	comp, _ = s.Input("jintian")
	if hasCandidate(collectCandidates(s), "今天天气") {
		t.Fatalf("输入 jintian 不应出现整词候选「今天天气」: %+v", collectCandidates(s))
	}

	// 联想安全：今 → 不弹 天天气；今天 → 弹 天气。
	s.Reset()
	s.Input("jin")
	mustSelectText(t, s, "今")
	assoc := s.Associate([]string{"jin"})
	if hasCandidate(assoc, "天天气") {
		t.Fatalf("上屏「今」后联想不应出现整词尾巴「天天气」: %+v", assoc)
	}
	s.Reset()
	s.Input("jintian")
	mustSelectText(t, s, "今天")
	assoc = s.Associate([]string{"jin", "tian"})
	if !hasCandidate(assoc, "天气") {
		t.Fatalf("上屏「今天」后联想应在分段边界给出「天气」，实际 %+v", assoc)
	}
}

// TestLegacyUserDictSegmentInference 覆盖旧版本数据（没有 Segs 字段）：
// 载入后仍须遵守「完整输入才整句、分段边界才续接」，不能因兼容旧数据
// 又把「天星期一」漏出来。
func TestLegacyUserDictSegmentInference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	now := time.Now().Unix()
	legacy := map[string]any{
		"words": map[string]any{
			userKey("今天星期一", []string{"jin", "tian", "xing", "qi", "yi"}): map[string]any{"count": 1, "last_used": now},
		},
		"phrases": map[string]any{
			userKey("今天天气", []string{"jin", "tian", "tian", "qi"}): map[string]any{
				"word": "今天天气", "sylls": "jin tian tian qi", "count": 1, "last_used": now,
			},
		},
		"bigrams": map[string]any{},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("构造旧版用户词典失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入旧版用户词典失败: %v", err)
	}
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("加载旧版用户词典失败: %v", err)
	}
	d := mustSeqDict(t)
	s := NewSession(d, u)

	// 完整输入：旧的整句记录仍作为整句候选可用。
	comp, _ := s.Input("jintianxingqiyi")
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "今天星期一" {
		t.Fatalf("旧整句记录在完整输入时仍应是首选，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}
	// 部分输入：不出现整句候选。
	s.Reset()
	s.Input("jintian")
	if hasCandidate(collectCandidates(s), "今天星期一") || hasCandidate(collectCandidates(s), "今天天气") {
		t.Fatalf("旧数据在部分输入时也不得整词匹配: %+v", collectCandidates(s))
	}
	// 联想：今 → 无尾巴；今天 → 分段边界续接。
	s.Reset()
	s.Input("jin")
	mustSelectText(t, s, "今")
	assoc := s.Associate([]string{"jin"})
	if hasCandidate(assoc, "天星期一") || hasCandidate(assoc, "天天气") {
		t.Fatalf("旧数据上屏「今」后联想不应出现整词尾巴: %+v", assoc)
	}
	s.Reset()
	s.Input("jintian")
	mustSelectText(t, s, "今天")
	assoc = s.Associate([]string{"jin", "tian"})
	if !hasCandidate(assoc, "星期一") {
		t.Fatalf("旧整句记录应在分段边界续接「星期一」，实际 %+v", assoc)
	}
	if !hasCandidate(assoc, "天气") {
		t.Fatalf("旧词组记录应在分段边界续接「天气」，实际 %+v", assoc)
	}
}

// TestSelectPrefixResetsPage 覆盖：在第 2 页选中前缀段后，剩余组词必须
// 回到第一页（否则用户会以为剩余拼音没有候选——2026-10-30 实测：
// jintianxingqiyi 翻到第 2 页选「今天」，剩余 xingqiyi 仍停在第 2 页，
// 首页的「星期一」被跳过）。
func TestSelectPrefixResetsPage(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	if _, err := s.Input("jintianxingqiyi"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	idx := findCandidate(t, s, "今天") // 内部翻页，停在第 2 页
	if s.Compose().Page == 0 {
		t.Fatalf("前置失败：「今天」应在第 2 页及以后（实际首页即命中），无法验证翻页重置")
	}
	rest, committed, err := s.SelectPrefix(idx)
	if err != nil || committed != "今天" {
		t.Fatalf("SelectPrefix 失败: committed=%q err=%v", committed, err)
	}
	if rest.Page != 0 {
		t.Fatalf("渐进上屏后剩余组词应回到第一页，实际 page=%d", rest.Page)
	}
	if rest.Candidates[0].Text != "星期一" {
		t.Fatalf("剩余首页首选应为「星期一」，实际 %+v", rest.Candidates[:min(3, len(rest.Candidates))])
	}
}

// TestSelectPrefixApostropheInput 覆盖：用户按音节分隔符输入（jin'tian'…）
// 时渐进上屏仍然生效，剩余串不以分隔符开头。
func TestSelectPrefixApostropheInput(t *testing.T) {
	d := mustSeqDict(t)
	s := NewSession(d, nil)
	if _, err := s.Input("jin'tian'xing'qi'yi"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	idx := findCandidate(t, s, "今天")
	comp, committed, err := s.SelectPrefix(idx)
	if err != nil || committed != "今天" {
		t.Fatalf("撇号输入渐进选「今天」失败: committed=%q err=%v", committed, err)
	}
	if comp.Preedit != "xing'qi'yi" {
		t.Fatalf("剩余拼音应为 xing'qi'yi，实际 %q", comp.Preedit)
	}
	if comp.Candidates[0].Text != "星期一" {
		t.Fatalf("剩余串首选应为「星期一」，实际 %+v", comp.Candidates[:min(3, len(comp.Candidates))])
	}
	if _, committed, err = s.SelectPrefix(0); err != nil || committed != "星期一" {
		t.Fatalf("撇号输入第二段失败: committed=%q err=%v", committed, err)
	}
	if s.Compose().Input != "" {
		t.Fatalf("整串应已耗尽，实际 %q", s.Compose().Input)
	}
}

// TestConsumeInputPrefix 直接锁定前缀消耗规则（含撇号与不匹配回退）。
func TestConsumeInputPrefix(t *testing.T) {
	cases := []struct {
		input, typed, want string
		ok                 bool
	}{
		{"jintianxingqiyi", "jintian", "xingqiyi", true},
		{"jin'tian'xing'qi'yi", "jintian", "xing'qi'yi", true},
		{"jin'tian", "jin", "tian", true},
		{"jintian", "jintian", "", true},
		{"jin", "jintian", "", false},
		{"zhongg", "zhongguo", "", false},
	}
	for _, c := range cases {
		got, ok := consumeInputPrefix(c.input, c.typed)
		if got != c.want || ok != c.ok {
			t.Errorf("consumeInputPrefix(%q,%q) = (%q,%v), 期望 (%q,%v)", c.input, c.typed, got, ok, c.want, c.ok)
		}
	}
}

// TestSentenceSyllableCap 锁定整句学习长度上限：超过 maxSentenceSylls 的
// 连打串不进入整句习惯（避免把误触长串学成词）。
func TestSentenceSyllableCap(t *testing.T) {
	d := mustSeqDict(t)
	u, err := NewUserDict(filepath.Join(t.TempDir(), "u.json"))
	if err != nil {
		t.Fatalf("NewUserDict 失败: %v", err)
	}
	s := NewSession(d, u)
	mk := func(n int) []learnEntry {
		entries := make([]learnEntry, 0, n)
		for i := 0; i < n; i++ {
			entries = append(entries, learnEntry{word: "啊", sylls: []string{"a"}})
		}
		return entries
	}
	s.learnProgressiveSequence(mk(maxSentenceSylls + 1))
	if len(u.file.Words) != 0 {
		t.Fatalf("超过上限的整句不应学习，实际写入 %d 条", len(u.file.Words))
	}
	s.learnProgressiveSequence(mk(maxSentenceSylls))
	if len(u.file.Words) == 0 {
		t.Fatalf("上限内的整句应学习（%d 音节）", maxSentenceSylls)
	}
}

// TestInferSegsMinimalSegmentation 覆盖旧数据分段推断：必须取「最少分段数」
// 的词典词序列，不能被最长词贪心带偏——2026-10-30 复检实测：旧记录「天星期一」
// 无 Segs 时贪心会先命中词典词「天星」→ 切成 [天星,期,一]，分段边界错误、
// 联想在上屏「天」后丢失「星期一」，错误分段词还会污染候选。
func TestInferSegsMinimalSegmentation(t *testing.T) {
	d, err := ParseDict(strings.NewReader("天星\ttian xing\t48\n天\ttian\t60\n期\tqi\t40\n一\tyi\t50\n星期一\txing qi yi\t80\n"))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	s := NewSession(d, nil)
	got := s.inferSegs("天星期一", []string{"tian", "xing", "qi", "yi"})
	want := []string{"天", "星期一"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("inferSegs(天星期一) 应取最少分段 [天 星期一]，实际 %#v", got)
	}
	if got := s.inferSegs("星期一", []string{"xing", "qi", "yi"}); got != nil {
		t.Fatalf("词典词不应推断为整句式分段，实际 %#v", got)
	}
}

// TestLegacySentenceTailBoundary 旧数据（无 Segs）的整句记录：
// 上屏「天」后应在分段边界续接「星期一」；尾巴再上屏学习时分段应为
// [天,星期一]（不是 [天星,期,一]），且不能把「天星」学成独立分段词。
func TestLegacySentenceTailBoundary(t *testing.T) {
	dictSrc := "天星\ttian xing\t48\n天\ttian\t60\n期\tqi\t40\n一\tyi\t50\n星期一\txing qi yi\t80\n"
	d, err := ParseDict(strings.NewReader(dictSrc))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "legacy.json")
	now := time.Now().Unix()
	legacy := map[string]any{
		"words": map[string]any{
			userKey("天星期一", []string{"tian", "xing", "qi", "yi"}): map[string]any{"count": 1, "last_used": now},
		},
		"phrases": map[string]any{},
		"bigrams": map[string]any{},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("构造旧版用户词典失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入旧版用户词典失败: %v", err)
	}
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("加载旧版用户词典失败: %v", err)
	}
	s := NewSession(d, u)
	s.Input("tian")
	mustSelectText(t, s, "天")
	assoc := s.Associate([]string{"tian"})
	if !hasCandidate(assoc, "星期一") {
		t.Fatalf("旧整句记录应在分段边界续接「星期一」，实际 %+v", assoc)
	}
	if _, err := s.AssociateSelect("天星期一", []string{"tian", "xing", "qi", "yi"}); err != nil {
		t.Fatalf("AssociateSelect 失败: %v", err)
	}
	u2 := mustUserDict(t, path)
	rec := u2.file.Words[userKey("天星期一", []string{"tian", "xing", "qi", "yi"})]
	if rec == nil || len(rec.Segs) != 2 || rec.Segs[0] != "天" || rec.Segs[1] != "星期一" {
		t.Fatalf("尾巴学习的分段应为 [天 星期一]，实际 %+v", rec)
	}
	if got := u2.file.Words[userKey("天星", []string{"tian", "xing"})]; got != nil {
		t.Fatalf("错误分段词「天星」不应被学习，实际 %+v", got)
	}
}

// TestSeqSegsResetWhenCompositionRestarts 覆盖组词中止后的伪整句防护：
// 渐进选「今天」→ 退格到空（未 Reset）→ 输入 wo 选「我」，不能学习出
// 「今天我 segs=[今天 我]」（2026-10-30 复检 J1：seqSegs 跨组词残留）。
func TestSeqSegsResetWhenCompositionRestarts(t *testing.T) {
	d, err := ParseDict(strings.NewReader("今天\tjin tian\t90\n今\tjin\t60\n我\two\t88\n星期一\txing qi yi\t80\n"))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "user.json")
	u := mustUserDict(t, path)
	s := NewSession(d, u)
	s.Input("jintianxingqiyi")
	idx := findCandidate(t, s, "今天")
	if _, _, err := s.SelectPrefix(idx); err != nil {
		t.Fatalf("渐进选「今天」失败: %v", err)
	}
	if s.Compose().Input == "" {
		t.Fatalf("前置失败：选「今天」后应剩余拼音")
	}
	for i := 0; i < 32 && s.Compose().Input != ""; i++ {
		if _, err := s.Backspace(); err != nil {
			t.Fatalf("Backspace 失败: %v", err)
		}
	}
	if s.Compose().Input != "" {
		t.Fatalf("退格后组词应为空，实际 %q", s.Compose().Input)
	}
	s.Input("wo")
	idx = findCandidate(t, s, "我")
	if _, committed, err := s.SelectPrefix(idx); err != nil || committed != "我" {
		t.Fatalf("新组词选「我」失败: committed=%q err=%v", committed, err)
	}
	if rec := u.file.Words[userKey("今天我", []string{"jin", "tian", "wo"})]; rec != nil {
		t.Fatalf("退格清空后重新组词不应拼出伪整句记录，实际 %+v", rec)
	}
	for k := range u.file.Words {
		if strings.HasPrefix(k, "今天我"+userKeySep) {
			t.Fatalf("不应存在伪整句记录 %q", k)
		}
	}
}
