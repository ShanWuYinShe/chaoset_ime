package engine

// determinism_test.go —— 组词结果确定性回归（2026-11-08 修复）。
//
// 背景：dict.go 前缀 BFS 曾依赖 map 遍历序（Go 每次 range 起点随机），
// 命中 maxResults/maxPrefixScan 截断时同一输入会得到不同候选总数/顺序；
// 修复见 dictNode.subtreeMin 与各查询 sort 的全序决胜键。
//
// 本测试对「原已知非确定性输入」+ 常规输入，在多种模式下重复组词，
// 断言 PageCount / Preedit / 首页候选完全一致。只比较首页而不翻页：
// 首页 + 总数已能覆盖此前的抖动形态，且避免 zh 这类 80+ 页输入翻页
// 把测试拖慢到秒级；全量序列由 compose golden 锁定。

import (
	"fmt"
	"strings"
	"testing"
)

// composeDeterminismInputs 覆盖原「已知非确定性」输入（b/b'a/ch/k/kkdz/sh/
// wmd/yb/zg/zh/zhongg、aq/bb/bd/bf/bg/bh）与常规整词/错拼/续接输入。
var composeDeterminismInputs = []string{
	"b", "b'a", "ch", "k", "kkdz", "sh", "wmd", "yb", "zg", "zh", "zhongg",
	"aq", "bb", "bd", "bf", "bg", "bh", "z", "q", "w",
	"zhongguo", "nihao", "jingchang", "shiguang", "zhogn", "niihao", "xi'an",
}

func TestComposeDeterministic(t *testing.T) {
	dict := mustCommonDict(t)
	const rounds = 12
	modes := []string{"plain", "fuzzy"}
	for _, mode := range modes {
		for _, input := range composeDeterminismInputs {
			want := composeDeterminismSignature(t, dict, mode, input)
			for round := 1; round < rounds; round++ {
				if got := composeDeterminismSignature(t, dict, mode, input); got != want {
					t.Fatalf("组词结果不确定：[%s] input=%q 第 %d 次与首次不一致（dict.go 前缀 BFS 的 map 序回归？）\n首次: %s\n本次: %s",
						mode, input, round+1, want, got)
				}
			}
		}
	}
	// user 模式单独跑一轮（每次新建内存用户词库有固定预热成本，输入取子集）：
	// 覆盖用户词条参与查询/续接时的排序确定性（同词多音、同频同长）。
	for _, input := range []string{"b", "zh", "zhongguo", "nihao", "shiguang", "wmd", "kkdz", "ce'shi"} {
		want := composeDeterminismSignature(t, dict, "user", input)
		for round := 1; round < 8; round++ {
			if got := composeDeterminismSignature(t, dict, "user", input); got != want {
				t.Fatalf("组词结果不确定：[user] input=%q 第 %d 次与首次不一致\n首次: %s\n本次: %s",
					input, round+1, want, got)
			}
		}
	}
}

// composeDeterminismSignature 生成一次 Input 的可比较签名：PageCount + Preedit +
// 首页候选全部字段（含未导出的 typoFix —— 它影响 SelectPrefix 语义）。
func composeDeterminismSignature(t *testing.T, dict *Dictionary, mode, input string) string {
	t.Helper()
	s := newComposeGoldenSession(t, dict, mode)
	comp, err := s.Input(input, false)
	if err != nil {
		t.Fatalf("[%s] Input(%q) 失败: %v", mode, input, err)
	}
	sig := fmt.Sprintf("pages=%d preedit=%q", comp.PageCount, comp.Preedit)
	for _, cand := range comp.Candidates {
		sig += fmt.Sprintf(" |%s/%s/%d/%v/%v/%v", cand.Text, cand.Syllables, cand.Score,
			cand.FromUser, cand.Segments, cand.typoFix)
	}
	return sig
}

// TestUserDictQueryDeterministic 锁住用户词库查询的确定性（2026-11-08 修复）。
//
// 多音字同词（着 zhe / 着 zhao）在 Words 表里是两条记录；查询结果按词去重的
// 旧实现「先收集、谁先遇到保留谁」，取决于 map 遍历顺序——同一输入重复组词会
// 得到不同读音的候选。修复：收集键改为 word+音节，排序（全序）之后再按词去重。
func TestUserDictQueryDeterministic(t *testing.T) {
	u, err := NewUserDict("") // 空路径 = 纯内存
	if err != nil {
		t.Fatalf("创建内存用户词库失败: %v", err)
	}
	bumps := []struct {
		word  string
		sylls []string
	}{
		{"着", []string{"zhe"}},
		{"着", []string{"zhao"}},
		{"中", []string{"zhong"}},
		{"中国", []string{"zhong", "guo"}},
		{"测试", []string{"ce", "shi"}},
		{"策士", []string{"ce", "shi"}},
	}
	for _, b := range bumps {
		if err := u.BumpWord(b.word, b.sylls); err != nil {
			t.Fatalf("预热用户词库失败（%s）: %v", b.word, err)
		}
	}
	queries := []struct {
		name string
		run  func() string
	}{
		{"QueryTokensExact", func() string {
			return sumEntries(u.QueryTokensExact([]Token{{Text: "z", IsExact: false}}, 100))
		}},
		{"QueryTokens", func() string {
			exacts, prefixes := u.QueryTokens([]Token{{Text: "zh", IsExact: false}}, 100)
			return sumEntries(exacts) + "|" + sumEntries(prefixes)
		}},
		{"QueryJoinedPinyin", func() string {
			return sumEntries(u.QueryJoinedPinyin("zhe", 20))
		}},
		{"QueryWordsByWordPrefix", func() string {
			return sumEntries(u.QueryWordsByWordPrefix("着"))
		}},
	}
	const rounds = 20
	for _, q := range queries {
		want := q.run()
		for round := 1; round < rounds; round++ {
			if got := q.run(); got != want {
				t.Fatalf("%s 结果不确定（第 %d 次）:\n首次: %s\n本次: %s", q.name, round+1, want, got)
			}
		}
	}
}

// sumEntries 把词条列表编码成可比较签名（词/音节/频率）。
func sumEntries(entries []*Entry) string {
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s/%s/%d;", e.Word, strings.Join(e.Syllables, "'"), e.Freq)
	}
	return sb.String()
}
