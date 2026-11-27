// checks_macos_async.go：macOS IMK 壳的异步引擎管线（issue #149）。
//
// 旧「超时韧性」组里与 macOS 同步路径绑定的断言（KeyRouter 的 backspace guard、
// selectPrefix/associate 失败退化、base 幂等重放等）在改成异步后不再成立：语义
// 平移到这里（失败保留组词/候选窗、显式选词兜底、慢不关窗），并新增零等待、
// 有序应用、纪元作废、线程隔离等不变量。Windows/Linux 的同步壳断言仍留在旧组。
package main

import (
	"fmt"
	"io/fs"
	"strings"
)

const groupMacOSAsync = "macOS 异步引擎管线"

const (
	macAsyncPipeline   = "shims/macos/src/EnginePipeline.swift"
	macAsyncController = "shims/macos/src/ChaosetControllerAsync.swift"
	macKeyRouterFile   = "shims/macos/src/KeyRouter.swift"
	macTestMain        = "shims/macos/tests/key_routing/main.swift"
	macKeyRunScript    = "shims/macos/tests/key_routing/run.sh"
)

// swiftBraceBlock 返回 anchor 之后第一个花括号块（含花括号）；找不到返回 ""。
// 用于精确判定「某分支块内」是否有禁止语句（全文 Index 会被块内位置骗过）。
func swiftBraceBlock(body, anchor string) string {
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

func checkMacOSAsyncPipeline(fsys fs.FS, problems *[]string) {
	const group = groupMacOSAsync
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf("%s：%s", group, fmt.Sprintf(format, args...)))
	}
	read := func(p string) string { return string(mustRead(fsys, p)) }

	// a) 管线存在：每控制器独占串行队列与自己的 EngineClient（daemon 组词会话
	// 按连接隔离），结果回主队列。
	checkContains(fsys, problems, group, macAsyncPipeline,
		"final class EnginePipeline",
		"private let queue = DispatchQueue(",
		"private let client: EngineClient",
		"DispatchQueue.main.async",
		"func submit(",
		"var connected: Bool",
		"unboundedRead: true",
		"client.abort()")
	checkContains(fsys, problems, group, macTransport,
		"private let unboundedRead: Bool", "func abort()", "poll(")

	// b) IMK 回调零等待：按键/控制器文件里不得直接调用引擎，也不得有阻塞原语。
	for _, p := range []string{macKeyRouterFile, macController, macAsyncController} {
		code := stripCppComments(read(p))
		for _, bad := range []string{
			"engine.input(", "engine.backspace(", "engine.backspaceWord(",
			"engine.select(", "engine.selectPrefix(", "engine.punct(",
			"engine.associate(", "engine.nextPage(", "engine.prevPage(",
			"engine.reset(", "engine.resetComposition(", "engine.userDelete(",
			"engine.commitRaw(", "DispatchSemaphore", "usleep(", "sleep(",
		} {
			if strings.Contains(code, bad) {
				fail("%s 出现 %q——IMK 主线程只允许入队 + 乐观渲染，引擎 I/O 必须在 EnginePipeline 线程", p, bad)
			}
		}
	}

	// c) 按键只入队 + 乐观预编辑；结果按 seq 顺序应用，且**失败先判、不弹出乐观项**。
	checkContains(fsys, problems, group, macAsyncController,
		"func optimisticComp()", "case .input:", "case .backspace:",
		"pending += op.text",
		"while let first = pendingOps.first, first.seq <= r.op.seq",
		"if !r.ok {", "handleEngineFailure(r)")
	// 按音节退格（⌥⌫）必须与 Windows/Linux 一样本地回放（预编辑立即变化），
	// 否则三端在结果回来前的显示行为不一致。
	checkContains(fsys, problems, group, macAsyncController,
		"func backspaceLocally(",
		"case .backspaceWord:",
		"backspaceLocally(&disp, &pending, shuangpin: shuangpin, bySyllable: true)")
	ctrl := stripCppComments(read(macAsyncController))
	iFail := strings.Index(ctrl, "if !r.ok {")
	iPop := strings.Index(ctrl, "while let first = pendingOps.first, first.seq <= r.op.seq")
	if iFail < 0 || iPop < 0 || iFail > iPop {
		fail("%s 的失败结果在出队之后才判——失败必须保留乐观项（本地显示不被抹掉）", macAsyncController)
	}

	// d) 纪元作废：失焦/取消/会话结束递增 epoch、清空乐观项并让引擎复位。
	checkContains(fsys, problems, group, macAsyncController,
		"func resetEnginePipeline(keepLearning: Bool)",
		"engineEpoch += 1", "pendingOps.removeAll()",
		"guard r.op.epoch == engineEpoch else { return }")
	if n := strings.Count(read(macController), "resetEnginePipeline("); n < 3 {
		fail("%s 的会话结束路径（activateServer/deactivateServer/endSession）没有全部复位管线（%d 处）", macController, n)
	}

	// e) 失败语义与 loading 占位：不弹乐观项、不关候选窗；显式选词/联想/标点用壳侧
	// 已显示文本兜底；在飞请求期间展示单个平静 "..." 占位，绝不靠 2s 强制清会话。
	checkContains(fsys, problems, group, macAsyncController,
		"case .selectPrefix:", "insertText(confirmedText + text",
		"insertText(confirmedText + r.op.text", "restartEngineFromLocalState()",
		"break  // 失败不收起联想面板",
		"var isLoadingCandidates: Bool",
		"func markEngineLost()", "func scheduleRebuild()")
	checkContains(fsys, problems, group, macController,
		"showNativeLoadingPanel(client: client)")
	checkContains(fsys, problems, group, "shims/macos/src/CandidatePanel.swift",
		"static func showLoading(")
	failBlock := swiftBraceBlock(ctrl, "private func handleEngineFailure(_ r: EngineResult) {")
	if failBlock == "" {
		fail("%s 找不到 handleEngineFailure 函数体", macAsyncController)
	} else {
		for _, bad := range []string{"CandidatePanelController.hide()", "candidateWindow?.hide()", "exitAssociate()"} {
			if strings.Contains(failBlock, bad) {
				fail("%s 的失败处理出现 %q——daemon 慢/失败不得关闭候选窗或清空组词", macAsyncController, bad)
			}
		}
	}
	// 候选窗只按既有语义路径关闭：refresh 的隐藏必须受「没有在飞按键」保护。
	if !strings.Contains(read(macController), "if pendingOps.isEmpty {") {
		fail("%s 的 refresh 没有把关闭候选窗限制在「没有在飞按键」时（慢/失败不关窗）", macController)
	}

	// f) 显式选词走有序管线：数字/空格在列表滞后时入队，不漏进文档、不顶屏拼音。
	checkContains(fsys, problems, group, macKeyRouterFile,
		"submitSelectPrefix(index:", "submitAssociateSelect(", "submitPunct(",
		`submitSelectPrefix(index: idx, text: "", syllables: [])`,
		`submitSelectPrefix(index: 0, text: "", syllables: [])`)

	// g) 测试覆盖：乐观预编辑 / 有序选词 / 失败不关窗，且 run.sh 用通配收集新文件。
	checkContains(fsys, problems, group, macAsyncPipeline,
		"sessionDesynced", "syncedConnGen", "lastFailureWasTransportError")
	checkContains(fsys, problems, group, macAsyncController,
		"submitNonComposingOp(", "hasDpPending()", "consumePrefixFallback(",
		"startEngineLostTimer()", "splitForPunctFallback(", "remainderAfterSelectPrefix(")
	checkContains(fsys, problems, group, macTestMain,
		"settleEngineForTests()", "failureInjectionForTests",
		"异步：结果未回投时按键立即出现在乐观预编辑里",
		"异步：快打后空格按完整输入选词（不是原始拼音）",
		"daemon 失败：候选窗不关闭",
		"⌥⌫ 乐观回放：结果未回投时预编辑立即退掉末音节",
		"⌥⌫ 乐观回放：用户自己敲的分隔符保留在输入串里")
	checkContains(fsys, problems, group, macKeyRunScript, "src/*.swift")

	// h) 文档记录异步契约。
	checkContains(fsys, problems, group, uiParityDoc,
		"macOS 异步引擎管线", "EnginePipeline", "乐观预编辑", "不关闭候选窗")
}
