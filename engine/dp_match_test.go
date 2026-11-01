package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// DP 匹配「已存在的词条」（2026-11-01 用户澄清）：
// 去掉的是把词库片段拼成词库里不存在的复合词的「造词」；词库里真实存在的
// 词条仍要用 DP/连写匹配拿到——长输入的常规音节切分枚举有上限，可能漏词
// （例：西安交通大学，输入 xianjiaotongdaxue 时枚举可能切不出该路径）。
// 单字候选始终保留，不受影响。

// mustJoinedDict 用词库 TSV 文本构造词典：每行「词 \t 拼音(空格分隔) \t 词频」。
func mustJoinedDict(t *testing.T, rows ...string) *Dictionary {
	t.Helper()
	d, err := ParseDict(strings.NewReader(strings.Join(rows, "\n") + "\n"))
	if err != nil {
		t.Fatalf("构造词典失败: %v", err)
	}
	return d
}

// collectAllCandidates 输入后收集全部分页候选。
func collectAllCandidates(t *testing.T, s *Session, input string) []Candidate {
	t.Helper()
	s.Input(input)
	comp := s.Compose()
	for comp.Page > 0 {
		comp, _ = s.PrevPage()
	}
	var all []Candidate
	for {
		all = append(all, comp.Candidates...)
		if comp.Page >= comp.PageCount-1 {
			break
		}
		next, err := s.NextPage()
		if err != nil {
			break
		}
		comp = next
	}
	return all
}

func candidatesContain(all []Candidate, text string) bool {
	for _, c := range all {
		if c.Text == text {
			return true
		}
	}
	return false
}

func TestQueryJoinedPinyinMatchesExistingEntries(t *testing.T) {
	d := mustJoinedDict(t,
		"先\txian\t90",
		"西安\txi an\t50",
		"交通\tjiao tong\t40",
		"西安交通大学\txi an jiao tong da xue\t20",
	)
	// 整个连写串命中词库里真实存在的长词。
	got := d.QueryJoinedPinyin("xianjiaotongdaxue", 10)
	if len(got) != 1 || got[0].Word != "西安交通大学" {
		t.Fatalf("连写匹配应恰好命中「西安交通大学」，实际 %+v", got)
	}
	// "xian" 同时是「先」(单音节) 与「西安」(xi+an) 的连写：两个已存在词条都要出。
	got = d.QueryJoinedPinyin("xian", 10)
	if len(got) != 2 || got[0].Word != "先" || got[1].Word != "西安" {
		t.Fatalf("xian 应命中「先」「西安」（按词频降序），实际 %+v", got)
	}
	// 连写不完整/多余字符时不许返回任何候选（不猜测）。
	if got := d.QueryJoinedPinyin("xianjiaotong", 10); len(got) != 0 {
		t.Fatalf("不完整连写不应命中，实际 %+v", got)
	}
	if got := d.QueryJoinedPinyin("xianjiaotongdaxuea", 10); len(got) != 0 {
		t.Fatalf("多余字符不应命中，实际 %+v", got)
	}
}

func TestComposeJoinedMatchLongDictWord(t *testing.T) {
	d := mustJoinedDict(t,
		"先\txian\t90",
		"西安\txi an\t50",
		"交通\tjiao tong\t40",
		"西安交通大学\txi an jiao tong da xue\t20",
	)
	all := collectAllCandidates(t, NewSession(d, nil), "xianjiaotongdaxue")
	if !candidatesContain(all, "西安交通大学") {
		t.Fatalf("词库里有「西安交通大学」，长输入必须能用 DP/连写匹配出该词，实际候选 %+v", all)
	}
	// 反向：候选仍然只能是词典里已存在的词，不得拼合造词。
	assertAllCandidatesInDict(t, d, "xianjiaotongdaxue", all)
}

func TestComposeJoinedMatchUserLearnedWord(t *testing.T) {
	d := mustJoinedDict(t, "先\txian\t90")
	u := mustUserDict(t, filepath.Join(t.TempDir(), "u.json"))
	if err := u.BumpWord("西安交通大学", []string{"xi", "an", "jiao", "tong", "da", "xue"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	all := collectAllCandidates(t, NewSession(d, u), "xianjiaotongdaxue")
	found := false
	for _, c := range all {
		if c.Text == "西安交通大学" && c.FromUser {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("用户学过的长词必须能用连写匹配出带 FromUser 的候选，实际 %+v", all)
	}
}

// TestComposeJoinedMatchBuiltinLongWord：内置词库若含「西安交通大学」，长输入必须
// 能出该词（修复音节切分枚举上限导致的漏匹配）。新词库若不含该词，用 Skip 保持
// 测试稳定（能力本身由合成词典用例锁定）。
func TestComposeJoinedMatchBuiltinLongWord(t *testing.T) {
	d := mustCommonDict(t)
	sylls := []string{"xi", "an", "jiao", "tong", "da", "xue"}
	if !d.Contains("西安交通大学", sylls) {
		t.Skip("内置词库不含「西安交通大学」，能力由合成词典用例覆盖")
	}
	all := collectAllCandidates(t, NewSession(d, nil), strings.Join(sylls, ""))
	if !candidatesContain(all, "西安交通大学") {
		t.Fatalf("内置词库有「西安交通大学」，长输入必须能出该词")
	}
	assertAllCandidatesInDict(t, d, strings.Join(sylls, ""), all)
}

// TestComposeNoFabricationAndSinglesKept：既保证不造词，也保证单字始终在。
func TestComposeNoFabricationAndSinglesKept(t *testing.T) {
	d := mustCommonDict(t)
	for _, input := range []string{"nihaoma", "womende", "zhonghuarenmingongheguo", "xianjiaotongdaxue"} {
		all := collectAllCandidates(t, NewSession(d, nil), input)
		assertAllCandidatesInDict(t, d, input, all)
		hasSingle := false
		for _, c := range all {
			if len([]rune(c.Text)) == 1 {
				hasSingle = true
				break
			}
		}
		if !hasSingle {
			t.Fatalf("%s: 候选里必须始终保留单字，实际 %+v", input, all)
		}
	}
}
