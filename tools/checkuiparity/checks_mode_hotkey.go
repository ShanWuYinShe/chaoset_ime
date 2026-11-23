// checks_mode_hotkey.go：无文本输入焦点时的中英切换热键（issue #132）契约。
//
// 背景：TSF 只在有文本输入焦点时把按键投递给输入法，桌面/资源管理器空白处/任务栏
// 等场景 Shift 到不了 TIP；零副作用契约又禁止 SetWindowsHookEx 之类的全局钩子。
// 现行方案是 UI 宿主用系统热键 `RegisterHotKey`（Ctrl+Shift+Space）——系统在按键时
// 直接投递 WM_HOTKEY，不钩进程、不占按键流。本组锁住三件事，任何一件回退都会让
// 「无输入框时切不了中英」重新出现，或者引入全局钩子式回归：
//
//	a) UI 宿主必须注册系统热键（RegisterHotKey + MOD_NOREPEAT），并在销毁时注销
//	   （不注销会让组合键在进程存活期内一直占着，其它程序永远收不到）；
//	b) WM_HOTKEY 必须转投 kUiToggleModeMsg（复用托盘左键的 System 档语义），
//	   不得自己翻转状态绕过 mode_scope（Process 档下不应影响其它进程）；
//	c) 不得引入全局键鼠钩子（零副作用红线）。
package main

import (
	"fmt"
	"io/fs"
	"strings"
)

func checkToggleModeHotkey(fsys fs.FS, problems *[]string) {
	const group = groupToggleModeHotkey
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf("%s：%s", group, fmt.Sprintf(format, args...)))
	}
	code := stripCppComments(mustRead(fsys, winModeHotkey))
	uiHostCode := stripCppComments(mustRead(fsys, winUiHost))
	// 热键实现必须在独立的叶子 TU 里（ui_host.cpp 有 1000 行预算），且 ui_host
	// 必须通过它接入——两半缺一都不工作。
	if !strings.Contains(uiHostCode, "uiRegisterToggleModeHotkey(g_uiHostWnd)") {
		fail("%s 没有接入 mode_hotkey 的注册调用——热键不会被创建", winUiHost)
	}
	if !strings.Contains(uiHostCode, "uiUnregisterToggleModeHotkey(hwnd)") {
		fail("%s 销毁时没有注销热键——组合键会被进程一直占着", winUiHost)
	}
	if !strings.Contains(uiHostCode, "uiIsToggleModeHotkey(wParam)") {
		fail("%s 的 WM_HOTKEY 分支没有用 uiIsToggleModeHotkey 判定", winUiHost)
	}

	// a) 注册 + 修饰键不重复触发 + 销毁注销。
	for _, needle := range []string{
		"RegisterHotKey(hwnd, kToggleHotkeyId",
		"MOD_NOREPEAT",
		"UnregisterHotKey(hwnd, kToggleHotkeyId)",
	} {
		if !strings.Contains(code, needle) {
			fail("%s 缺少 %q——无文本输入焦点时的中英切换热键（issue #132）不得回退", winUiHost, needle)
		}
	}
	// 注册失败必须降级而不是崩溃/阻塞：热键是可选的补充入口。
	if !strings.Contains(code, "g_toggleHotkeyRegistered = false") {
		fail("%s 的热键注册失败分支未降级（应只记日志并保持托盘左键可用）", winUiHost)
	}

	// b) WM_HOTKEY 转投 kUiToggleModeMsg，不自己翻转状态。
	hotkeyBody, line, ok := cppFunctionBody(uiHostCode, "LRESULT CALLBACK uiHostWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam)")
	if !ok {
		fail("%s 找不到 uiHostWndProc 定义", winUiHost)
		return
	}
	if !strings.Contains(hotkeyBody, "case WM_HOTKEY:") {
		fail("%s 的窗口过程（约第 %d 行）缺少 WM_HOTKEY 分支——热键按下不会有任何动作", winUiHost, line)
	}
	if strings.Contains(hotkeyBody, "case WM_HOTKEY:") {
		// 取 WM_HOTKEY 到下一个 case 之间的一段，确认只是转投。
		start := strings.Index(hotkeyBody, "case WM_HOTKEY:")
		rest := hotkeyBody[start:]
		end := strings.Index(rest[5:], "case ")
		seg := rest
		if end > 0 {
			seg = rest[:end+5]
		}
		if !strings.Contains(seg, "kUiToggleModeMsg") {
			fail("%s 的 WM_HOTKEY 分支没有转投 kUiToggleModeMsg——必须复用托盘左键的 System 档语义", winUiHost)
		}
		if strings.Contains(seg, "uiApplyEnglish(") {
			fail("%s 的 WM_HOTKEY 分支直接调 uiApplyEnglish——绕过了 mode_scope 判断（Process 档不得改全局状态）", winUiHost)
		}
	}

	// c) 不得引入全局键鼠钩子（零副作用红线）。
	for _, banned := range []string{"SetWindowsHookEx", "LowLevelKeyboardProc", "RegisterRawInputDevices"} {
		if strings.Contains(code, banned) || strings.Contains(uiHostCode, banned) {
			fail("mode_hotkey/ui_host 出现 %q——无焦点切换只能用 RegisterHotKey，禁止全局键鼠钩子", banned)
		}
	}

	// 文档：契约与代价（全局独占）必须写下来，便于用户理解为何选组合键。
	checkContains(fsys, problems, group, uiParityDoc, "Ctrl+Shift+Space", "RegisterHotKey")
}
