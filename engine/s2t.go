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

var (
	t2sOnce  sync.Once
	t2sChars map[rune]rune // 单字反向映射（繁体 → 首个推荐简体）
)

// t2sLazyInit 从 STCharacters.txt 构建繁→简反向映射：原表一行「简体 繁1
// 繁2 …」，反向时每个繁体候选指回该行首列的简体（OpenCC 推荐字形）。
func t2sLazyInit() {
	t2sOnce.Do(func() {
		t2sChars = make(map[rune]rune, 8192)
		data, err := s2tDataFS.ReadFile("dp_data/STCharacters.txt")
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
			keyR := []rune(key)
			if len(keyR) != 1 {
				continue
			}
			for _, trad := range strings.Fields(val) {
				tr := []rune(trad)
				if len(tr) == 1 {
					if _, dup := t2sChars[tr[0]]; !dup {
						t2sChars[tr[0]] = keyR[0]
					}
				}
			}
		}
	})
}

// ToSimplified 把文本中的繁体字回退为简体（逐字反向映射，繁→简无歧义
// 场景：一繁多简时取 OpenCC 首个推荐简体）。词典生成端用它识别并剔除
// 繁体变体词条——简体输入法的 -traditional 输出是运行时转换
// （ToTraditional），词典本身不需要繁体词条。
func ToSimplified(s string) string {
	t2sLazyInit()
	runes := []rune(s)
	changed := false
	for i, r := range runes {
		if s, ok := t2sChars[r]; ok {
			runes[i] = s
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(runes)
}
