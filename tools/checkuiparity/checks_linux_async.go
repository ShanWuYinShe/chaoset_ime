// checks_linux_async.go：Linux ibus 壳的异步引擎管线契约（issue #149 三端统一）。
//
// 旧实现：process_key_event 在 ibus 主循环线程同步调用 daemon 的 input/backspace/
// selectPrefix/punct/associate…，daemon 慢或挂起（SO_RCVTIMEO 300ms 起步、懒启动
// 最长约 5s）会把整个输入法主循环按住，候选窗与预编辑一起停摆。
//
// 现实现：每个 engine 实例一个 EnginePump 线程（独占连接、FIFO、200ms 截止），
// 主循环只「入队 + 用乐观组词态渲染」，结果经 g_idle 回投后按 seq 应用。
// 本组锁住以下不变量，任何一条回退都会让按键重新阻塞主循环：
//
//	a) 主循环 TU 不得直连 daemon/等待；
//	b) 按键入口只入队 + 乐观渲染（字母分支先提交后渲染）；
//	c) 工作线程不碰 ibus，结果只在 g_idle 回调里应用；
//	d) 失败结果不弹出乐观项；失焦/取消纪元作废；失联 kRebuild 整串重建；
//	e) 候选表/预编辑只在既有语义路径（focusOut/Esc/上屏/模式切换）关闭；
//	f) 构建脚本必须包含新的引擎线程 TU。
package main

import (
	"fmt"
	"io/fs"
	"strings"
)

const groupLinuxAsync = "Linux 异步引擎管线"

// linuxBraceBlock 返回 anchor 之后第一个花括号块（含花括号）的原文；找不到返回 ""。
// 只在本组内使用，避免依赖其它检查组文件的私有 helper。
func linuxBraceBlock(body, anchor string) string {
	i := strings.Index(body, anchor)
	if i < 0 {
		return ""
	}
	depth := 0
	start := -1
	for j := i; j < len(body); j++ {
		switch body[j] {
		case '{':
			depth++
			if start < 0 {
				start = j
			}
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				return body[start : j+1]
			}
		}
	}
	return ""
}

func checkLinuxAsyncPipeline(fsys fs.FS, problems *[]string) {
	const group = groupLinuxAsync
	const (
		linuxMain      = "shims/linux/engine/main.cpp"
		linuxAsync     = "shims/linux/engine/async_pipeline.cpp"
		linuxStateHpp  = "shims/linux/engine/engine_state.hpp"
		linuxPumpHpp   = "shims/linux/engine/engine_pump.hpp"
		linuxPumpCpp   = "shims/linux/engine/engine_pump.cpp"
		linuxDaemonHpp = "shims/linux/engine/daemon.hpp"
		linuxBuild     = "shims/linux/engine/build.sh"
	)
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf("%s：%s", group, fmt.Sprintf(format, args...)))
	}
	mainCode := stripCppComments(mustRead(fsys, linuxMain))
	asyncCode := stripCppComments(mustRead(fsys, linuxAsync))
	code := mainCode + "\n" + asyncCode
	need := func(path, what string, body string, needles ...string) {
		for _, n := range needles {
			if !strings.Contains(body, n) {
				fail("%s 的 %s 缺少 %q——Linux 异步管线的零等待/乐观语义不得回退", path, what, n)
			}
		}
	}

	// a) 主循环 TU 不得直连引擎或同步等待：所有 daemon I/O 只在 EnginePump 线程。
	for _, bad := range []string{
		"daemon_.", "ensureReady(", "inputWithRebuild(", "callComp(",
		"g_usleep(", "Sleep(",
	} {
		if strings.Contains(code, bad) {
			fail("%s 出现 %q——ibus 主循环线程不得直连 daemon/等待，引擎 I/O 全部走 EnginePump 线程", linuxMain, bad)
		}
	}

	// b) 按键入口只入队 + 乐观渲染，绝不等待。
	if body, line, ok := cppFunctionBody(mainCode,
		"bool IbusEngineState::processKeyEvent(guint keyval, guint keycode, guint state)"); ok {
		need(linuxMain, "processKeyEvent", body, "submit(", "renderOptimistic(", "optimisticInput(")
		// 字母分支：先提交，再乐观渲染。
		if i := strings.Index(body, "submitInput(std::string(1, static_cast<char>(keyval)));"); i >= 0 {
			if j := strings.Index(body[i:], "renderOptimistic();"); j < 0 {
				fail("%s 的 processKeyEvent（约第 %d 行）字母分支提交后没有乐观渲染——预编辑会等待 daemon", linuxMain, line)
			}
		} else {
			fail("%s 的 processKeyEvent 找不到字母提交点（签名/语句变了？需同步更新本检查）", linuxMain)
		}
	} else {
		fail("%s 找不到 processKeyEvent 定义（签名变了？需同步更新本检查）", linuxMain)
	}

	// c) 引擎线程：每 engine 一个、FIFO、有截止、结果只经通知回调唤醒主循环。
	checkContains(fsys, problems, group, linuxPumpHpp,
		"class EnginePump", "std::thread", "std::deque<EngineOp> queue_",
		"kEngineRequestDeadlineMs", "kEngineReconnectCooldownMs", "joinOk()")
	checkContains(fsys, problems, group, linuxPumpCpp,
		"cv_.wait(", "queue_.pop_front();", "notify_()", "joinOk_ = false")
	pumpCode := stripCppComments(mustRead(fsys, linuxPumpCpp))
	for _, bad := range []string{"ibus_", "IBus"} {
		if strings.Contains(pumpCode, bad) {
			fail("%s 出现 %q——工作线程绝不能触碰 ibus API（结果只能在主循环应用）", linuxPumpCpp, bad)
		}
	}
	checkContains(fsys, problems, group, linuxAsync, "g_idle_add_full(", "scheduleResultsIdle(")
	checkContains(fsys, problems, group, linuxMain, "void IbusEngineState::drainResults()", "state.store(nullptr)")
	checkContains(fsys, problems, group, linuxStateHpp, "struct ResultWake")

	// d) 失败语义：失败不弹乐观项、纪元作废、失联整串重建、宽限落盘。
	if body, line, ok := cppFunctionBody(asyncCode,
		"void IbusEngineState::applyResult(const EngineResult& r)"); ok {
		need(linuxMain, "applyResult", body,
			"if (!r.ok)", "pendingOps_.front().seq <= r.op.seq",
			"restartFromLocalState(", "markEngineLost(",
			"if (!text.empty()) commit(all);")
		// 失败分支块内**不得**出现乐观项出队：本键效果必须留到整串重建覆盖。
		failBlock := linuxBraceBlock(body, "if (!r.ok)")
		if failBlock == "" {
			fail("%s 的 applyResult（约第 %d 行）结构变了：找不到失败分支块", linuxMain, line)
		} else if strings.Contains(failBlock, "pendingOps_.front().seq <= r.op.seq") {
			fail("%s 的 applyResult（约第 %d 行）在失败结果上弹出了乐观项——本键效果必须留在本地显示里", linuxMain, line)
		}
	} else {
		fail("%s 找不到 applyResult 定义", linuxMain)
	}
	need(linuxMain, "异步核心", asyncCode,
		"kEngineGraceMs", "scheduleRebuild(", "giveUpEngineState(",
		"EngineOpKind::kRebuild", "++epoch_", "pendingOps_.clear()")
	// 宽限放弃必须按壳侧完整输入串落盘（commit(raw)），一个键都不丢。
	if body, _, ok := cppFunctionBody(asyncCode, "void IbusEngineState::giveUpEngineState()"); ok {
		need(linuxMain, "giveUpEngineState", body, "optimisticComp()", "resetPipeline(true)", "commit(raw)")
	} else {
		fail("%s 找不到 giveUpEngineState 定义", linuxMain)
	}

	// e) 乐观渲染用「已应用组词态 + 未应用操作」；候选表只在既有语义路径关闭。
	need(linuxAsync, "渲染路径", asyncCode,
		"void IbusEngineState::renderOptimistic()", "void IbusEngineState::display(")
	need(linuxMain, "生命周期", mainCode, "void IbusEngineState::focusOut()", "hideLookupTable();")

	// f) daemon 层提供单次非阻塞探测与可配截止（供引擎线程使用），三端对齐断连守卫。
	checkContains(fsys, problems, group, linuxDaemonHpp,
		"bool tryConnectOnce()", "void setDeadlineMs(int ms)", "int deadlineMs_ = 300")
	checkContains(fsys, problems, group, linuxPumpCpp,
		"tryConnectOrSpawn(", "sessionDesynced_", "syncedConnGen_")
	checkContains(fsys, problems, group, linuxAsync,
		"submitNonComposingOp(", "hasDpPending()", "startLostTimer(",
		"commitPunctFallback(", "handleSelectPrefixFallback(")
	// 构建脚本必须同时包含共享 IPC 核心与被拆出的两个 TU（漏 core.cpp 会链接失败）。
	checkContains(fsys, problems, group, linuxBuild,
		"engine_pump.cpp", "async_pipeline.cpp", "../../common/ipc/core.cpp", "daemon.cpp")

	// g) 文档必须记录 Linux 异步契约（与 docs/ui-parity.md 同步）。
	checkContains(fsys, problems, group, uiParityDoc,
		"Linux 异步引擎管线", "EnginePump", "g_idle", "候选窗")
}
