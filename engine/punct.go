package engine

import "fmt"

// punctMap 是 ASCII 标点键 → 中文标点的直映表。
// 引号类（" '）不在此表：它们按开闭交替配对，状态在 Session。
var punctMap = map[rune]string{
	',':  "，",
	'.':  "。",
	';':  "；",
	':':  "：",
	'?':  "？",
	'!':  "！",
	'(':  "（",
	')':  "）",
	'[':  "【",
	']':  "】",
	'<':  "《",
	'>':  "》",
	'\\': "、",
	'~':  "～",
	'^':  "……",
}

// punctCycles 是同键连按的循环序列（微软拼音惯例：全角与半角交替，
// 循环停留于末项——句号连按得 。.. ..）。不在表内的标点固定输出。
var punctCycles = map[rune][]string{
	'.': {"。", "."},
	',': {"，", ","},
}

// IsPunctKey 报告该字符是否为引擎接管的标点键。
// 注意 ' 的双重身份：组词中是音节分隔符（应送 Input），空组词时才是标点键。
func IsPunctKey(r rune) bool {
	if _, ok := punctMap[r]; ok {
		return true
	}
	return r == '"' || r == '\''
}

// Punct 处理一个标点键，返回本次应上屏的全部文本：
//   - 空组词：输出映射后的中文标点；
//   - 组词中：先上屏首选候选（复用 Select 路径，记调频与学习），
//     再输出中文标点；无候选的残缺输入直接丢弃（保留学习上下文）。
//
// 组词中的 ' 是音节分隔符，不作为标点，返回错误（调用方应把组词中的
// ' 送 Input）。映射表见 punctMap；双/单引号各自按开闭交替配对；
// 句号/逗号支持同键连按循环；半角模式下标点原样输出（ToggleHalfWidth）。
func (s *Session) Punct(key string) (string, error) {
	defer s.resetSeqIfEmpty() // 标点丢弃残缺输入/上屏首选后，本轮渐进段累积作废
	runes := []rune(key)
	if len(runes) != 1 {
		return "", fmt.Errorf("标点键必须是单字符，收到 %q", key)
	}
	r := runes[0]
	if !IsPunctKey(r) {
		return "", fmt.Errorf("未知标点键 %q", key)
	}
	if s.input != "" && r == '\'' {
		return "", fmt.Errorf("组词中的 ' 是音节分隔符，不作为标点提交")
	}
	// 半角模式：标点原样输出（无循环、引号不配对），组词合并行为不变
	if s.halfWidth {
		out := ""
		if s.input != "" {
			comp := s.Compose()
			if len(comp.Candidates) > 0 {
				committed, err := s.Select(0)
				if err != nil {
					return "", err
				}
				out = committed
			} else {
				s.input = ""
			}
		}
		s.resetPunctCycle()
		return out + string(r), nil
	}
	// 同键连按循环：上一次标点输出与本键相同（其间未被其他输入/上屏
	// 打断——Input/Backspace/Reset/Select 会重置循环位置）则推进序列
	idx := 0
	if s.lastPunct == r {
		idx = s.lastPunctIdx + 1
	}
	out := ""
	if s.input != "" {
		comp := s.Compose()
		if len(comp.Candidates) > 0 {
			committed, err := s.Select(0)
			if err != nil {
				return "", err
			}
			out = committed
		} else {
			s.input = "" // 残缺输入无候选：丢弃，不清学习上下文
		}
	}
	s.lastPunct = r
	s.lastPunctIdx = idx
	return out + s.mapPunct(r, idx), nil
}

// mapPunct 返回标点键本次应输出的标点：引号按开闭交替翻转状态；
// 配置了循环序列的键（句号/逗号）按同键连按次数轮转；其余固定映射。
func (s *Session) mapPunct(r rune, idx int) string {
	switch r {
	case '"':
		s.dquoteOpen = !s.dquoteOpen
		if s.dquoteOpen {
			return "“"
		}
		return "”"
	case '\'':
		s.squoteOpen = !s.squoteOpen
		if s.squoteOpen {
			return "‘"
		}
		return "’"
	}
	if cycle, ok := punctCycles[r]; ok {
		return cycle[idx%len(cycle)]
	}
	return punctMap[r]
}
