// Package dict 是 Go 工具链（导入/词典生成）共享的词库格式能力：
// TSV / Rime dict.yaml / CC-CEDICT 解析、拼音归一化与音节校验、TSV 导出。
//
// 运行时引擎（匹配/排序/组词）的唯一实现是 Rust（crates/chaoset-engine），
// 本包不复制任何引擎算法，只保留「词库文件的读写与规范化」这一离线工具
// 职责，供 chaoset-import / chaoset-dictgen 使用。
package dict

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Entry 是一条词库记录：词、音节序列（ü 写作 v）、词频。
type Entry struct {
	Word      string
	Syllables []string
	Freq      int
}

// parseErrSamples 是宽松解析时写进警告的坏行样本上限。
const parseErrSamples = 3

type parseStats struct {
	skipped int
	samples []string
}

func (s *parseStats) skip(lineNo int, reason string) {
	s.skipped++
	if len(s.samples) < parseErrSamples {
		s.samples = append(s.samples, fmt.Sprintf("第 %d 行%s", lineNo, reason))
	}
}

// ParseAuto 解析 TSV、Rime dict.yaml 或 CC-CEDICT 词库（自动识别）；
// 面向导入场景：宽容模式，无法归一化为合法音节的行跳过并计数。
func ParseAuto(r io.Reader) ([]*Entry, int, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, fmt.Errorf("读取词库失败: %w", err)
	}
	if looksLikeCCCEDICT(string(content)) {
		entries, skipped, err := parseCCCEDICT(string(content))
		return entries, skipped, err
	}
	entries, st, err := parseTSVLenient(strings.NewReader(stripYAMLHeader(string(content))))
	if err != nil {
		return nil, 0, err
	}
	return entries, st.skipped, nil
}

// stripYAMLHeader 若内容以 --- 开头（Rime dict.yaml），去掉到第二条
// --- 或 ... 为止的 YAML 头。
func stripYAMLHeader(content string) string {
	lines := strings.Split(content, "\n")
	start := 0
	for start < len(lines) {
		trim := strings.TrimSpace(lines[start])
		if trim == "" || strings.HasPrefix(trim, "#") {
			start++
			continue
		}
		break
	}
	if start >= len(lines) || strings.TrimSpace(lines[start]) != "---" {
		return content
	}
	start++
	for start < len(lines) {
		trim := strings.TrimSpace(lines[start])
		if trim == "---" || trim == "..." {
			return strings.Join(lines[start+1:], "\n")
		}
		start++
	}
	return ""
}

// parseTSVLenient 解析「词\t拼音\t词频」TSV：能按严格规则解析的行原样保留
// （与严格加载结果一致），否则尝试归一化（去声调、ü→v、连写切分），仍失败
// 才跳过并计数；缺省词频 20，上限压缩到 99（用户学习权重远高于静态词频）。
func parseTSVLenient(r io.Reader) ([]*Entry, parseStats, error) {
	var st parseStats
	entries := make([]*Entry, 0, 256)
	seen := make(map[string]int, 256)
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			st.skip(lineNo, "缺少制表符分隔（应为「词\t拼音\t词频」）")
			continue
		}
		word := strings.TrimSpace(parts[0])
		sylls := strings.Fields(parts[1])
		freq := 0
		if len(parts) >= 3 {
			if n, nerr := strconv.Atoi(strings.TrimSpace(parts[2])); nerr == nil {
				freq = n
			}
		}
		canonical := word != "" && len(sylls) > 0
		if canonical {
			for _, s := range sylls {
				if !IsSyllable(s) {
					canonical = false
					break
				}
			}
		}
		if !canonical {
			sylls = normalizePinyin(parts[1])
			if word == "" || len(sylls) == 0 {
				st.skip(lineNo, "词或拼音无法解析为合法音节")
				continue
			}
		}
		if freq <= 0 {
			freq = 20
		}
		if freq > 99 {
			freq = 99
		}
		key := word + "\x00" + strings.Join(sylls, " ")
		if prev, ok := seen[key]; ok {
			if freq > entries[prev].Freq {
				entries[prev].Freq = freq
			}
			continue
		}
		seen[key] = len(entries)
		entries = append(entries, &Entry{Word: word, Syllables: sylls, Freq: freq})
	}
	if err := sc.Err(); err != nil {
		return nil, st, fmt.Errorf("读取词库失败: %w", err)
	}
	return entries, st, nil
}

// cedictDefaultFreq 是 CC-CEDICT 词条的合成词频：低于种子词库的常用词频，
// 保证与自有词库合并时用户精选词条优先。
const cedictDefaultFreq = 30

// looksLikeCCCEDICT 依据首个非注释行判断内容是否为 CC-CEDICT u8 格式：
// 繁体 简体 [拼音] /英文释义/（空格分隔，第三个词以 [ 开头）。
func looksLikeCCCEDICT(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tokens := strings.Fields(line)
		return len(tokens) >= 3 && strings.HasPrefix(tokens[2], "[")
	}
	return false
}

// cedictWordOK 过滤非纯汉字词条，仅保留 CJK 汉字（含 〇 与兼容区）。
func cedictWordOK(word string) bool {
	if word == "" {
		return false
	}
	for _, r := range word {
		switch {
		case r >= 0x3400 && r <= 0x9FFF:
		case r >= 0xF900 && r <= 0xFAFF:
		case r == 0x3007:
		default:
			return false
		}
	}
	return true
}

// parseCCCEDICT 解析 CC-CEDICT u8 格式（lenient）：取简体列、拼音剥离
// 方括号与声调数字、u: 归一为 v，词频统一 cedictDefaultFreq。
func parseCCCEDICT(content string) ([]*Entry, int, error) {
	skipped := 0
	entries := make([]*Entry, 0, 1024)
	seen := make(map[string]int, 1024)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		bra := strings.Index(line, "[")
		ket := strings.Index(line, "]")
		if bra < 0 || ket <= bra {
			skipped++
			continue
		}
		head := strings.Fields(line[:bra])
		if len(head) < 2 {
			skipped++
			continue
		}
		word := head[1]
		if !cedictWordOK(word) {
			skipped++
			continue
		}
		sylls := normalizePinyin(strings.ReplaceAll(line[bra+1:ket], "u:", "v"))
		if sylls == nil {
			skipped++
			continue
		}
		key := word + "\x00" + strings.Join(sylls, " ")
		if prev, ok := seen[key]; ok {
			if cedictDefaultFreq > entries[prev].Freq {
				entries[prev].Freq = cedictDefaultFreq
			}
			continue
		}
		seen[key] = len(entries)
		entries = append(entries, &Entry{Word: word, Syllables: sylls, Freq: cedictDefaultFreq})
	}
	return entries, skipped, nil
}
