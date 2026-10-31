package engine

import (
	"testing"
)

// TestNormalizePinyinContract：词库生成工具复用的公共归一化 API。
// 覆盖音调符号、数字后缀、ü→v、连写自动切分与非法输入。
func TestNormalizePinyinContract(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"音调符号", "nǐ hǎo", []string{"ni", "hao"}},
		{"数字后缀", "ni3hao3", []string{"ni", "hao"}},
		{"ü 归一化", "nü", []string{"nv"}},
		{"连写切分", "hundunshurufa", []string{"hun", "dun", "shu", "ru", "fa"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizePinyin(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("NormalizePinyin(%q) = %v，期望 %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("NormalizePinyin(%q) = %v，期望 %v", c.in, got, c.want)
				}
			}
		})
	}
	if got := NormalizePinyin("not a pinyin!"); got != nil {
		t.Fatalf("非法拼音应返回 nil，实际 %v", got)
	}
}

// TestPrevWordContract：最近一次上屏词（渐进上屏/联想按字前缀查询用）。
func TestPrevWordContract(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	if _, err := s.Input("nihao"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := s.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if got := s.PrevWord(); got != "你好" {
		t.Fatalf("PrevWord = %q，期望「你好」", got)
	}
}

// TestPageSizeContract：每页候选数必须与 ibus 壳/文档一致（6）。
func TestPageSizeContract(t *testing.T) {
	if got := PageSize(); got != 6 {
		t.Fatalf("PageSize() = %d，期望 6", got)
	}
}

// TestFindSentenceCandidatesWrapper：公共包装与 Tokens 版本行为一致。
func TestFindSentenceCandidatesWrapper(t *testing.T) {
	d := mustCommonDict(t)
	cands := FindSentenceCandidates(d, nil, []string{"wo", "men", "de"}, 6)
	found := false
	for _, c := range cands {
		if c.Text == "我们的" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("FindSentenceCandidates(wo'men'de) 应含「我们的」，实际 %+v", cands)
	}
}

// TestQueryPhrasesPrefixContract：用户合成词组按音节前缀查询（联想用）。
func TestQueryPhrasesPrefixContract(t *testing.T) {
	u := mustUserDict(t, "")
	if err := u.BumpPhraseWithSegs("现在时间", []string{"xian", "zai", "shi", "jian"},
		[]string{"现在", "时间"}); err != nil {
		t.Fatalf("BumpPhraseWithSegs 失败: %v", err)
	}
	got := u.QueryPhrasesPrefix([]string{"xian", "zai"})
	if len(got) == 0 || got[0].Word != "现在时间" {
		t.Fatalf("QueryPhrasesPrefix(xian'zai) 应含「现在时间」，实际 %+v", got)
	}
	if got := u.QueryPhrasesPrefix(nil); got != nil {
		t.Fatalf("空前缀应返回 nil，实际 %+v", got)
	}
}

// TestQueryVariantsContract：模糊变体展开（关闭/残缺前缀/开启/空路径）。
func TestQueryVariantsContract(t *testing.T) {
	if got := queryVariants([]string{"zhang"}, "", false); len(got) != 1 || got[0][0] != "zhang" {
		t.Fatalf("模糊关闭应原样返回，实际 %v", got)
	}
	if got := queryVariants([]string{"zhang"}, "z", true); len(got) != 1 || got[0][0] != "zhang" {
		t.Fatalf("残缺前缀不应做模糊展开，实际 %v", got)
	}
	got := queryVariants([]string{"zhang"}, "", true)
	if len(got) <= 1 {
		t.Fatalf("zhang 开启模糊应有多条变体，实际 %v", got)
	}
	if got[0][0] != "zhang" {
		t.Fatalf("首条必须是原路径，实际 %v", got)
	}
	for _, v := range got {
		if len(v) != 1 || !IsSyllable(v[0]) {
			t.Fatalf("模糊变体必须是合法音节，实际 %v", v)
		}
	}
	if got := queryVariants(nil, "", true); len(got) != 1 || len(got[0]) != 0 {
		t.Fatalf("空路径应原样返回，实际 %v", got)
	}
}
