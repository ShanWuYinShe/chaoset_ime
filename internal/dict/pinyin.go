package dict

import (
	"strings"
)

// 音调符号 → 基础字母（ü 系 → v），覆盖 Rime 词库常见的标调写法。
var toneReplacer = strings.NewReplacer(
	"ā", "a", "á", "a", "ǎ", "a", "à", "a",
	"ē", "e", "é", "e", "ě", "e", "è", "e",
	"ī", "i", "í", "i", "ǐ", "i", "ì", "i",
	"ō", "o", "ó", "o", "ǒ", "o", "ò", "o",
	"ū", "u", "ú", "u", "ǔ", "u", "ù", "u",
	"ǖ", "v", "ǘ", "v", "ǚ", "v", "ǜ", "v",
	"ü", "v",
)

// specialSylls 处理 Rime 词库里 ü 写作 u 的常见音节（略/虐类）。
var specialSylls = map[string]string{
	"lue": "lve",
	"nue": "nve",
}

// stripToneDigits 删除拼音编码里的调号数字（1-5）。数字在拼音编码里只可能
// 是声调标记；空格分隔的 `ni3 hao3` 与连写 `ni3hao3` 都要支持（2026-10-31
// 修复：此前只 TrimRight 末尾数字，连写带调号整串归一化失败）。
func stripToneDigits(tok string) string {
	return strings.Map(func(r rune) rune {
		if r >= '1' && r <= '5' {
			return -1
		}
		return r
	}, tok)
}

// normalizePinyin 把一行拼音编码归一化为音节切片：
// 全小写、去音调符号（ü 系→v）、去音调数字后缀。
// 任一音节非法则返回 nil（调用方跳过该行）。
func normalizePinyin(code string) []string {
	code = strings.ReplaceAll(code, "'", " ")
	tokens := strings.Fields(code)
	sylls := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.ToLower(tok)
		tok = toneReplacer.Replace(tok)
		tok = stripToneDigits(tok)
		if alt, ok := specialSylls[tok]; ok {
			tok = alt
		}
		if !IsSyllable(tok) {
			// 别人编辑/导出的词库常见"连写拼音"（hundun、shurufa）：整串能
			// 切成合法音节就自动切分，切不干净才按无效行跳过。
			parts := segmentRunon(tok)
			if parts == nil {
				return nil
			}
			sylls = append(sylls, parts...)
			continue
		}
		sylls = append(sylls, tok)
	}
	return sylls
}

// NormalizePinyin 把拼音编码归一化为音节切片（全小写、去音调符号、
// ü→v、去音调数字后缀、连写自动切分）；任一首节无法归一化时返回 nil。
// 词库生成工具（cmd/chaoset-dictgen）复用它，保证生成与导入走同一套规则。
func NormalizePinyin(code string) []string { return normalizePinyin(code) }

// segmentRunon 把连写拼音整串切成合法音节（hundunshurufa → hun dun shu ru fa）。
// 仅当整串都能切完才返回切片，否则返回 nil；切法取"最长首音节优先"。
func segmentRunon(s string) []string {
	const maxRunOn = 64 // 异常长串直接放弃，避免 O(n²) 退化
	if s == "" || len(s) > maxRunOn {
		return nil
	}
	n := len(s)
	dp := make([][]string, n+1)
	dp[0] = []string{}
	for i := 1; i <= n; i++ {
		for j := 0; j < i; j++ {
			if dp[j] == nil {
				continue
			}
			if IsSyllable(s[j:i]) {
				dp[i] = append(append([]string{}, dp[j]...), s[j:i])
				break // j 递增 → 首音节最长者优先
			}
		}
	}
	return dp[n]
}
