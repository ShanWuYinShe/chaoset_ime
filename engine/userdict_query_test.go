package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// queryTestClock 是查询类测试统一使用的固定时刻，便于按调频公式精确断言 Freq。
var queryTestClock = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// newQueryTestUserDict 构造注入了固定时钟的用户词典（时钟不前进，时间衰减恒为 1.0）。
func newQueryTestUserDict(t *testing.T) (*UserDict, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user.json")
	u := mustUserDict(t, path)
	u.setClock(func() time.Time { return queryTestClock })
	return u, path
}

// bumpN 连续调用 n 次 bump，任一失败即终止测试。
func bumpN(t *testing.T, n int, f func() error) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := f(); err != nil {
			t.Fatalf("bump 失败: %v", err)
		}
	}
}

// 回归测试：QueryTokensExact 必须能查到 BumpWord 记录的调频词。
// 历史缺陷：查询侧用 "\t" 拆分 userKey 生成的 "词\x00音节" 键，
// 拆分恒失败导致整个循环体 continue，简拼与整句 DP 路径永远看不到用户调频词。
func TestUserDictQueryTokensExactFindsBumpedWord(t *testing.T) {
	u, _ := newQueryTestUserDict(t)
	bumpN(t, 3, func() error { return u.BumpWord("中国", []string{"zhong", "guo"}) })

	// ① 简拼 token（声母前缀）：z 匹配 zhong、g 匹配 guo
	got := u.QueryTokensExact([]Token{{Text: "z"}, {Text: "g"}}, 10)
	if len(got) != 1 || got[0].Word != "中国" {
		t.Fatalf("简拼 token 查询应命中用户调频词「中国」，实际: %+v", got)
	}
	// 调频公式：次数 × UserBoostPerCount × 时间衰减（当天为 1.0）
	if want := 3 * UserBoostPerCount; got[0].Freq != want {
		t.Fatalf("简拼查询 Freq = %d, 期望 %d", got[0].Freq, want)
	}
	if joined := strings.Join(got[0].Syllables, " "); joined != "zhong guo" {
		t.Fatalf("命中词条的 Syllables = %q, 期望 %q", joined, "zhong guo")
	}

	// ② 全拼 token 同样命中，且 Freq 一致
	got = u.QueryTokensExact([]Token{{Text: "zhong", IsExact: true}, {Text: "guo", IsExact: true}}, 10)
	if len(got) != 1 || got[0].Word != "中国" {
		t.Fatalf("全拼 token 查询应命中用户调频词「中国」，实际: %+v", got)
	}
	if want := 3 * UserBoostPerCount; got[0].Freq != want {
		t.Fatalf("全拼查询 Freq = %d, 期望 %d", got[0].Freq, want)
	}

	// ③ 单字调频词也必须能被单 token 查询命中（简拼单字槽）
	bumpN(t, 5, func() error { return u.BumpWord("我", []string{"wo"}) })
	single := u.QueryTokensExact([]Token{{Text: "w"}}, 10)
	if len(single) != 1 || single[0].Word != "我" {
		t.Fatalf("单字简拼查询应命中「我」，实际: %+v", single)
	}
	if want := 5 * UserBoostPerCount; single[0].Freq != want {
		t.Fatalf("单字查询 Freq = %d, 期望 %d", single[0].Freq, want)
	}
}

// 回归测试：QueryTokens 对用户调频词同样要区分 exactMatches 与 prefixMatches。
func TestUserDictQueryTokensSplitsWordsByLength(t *testing.T) {
	u, _ := newQueryTestUserDict(t)
	bumpN(t, 2, func() error { return u.BumpWord("人民", []string{"ren", "min"}) })
	bumpN(t, 1, func() error { return u.BumpWord("人民币", []string{"ren", "min", "bi"}) })
	bumpN(t, 1, func() error { return u.BumpWord("人民日报", []string{"ren", "min", "ri", "bao"}) })

	exacts, prefixes := u.QueryTokens([]Token{{Text: "r"}, {Text: "m"}}, 10)
	if len(exacts) != 1 || exacts[0].Word != "人民" {
		t.Fatalf("exactMatches 期望包含且仅包含「人民」，实际: %+v", exacts)
	}
	if want := 2 * UserBoostPerCount; exacts[0].Freq != want {
		t.Fatalf("exactMatches Freq = %d, 期望 %d", exacts[0].Freq, want)
	}
	if len(prefixes) != 2 {
		t.Fatalf("prefixMatches 期望包含「人民币」「人民日报」两条，实际: %+v", prefixes)
	}
	// 同分（各 1 次）时按字数升序：人民币(3 字) 在 人民日报(4 字) 之前
	if prefixes[0].Word != "人民币" || prefixes[1].Word != "人民日报" {
		t.Fatalf("prefixMatches 顺序不符，实际: %+v", prefixes)
	}
	for _, e := range prefixes {
		if want := UserBoostPerCount; e.Freq != want {
			t.Fatalf("%s 的 Freq = %d, 期望 %d", e.Word, e.Freq, want)
		}
	}
	if got := strings.Join(prefixes[1].Syllables, " "); got != "ren min ri bao" {
		t.Fatalf("prefixMatches 的 Syllables = %q, 期望 %q", got, "ren min ri bao")
	}
}

// 回归测试：同一词既有用户短语记录又有调频记录时，查询结果不得重复（seen 去重）。
func TestUserDictQueryTokensDedupPhraseAndWord(t *testing.T) {
	u, _ := newQueryTestUserDict(t)
	bumpN(t, 1, func() error { return u.BumpWord("单位", []string{"dan", "wei"}) })
	bumpN(t, 2, func() error { return u.BumpPhrase("单位", []string{"dan", "wei"}) })

	tokens := []Token{{Text: "d"}, {Text: "w"}}

	exacts, prefixes := u.QueryTokens(tokens, 10)
	if len(exacts) != 1 || exacts[0].Word != "单位" {
		t.Fatalf("QueryTokens 期望「单位」只出现一次，实际 exactMatches: %+v, prefixMatches: %+v", exacts, prefixes)
	}
	if len(prefixes) != 0 {
		t.Fatalf("「单位」不应同时出现在 prefixMatches，实际: %+v", prefixes)
	}
	// 短语先入 seen，因此保留短语得分：2 × UserBoostPerCount
	if want := 2 * UserBoostPerCount; exacts[0].Freq != want {
		t.Fatalf("去重后 Freq = %d, 期望 %d", exacts[0].Freq, want)
	}

	exactsOnly := u.QueryTokensExact(tokens, 10)
	if len(exactsOnly) != 1 || exactsOnly[0].Word != "单位" {
		t.Fatalf("QueryTokensExact 期望「单位」只出现一次，实际: %+v", exactsOnly)
	}
}

// 回归测试：落盘键格式必须保持 "词\x00音节"，重载后查询仍能命中，
// 且 ExportTSV 与查询侧对同一个键的拆分方式一致。
func TestUserDictQueryTokensAfterReloadKeepsKeyFormat(t *testing.T) {
	u, path := newQueryTestUserDict(t)
	bumpN(t, 3, func() error { return u.BumpWord("中国", []string{"zhong", "guo"}) })
	if err := u.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	// 磁盘格式向后兼容：键仍是 词 + NUL + 音节，分隔符不得改成 "\t" 等其它字符
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取用户词典失败: %v", err)
	}
	// JSON 中 NUL 被转义为 \u0000
	wantKey := "中国" + "\\u0000" + "zhong guo"
	if !strings.Contains(string(data), wantKey) {
		t.Fatalf("落盘键格式应为 词 + NUL + 音节（JSON 转义后 %q），实际文件内容：\n%s", wantKey, data)
	}

	reloaded, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重载用户词典失败: %v", err)
	}
	reloaded.setClock(func() time.Time { return queryTestClock })
	got := reloaded.QueryTokensExact([]Token{{Text: "z"}, {Text: "g"}}, 10)
	if len(got) != 1 || got[0].Word != "中国" {
		t.Fatalf("重载后简拼查询应命中「中国」，实际: %+v", got)
	}
	if want := 3 * UserBoostPerCount; got[0].Freq != want {
		t.Fatalf("重载后 Freq = %d, 期望 %d", got[0].Freq, want)
	}

	// ExportTSV 对同一个键的拆分必须与查询侧一致
	var b strings.Builder
	if err := reloaded.ExportTSV(&b, false); err != nil {
		t.Fatalf("ExportTSV 失败: %v", err)
	}
	if !strings.Contains(b.String(), "中国\tzhong guo\t3\t") {
		t.Fatalf("ExportTSV 应按 词 + TAB + 拼音 输出，实际：\n%s", b.String())
	}
}
