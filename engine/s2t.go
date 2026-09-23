package engine

import (
	"embed"
	"strings"
	"sync"
)

// 简繁转换数据来自 OpenCC（Apache-2.0，见 dp_data/README.md）：
//   - STCharacters.txt 单字映射（繁体多候选取首个推荐项）
//   - STPhrases.txt 词级映射（一词一行，解决 发/干/面/里/后 等歧义字
//     的上下文判定，如 头发→頭髮、公里→公里）
//
// 转换策略：词级最长匹配优先，未命中词组的单字查字表，表外字符
// 原样保留。数据经 go:embed 内嵌，首次使用时惰性解析（sync.Once）。
//
//go:embed dp_data/STCharacters.txt dp_data/STPhrases.txt
var s2tDataFS embed.FS

var (
	s2tOnce      sync.Once
	s2tChars     map[rune]string   // 单字映射
	s2tPhraseMap map[string]string // 词级映射（简体词 → 繁体词）
	s2tMaxPhrase int               // 最长词组长度（字符）
)

// s2tLazyInit 解析内嵌数据，构建单字表与词组表。仅执行一次。
func s2tLazyInit() {
	s2tOnce.Do(func() {
		s2tChars = make(map[rune]string, 4096)
		s2tPhraseMap = make(map[string]string, 50000)
		s2tMaxPhrase = 0

		load := func(name string, phrase bool) {
			data, err := s2tDataFS.ReadFile("dp_data/" + name)
			if err != nil {
				panic("engine: 读取内嵌简繁数据失败: " + err.Error())
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				key, val, ok := strings.Cut(line, "\t")
				if !ok || key == "" || val == "" {
					continue
				}
				// 多候选项以空格分隔，取首个（OpenCC 推荐字形）
				if i := strings.IndexByte(val, ' '); i >= 0 {
					val = val[:i]
				}
				if phrase {
					s2tPhraseMap[key] = val
					if n := len([]rune(key)); n > s2tMaxPhrase {
						s2tMaxPhrase = n
					}
				} else {
					r := []rune(key)
					if len(r) == 1 {
						s2tChars[r[0]] = val
					}
				}
			}
		}
		load("STCharacters.txt", false)
		load("STPhrases.txt", true)
	})
}

// ToTraditional 把文本转换为繁体：词级最长匹配优先（OpenCC 词组表），
// 未命中词组的单字查字表，表外字符原样保留。ASCII 与标点不受影响。
func ToTraditional(s string) string {
	s2tLazyInit()
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); {
		// 词级最长匹配
		matched := false
		for l := s2tMaxPhrase; l >= 2; l-- {
			if i+l > len(runes) {
				continue
			}
			if t, ok := s2tPhraseMap[string(runes[i:i+l])]; ok {
				b.WriteString(t)
				i += l
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if t, ok := s2tChars[runes[i]]; ok {
			b.WriteString(t)
		} else {
			b.WriteRune(runes[i])
		}
		i++
	}
	return b.String()
}
