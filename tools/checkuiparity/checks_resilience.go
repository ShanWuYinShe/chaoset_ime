// checks_resilience.go：超时韧性——「引擎请求超时不拆组词」的端到端契约。
//
// issue #122：壳侧按键请求有有界截止（issue #149 后正常按键 150ms、失步重建
// 30ms），而桌面系统上更长的调度停顿仍无法杜绝（引擎再快也一样）。旧实现在任一
// 引擎请求超时后立刻拆组词——预编辑拼音被擦掉、候选窗关闭、当前键放行成英文——
// 并叠加 5s 熔断，用户看到「候选窗突然关闭，之后只能打英文」。现行契约分三段，
// 任何一段回退症状都会复现：
//
//	a) 壳：同步态每个引擎请求失败点都先经超时分流（enterDesyncOnTimeout /
//	   keepCompositionOnTimeout），在 fail-open 收尾之前不得跳过它；失步中的
//	   按键由 handleKeyDesynced 处理，重连后用 input 的 base 参数整串重建，
//	   绝不在失步处理器里 fail-open 拆组词、也绝不照旧候选选词；按键路径不套
//	   5s 熔断（否则超时后的下一键重连不了）；
//	b) 协议：C++ IPC 核心、daemon 与引擎都认 input 的可选 base（先把引擎输入
//	   串整串重建为 base，再追加 text）；
//	c) daemon：命名管道服务端常驻监听实例，主循环不阻塞在连接上，建实例失败
//	   退避重试而不是让 daemon 退出。
package main

import (
	"fmt"
	"io/fs"
	"strings"
)

// timeoutGuarded 检查 code 里每一处 request 之后：fail-open 收尾（flushOrCancel）
// 之前是否先经过超时分流（函数名以 OnTimeout 结尾）。返回 (请求点数, 是否全部
// 受保护)。只看请求点之后的一段窗口——成功分支的长度有限，失败处理紧随其后。
func timeoutGuarded(code, request string) (int, bool) {
	const window = 1500
	sites, guarded := 0, true
	for start := 0; ; {
		i := strings.Index(code[start:], request)
		if i < 0 {
			break
		}
		i += start
		sites++
		rest := code[i+len(request):]
		if len(rest) > window {
			rest = rest[:window]
		}
		guard, flush := strings.Index(rest, "OnTimeout("), strings.Index(rest, "flushOrCancel(")
		if guard < 0 || (flush >= 0 && flush < guard) {
			guarded = false
		}
		start = i + len(request)
	}
	return sites, guarded
}

func checkTimeoutResilience(fsys fs.FS, problems *[]string) {
	const group = groupTimeoutResilience
	data, err := fs.ReadFile(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return
	}
	code := stripCppComments(string(data))
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf("%s：%s", group, fmt.Sprintf(format, args...)))
	}

	// a1) 组词中发起引擎请求的调用点：失败处理必须先经超时分流，再谈 fail-open。
	requests := []struct{ request, label string }{
		{"comp_ = session_.input(ch, halfWidth_);", "字母追加"},
		{"comp_ = session_.input(\";\", halfWidth_);", "双拼 ';' 追加"},
		{"comp_ = session_.input(\"'\", halfWidth_);", "撇号追加"},
		{"comp_ = session_.backspace();", "退格"},
		{"comp_ = session_.backspaceWord();", "按音节回退"},
		{"comp_ = isPrev ? session_.prevPage() : session_.nextPage();", "翻页键"},
		{"comp_ = next ? session_.nextPage() : session_.prevPage();", "方向键/鼠标翻页"},
		{"session_.punct(key, halfWidth_, &committed)", "标点"},
		{"session_.selectPrefix(", "渐进上屏"},
	}
	for _, r := range requests {
		sites, guarded := timeoutGuarded(code, r.request)
		if sites == 0 {
			fail("找不到「%s」的引擎请求点 %q（签名/变量名变了？需同步更新本检查）", r.label, r.request)
			continue
		}
		if !guarded {
			fail("「%s」的引擎请求点 %q：失败处理在超时分流（*OnTimeout）之前就走到 fail-open 收尾——"+
				"引擎请求超时必须保留组词，不得擦掉预编辑拼音", r.label, r.request)
		}
	}

	// a2) 入口函数：都会在组词中发起引擎请求，必须带超时分流；鼠标入口在失步中
	// 还要先重建再动作（屏幕上的旧候选对应的是重建前的输入）。
	for _, fn := range []struct {
		name, sig string
		needs     []string
	}{
		{"handleKey", "bool ChaosetTIP::handleKey(WPARAM vk, ITfContext* pic)",
			[]string{"handleKeyDesynced(", "enterDesyncOnTimeout(", "keepCompositionOnTimeout("}},
		{"flipCompositionPage", "void ChaosetTIP::flipCompositionPage(bool next, ITfContext* pic)",
			[]string{"enterDesyncOnTimeout("}},
		{"selectCandidateByIndex", "void ChaosetTIP::selectCandidateByIndex(int index)",
			[]string{"syncDaemonInput(", "enterDesyncOnTimeout("}},
		{"deleteCandidateByIndex", "void ChaosetTIP::deleteCandidateByIndex(int index)",
			[]string{"syncDaemonInput(", "enterDesyncOnTimeout("}},
		{"flipPage", "void ChaosetTIP::flipPage(bool next)",
			[]string{"syncDaemonInput(", "enterDesyncOnTimeout("}},
	} {
		body, line, ok := cppFunctionBody(code, fn.sig)
		if !ok {
			fail("%s 找不到 %s 的函数体（签名变了？）", winShell, fn.name)
			continue
		}
		for _, need := range fn.needs {
			if !strings.Contains(body, need) {
				fail("%s 的 %s（约第 %d 行）缺少 %q——引擎请求超时必须保留组词/先重建再动作",
					winShell, fn.name, line, need)
			}
		}
	}

	// a3) 失步处理器自身：绝不 fail-open 拆组词（放弃时也是把本地内容按原始串
	// 落盘），绝不照旧候选选词。
	if body, line, ok := cppFunctionBody(code, "bool ChaosetTIP::handleKeyDesynced(WPARAM vk, ITfContext* pic, bool* eaten)"); ok {
		for _, bad := range []string{"flushOrCancel(", "selectPrefix("} {
			if strings.Contains(body, bad) {
				fail("%s 的 handleKeyDesynced（约第 %d 行）出现 %q——失步处理器不得拆组词或照旧候选选词",
					winShell, line, bad)
			}
		}
	}

	// a4) 按键路径不套 5s 熔断：熔断会让超时后的下一键直接 fail-open，重连不了。
	if body, line, ok := cppFunctionBody(code, "bool ensureDaemonFast(PipeTransport& transport)"); ok {
		if strings.Contains(body, "circuitOpen(") {
			fail("%s 的 ensureDaemonFast（约第 %d 行）又套上了 circuitOpen 熔断——按键路径每键只做一次非阻塞探测，不得熔断",
				winShell, line)
		}
	} else {
		fail("%s 找不到 ensureDaemonFast 定义", winShell)
	}

	// a5) **读超时不断连**（用户要求：不要因为超时就断开连接导致不能输入中文）。
	// 断连会让 daemon 会话随连接消失、用户已输入的拼音全丢；保留连接时 daemon 的
	// Session 原地不动，下一键就是正常增量输入。写超时仍必须断连（帧可能只写了
	// 一半、字节流无法再对齐），这条区分不能混。
	checkContains(fsys, problems, group, winTransport,
		"bool lastReadTimedOut() const override", "lastReadTimedOut_ = !isWrite",
		"读超时：保留连接")
	if body, line, ok := cppFunctionBody(stripCppComments(mustRead(fsys, winTransport)),
		"bool pipeIo(bool isWrite, const std::uint8_t* data, std::size_t len)"); ok {
		// 读超时分支必须「未消费字节才保留连接」（nothingConsumed 守卫），
		// 写超时与「已消费字节」都必须 failConn。
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
	// a6) **只有「一个字节都没消费」的读超时才能保留连接**。读超时若已部分读入，
	// 这些字节已从管道/socket 取出却被丢弃，帧边界永久错位——保留连接只会让后续每次
	// 读取都解析到垃圾。三端必须用「本次 readAll 是否消费过字节」把这个分支分开，
	// 且 drain 遇到部分读取要断连（不能只清计数）。
	// 断言判据本身（off == 0 表示本次 readAll 没消费过字节；cancelled 是取消前
	// 已传输字节数），不能只断言变量名——把判据改成恒 true 也必须被抓住。
	for _, needle := range []string{"off == 0 && cancelled == 0", "nothingConsumed"} {
		if !strings.Contains(stripCppComments(mustRead(fsys, winTransport)), needle) {
			fail("%s 的读超时分支没有用 %q 判「是否已消费字节」——部分读取必须按连接已坏处理", winTransport, needle)
		}
	}
	checkContains(fsys, problems, group, macTransport, "lastReadTimedOut = (off == 0)")
	// Linux 壳同一契约（三端一致，语义见 core.hpp）：声明在 daemon.hpp、实现在 daemon.cpp。
	checkContains(fsys, problems, group, "shims/linux/engine/daemon.hpp",
		"lastReadTimedOut() const override")
	checkContains(fsys, problems, group, linuxEngineMain, "lastReadTimedOut_ = (off == 0)")
	if body, _, ok := cppFunctionBody(stripCppComments(mustRead(fsys, winCoreCpp)), "bool Session::drainStaleResponse()"); ok {
		// 非干净超时分支必须 closeConn（EOF/真错/部分读取都会让字节流不可用）。
		// 必须精确取「lastReadTimedOut 判断之后、帧长解析之前」这段——drain 里另外
		// 两处不合帧长的分支也照抄了 closeConn，全文搜索会漏掉这一处回退。
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
	// macOS 侧同一契约（Swift 独立实现，必须一起保持）。
	checkContains(fsys, problems, group, macTransport,
		"lastReadTimedOut = true", "func drainStaleResponse()",
		// 结构片段：读超时必须走「保留连接 + 记账待丢」这一支，不能改回无条件 disconnect。
		"if lastReadTimedOut && staleResponses < 2 {", "staleResponses += 1")
	// a7) **读超时后不得原样重放增量 `input`**：daemon 可能已经处理过那次请求（只是
	// 响应迟到），原样重发会把同一段字符追加两次（预编辑 "aa"、上屏错字）。macOS 用
	// base 做幂等重放（daemon 先把引擎输入串置为 base 再追加）、Linux 用 inputWithRebuild；
	// Windows 走既有的失步 base 重建。三端都必须把「壳侧已知的完整输入串」传下去。
	checkContains(fsys, problems, group, macEngineClient,
		// call 层：读超时不得走「重连重试」（那会把增量 input 重放一次）。
		"if transport.lastFailureWasReadTimeout { return nil }",
		// input 层：读超时必须能用 base 做幂等重放（不知道完整串才放弃）。
		"func input(_ text: String, halfWidth: Bool, base: String? = nil,",
		"rebuild: Bool = false) -> Comp?",
		"guard let base else { return nil }",
		// 正常路径不得带 base：带 base 会让 daemon 每键 restore_input，双拼暂存的
		// 半键（dp_pending）随重建被清空，两键永远无法成对解码。只有显式 rebuild
		// 或失败重放才带。
		"if rebuild, let base { params[\"base\"] = base }",
		// 断连重试同样必须带 base：新连接是空会话，不带 base 会丢掉已有组词。
		"if let base { params[\"base\"] = base }",
		// selectPrefix 会消费前缀并写学习数据，必须禁重试，避免读超时后二次消费。
		"func selectPrefix(index: Int) -> Comp? {\n        guard let result = call(method: \"selectPrefix\",\n                                params: [\"index\": index],\n                                retryOnFailure: false) else { return nil }")
	// macOS/Linux 壳均已改异步引擎管线（issue #149）：按键不再直接调引擎，
	// base 幂等重放留在 EngineClient/daemon 层（上面的断言），两端契约分别由
	// 「macOS 异步引擎管线」「Linux 异步引擎管线」组覆盖。
	checkContains(fsys, problems, group, linuxEngineMain, "Comp Daemon::inputWithRebuild(",
		"session_.input(text, halfWidth, base.c_str())")

	// a8) **退格/翻页失败不得吞键、不得在失败路径上强上屏**。退格键若在组词中失败
	// 就直接放行给宿主，会误删文档内容；而旧实现反过来「吞键 + flushConfirmed 强上屏」，
	// 失败时把已确认段写了出去。现行契约：退格失败保留组词与按键（下一次 input 带 base
	// 重放修正状态）、翻页失败原样放行（翻页键在宿主里无破坏性）。
	// macOS/Linux 退格在异步管线下只入队；失败结果不弹出乐观项 → 组词与按键
	// 天然保留，由各自的异步管线组断言（不再有同步 guard/收尾函数可查）。

	// a9) **选词失败的上屏文本必须是候选中文，不是拼音串**：daemon 不可达时 macOS 壳会
	// 退化为「整句上屏」保证不丢输入；用 cur.input（如 "nihao"）上屏是错误结果，应当用
	// 壳侧上一次成功响应里的候选文本（本机可靠数据）。Linux 侧选词失败必须消费按键并
	// 保留组词——放行（return false）会把空格/数字真的打进文档。
	// macOS 选词失败兜底已迁到异步管线（ChaosetControllerAsync 的失败分支用
	// 壳侧已显示文本上屏），由「macOS 异步引擎管线」组断言；这里保留 Linux/Windows。
	if strings.Count(code, "committed = pickedText;") < 3 {
		fail("%s 的 selectPrefix 失败退化分支不足 3 处（数字/空格/点选各需退化上屏候选中文）", winShell)
	}
	if !strings.Contains(code, "commit(ctx.get(), pick.text)") {
		fail("%s 的联想点选失败缺少退化上屏 pick.text", winShell)
	}
	// macOS 的选词失败兜底与 Linux 的选词失败兜底都已迁到各自异步管线的失败
	// 分支（壳侧已显示文本上屏），由「macOS/Linux 异步引擎管线」组断言。
	// 联想选词（空格确认与数字确认两条路径）失败同样要退化上屏候选文本，不能静默无动作。
	// 联想选词失败兜底：macOS 异步管线在 handleEngineFailure 的 associateSelect
	// 分支统一上屏壳侧文本（见「macOS 异步引擎管线」组）。

	// UT 把关：脚本化传输模拟「读超时 → 迟到响应」，六条断言必须存在。
	checkContains(fsys, problems, group, winCoreTest,
		"testReadTimeoutKeepsConnection", "ScriptedTransport",
		"读超时不得断开连接", "迟到响应被丢弃", "写失败必须断连",
		"部分读取后超时（字节流已错位）必须断连", "drainStaleResponse 遇到部分读取必须断连")

	// a10) **TSF 按键路径的截止时间两档**（issue #149）：正常按键 150ms、失步重建 30ms。
	// 旧实现全路径 30ms，低于整链路长尾（本仓 macOS 壳 8 核满载实测 p99 17–20ms、
	// 最大 31–57ms），
	// 正常输入会被误判成失步——失步期的选词/翻页键只刷新候选、不执行本次选词。两档
	// 的分工：150ms 覆盖长尾、仍远小于 macOS/Linux 独立进程壳的 250/300ms（Windows
	// TIP 在宿主 UI 线程内同步执行，最坏阻塞必须更小）；失步重建只做「尽快回到同步
	// 态」，用 30ms 短截止，避免 daemon 繁忙/挂起时把宿主 UI 线程按在长截止上。
	checkContains(fsys, problems, group, winTransport,
		"constexpr DWORD kKeyRequestDeadlineMs = 150;",
		"constexpr DWORD kDesyncRebuildDeadlineMs = 30;")
	checkContains(fsys, problems, group, winTipCore,
		"transport_.setAutoConnect(false);",
		"transport_.setDeadlineMs(kKeyRequestDeadlineMs);")
	checkContains(fsys, problems, group, winTipDesync,
		"transport_.setDeadlineMs(kDesyncRebuildDeadlineMs);",
		"transport_.setDeadlineMs(kKeyRequestDeadlineMs);")
	// 截止切换必须按「切短 → 发重建请求 → 还回正常截止」的顺序：顺序错要么让重建
	// 仍按长截止阻塞宿主 UI 线程，要么把后续所有按键都压在 30ms 预算上。
	if body, line, ok := cppFunctionBody(stripCppComments(mustRead(fsys, winTipDesync)),
		"ChaosetTIP::SyncResult ChaosetTIP::syncDaemonInput()"); ok {
		iShort := strings.Index(body, "setDeadlineMs(kDesyncRebuildDeadlineMs)")
		iInput := strings.Index(body, "session_.input(")
		iRestore := strings.Index(body, "setDeadlineMs(kKeyRequestDeadlineMs)")
		if iShort < 0 || iInput < 0 || iRestore < 0 || !(iShort < iInput && iInput < iRestore) {
			fail("%s 的 syncDaemonInput（约第 %d 行）截止时间切换顺序不对：应先切短截止 → 发重建请求 → 还回正常截止",
				winTipDesync, line)
		}
	} else {
		fail("%s 找不到 syncDaemonInput 定义", winTipDesync)
	}
	// 截止只能走命名常量：散落的字面量会让两档语义在后续修改中静默漂移。
	checkExcludes(fsys, problems, group, winTipCore, "setDeadlineMs(30)", "setDeadlineMs(150)")
	checkExcludes(fsys, problems, group, winTipDesync, "setDeadlineMs(30)", "setDeadlineMs(150)")

	// b) 协议三处都认 input 的 base。
	checkContains(fsys, problems, group, winShell, "daemonDesynced_", "pendingKeys_", "lastIoWasTimeout(")
	checkContains(fsys, problems, group, winCoreHpp, "const char* base = nullptr")
	checkContains(fsys, problems, group, winCoreCpp, `,\"base\":\"`)
	checkContains(fsys, problems, group, ipcServer, `params.get("base")`, "input_with_base(")
	checkContains(fsys, problems, group, rustSession, "pub fn restore_input(", "pub fn input_with_base(")

	// c) daemon 命名管道服务端：常驻监听实例、主循环不阻塞在连接上、失败退避重试。
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

	// 文档必须记录契约（防止实现与 docs/ui-parity.md 脱节）。
	checkContains(fsys, problems, group, uiParityDoc, "失步", "daemonDesynced_",
		"enterDesyncOnTimeout", "TSF 正常按键 150ms、", "失步重建 30ms")
}
