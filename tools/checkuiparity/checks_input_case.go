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
	// 必须用 event.characters 取实际大小写；charactersIgnoringModifiers 会忽略
	// CapsLock（Shift 仍生效），CapsLock 字母会被吞成小写，导致预编辑/回车原样
	// 上屏丢大小写（用户真机反馈）。
	checkContains(fsys, problems, group, "shims/macos/src/KeyRouter.swift",
		"scalar.value >= 65 && scalar.value <= 90",
		"guard let chars = event.characters,",
	)
	checkExcludes(fsys, problems, group, "shims/macos/src/KeyRouter.swift",
		"guard let chars = event.charactersIgnoringModifiers",
	)
	// 合成事件的 charactersIgnoringModifiers 必须符合 AppKit 语义（CapsLock 被
	// 忽略、Shift 生效）：CapsLock 用例显式传 ignoring 小写，否则测试会拿大写
	// ignoring 自我实现「通过」，旧壳的丢大写路径就测不出来。
	checkContains(fsys, problems, group, "shims/macos/tests/key_routing/main.swift",
		"Shift+字母预编辑保留大小写", "回车原样上屏大写拼音",
		"CapsLock 字母预编辑保留大小写", "回车原样上屏 CapsLock 大写拼音",
		"ignoring: ch.lowercased()",
	)
	checkExcludes(fsys, problems, group, "shims/macos/tests/key_routing/main.swift",
		"charactersIgnoringModifiers: chars.lowercased()",
	)

	// Windows 壳：Shift/CapsLock 下的大写字母同样进组词（OnTestKeyDown / wouldHandle / handleKey
	// 均按 a-z/A-Z 放行入组词）；shiftDown() 只看物理 VK_SHIFT，不得混入 VK_CAPITAL
	//（否则 CapsLock 会反转失步态大小写并破坏数字/翻页/标点/Ctrl+U）。
	checkContains(fsys, problems, group, "shims/windows/src/tip_keys.cpp",
		"const bool letter = wParam >= kVkFirstLetter && wParam <= kVkLastLetter;",
		"if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return true;",
		"bool ChaosetTIP::shiftDown() {\n    return (GetKeyState(VK_SHIFT) & 0x8000) != 0;\n}",
		"const char base = (shiftDown != capsOn) ? 'A' : 'a';",
	)
	checkExcludes(fsys, problems, group, "shims/windows/src/tip_keys.cpp",
		"大写英文不进组词", // 旧行为注释：回退即红
		"shiftActive(",
		"!shift && (vk >= kVkFirstLetter",
		"!shiftDown() && vk >= kVkFirstLetter",
	)
	checkContains(fsys, problems, group, "shims/windows/src/tip_desync.cpp",
		"const bool shift = shiftDown();",
	)
	checkExcludes(fsys, problems, group, "shims/windows/src/tip_desync.cpp",
		"shiftActive(",
	)

	// Linux 壳：a-z 与 A-Z 都转发。
	checkContains(fsys, problems, group, "shims/linux/engine/main.cpp",
		"(keyval >= 'A' && keyval <= 'Z')",
	)
}
