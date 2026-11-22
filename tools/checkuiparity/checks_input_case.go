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
// 这项契约跨「Rust 引擎 + 三个壳」，任一端回退（例如壳重新只认 a-z、引擎
// 重新 ToLower 后过滤）都会让用户重新被迫切输入法，所以静态锁死。
func checkLetterCaseComposition(fsys fs.FS, problems *[]string) {
	const group = groupLetterCaseComposition

	// Rust 引擎（唯一实现）：原样保存 + 小写匹配视图 + 预编辑还原大小写。
	checkContains(fsys, problems, group, "crates/chaoset-engine/src/session.rs",
		"fn match_input(&self) -> String {",
		"fn preedit_with_case(input: &str, tokens: &[Token]) -> String {",
		"c.is_ascii_alphabetic()",
	)

	// macOS 壳：a-z 与 A-Z 都算组词字符（大写不再 passThrough），且字符级路由
	// 必须用 event.characters 取实际大小写；charactersIgnoringModifiers 会把
	// Shift+字母吞成小写，导致预编辑/回车原样上屏丢大小写（用户真机反馈）。
	checkContains(fsys, problems, group, "shims/macos/src/KeyRouter.swift",
		"scalar.value >= 65 && scalar.value <= 90",
		"guard let chars = event.characters,",
	)
	checkExcludes(fsys, problems, group, "shims/macos/src/KeyRouter.swift",
		"guard let chars = event.charactersIgnoringModifiers",
	)
	checkContains(fsys, problems, group, "shims/macos/tests/key_routing/main.swift",
		"Shift+字母预编辑保留大小写", "回车原样上屏大写拼音",
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
