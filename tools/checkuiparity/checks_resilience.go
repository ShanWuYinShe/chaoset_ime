// checks_resilience.go：超时韧性——「引擎请求超时不拆组词」的端到端契约。
//
// issue #122：壳侧按键请求有 30ms 截止，而桌面系统上几十毫秒的调度停顿无法
// 杜绝（引擎再快也一样）。旧实现在任一引擎请求超时后立刻拆组词——预编辑拼音
// 被擦掉、候选窗关闭、当前键放行成英文——并叠加 5s 熔断，用户看到「候选窗
// 突然关闭，之后只能打英文」。现行契约分三段，任何一段回退症状都会复现：
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
		"if let base { params[\"base\"] = base }")
	checkContains(fsys, problems, group, "shims/macos/src/KeyRouter.swift", "base: current?.input ?? \"\"")
	checkContains(fsys, problems, group, linuxEngine, "inputWithRebuild")
	checkContains(fsys, problems, group, linuxEngineMain, "Comp Daemon::inputWithRebuild(",
		"session_.input(text, halfWidth, base.c_str())")

	// a8) **退格/翻页失败不得吞键、不得在失败路径上强上屏**。退格键若在组词中失败
	// 就直接放行给宿主，会误删文档内容；而旧实现反过来「吞键 + flushConfirmed 强上屏」，
	// 失败时把已确认段写了出去。现行契约：退格失败保留组词与按键（下一次 input 带 base
	// 重放修正状态）、翻页失败原样放行（翻页键在宿主里无破坏性）。
	checkContains(fsys, problems, group, "shims/macos/src/KeyRouter.swift",
		"guard let comp = engine.backspaceWord() else {",
		"guard let comp = engine.backspace() else {",
		"guard let comp = isPageUp ? engine.prevPage() : engine.nextPage() else {")
	// 失败分支必须只 refresh/放行，不能 flushConfirmed、不能把 current 置空。
	for _, bad := range []string{"current = engine.backspace()", "current = engine.backspaceWord()"} {
		if strings.Contains(stripCppComments(mustRead(fsys, "shims/macos/src/KeyRouter.swift")), bad) {
			fail("%s 出现 %q——退格调用必须 guard 失败分支（失败时保留组词与按键）", "shims/macos/src/KeyRouter.swift", bad)
		}
	}
	// Linux：同一个收尾辅助函数里，失败必须直接返回（保留组词与按键）。
	linuxMain := stripCppComments(mustRead(fsys, linuxEngine))
	if i := strings.Index(linuxMain, "bool handleBackspace(chaoset::Comp comp) {"); i >= 0 {
		if !strings.Contains(linuxMain[i:i+200], "if (!comp.ok) return true;") {
			fail("%s 的 handleBackspace 失败分支没有保留组词/按键——退格不得穿透误删、也不得强上屏", linuxEngine)
		}
	} else {
		fail("%s 找不到 handleBackspace 收尾函数", linuxEngine)
	}

	// a9) **选词失败的上屏文本必须是候选中文，不是拼音串**：daemon 不可达时 macOS 壳会
	// 退化为「整句上屏」保证不丢输入；用 cur.input（如 "nihao"）上屏是错误结果，应当用
	// 壳侧上一次成功响应里的候选文本（本机可靠数据）。Linux 侧选词失败必须消费按键并
	// 保留组词——放行（return false）会把空格/数字真的打进文档。
	checkContains(fsys, problems, group, macController,
		"let fallback = cur.candidates[index].text",
		"(engine.select(index) ?? fallback)")
	if strings.Contains(stripCppComments(mustRead(fsys, macController)), "engine.select(index) ?? cur.input") {
		fail("%s 的选词失败 fallback 又用拼音串 cur.input——daemon 挂了会上屏英文拼音", macController)
	}
	checkContains(fsys, problems, group, linuxEngine,
		"if (!daemon_.session().selectPrefix(idx, &rest)) return true;")

	// UT 把关：脚本化传输模拟「读超时 → 迟到响应」，六条断言必须存在。
	checkContains(fsys, problems, group, winCoreTest,
		"testReadTimeoutKeepsConnection", "ScriptedTransport",
		"读超时不得断开连接", "迟到响应被丢弃", "写失败必须断连",
		"部分读取后超时（字节流已错位）必须断连", "drainStaleResponse 遇到部分读取必须断连")

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
	checkContains(fsys, problems, group, uiParityDoc, "失步", "daemonDesynced_", "enterDesyncOnTimeout")
}
