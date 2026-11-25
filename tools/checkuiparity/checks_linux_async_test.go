// checks_linux_async_test.go：Linux 异步引擎管线的变异回归。
//
// 真实仓库必须通过；以下回退都必须被捕获（任何一条都会让按键重新阻塞 ibus
// 主循环、丢键或把过期结果写进新会话）：
//  1. 按键路径重新直连 daemon；
//  2. 结果应用去掉乐观项出队（seq 顺序失效）；
//  3. 失败结果也弹出乐观项（用户刚输入的拼音被抹掉）；
//  4. 失焦/取消不作废在飞结果（纪元不递增）；
//  5. 宽限放弃不按原始串落盘（丢键）；
//  6. 显式选词失败不兜底上屏（静默丢选词）。
package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestCheckLinuxAsyncPipelineMutations(t *testing.T) {
	const (
		linuxMain     = "shims/linux/engine/main.cpp"
		linuxAsync    = "shims/linux/engine/async_pipeline.cpp"
		linuxStateHpp = "shims/linux/engine/engine_state.hpp"
	)
	files := []string{
		linuxMain, linuxAsync, linuxStateHpp,
		"shims/linux/engine/engine_pump.hpp",
		"shims/linux/engine/engine_pump.cpp",
		"shims/linux/engine/daemon.hpp",
		"shims/linux/engine/build.sh",
		uiParityDoc,
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
	checkLinuxAsyncPipeline(build(nil), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实实现未通过 Linux 异步契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	expect := func(name, path, old, new, want string) {
		t.Helper()
		if !strings.Contains(orig[path], old) {
			t.Fatalf("%s 未生效：%s 里找不到 %q", name, path, old)
		}
		mut := map[string]string{path: strings.Replace(orig[path], old, new, 1)}
		var problems []string
		checkLinuxAsyncPipeline(build(mut), &problems)
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, groupLinuxAsync) {
			t.Fatalf("%s 应被捕获，实际：%v", name, problems)
		}
		if !strings.Contains(joined, want) {
			t.Fatalf("%s 应报出 %q，实际：%v", name, want, problems)
		}
	}

	expect("按键路径重新直连 daemon", linuxMain,
		"            submitInput(std::string(1, static_cast<char>(keyval)));",
		"            daemon_.ensureReady(); submitInput(std::string(1, static_cast<char>(keyval)));",
		"daemon_.")
	expect("结果应用去掉乐观项出队（seq 顺序失效）", linuxAsync,
		"    while (!pendingOps_.empty() && pendingOps_.front().seq <= r.op.seq) pendingOps_.pop_front();",
		"",
		"pendingOps_.front().seq <= r.op.seq")
	expect("失败结果也弹出乐观项", linuxAsync,
		"    if (!r.ok) {\n        // 失败结果不弹出乐观项",
		"    if (!r.ok) {\n        while (!pendingOps_.empty() && pendingOps_.front().seq <= r.op.seq) pendingOps_.pop_front();\n        // 失败结果不弹出乐观项",
		"弹出了乐观项")
	expect("失焦/取消不作废在飞结果", linuxAsync,
		"        ++epoch_;\n        pendingOps_.clear();",
		"        pendingOps_.clear();",
		"++epoch_")
	expect("宽限放弃不按原始串落盘", linuxAsync,
		"        confirmed_.clear();\n        commit(raw);",
		"        confirmed_.clear();",
		"commit(raw)")
	expect("显式选词失败不兜底上屏", linuxAsync,
		"            if (!text.empty()) commit(all);",
		"            // 兜底删除",
		"if (!text.empty()) commit(all);")
}
