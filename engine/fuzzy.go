package engine

import (
	"sort"
	"strings"
)

// 模糊音规则（微软拼音常用集）：
//   - 声母对：zh/z、ch/c、sh/s、n/l
//   - 韵母对：an/ang、en/eng、in/ing（含 ian/iang、uan/uang 组合韵母）
//
// 韵母对按切片排列以实现「最长后缀优先」匹配（liang 命中 iang 而非 ang）。
var (
	fuzzyInitials = map[string]string{
		"zh": "z", "ch": "c", "sh": "s",
		"z": "zh", "c": "ch", "s": "sh",
		"n": "l", "l": "n",
	}
	fuzzyFinalPairs = [][2]string{
		{"iang", "ian"}, {"ian", "iang"},
		{"uang", "uan"}, {"uan", "uang"},
		{"ang", "an"}, {"an", "ang"},
		{"eng", "en"}, {"en", "eng"},
		{"ing", "in"}, {"in", "ing"},
	}
)

// fuzzyVariants 返回音节的模糊等价集合（首元素为音节本身，其余按
// 字典序）。声母替换与韵母替换可叠加（zhang → zang / zhan / zan），
// 所有变体均经 IsSyllable 校验为合法音节。
func fuzzyVariants(syl string) []string {
	set := map[string]bool{syl: true}
	// 两阶段展开：先声母替换，再对每个中间结果做韵母替换，
	// 使「声母+韵母」双替换的组合（zhang→zan）也能生成。
	stage1 := map[string]bool{}
	for v := range set {
		stage1[v] = true
		for pre, alt := range fuzzyInitials {
			if strings.HasPrefix(v, pre) {
				cand := alt + v[len(pre):]
				if IsSyllable(cand) {
					stage1[cand] = true
				}
			}
		}
	}
	for v := range stage1 {
		set[v] = true
		for _, pair := range fuzzyFinalPairs {
			suf, alt := pair[0], pair[1]
			if strings.HasSuffix(v, suf) {
				cand := v[:len(v)-len(suf)] + alt
				if IsSyllable(cand) {
					set[cand] = true
				}
				break // 最长后缀优先：命中即止
			}
		}
	}
	out := []string{syl}
	rest := make([]string, 0, len(set))
	for v := range set {
		if v != syl {
			rest = append(rest, v)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// maxFuzzyPaths 限制单条切分路径的模糊变体查询数（笛卡尔积封顶）。
const maxFuzzyPaths = 8

// queryVariants 展开一条切分路径的查询形态：模糊关闭、含残缺音节
// （残缺前缀查询不做模糊，避免组合爆炸）或空路径时原样返回；开启时
// 对完整音节的模糊变体做笛卡尔积（封顶 maxFuzzyPaths，首条为原路径）。
func queryVariants(sylls []string, frag string, fuzzy bool) [][]string {
	if !fuzzy || frag != "" || len(sylls) == 0 {
		return [][]string{sylls}
	}
	sets := make([][]string, len(sylls))
	for i, syl := range sylls {
		sets[i] = fuzzyVariants(syl)
	}
	out := make([][]string, 0, maxFuzzyPaths)
	cur := make([]string, 0, len(sylls))
	var build func(i int)
	build = func(i int) {
		if len(out) >= maxFuzzyPaths {
			return
		}
		if i == len(sets) {
			out = append(out, append([]string(nil), cur...))
			return
		}
		for _, v := range sets[i] {
			cur = append(cur, v)
			build(i + 1)
			cur = cur[:len(cur)-1]
		}
	}
	build(0)
	return out
}
