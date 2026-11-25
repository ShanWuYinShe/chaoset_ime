// checks_focus_loss.go：候选窗无特权——焦点离开即关闭、清空候选与输入（三端契约）。
//
// 用户原则：候选窗只是一个普通弹出窗口，不得留在他处截走鼠标。IMK 只在客户端
// 文本焦点变化时回调 commitComposition/deactivateServer，应用切换、Space 切换、
// 联想态（无 marked text）都不会触发；Windows OnSetFocus(FALSE) 与 Linux focus_out
// 则本来就按「会话结束」收尾。macOS 用进程级 NSWorkspace 通知 + 面板失键通知补齐，
// 三端语义一致：已确认段落盘、剩余拼音丢弃、联想退出、面板与提示收起。
package main

import (
	"fmt"
	"io/fs"
	"strings"
)

func checkCandidateNoPrivilege(fsys fs.FS, problems *[]string) {
	const group = groupCandidateNoPrivilege
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf("%s：%s", group, fmt.Sprintf(format, args...)))
	}
	// macOS：进程级焦点守卫（应用激活 / Space 切换）+ 面板失键钩子，收尾走统一入口。
	checkContains(fsys, problems, group, macController,
		"NSWorkspace.didActivateApplicationNotification", "NSWorkspace.activeSpaceDidChangeNotification",
		"func abandonSessionOnFocusLoss()", "static func handleFocusLossSignal(pid: pid_t?)",
		"CandidatePanelController.onResignKey", "refresh(lastClient)")
	checkContains(fsys, problems, group, macPanel,
		"NSWindow.didResignKeyNotification", "static var onResignKey")
	checkContains(fsys, problems, group, macKeyTest, "handleFocusLossSignal")
	// Windows：OnSetFocus(FALSE) 必须重置会话并收候选窗（不是只关按键闸门）。
	if body, line, ok := cppFunctionBody(stripCppComments(mustRead(fsys, winShell)),
		"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetFocus(BOOL fForeground)"); ok {
		for _, needle := range []string{"resetSession()", "endComposition()"} {
			if !strings.Contains(body, needle) {
				fail("%s 的 OnSetFocus（约第 %d 行）缺少 %s——失焦必须结束会话并收候选窗", winShell, line, needle)
			}
		}
	} else {
		fail("%s 找不到 ChaosetTIP::OnSetFocus 定义", winShell)
	}
	// Linux：focus_out 回调必须绑定，且 focusOut() 函数体本身要重置会话并收 lookup table
	// （文件里别处也有 hideLookupTable()，按函数体判，不能整文件找）。
	checkContains(fsys, problems, group, linuxEngine, "focus_out = on_focus_out", "void IbusEngineState::focusOut()")
	if body, line, ok := cppFunctionBody(stripCppComments(mustRead(fsys, linuxEngine)), "void IbusEngineState::focusOut()"); ok {
		for _, needle := range []string{"sessionReset()", "hideLookupTable()"} {
			if !strings.Contains(body, needle) {
				fail("%s 的 focusOut（约第 %d 行）缺少 %s——失焦必须结束会话并收候选表", linuxEngine, line, needle)
			}
		}
	} else {
		fail("%s 找不到 focusOut() 定义", linuxEngine)
	}
	// 文档记录契约。
	checkContains(fsys, problems, group, uiParityDoc, "候选窗没有特权", "焦点守卫")
}
