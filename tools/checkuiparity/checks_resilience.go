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
