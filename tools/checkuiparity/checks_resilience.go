// checks_resilience.go：异步引擎管线——Windows 壳按键路径零 IPC 等待的端到端契约
// （issue #149；取代旧的「超时韧性」组）。
//
// 旧实现（issue #122）在 TSF 回调里同步等 daemon（30ms 截止），超时进失步状态机，
// 再配一套「选词只刷新候选」的降级语义——截止调大只是把误判换成更久的宿主阻塞。
// 异步管线把引擎 I/O 整体移到每 TIP 的 EnginePump 线程：按键只入队 + 乐观渲染，
// 结果经 message-only 窗口回投后按 seq 应用。本组锁住以下不变量，任何一条回退都
// 会让按键重新阻塞宿主 UI 线程：
//
//	a) TIP TU 不得直连引擎（session_/transport_/ensureDaemonFast）；
//	b) 按键入口只入队 + 乐观 render，随后才投递，绝不等待；
//	c) 结果按 seq 顺序应用、失败不弹出乐观项、过期（纪元不同）结果丢弃；
//	d) 失焦/取消递增纪元并清空管线，绝不把迟到结果写进新会话；
//	e) daemon 失败保留本地组词，2s 宽限后按原始串落盘；显式选词用已显示文本兜底；
//	f) 引擎线程每 TIP 一个、独占连接、FIFO、有截止、结果回投 message-only 窗口。
package main

import (
	"fmt"
	"io/fs"
	"strings"
)

// cppBraceBlock 返回 anchor 之后第一个花括号块（含花括号）的原文；找不到返回 ""。
// 用于精确判定「某分支块内」是否有禁止语句（全文 Index 顺序会被分支内的位置骗过）。
func cppBraceBlock(body, anchor string) string {
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

func checkAsyncEnginePipeline(fsys fs.FS, problems *[]string) {
	const group = groupAsyncEngine
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf("%s：%s", group, fmt.Sprintf(format, args...)))
	}
	bodyOf := func(path, sig string) (string, int, bool) {
		return cppFunctionBody(stripCppComments(mustRead(fsys, path)), sig)
	}
	need := func(path, what string, body string, needles ...string) {
		for _, n := range needles {
			if !strings.Contains(body, n) {
				fail("%s 的 %s 缺少 %q——异步管线的零等待/乐观语义不得回退", path, what, n)
			}
		}
	}

	// a) TIP TU 不得直连引擎：所有 daemon I/O 只允许在 engine_pump.cpp。
	for _, path := range []string{winTipCore, winTipKeys, winTipCands, winTipComp, winTipDesync, winTipAsync} {
		code := stripCppComments(mustRead(fsys, path))
		for _, bad := range []string{"session_.", "transport_.", "ensureDaemonFast(", "ensureDaemonConnected("} {
			if strings.Contains(code, bad) {
				fail("%s 出现 %q——TSF 线程不得直连 daemon，引擎 I/O 全部走 EnginePump 线程", path, bad)
			}
		}
	}

	// b) 按键入口：先入队（乐观项）→ 乐观渲染 → 再投递引擎线程；绝不等待。
	if body, line, ok := bodyOf(winTipKeys, "bool ChaosetTIP::handleKey(WPARAM vk, ITfContext* pic)"); ok {
		need(winTipKeys, "handleKey", body, "queuePendingOp(", "dispatchPendingOp(", "render(", "submitEngineOp(")
		// 顺序只在字母分支内检查：别的分支（退格/Alt）也可以合理先渲染。
		letter := ""
		if i := strings.Index(body, "if (vk >= kVkFirstLetter && vk <= kVkLastLetter)"); i >= 0 {
			letter = body[i:]
			if end := strings.Index(letter, "// 候选翻页"); end > 0 {
				letter = letter[:end]
			}
		}
		iQueue := strings.Index(letter, "queuePendingOp(")
		iRender := strings.Index(letter, "render(")
		iDispatch := strings.Index(letter, "dispatchPendingOp(")
		if letter == "" || iQueue < 0 || iRender < 0 || iDispatch < 0 ||
			!(iQueue < iRender && iRender < iDispatch) {
			fail("%s 的 handleKey（约第 %d 行）字母分支必须「先入队乐观项 → 立即乐观渲染 → 再投递引擎」，实测顺序不满足", winTipKeys, line)
		}
		for _, bad := range []string{"WaitForSingleObject", "Sleep(", "session_.", "ensureDaemonFast("} {
			if strings.Contains(body, bad) {
				fail("%s 的 handleKey（约第 %d 行）出现 %q——按键路径绝不允许等待/直连引擎", winTipKeys, line, bad)
			}
		}
	} else {
		fail("%s 找不到 handleKey 定义（签名变了？需同步更新本检查）", winTipKeys)
	}

	// c) 结果按 seq 顺序应用；失败结果【不】弹出乐观项（本地显示要保留到整串
	// 重建覆盖），过期纪元的结果在入口丢弃。
	if body, line, ok := bodyOf(winTipAsync, "void ChaosetTIP::applyEngineResult(const EngineResult& r)"); ok {
		need(winTipAsync, "applyEngineResult", body, "if (!r.ok)", "pendingOps_.front().seq <= r.op.seq")
		// 失败分支块内**不得**出现乐观项出队：本键的效果必须留在本地显示里，
		// 等其后的整串重建结果（seq 更大）再一并覆盖。
		failBlock := cppBraceBlock(body, "if (!r.ok)")
		if failBlock == "" {
			fail("%s 的 applyEngineResult（约第 %d 行）结构变了：找不到失败分支块", winTipAsync, line)
		} else if strings.Contains(failBlock, "pendingOps_.front().seq <= r.op.seq") {
			fail("%s 的 applyEngineResult（约第 %d 行）在失败结果上弹出了乐观项——本键的效果必须留在本地显示里直到整串重建覆盖", winTipAsync, line)
		}
		need(winTipAsync, "applyEngineResult", body, "applyCommitted(", "r.op.text")
	} else {
		fail("%s 找不到 applyEngineResult 定义", winTipAsync)
	}
	if body, _, ok := bodyOf(winTipAsync, "void ChaosetTIP::applyEngineResults()"); ok {
		need(winTipAsync, "applyEngineResults", body, "r.op.epoch != engineEpoch_")
	} else {
		fail("%s 找不到 applyEngineResults 定义", winTipAsync)
	}

	// d) 失焦/取消：纪元递增 + 清空壳侧乐观项 + 丢弃引擎线程积压 + 引擎复位。
	if body, _, ok := bodyOf(winTipAsync, "void ChaosetTIP::resetEnginePipeline(bool newSession)"); ok {
		need(winTipAsync, "resetEnginePipeline", body,
			"++engineEpoch_;", "pendingOps_.clear();", "pump_->clearPending();",
			"EngineOpKind::kReset", "EngineOpKind::kResetComposition")
	} else {
		fail("%s 找不到 resetEnginePipeline 定义", winTipAsync)
	}
	core := stripCppComments(mustRead(fsys, winTipCore))
	if n := strings.Count(core, "resetEnginePipeline(true)"); n < 2 {
		fail("%s 只有 %d 处 resetEnginePipeline(true)——失焦（OnSetFocus(FALSE)）与合成被外部终止（OnCompositionTerminated）都必须作废在飞结果", winTipCore, n)
	}

	// e) daemon 失败：保留本地组词、2s 宽限后按原始串落盘；显式选词/联想/标点
	// 用壳侧已显示文本兜底上屏，绝不静默丢键。
	async := stripCppComments(mustRead(fsys, winTipAsync))
	for _, n := range []string{"kEngineGraceMs", "markEngineLost(", "scheduleRebuild(", "giveUpEngineState(", "postDaemonConnect("} {
		if !strings.Contains(async, n) {
			fail("%s 缺少 %q——daemon 不可用时必须保留本地组词并在宽限后落盘", winTipAsync, n)
		}
	}
	if body, _, ok := bodyOf(winTipAsync, "void ChaosetTIP::giveUpEngineState()"); ok {
		need(winTipAsync, "giveUpEngineState", body, "optimisticComp()", "commit(")
	} else {
		fail("%s 找不到 giveUpEngineState 定义", winTipAsync)
	}

	// f) 乐观预编辑：渲染用的预编辑必须来自 optimisticComp（comp_ + 未应用操作），
	// 组词判定同理——否则首个按键的结果回来前会把后续按键放行成宿主英文。
	if body, _, ok := bodyOf(winTipComp, "void ChaosetTIP::applyPreedit(ITfContext* ctx, TfEditCookie ec)"); ok {
		need(winTipComp, "applyPreedit", body, "optimisticComp()")
	} else {
		fail("%s 找不到 applyPreedit 定义", winTipComp)
	}
	checkContains(fsys, problems, group, winTipCore,
		"bool ChaosetTIP::isComposing() const { return !optimisticComp().input.empty(); }")

	// g) 引擎线程：每 TIP 一个、独占连接（daemon 组词会话按连接隔离）、FIFO、
	// 有截止、结果回投 message-only 窗口。
	checkContains(fsys, problems, group, winEnginePumpHpp,
		"class EnginePump", "std::deque<EngineOp> queue_", "chaoset::Session session_",
		"kEngineRequestDeadlineMs", "kEngineReconnectCooldownMs")
	checkContains(fsys, problems, group, winEnginePumpCpp,
		"cv_.wait(lk, [this] { return stop_ || !queue_.empty(); });",
		"queue_.pop_front();",
		"postTipThread(tid_, kTipEngineResultMsg, 0, 0);",
		"connected_.store(true)")
	checkContains(fsys, problems, group, winTipHpp, "std::unique_ptr<chaoset::win::EnginePump> pump_;")
	checkContains(fsys, problems, group, winTipThreadHpp, "kTipEngineResultMsg")
	checkContains(fsys, problems, group, winTipThreadCpp, "onWorkerEngineResult()")
	checkContains(fsys, problems, group, winTipCore, "void ChaosetTIP::onWorkerEngineResult()")

	// h) 卸载安全：析构必须先停引擎线程；线程未能退出时绝不销毁仍被引用的对象。
	if body, _, ok := bodyOf(winTipCore, "ChaosetTIP::~ChaosetTIP()"); ok {
		need(winTipCore, "析构", body, "pump_->stop()", "joinOk()")
	} else {
		fail("%s 找不到析构函数", winTipCore)
	}

	// i) 文档必须记录异步管线契约（防止实现与 docs/ui-parity.md 脱节）。
	checkContains(fsys, problems, group, uiParityDoc,
		"异步引擎管线", "EnginePump", "kTipEngineResultMsg",
		"按键路径不等待", "乐观预编辑")

	// j) 传输/会话层的超时契约不变（macOS/Linux 仍是同步壳；Windows 的异步引擎
	// 线程在 I/O 失败时主动断连、用整串重建恢复——策略在 engine_pump.cpp，但
	// 「干净读超时才保留连接」的传输层判据与部分读取防护必须继续保持，否则
	// mac/Linux 会重新出现「一次读超时丢整个会话」或字节流错位）。
	checkContains(fsys, problems, group, winTransport,
		"bool lastReadTimedOut() const override", "lastReadTimedOut_ = !isWrite",
		"读超时：保留连接")
	if body, line, ok := cppFunctionBody(stripCppComments(mustRead(fsys, winTransport)),
		"bool pipeIo(bool isWrite, const std::uint8_t* data, std::size_t len)"); ok {
		if !strings.Contains(body, "if (!isWrite && nothingConsumed) {") {
			fail("%s 的 pipeIo（约第 %d 行）缺少读/写超时区分或没守住「未消费字节才保留」——读超时不得无条件保留连接", winTransport, line)
		}
	} else {
		fail("%s 找不到 pipeIo 定义", winTransport)
	}
	checkContains(fsys, problems, group, winCoreHpp,
		"virtual bool lastReadTimedOut() const", "staleResponses_")
	checkContains(fsys, problems, group, winCoreCpp,
		"bool Session::drainStaleResponse()", "++staleResponses_")
	for _, needle := range []string{"off == 0 && cancelled == 0", "nothingConsumed"} {
		if !strings.Contains(stripCppComments(mustRead(fsys, winTransport)), needle) {
			fail("%s 的读超时分支没有用 %q 判「是否已消费字节」——部分读取必须按连接已坏处理", winTransport, needle)
		}
	}
	checkContains(fsys, problems, group, macTransport, "lastReadTimedOut = (off == 0)")
	checkContains(fsys, problems, group, "shims/linux/engine/daemon.hpp",
		"lastReadTimedOut() const override")
	checkContains(fsys, problems, group, linuxEngineMain, "lastReadTimedOut_ = (off == 0)")
	if body, _, ok := cppFunctionBody(stripCppComments(mustRead(fsys, winCoreCpp)), "bool Session::drainStaleResponse()"); ok {
		seg := ""
		if i := strings.Index(body, "if (t_.lastReadTimedOut()) {"); i >= 0 {
			rest := body[i:]
			if end := strings.Index(rest, "const std::uint32_t size"); end > 0 {
				seg = rest[:end]
			}
		}
		if seg == "" {
			fail("%s 的 drainStaleResponse 结构变了：找不到「干净超时判断 → 帧长解析」这一段", winCoreCpp)
		} else if !strings.Contains(seg, "t_.closeConn();") {
			fail("%s 的 drainStaleResponse 在非干净超时分支没有断连——部分读取会让字节流错位", winCoreCpp)
		}
	} else {
		fail("%s 找不到 drainStaleResponse 定义", winCoreCpp)
	}
	checkContains(fsys, problems, group, macTransport,
		"lastReadTimedOut = true", "func drainStaleResponse()",
		"if lastReadTimedOut && staleResponses < 2 {", "staleResponses += 1")
	// 读超时后不得原样重放增量 input（daemon 可能已处理，只是响应迟到）。
	checkContains(fsys, problems, group, macEngineClient,
		"if transport.lastFailureWasReadTimeout { return nil }",
		"func input(_ text: String, halfWidth: Bool, base: String? = nil,",
		"rebuild: Bool = false) -> Comp?",
		"guard let base else { return nil }",
		"if rebuild, let base { params[\"base\"] = base }",
		"if let base { params[\"base\"] = base }",
		"func selectPrefix(index: Int) -> Comp? {\n        guard let result = call(method: \"selectPrefix\",\n                                params: [\"index\": index],\n                                retryOnFailure: false) else { return nil }")
	// macOS/Linux 壳均已改异步引擎管线（issue #149）：按键侧不再直接调用
	// inputWithRebuild，base 幂等重放留在 EngineClient/daemon 层（上面的断言），
	// 两端契约分别由「macOS 异步引擎管线」「Linux 异步引擎管线」组覆盖。
	checkContains(fsys, problems, group, linuxEngineMain, "Comp Daemon::inputWithRebuild(",
		"session_.input(text, halfWidth, base.c_str())")

	// k) 退格/翻页失败不得吞键、不得在失败路径上强上屏。macOS/Linux 已改为异步
	// 管线：退格/翻页只入队，失败结果不弹出乐观项 → 组词与按键天然保留，由各自的
	// 异步管线组断言；Windows 由「失败不弹乐观项 + 宽限落盘」保证同一语义（见 e/h）。

	// l) 选词失败的上屏文本必须是候选中文，不是拼音串。macOS/Linux 的选词/联想/
	// 标点失败兜底已迁到各自异步管线的失败分支（壳侧已显示文本上屏），由
	// 「macOS/Linux 异步引擎管线」组断言；Windows 的兜底见 e/h。

	// m) IPC 协议三处都认 input 的 base（Windows 的重建请求也用它）。
	checkContains(fsys, problems, group, winShell, "lastIoWasTimeout(")
	checkContains(fsys, problems, group, winCoreHpp, "const char* base = nullptr")
	checkContains(fsys, problems, group, winCoreCpp, `,\"base\":\"`)
	checkContains(fsys, problems, group, ipcServer, "params.get(\"base\")", "input_with_base(")
	checkContains(fsys, problems, group, rustSession, "pub fn restore_input(", "pub fn input_with_base(")

	// n) daemon 命名管道服务端：常驻监听实例、主循环不阻塞在连接上、失败退避重试。
	checkContains(fsys, problems, group, rustDaemonPipe,
		"PIPE_LISTEN_INSTANCES", "run_listener_pool(", "ConnectNamedPipe(", "fn spawn_pipe_worker(")
	checkContains(fsys, problems, group, rustDaemonServer, "fn run_listener_pool<", "spawn_failed")
	if pipeSrc, err := fs.ReadFile(fsys, rustDaemonPipe); err == nil {
		text := string(pipeSrc)
		main, worker := strings.Index(text, "pub fn run_windows_server("), strings.Index(text, "fn spawn_pipe_worker(")
		if main < 0 || worker < 0 || main > worker {
			fail("%s 的 run_windows_server / spawn_pipe_worker 结构变了（需同步更新本检查）", rustDaemonPipe)
		} else if strings.Contains(text[main:worker], "ConnectNamedPipe(") {
			fail("%s 的 run_windows_server 主循环里出现 ConnectNamedPipe——主循环只补位与回收，阻塞等连接必须在监听线程里",
				rustDaemonPipe)
		}
	}
	// UT 把关：脚本化传输模拟「读超时 → 迟到响应」。
	checkContains(fsys, problems, group, winCoreTest,
		"testReadTimeoutKeepsConnection", "ScriptedTransport",
		"读超时不得断开连接", "迟到响应被丢弃", "写失败必须断连",
		"部分读取后超时（字节流已错位）必须断连", "drainStaleResponse 遇到部分读取必须断连")
}
