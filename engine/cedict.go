package engine

import (
	"fmt"
	"io"
	"strings"
)

// cedictDefaultFreq 是 CC-CEDICT 词条的合成词频：低于种子词库的常用
// 词频，保证与自有词库合并时用户精选词条优先。
const cedictDefaultFreq = 30

// looksLikeCCCEDICT 依据首个非注释行判断内容是否为 CC-CEDICT u8
// 格式：繁体 简体 [拼音] /英文释义/（空格分隔，第三个词以 [ 开头）。
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

// cedictWordOK 过滤非纯汉字词条（CC-CEDICT 含少量带拉丁字母、
// 全角符号的条目），仅保留 CJK 汉字（含 〇 与兼容区）。
func cedictWordOK(word string) bool {
	if word == "" {
		return false
	}
	for _, r := range word {
		switch {
		case r >= 0x3400 && r <= 0x9FFF: // CJK 扩展 A + 基本区
		case r >= 0xF900 && r <= 0xFAFF: // 兼容表意区
		case r == 0x3007: // 〇
		default:
			return false
		}
	}
	return true
}

// ParseCCCEDICT 解析 CC-CEDICT u8 格式词库（lenient：非法行跳过并
// 计数）。取简体列作为词条；拼音剥离方括号与声调数字、u: 记法归一
// 为 v；词频统一合成为 cedictDefaultFreq（合并时用户词库优先）。
// 文件头部的许可证与署名注释块会被跳过——重新分发导出结果时请
// 遵循 CC-CEDICT 的 CC-BY-SA 4.0 要求保留署名。
func ParseCCCEDICT(r io.Reader) (*Dictionary, int, error) {
	d := NewDictionary()
	skipped := 0
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, fmt.Errorf("读取 CC-CEDICT 词库失败: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 真实格式（空格分隔、CRLF 行尾已由 TrimSpace 处理）：
		// 繁体 简体 [拼音] /英文释义/
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
		word := head[1] // 简体列
		pinyin := line[bra+1 : ket]
		if !cedictWordOK(word) {
			skipped++
			continue
		}
		sylls := normalizePinyin(strings.ReplaceAll(pinyin, "u:", "v"))
		if sylls == nil {
			skipped++
			continue
		}
		d.add(word, sylls, cedictDefaultFreq)
	}
	return d, skipped, nil
}
