package main

import (
	"io/fs"
)

// checks_input_case.go：三端「字母入组词保留大小写」契约。
//
// 用户诉求（2026-11-21）：中文模式下直接敲大写字母（Shift/大写锁定）不能
// 被丢弃或放行给宿主，否则为了输一段英文得来回切换输入法。正确行为：
//  1. 三端壳把 a-z / A-Z 都转发给引擎 input（大写不 passthrough）；
//  2. 引擎把原始大小写保存在组词串里，回车（CommitRaw）原样上屏；
//  3. 拼音匹配走小写视图（matchInput），候选与全小写一致。
//
// 这项契约跨「Go 引擎 + 三个壳」，任一端回退（例如壳重新只认 a-z、引擎
// 重新 ToLower 后过滤）都会让用户重新被迫切输入法，所以静态锁死。
func checkLetterCaseComposition(fsys fs.FS, problems *[]string) {
	const group = groupLetterCaseComposition

	// 引擎：接收大小写原样保存 + 匹配用小写视图 + 预编辑保留大小写。
	checkContains(fsys, problems, group, "engine/session.go",
		"func (s *Session) matchInput() string {",
		"strings.ToLower(s.input)",
		"r >= 'A' && r <= 'Z'",
		"s.input += string(r)", // 原样保存（先于 matchInput 的小写视图）
	)
	checkContains(fsys, problems, group, "engine/compose_state.go",
		"s.matchInput()",
	)
	checkContains(fsys, problems, group, "engine/compose.go",
		"func preeditWithCase(input string, tokens []Token) string {",
		"preeditWithCase(s.input, st.patterns[0])",
	)

	// macOS 壳：a-z 与 A-Z 都算组词字符（大写不再 passThrough）。
	checkContains(fsys, problems, group, "shims/macos/src/KeyRouter.swift",
		"scalar.value >= 65 && scalar.value <= 90",
	)

	// Windows 壳：按 Shift 状态转发大写字母，不再「大写英文不进组词」。
	checkContains(fsys, problems, group, "shims/windows/src/tip_keys.cpp",
		"const char base = (shiftDown != capsOn) ? 'A' : 'a';",
	)
	checkExcludes(fsys, problems, group, "shims/windows/src/tip_keys.cpp",
		"大写英文不进组词", // 旧行为注释：回退即红
	)

	// Linux 壳：a-z 与 A-Z 都转发。
	checkContains(fsys, problems, group, "shims/linux/engine/main.cpp",
		"(keyval >= 'A' && keyval <= 'Z')",
	)
}
