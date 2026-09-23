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
// ' 送 Input）。映射表见 punctMap；双/单引号各自按开闭交替配对。
func (s *Session) Punct(key string) (string, error) {
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
	return out + s.mapPunct(r), nil
}

// mapPunct 返回标点键对应的中文标点；引号按开闭交替翻转状态。
func (s *Session) mapPunct(r rune) string {
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
	return punctMap[r]
}
