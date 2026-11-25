// checks_macos_async_test.go：macOS 异步引擎管线的变异测试。
//
// 真实仓库必须通过；以下回退都必须被捕获（任何一条都会让 IMK 回调重新阻塞宿主、
// 或让 daemon 慢/失败时关候选窗/丢按键）：
//  1. IMK 回调重新同步调用引擎；
//  2. 结果应用不再按 seq 出队（乐观项永远不消失）；
//  3. 失败结果也先出队（用户刚输入的预编辑被抹掉）；
//  4. 失败处理关闭候选窗/清空组词；
//  5. 纪元作废被删（失焦后的迟到结果写进新会话）；
//  6. 显式选词失败静默（不上屏兜底文本）；
//  7. refresh 不再保护在飞按键（慢就关候选窗）；
//  8. 文档删掉异步契约。
package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestCheckMacOSAsyncPipeline(t *testing.T) {
	files := []string{
		macAsyncPipeline, macAsyncController, macKeyRouterFile, macController,
		macTestMain, macKeyRunScript, uiParityDoc,
	}
	orig := map[string]string{}
	for _, p := range files {
		orig[p] = string(readRepo(t, p))
	}
	build := func(mut map[string]string) fstest.MapFS {
		m := fstest.MapFS{}
		for _, p := range files {
			data := orig[p]
			if s, ok := mut[p]; ok {
				data = s
			}
			m[p] = &fstest.MapFile{Data: []byte(data)}
		}
		return m
	}

	var problems []string
	checkMacOSAsyncPipeline(build(nil), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实实现未通过 macOS 异步管线契约（%d 项）：\n%s",
			len(problems), strings.Join(problems, "\n"))
	}

	expect := func(name, path, old, new string, want ...string) {
		t.Helper()
		if !strings.Contains(orig[path], old) {
			t.Fatalf("%s 未生效：%s 里找不到 %q", name, path, old)
		}
		mut := map[string]string{path: strings.Replace(orig[path], old, new, 1)}
		var problems []string
		checkMacOSAsyncPipeline(build(mut), &problems)
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, groupMacOSAsync) {
			t.Fatalf("%s 应被捕获，实际：%v", name, problems)
		}
		for _, w := range want {
			if !strings.Contains(joined, w) {
				t.Fatalf("%s 应报出 %q，实际：%v", name, w, problems)
			}
		}
	}

	expect("IMK 回调重新同步调引擎", macKeyRouterFile,
		"        if isCompositionChar(chars) {",
		"        if isCompositionChar(chars) {\n            _ = engine.input(chars, halfWidth: halfWidth)",
		"engine.input(")
	expect("结果应用不再按 seq 出队", macAsyncController,
		"        while let first = pendingOps.first, first.seq <= r.op.seq {",
		"        while false {",
		"while let first = pendingOps.first")
	expect("失败结果也先出队", macAsyncController,
		"        if !r.ok {",
		"        while let first = pendingOps.first, first.seq <= r.op.seq { pendingOps.removeFirst() }\n        if !r.ok {",
		"失败必须保留乐观项")
	expect("失败处理关闭候选窗", macAsyncController,
		"        case .associate:\n            break  // 失败不收起联想面板",
		"        case .associate:\n            CandidatePanelController.hide()",
		"不得关闭候选窗")
	expect("纪元作废被删", macAsyncController,
		"        guard r.op.epoch == engineEpoch else { return }  // 过期（失焦/取消后的结果）",
		"",
		"guard r.op.epoch == engineEpoch")
	expect("显式选词失败静默", macAsyncController,
		"                client?.insertText(confirmedText + text,",
		"                // 兜底删掉",
		"insertText(confirmedText + text")
	expect("refresh 不再保护在飞按键", macController,
		"            if pendingOps.isEmpty {\n                exitAssociate()",
		"            if true {\n                exitAssociate()",
		"关闭候选窗限制")
	expect("文档删掉异步契约", uiParityDoc,
		"不关闭候选窗", "关闭候选窗", "不关闭候选窗")
}
