package main

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// readRepo 读取逻辑文件（与 Run 相同的多源聚合）。2026-11-06 架构拆分后，
// 单测不能直接读单文件：CandidatePanel.swift / win32.cpp 等只是聚合的一部分。
func readRepo(t *testing.T, p string) []byte {
	t.Helper()
	data, err := fs.ReadFile(bundledFS(os.DirFS("../..")), p)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", p, err)
	}
	return data
}

// TestRunOnRepo 在真实仓库上跑一遍全部契约：
//   - 当前仓库必须通过（否则本工具会把 CI 卡红）；
//   - 任何一条规则对应的文件/文案被删改时，本测试会先失败（规则接线回归）。
func TestRunOnRepo(t *testing.T) {
	problems := Run(os.DirFS("../.."))
	if len(problems) != 0 {
		t.Fatalf("真实仓库未通过 UI 一致性契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

func TestCheckKeyRoutingCount(t *testing.T) {
	valid := "let expectedChecks = 2\ncheck(\"a\", true)\ncheck(\"b\", true)\n"
	fsys := fstest.MapFS{macKeyTest: &fstest.MapFile{Data: []byte(valid)}}
	var problems []string
	checkKeyRoutingCount(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("expectedChecks 与断言数一致时应通过，实际：%v", problems)
	}

	bad := strings.Replace(valid, "expectedChecks = 2", "expectedChecks = 3", 1)
	problems = nil
	checkKeyRoutingCount(fstest.MapFS{macKeyTest: &fstest.MapFile{Data: []byte(bad)}}, &problems)
	if len(problems) != 1 || !strings.Contains(problems[0], "不一致") {
		t.Fatalf("expectedChecks 与断言数不一致应被捕获，实际：%v", problems)
	}
}

// TestCheckCandidateWindow 用真实候选窗源码验证候选窗契约，并做一次变异：
// 改 Windows 正文字号应被「候选窗几何」捕获。
func TestCheckCandidateWindow(t *testing.T) {
	macText := readRepo(t, macPanel)
	winText := string(readRepo(t, winShell))
	buildText := readRepo(t, winBuild)
	fsys := fstest.MapFS{
		macPanel: &fstest.MapFile{Data: []byte(macText)},
		winShell: &fstest.MapFile{Data: []byte(winText)},
		winBuild: &fstest.MapFile{Data: buildText},
	}
	var problems []string
	checkCandidateWindow(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实候选窗源码未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	mutated := strings.Replace(string(winText), "-MulDiv(15, dpi, 72)", "-MulDiv(16, dpi, 72)", 1)
	if mutated == string(winText) {
		t.Fatal("变异未生效：Windows 源码里找不到 -MulDiv(15, dpi, 72)")
	}
	problems = nil
	checkCandidateWindow(fstest.MapFS{
		macPanel: &fstest.MapFile{Data: []byte(macText)},
		winShell: &fstest.MapFile{Data: []byte(mutated)},
		winBuild: &fstest.MapFile{Data: buildText},
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "正文字号") {
		t.Fatalf("改一端字号应被捕获，实际：%v", problems)
	}
}

// TestCheckSettingsGearAndTray 用真实源码验证 2026-11-06 阶段2 新增契约，并做
// 三次变异：
//  1. mac mouseDown 直接 onSettings?()（齿轮按下即弹窗）必须被捕获；
//  2. Windows onLButtonDown 直接 showSettingsWindow 必须被捕获；
//  3. 安装器重新写 ChaosetUi Run 项必须被捕获。
func TestCheckSettingsGearAndTray(t *testing.T) {
	paths := []string{macPanel, winShell, linuxReadme, winInstaller, winInstallBat,
		uiParityDoc, macLauncher, winLauncher, winLauncherHpp, winUiHost,
		slintApp, slintBridge, slintEvents, slintOpenUrl, slintComponents, linuxSetup, linuxComp,
		installSh, linuxArchPkg, makefile, macBuild, macInstall, archDoc}
	fsys := fstest.MapFS{}
	for _, p := range paths {
		fsys[p] = &fstest.MapFile{Data: readRepo(t, p)}
	}
	var problems []string
	checkSettingsCenterContract(fsys, &problems)
	checkSettingsSharedText(fsys, &problems)
	checkCandidateGearMouseUp(fsys, &problems)
	checkTrayLazyLifecycle(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实源码未通过设置中心契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}
	// 变异 1：Windows 启动器里删掉设置中心文件名 → 回退成没有设置入口。
	winLaunch := string(fsys[winLauncher].Data)
	mutated := strings.Replace(winLaunch, `kSettingsExeName[] = L"chaoset-settings.exe"`, `kSettingsExeName[] = L"chaoset-settings.exe.off"`, 1)
	if mutated == winLaunch {
		t.Fatal("变异未生效：settings_launcher.cpp 里找不到 chaoset-settings.exe")
	}
	mut := fstest.MapFS{}
	for k, v := range fsys {
		mut[k] = v
	}
	mut[winLauncher] = &fstest.MapFile{Data: []byte(mutated)}
	problems = nil
	checkSettingsCenterContract(mut, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "chaoset-settings.exe") {
		t.Fatalf("Windows 启动器缺设置中心文件名应被捕获，实际：%v", problems)
	}
	// 变异 2：Slint 设置中心尺寸回退（640px → 600px）应被捕获。
	slintText := string(fsys[slintApp].Data)
	mutatedSlint := strings.Replace(slintText, "preferred-width: 640px;", "preferred-width: 600px;", 1)
	if mutatedSlint == slintText {
		t.Fatal("变异未生效：app.slint 里找不到 preferred-width: 640px;")
	}
	mut[slintApp] = &fstest.MapFile{Data: []byte(mutatedSlint)}
	problems = nil
	checkSettingsCenterContract(mut, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "preferred-width: 640px") {
		t.Fatalf("Slint 设置中心尺寸回退应被捕获，实际：%v", problems)
	}
}

// TestCheckTsfZeroBlockContract 用真实 win32.cpp + ui-parity 文档验证零阻塞契约，
// 并做 4 个变异（任一回归都必须被捕获）：
//  1. TSF 回调（Activate/OnSetFocus 共享调用点）体内注入 Sleep(1)——宿主 UI 线程睡眠；
//  2. diagPrintf 去掉 enqueueLogLine——tsf.log 回退同步写；
//  3. 前台钩子回调注入 Sleep(1)——钩子回调不再只 PostMessage；
//  4. DllCanUnloadNow 去掉 g_liveTipMsgWindows——worker/消息窗口活着时卸载 DLL。
func TestCheckTsfZeroBlockContract(t *testing.T) {
	winText := string(readRepo(t, winShell))
	docText := readRepo(t, uiParityDoc)
	fsys := func(win string) fstest.MapFS {
		return fstest.MapFS{
			winShell:    &fstest.MapFile{Data: []byte(win)},
			uiParityDoc: &fstest.MapFile{Data: docText},
		}
	}
	var problems []string
	checkTsfZeroBlockContract(fsys(string(winText)), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实零阻塞实现未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 变异 1：TSF 回调体内注入 Sleep(1)（注入到 Activate 回调体；2026-11-07
	// 拆分后 syncEnglishFromAuthority 的首次出现位置在状态机辅助函数里，不再
	// 属于被检查的回调体，所以锚点改为 Activate 体内的语句）。
	mutated := strings.Replace(string(winText),
		"g_lastOp = kOpActivate;", "Sleep(1); g_lastOp = kOpActivate;", 1)
	if mutated == string(winText) {
		t.Fatal("变异 1 未生效：源码里找不到 g_lastOp = kOpActivate;")
	}
	problems = nil
	checkTsfZeroBlockContract(fsys(mutated), &problems)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "TSF 回调零跨进程等待") || !strings.Contains(joined, "Sleep(") {
		t.Fatalf("TSF 回调注入 Sleep 应被捕获，实际：%v", problems)
	}

	// 变异 2：diagPrintf 去掉 enqueueLogLine（tsf.log 回退同步写）。
	mutated = strings.Replace(string(winText), "enqueueLogLine(line, n + m);", "", 1)
	if mutated == string(winText) {
		t.Fatal("变异 2 未生效：源码里找不到 enqueueLogLine(line, n + m);")
	}
	problems = nil
	checkTsfZeroBlockContract(fsys(mutated), &problems)
	if joined = strings.Join(problems, "\n"); !strings.Contains(joined, "enqueueLogLine") {
		t.Fatalf("diagPrintf 去掉 enqueueLogLine 应被捕获，实际：%v", problems)
	}

	// 变异 3：前台钩子回调不再只 PostMessage（注入 Sleep 并把 PostMessage 注释掉）。
	mutated = strings.Replace(string(winText),
		"PostMessageW(g_tlTipMsgWnd, kTipForegroundMsg,",
		"Sleep(1); // PostMessageW(g_tlTipMsgWnd, kTipForegroundMsg,", 1)
	if mutated == string(winText) {
		t.Fatal("变异 3 未生效：源码里找不到钩子回调的 PostMessageW")
	}
	problems = nil
	checkTsfZeroBlockContract(fsys(mutated), &problems)
	joined = strings.Join(problems, "\n")
	if !strings.Contains(joined, "前台钩子回调") || !strings.Contains(joined, "Sleep(") {
		t.Fatalf("前台钩子回调注入 Sleep/去掉 PostMessage 应被捕获，实际：%v", problems)
	}

	// 变异 4：DllCanUnloadNow 不计 message-only 窗口。
	mutated = strings.Replace(string(winText),
		"    if (g_liveTipMsgWindows != 0) return S_FALSE;\n", "", 1)
	if mutated == string(winText) {
		t.Fatal("变异 4 未生效：源码里找不到 g_liveTipMsgWindows 计数")
	}
	problems = nil
	checkTsfZeroBlockContract(fsys(mutated), &problems)
	if joined = strings.Join(problems, "\n"); !strings.Contains(joined, "DllCanUnloadNow") {
		t.Fatalf("DllCanUnloadNow 去掉 message-only 窗口计数应被捕获，实际：%v", problems)
	}
}

func TestCheckShellVarEscaping(t *testing.T) {
	bad := fstest.MapFS{
		"shims/a.sh": &fstest.MapFile{Data: []byte("echo \"（$ARCH）\"\n")},
	}
	var problems []string
	checkShellVarEscaping(bad, &problems)
	if len(problems) != 1 || !strings.Contains(problems[0], "a.sh:1") {
		t.Fatalf("应捕获 $VAR 紧跟非 ASCII，实际：%v", problems)
	}

	good := fstest.MapFS{
		"shims/a.sh": &fstest.MapFile{Data: []byte("echo \"（${ARCH}）\"\n")},
	}
	problems = nil
	checkShellVarEscaping(good, &problems)
	if len(problems) != 0 {
		t.Fatalf("${VAR} 形式应通过，实际：%v", problems)
	}
}

// TestCheckImportSafety 用真实两端源码 + Rust 导入工具验证导入安全契约，并做两次变异：
// 设置中心或 Rust 导入实现去掉 -backup（备份语义不能丢）都应被捕获。
func TestCheckImportSafety(t *testing.T) {
	macText := string(readRepo(t, macSetup))
	winText := string(readRepo(t, winShell))
	bridgeText := string(readRepo(t, slintBridge))
	eventsText := string(readRepo(t, slintEvents))
	importText := string(readRepo(t, chaosetImport))
	fsText := string(readRepo(t, rustImportFs))
	base := func(bridge, importSrc string) fstest.MapFS {
		return fstest.MapFS{
			macSetup:      &fstest.MapFile{Data: []byte(macText)},
			winShell:      &fstest.MapFile{Data: []byte(winText)},
			slintBridge:   &fstest.MapFile{Data: []byte(bridge)},
			slintEvents:   &fstest.MapFile{Data: []byte(eventsText)},
			chaosetImport: &fstest.MapFile{Data: []byte(importSrc)},
			rustImportFs:  &fstest.MapFile{Data: []byte(fsText)},
		}
	}
	var problems []string
	checkImportSafety(base(bridgeText, importText), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实导入实现未通过安全契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}
	// 变异 1：设置中心导入调用去掉 -backup（备份语义不能丢）应被捕获。
	mutated := strings.Replace(bridgeText, `"-backup"`, `"-nobackup"`, 1)
	if mutated == bridgeText {
		t.Fatal("变异未生效：bridge.rs 里找不到 -backup")
	}
	problems = nil
	checkImportSafety(base(mutated, importText), &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "-backup") {
		t.Fatalf("设置中心去掉 -backup 应被捕获，实际：%v", problems)
	}
	// 变异 2：Rust 导入实现（dictimport.rs）去掉 -backup 应被捕获。
	// 用 ReplaceAll：usage 文案与参数解析/单测里都有 -backup，只改一处不算丢能力。
	mutatedImport := strings.ReplaceAll(importText, "-backup", "-nobackup")
	if mutatedImport == importText {
		t.Fatal("变异未生效：dictimport.rs 里找不到 -backup")
	}
	problems = nil
	checkImportSafety(base(bridgeText, mutatedImport), &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "-backup") {
		t.Fatalf("Rust 导入实现去掉 -backup 应被捕获，实际：%v", problems)
	}
}

// TestCheckWindowsVersionInfo 用真实安装器/模板/构建脚本验证版本信息契约，
// 并做变异：删掉安装器的 VIProductVersion 或写死版本号都应被捕获。
func TestCheckWindowsVersionInfo(t *testing.T) {
	paths := []string{winInstaller, winResource, makefile, winBuild, releaseYML, ciYML, winShell, winInstallBat,
		winPackageMsvc, winVersionCheck, rustCtlBuildScript, rustDaemonBuildScript}
	fsys := fstest.MapFS{}
	for _, p := range paths {
		data := readRepo(t, p)
		fsys[p] = &fstest.MapFile{Data: data}
	}
	original := string(fsys[winInstaller].Data)
	var problems []string
	checkWindowsVersionInfo(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实版本信息实现未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	mutated := strings.ReplaceAll(original, "VIProductVersion", "NoVersionProduct")
	if mutated == string(fsys[winInstaller].Data) {
		t.Fatal("变异未生效：安装器里找不到 VIProductVersion")
	}
	problems = nil
	fsys[winInstaller] = &fstest.MapFile{Data: []byte(mutated)}
	checkWindowsVersionInfo(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "VIProductVersion") {
		t.Fatalf("删掉 VIProductVersion 应被捕获，实际：%v", problems)
	}

	// 写死版本号：契约应拒绝（make check-version 是第二道防线）。
	problems = nil
	fsys[winInstaller] = &fstest.MapFile{Data: []byte(
		strings.Replace(original,
			`VIProductVersion "${VERSION_QUAD}"`,
			`VIProductVersion "0.2.12.0"`, 1))}
	checkWindowsVersionInfo(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "不应包含") {
		t.Fatalf("写死版本号应被捕获，实际：%v", problems)
	}
}

// TestCheckVersionSource 用真实文件验证「只读 CHAOS_VERSION_TAG，读不到就生成
// 年月日+hash」契约，并做变异：version.sh 去掉环境变量 / rev-parse / 日期、
// release.yml 回退到 describe --tags 都应被捕获。
func TestCheckVersionSource(t *testing.T) {
	paths := []string{makefile, winBuild, macBuild, releaseYML}
	fsys := fstest.MapFS{}
	for _, p := range append(paths, "scripts/version.sh") {
		data := readRepo(t, p)
		fsys[p] = &fstest.MapFile{Data: data}
	}
	var problems []string
	checkVersionSource(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实版本源实现未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	orig := string(fsys["scripts/version.sh"].Data)

	// 变异 1：version.sh 不再读取 CHAOS_VERSION_TAG（tag 版本注入会失效）。
	problems = nil
	fsys["scripts/version.sh"] = &fstest.MapFile{Data: []byte(
		strings.ReplaceAll(orig, "CHAOS_VERSION_TAG", "IGNORED_VERSION_VAR"))}
	checkVersionSource(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "CHAOS_VERSION_TAG") {
		t.Fatalf("version.sh 去掉 CHAOS_VERSION_TAG 应被捕获，实际：%v", problems)
	}
	fsys["scripts/version.sh"] = &fstest.MapFile{Data: []byte(orig)}

	// 变异 2：version.sh 不再用 commit hash。
	problems = nil
	fsys["scripts/version.sh"] = &fstest.MapFile{Data: []byte(
		strings.ReplaceAll(orig, "rev-parse", "describe --tags"))}
	checkVersionSource(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "rev-parse") {
		t.Fatalf("version.sh 去掉 rev-parse 应被捕获，实际：%v", problems)
	}
	fsys["scripts/version.sh"] = &fstest.MapFile{Data: []byte(orig)}

	// 变异 3：version.sh 去掉日期（变回纯 hash）。
	problems = nil
	fsys["scripts/version.sh"] = &fstest.MapFile{Data: []byte(
		strings.ReplaceAll(orig, "date +%Y%m%d", "date +%s"))}
	checkVersionSource(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "date +%Y%m%d") {
		t.Fatalf("version.sh 去掉年月日格式应被捕获，实际：%v", problems)
	}
	fsys["scripts/version.sh"] = &fstest.MapFile{Data: []byte(orig)}

	// 变异 4：release.yml 回退到「最近的 tag」，且不再走 Makefile 打包链。
	ymlorig := string(fsys[releaseYML].Data)
	problems = nil
	mutated := strings.ReplaceAll(ymlorig, "make package-macos", "git describe --tags")
	if mutated == ymlorig {
		t.Fatal("变异 4 未生效：release.yml 里找不到 make package-macos")
	}
	fsys[releaseYML] = &fstest.MapFile{Data: []byte(mutated)}
	checkVersionSource(fsys, &problems)
	joined := strings.Join(problems, "\n")
	if len(problems) == 0 || !strings.Contains(joined, "不应包含") {
		t.Fatalf("release.yml 回退到 git describe --tags 应被捕获，实际：%v", problems)
	}
	if !strings.Contains(joined, "make package-macos") {
		t.Fatalf("release.yml 缺 Makefile 打包链应被捕获，实际：%v", problems)
	}
}

func TestCheckContains(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("hello 学习数据")},
	}
	var problems []string
	checkContains(fsys, &problems, "g", "a.txt", "hello", "学习数据")
	if len(problems) != 0 {
		t.Fatalf("应通过，实际：%v", problems)
	}

	problems = nil
	checkContains(fsys, &problems, "g", "a.txt", "missing")
	if len(problems) != 1 || !strings.Contains(problems[0], "missing") {
		t.Fatalf("应报缺少子串，实际：%v", problems)
	}

	problems = nil
	checkContains(fsys, &problems, "g", "nope.txt", "x")
	if len(problems) != 1 || !strings.Contains(problems[0], "读取") {
		t.Fatalf("应报读取失败，实际：%v", problems)
	}
}

func TestCheckExcludes(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("chaoset-userdict")},
	}
	var problems []string
	checkExcludes(fsys, &problems, "g", "a.txt", "dbus-send")
	if len(problems) != 0 {
		t.Fatalf("应通过，实际：%v", problems)
	}

	problems = nil
	checkExcludes(fsys, &problems, "g", "a.txt", "chaoset-userdict")
	if len(problems) != 1 || !strings.Contains(problems[0], "不应包含") {
		t.Fatalf("应报禁止子串，实际：%v", problems)
	}
}

func TestCheckExists(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("x")},
	}
	var problems []string
	checkExists(fsys, &problems, "g", "a.txt")
	if len(problems) != 0 {
		t.Fatalf("应通过，实际：%v", problems)
	}

	problems = nil
	checkExists(fsys, &problems, "g", "nope.txt")
	if len(problems) != 1 || !strings.Contains(problems[0], "缺少") {
		t.Fatalf("应报缺少文件，实际：%v", problems)
	}
}

func TestCheckCandidateMouseUpSemantics(t *testing.T) {
	winText := string(readRepo(t, winShell))
	macText := readRepo(t, macPanel)
	fsys := func(win, mac string) fstest.MapFS {
		return fstest.MapFS{
			winShell: &fstest.MapFile{Data: []byte(win)},
			macPanel: &fstest.MapFile{Data: []byte(mac)},
		}
	}
	var problems []string
	checkCandidateMouseUpSemantics(fsys(string(winText), string(macText)), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实候选窗左键实现未通过 mouseup 契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 变异 1：Windows 的 onLButtonDown 直接上屏（回退到按下即选）。
	mutatedWin := strings.Replace(string(winText),
		"void CandidateWindow::onLButtonDown(int x, int y) {\n",
		"void CandidateWindow::onLButtonDown(int x, int y) {\n    if (tip_ != nullptr) tip_->selectCandidateByIndex(0);\n", 1)
	if mutatedWin == string(winText) {
		t.Fatal("变异 1 未生效：源码里找不到 onLButtonDown 函数头")
	}
	problems = nil
	checkCandidateMouseUpSemantics(fsys(mutatedWin, string(macText)), &problems)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "onLButtonDown") || !strings.Contains(joined, "selectCandidateByIndex") {
		t.Fatalf("Windows onLButtonDown 直接上屏应被捕获，实际：%v", problems)
	}

	// 变异 2：macOS 的 mouseDown 直接上屏。
	mutatedMac := strings.Replace(string(macText),
		"override func mouseDown(with event: NSEvent) {\n",
		"override func mouseDown(with event: NSEvent) {\n        onSelect?(0)\n", 1)
	if mutatedMac == string(macText) {
		t.Fatal("变异 2 未生效：源码里找不到 mouseDown 函数头")
	}
	problems = nil
	checkCandidateMouseUpSemantics(fsys(string(winText), mutatedMac), &problems)
	if joined = strings.Join(problems, "\n"); !strings.Contains(joined, "mouseDown") || !strings.Contains(joined, "onSelect?(") {
		t.Fatalf("macOS mouseDown 直接上屏应被捕获，实际：%v", problems)
	}

	// 变异 3：只删 Windows 空白区 hide()，保留 tip_ == nullptr 分支的 hide()。
	// 旧断言只查「函数体包含 hide()」会被这个分支冒充而漏报。
	const winBlankHide = "\n    // 空白区点击（未命中候选/齿轮/翻页）：收起候选窗，不再静默吞掉鼠标事件。\n    // 2026-10-29 鼠标零干扰契约；2026-11-06 改 mouseup 时被漏掉，本次恢复。\n    hide();"
	mutatedWin = strings.Replace(string(winText), winBlankHide, "", 1)
	if mutatedWin == string(winText) {
		t.Fatal("变异 3 未生效：源码里找不到空白区 hide()")
	}
	problems = nil
	checkCandidateMouseUpSemantics(fsys(mutatedWin, string(macText)), &problems)
	if joined = strings.Join(problems, "\n"); !strings.Contains(joined, "未在空白区点击时 hide()") {
		t.Fatalf("Windows 空白区 hide() 被删应被捕获，实际：%v", problems)
	}

	// 变异 4：翻页分支去掉 return，翻页后落到空白区 hide()（PR #119 回归）。
	mutatedWin = strings.Replace(string(winText),
		"        return;  // 翻页命中后不得落到空白区 hide()（否则刚翻出的候选窗被收起）。\n",
		"", 1)
	if mutatedWin == string(winText) {
		t.Fatal("变异 4 未生效：源码里找不到翻页分支的 return")
	}
	problems = nil
	checkCandidateMouseUpSemantics(fsys(mutatedWin, string(macText)), &problems)
	if joined = strings.Join(problems, "\n"); !strings.Contains(joined, "翻页分支缺少 return") {
		t.Fatalf("Windows 翻页分支缺 return 应被捕获，实际：%v", problems)
	}

	// 变异 5：删 macOS 控制器 onDismiss 接线（视图声明仍在，不能算通过）。
	mutatedMac = strings.Replace(string(macText),
		"        v.onDismiss = { CandidatePanelController.hide() }\n", "", 1)
	if mutatedMac == string(macText) {
		t.Fatal("变异 5 未生效：源码里找不到 onDismiss 接线")
	}
	problems = nil
	checkCandidateMouseUpSemantics(fsys(string(winText), mutatedMac), &problems)
	if joined = strings.Join(problems, "\n"); !strings.Contains(joined, "CandidatePanelController") {
		t.Fatalf("macOS 控制器 onDismiss 未接线应被捕获，实际：%v", problems)
	}
}

// TestCheckShimModuleLayoutAndDocs 锁住 2026-11-06 架构拆分：
// docs/architecture.md 模块地图里的文件必须存在且在预算内；删掉/合并回大文件
// 必须被机器断言抓住。
func TestCheckShimModuleLayoutAndDocs(t *testing.T) {
	root := os.DirFS("../../")
	var problems []string
	checkShimModuleLayout(root, &problems)
	checkArchitectureDoc(root, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实仓库未通过架构模块布局契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 用真实文件构建 MapFS 后删掉一个模块文件：必须报「缺少模块文件」。
	base := fstest.MapFS{}
	for _, m := range shimModuleBudgets {
		data, err := fs.ReadFile(root, m.path)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", m.path, err)
		}
		base[m.path] = &fstest.MapFile{Data: append([]byte(nil), data...)}
	}
	doc, err := fs.ReadFile(root, archDoc)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", archDoc, err)
	}
	base[archDoc] = &fstest.MapFile{Data: append([]byte(nil), doc...)}
	delete(base, "shims/windows/src/diagnostics.cpp")
	problems = nil
	checkShimModuleLayout(base, &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "缺少模块文件") {
		t.Fatalf("删除模块文件应被捕获，实际：%v", problems)
	}

	// 变异 2：把模块文件塞成超预算（模拟大文件回潮）。
	big := []byte(strings.Repeat("// filler\n", 5300+50))
	base["shims/windows/src/win32.cpp"] = &fstest.MapFile{Data: big}
	problems = nil
	checkShimModuleLayout(base, &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "超过预算") {
		t.Fatalf("超预算大文件应被捕获，实际：%v", problems)
	}
}

// TestCheckLinuxSetupAssembly 锁住 Linux 设置窗装配回归：
// GtkStack 页面误用 gtk_box_pack_start、胶囊导航不切 stack 页都必须被捕获。

// TestCheckDllUnloadSafety 锁住 DLL 动态卸载安全：删掉 removeCrashHandler() 或
// UnregisterClassW 必须被捕获（否则宿主会崩在已卸载模块里）。
func TestCheckDllUnloadSafety(t *testing.T) {
	winText := string(readRepo(t, winShell))
	diagText := readRepo(t, "shims/windows/src/diagnostics.cpp")
	diagHpp := readRepo(t, "shims/windows/src/diagnostics.hpp")
	uiHostText := readRepo(t, "shims/windows/src/ui_host.cpp")
	fsys := func(win string) fstest.MapFS {
		return fstest.MapFS{
			winShell:                            &fstest.MapFile{Data: []byte(win)},
			"shims/windows/src/diagnostics.cpp": &fstest.MapFile{Data: diagText},
			"shims/windows/src/diagnostics.hpp": &fstest.MapFile{Data: diagHpp},
			"shims/windows/src/ui_host.cpp":     &fstest.MapFile{Data: uiHostText},
		}
	}
	var problems []string
	checkDllUnloadSafety(fsys(string(winText)), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实 DLL 卸载路径未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}
	mutated := strings.Replace(string(winText),
		"        removeCrashHandler();\n        shellWorkerStop();",
		"        shellWorkerStop();", 1)
	if mutated == string(winText) {
		t.Fatal("变异未生效：找不到 DllMain 里的 removeCrashHandler();")
	}
	problems = nil
	checkDllUnloadSafety(fsys(mutated), &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "removeCrashHandler") {
		t.Fatalf("删掉卸载时 removeCrashHandler 应被捕获，实际：%v", problems)
	}
}

// TestCheckRustCLIShipping 用真实构建/打包链验证「发行面 Rust 化」契约，并做变异：
// Makefile 恢复一条 go build ./cmd/chaosetctl 必须被捕获（回退双实现）。
func TestCheckRustCLIShipping(t *testing.T) {
	paths := []string{makefile, macBuild, winPackageMsvc, linuxArchPkg, winInstaller,
		winInstallBat, winVersionCheck, rustCtlBuildScript, rustDaemonBuildScript,
		"crates/chaosetctl/src/buildinfo.rs", "crates/chaosetd/src/main.rs", macInstall, macSetup}
	fsys := fstest.MapFS{}
	for _, p := range paths {
		fsys[p] = &fstest.MapFile{Data: readRepo(t, p)}
	}
	var problems []string
	checkRustCLIShipping(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实发行链未通过 Rust CLI 契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}
	// 变异：Makefile 回退成 go build 已删除的 ctl。
	mutated := string(fsys[makefile].Data) + "\n\tgo build -o bin/chaosetctl ./cmd/chaosetctl\n"
	fsys[makefile] = &fstest.MapFile{Data: []byte(mutated)}
	problems = nil
	checkRustCLIShipping(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "cmd/chaosetctl") {
		t.Fatalf("Makefile 回退 go build ctl 应被捕获，实际：%v", problems)
	}
}

// TestGroupRegistry 锁住检查组注册表的基本契约：组名非空且唯一（去重后）、
// 执行体非 nil、组数不退化。成功输出由 groupNames() 生成，因此本测试同时守住
// 「实际运行的组」与「打印的组」不会变成两份漂移的清单。
func TestGroupRegistry(t *testing.T) {
	if len(checkGroups) < 40 {
		t.Fatalf("注册表只有 %d 个检查组，疑似有组被误删", len(checkGroups))
	}
	seen := map[string]bool{}
	for i, g := range checkGroups {
		if g.name == "" {
			t.Fatalf("注册表第 %d 项组名为空", i)
		}
		if g.run == nil {
			t.Fatalf("注册表第 %d 项（%s）没有执行体", i, g.name)
		}
	}
	for i, name := range groupNames() {
		if seen[name] {
			t.Fatalf("groupNames() 出现重复组名 %q", name)
		}
		seen[name] = true
		if i > 0 && strings.TrimSpace(name) == "" {
			t.Fatalf("groupNames() 第 %d 项为空", i)
		}
	}
	if len(groupNames()) == 0 {
		t.Fatal("groupNames() 为空，main 的成功输出会没有任何组名")
	}
}

// TestCheckCandidatePageSize 锁住「每页候选数」四端一致：真实仓库必须通过；
// 引擎 pageSize 漂移、macOS associatePageSize 漂移都必须被捕获。
// TestCheckNoGoEngine 锁住「Go 引擎已删除」：真实仓库必须通过；engine/ 目录
// 重新出现或任何 Go 文件 import chaoset.ime/engine 都必须被捕获（双实现漂移回归）。
func TestCheckNoGoEngine(t *testing.T) {
	var problems []string
	checkNoGoEngine(os.DirFS("../../"), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实仓库不应存在 Go 引擎依赖（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 变异 1：engine/ 目录重新出现。
	fsys := fstest.MapFS{
		"engine/session.go": {Data: []byte("package engine\n")},
	}
	problems = nil
	checkNoGoEngine(fsys, &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "engine/ 目录不应存在") {
		t.Fatalf("engine/ 目录回退应被捕获，实际：%v", problems)
	}

	// 变异 2：Go 文件重新 import 引擎包（注释里的同名字样不算）。
	fsys = fstest.MapFS{
		"cmd/foo/main.go": {Data: []byte("package main\n\nimport (\n\t\"chaoset.ime/engine\"\n)\n")},
	}
	problems = nil
	checkNoGoEngine(fsys, &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "仍 import chaoset.ime/engine") {
		t.Fatalf("Go 文件重新依赖引擎应被捕获，实际：%v", problems)
	}
}

func TestCheckCandidatePageSize(t *testing.T) {
	pageSizePaths := []string{rustSession, macController, winShell, linuxEngine}
	makeFS := func(overrides map[string]string) fstest.MapFS {
		fsys := fstest.MapFS{}
		for _, p := range pageSizePaths {
			data := readRepo(t, p)
			if over, ok := overrides[p]; ok {
				data = []byte(over)
			}
			fsys[p] = &fstest.MapFile{Data: data}
		}
		return fsys
	}
	var problems []string
	checkCandidatePageSize(makeFS(nil), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实仓库未通过每页候选数契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 变异 1：引擎 pageSize 6 -> 9，三端都应报错。
	engineText := string(readRepo(t, rustSession))
	mutatedEngine := strings.Replace(engineText, "pub const PAGE_SIZE: usize = 6;", "pub const PAGE_SIZE: usize = 9;", 1)
	if mutatedEngine == engineText {
		t.Fatal("变异未生效：session.rs 里找不到 PAGE_SIZE = 6")
	}
	problems = nil
	checkCandidatePageSize(makeFS(map[string]string{rustSession: mutatedEngine}), &problems)
	joined := strings.Join(problems, "\n")
	for _, end := range []string{"macOS", "Windows", "Linux"} {
		if !strings.Contains(joined, end+" 每页候选数 = 6") {
			t.Fatalf("引擎 pageSize 漂移应捕获 %s 端不一致，实际：%v", end, problems)
		}
	}

	// 变异 2：macOS associatePageSize 6 -> 9，只报 macOS。
	macText := string(readRepo(t, macController))
	mutatedMac := strings.Replace(macText, "static let associatePageSize = 6", "static let associatePageSize = 9", 1)
	if mutatedMac == macText {
		t.Fatal("变异未生效：ChaosetController.swift 里找不到 associatePageSize = 6")
	}
	problems = nil
	checkCandidatePageSize(makeFS(map[string]string{macController: mutatedMac}), &problems)
	if len(problems) != 1 || !strings.Contains(problems[0], "macOS 每页候选数 = 9") {
		t.Fatalf("macOS 页容量漂移应被捕获，实际：%v", problems)
	}
}

// TestCheckMacTisRegisterIdempotent 用真实源码验证 macOS 输入源登记幂等契约，
// 并做变异：删掉 register 的「已登记则跳过」分支必须被捕获。
func TestCheckMacTisRegisterIdempotent(t *testing.T) {
	paths := []string{macTis, macSetup, macInstall, macReregister, macReadme}
	fsys := fstest.MapFS{}
	for _, p := range paths {
		fsys[p] = &fstest.MapFile{Data: readRepo(t, p)}
	}
	var problems []string
	checkMacTisRegisterIdempotent(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实 macOS 安装/登记实现未通过幂等契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	tis := string(fsys[macTis].Data)
	mutated := strings.Replace(tis, "if existing {", "if false {", 1)
	if mutated == tis {
		t.Fatal("变异未生效：tis.swift 里找不到 register 的跳过分支")
	}
	fsys[macTis] = &fstest.MapFile{Data: []byte(mutated)}
	problems = nil
	checkMacTisRegisterIdempotent(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "幂等") {
		t.Fatalf("删除幂等分支应被捕获，实际：%v", problems)
	}
}

// TestCheckTimeoutResilience 锁住「超时韧性」契约：真实仓库必须通过；以下回退
// 都必须被捕获（任何一条都会让 issue #122 的症状复现）：
//  1. 字母追加失败点去掉超时保留——超时又直接 fail-open 擦掉拼音；
//  2. 翻页失败点去掉超时分流；
//  3. 按键路径重新套上 5s 熔断——超时后的下一键重连不了；
//  4. 失步处理器改走 flushOrCancel 拆组词；
//  5. daemon 主循环里阻塞等连接（旧的单监听实例模型）；
//  6. daemon 不再认 input 的 base。
func TestCheckTimeoutResilience(t *testing.T) {
	files := []string{
		winShell, winCoreHpp, winCoreCpp, ipcServer, rustSession,
		rustDaemonServer, rustDaemonPipe, uiParityDoc,
		winTransport, winCoreTest, macTransport,
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
	checkTimeoutResilience(build(nil), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实实现未通过超时韧性契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	expect := func(name, path, old, new string, want ...string) {
		t.Helper()
		if !strings.Contains(orig[path], old) {
			t.Fatalf("%s 未生效：%s 里找不到 %q", name, path, old)
		}
		mut := map[string]string{path: strings.Replace(orig[path], old, new, 1)}
		var problems []string
		checkTimeoutResilience(build(mut), &problems)
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, groupTimeoutResilience) {
			t.Fatalf("%s 应被捕获，实际：%v", name, problems)
		}
		for _, w := range want {
			if !strings.Contains(joined, w) {
				t.Fatalf("%s 应报出 %q，实际：%v", name, w, problems)
			}
		}
	}

	expect("字母追加去掉超时保留", winShell,
		"if (keepCompositionOnTimeout(prev, ch, pic)) return true;", "",
		"字母追加")
	expect("翻页失败点去掉超时分流", winShell,
		"if (enterDesyncOnTimeout(prev)) return true;", "",
		"翻页键")
	expect("按键路径重新套熔断", winShell,
		"    if (transport.isConnected()) return true;\n    if (transport.tryConnectOnce()) return true;\n",
		"    if (transport.isConnected()) return true;\n    if (transport.circuitOpen()) return false;\n    if (transport.tryConnectOnce()) return true;\n",
		"circuitOpen")
	expect("失步处理器拆组词", winShell,
		"        resetComposition();\n        commit(pic, raw);\n        *eaten = false;",
		"        flushOrCancel(pic);\n        *eaten = false;",
		"flushOrCancel(")
	expect("daemon 主循环阻塞等连接", rustDaemonPipe,
		"    let pipe_name = pipe_name.to_string();\n    run_listener_pool(",
		"    let pipe_name = pipe_name.to_string();\n    let _ = ConnectNamedPipe(std::ptr::null_mut(), std::ptr::null_mut());\n    run_listener_pool(",
		"ConnectNamedPipe")
	expect("daemon 不认 base", ipcServer,
		`let base = params.get("base").and_then(|v| v.as_str());`,
		"let base: Option<&str> = None;",
		`params.get(`)
	// 读超时又改回断连（用户明确要求「不要因为超时就断开连接」）：
	// Windows 的读超时分支不再直接返回、macOS 的不再保留连接，都必须被捕获。
	expect("Windows 读超时又断连", winTransport,
		"                        if (!isWrite) {",
		"                        if (false) {",
		"读/写超时的区分")
	expect("macOS 读超时又断连", macTransport,
		"            if lastReadTimedOut && staleResponses < 2 {",
		"            if false {",
		"lastReadTimedOut")
}

// TestCheckCandidateNoPrivilege 锁住「候选窗无特权：焦点离开即收」的三端契约：
// 真实仓库必须通过；以下回退都必须被捕获：
//  1. macOS 去掉应用切换/Space 切换的 NSWorkspace 守卫（候选窗留在别的应用上截鼠标）；
//  2. macOS 焦点收尾不再清客户端 marked 区（下划线拼音残留）；
//  3. Windows OnSetFocus(FALSE) 不再 resetSession；
//  4. Linux focus_out 不再收 lookup table。
func TestCheckCandidateNoPrivilege(t *testing.T) {
	files := []string{macController, macPanel, macKeyTest, winShell, linuxEngine, uiParityDoc}
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
	checkCandidateNoPrivilege(build(nil), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实实现未通过候选窗无特权契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	expect := func(name, path, old, new string) {
		t.Helper()
		if !strings.Contains(orig[path], old) {
			t.Fatalf("%s 未生效：%s 里找不到 %q", name, path, old)
		}
		var problems []string
		checkCandidateNoPrivilege(build(map[string]string{path: strings.Replace(orig[path], old, new, 1)}), &problems)
		if len(problems) == 0 {
			t.Fatalf("%s 应被捕获，实际无问题", name)
		}
		if !strings.Contains(strings.Join(problems, "\n"), groupCandidateNoPrivilege) {
			t.Fatalf("%s 应报在 %s 组，实际：%v", name, groupCandidateNoPrivilege, problems)
		}
	}

	expect("macOS 去掉 Space 切换守卫", macController,
		"forName: NSWorkspace.activeSpaceDidChangeNotification", "forName: NSWorkspace.didHideNotification")
	expect("macOS 焦点收尾不清 marked 区", macController, "        refresh(lastClient)\n", "")
	expect("Windows 失焦不重置会话", winShell, "        if (isComposing() || !confirmedPreedit_.empty()) {\n            resetSession();", "        if (isComposing() || !confirmedPreedit_.empty()) {")
	expect("Linux focus_out 不收 lookup table", linuxEngine,
		"        exitAssociate();\n        hidePreedit();\n        hideLookupTable();\n        last_ = chaoset::Comp();\n    }\n    void reset() {",
		"        exitAssociate();\n        hidePreedit();\n        last_ = chaoset::Comp();\n    }\n    void reset() {")
}

// TestCheckToggleModeHotkey 锁住 issue #132「无文本输入焦点时的中英切换热键」契约：
// 真实仓库必须通过；以下回退都必须被捕获：
//  1. 不注册系统热键（功能消失）；
//  2. 去掉 MOD_NOREPEAT（按住会来回快速切中英）；
//  3. 销毁时不注销（组合键被进程一直占着，其它程序收不到）；
//  4. WM_HOTKEY 分支直接翻转状态（绕过 mode_scope，Process 档会污染全局）。
func TestCheckToggleModeHotkey(t *testing.T) {
	files := []string{winModeHotkey, winUiHost, uiParityDoc}
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
	checkToggleModeHotkey(build(nil), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实实现未通过无焦点中英切换热键契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	expect := func(name, path, old, new string) {
		t.Helper()
		if !strings.Contains(orig[path], old) {
			t.Fatalf("%s 未生效：%s 里找不到 %q", name, path, old)
		}
		var problems []string
		checkToggleModeHotkey(build(map[string]string{path: strings.Replace(orig[path], old, new, 1)}), &problems)
		if len(problems) == 0 {
			t.Fatalf("%s 应被捕获，实际无问题", name)
		}
		if !strings.Contains(strings.Join(problems, "\n"), groupToggleModeHotkey) {
			t.Fatalf("%s 应报在 %s 组，实际：%v", name, groupToggleModeHotkey, problems)
		}
	}

	expect("不注册系统热键", winModeHotkey,
		"::RegisterHotKey(hwnd, kToggleHotkeyId, kToggleHotkeyMods, kToggleHotkeyVk)", "false")
	expect("去掉 MOD_NOREPEAT", winModeHotkey,
		"MOD_CONTROL | MOD_SHIFT | MOD_NOREPEAT", "MOD_CONTROL | MOD_SHIFT")
	expect("销毁时不注销热键", winModeHotkey,
		"::UnregisterHotKey(hwnd, kToggleHotkeyId);", "")
	expect("WM_HOTKEY 直接翻转状态绕过 mode_scope", winUiHost,
		"                PostMessageW(hwnd, kUiToggleModeMsg, 0, 0);",
		"                uiApplyEnglish(!g_uiEnglish);")
}

// TestCheckShiftToggleAccountingRejectsHotkeyDoubleToggle 锁住 Ctrl/Alt/Win
// 按住时的 Shift 不得被当成空按：UI 宿主的 Ctrl+Shift+Space 全局热键会吞掉
// Space，TIP 若在松 Shift 时再切一次，会双翻转并 flush 当前组词（process 档
// 还会绕过 UI 宿主的 no-op 改当前进程状态）。
func TestCheckShiftToggleAccountingRejectsHotkeyDoubleToggle(t *testing.T) {
	files := map[string]string{
		winShell:      string(readRepo(t, winShell)),
		linuxEngine:   string(readRepo(t, linuxEngine)),
		macController: string(readRepo(t, macController)),
		uiParityDoc:   string(readRepo(t, uiParityDoc)),
	}
	build := func(win string) fstest.MapFS {
		m := fstest.MapFS{}
		for p, s := range files {
			m[p] = &fstest.MapFile{Data: []byte(s)}
		}
		m[winShell] = &fstest.MapFile{Data: []byte(win)}
		return m
	}

	var problems []string
	checkShiftToggleAccounting(build(files[winShell]), &problems)
	if len(problems) != 0 {
		t.Fatalf("真实 Shift 记账契约未通过（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	mutated := strings.Replace(files[winShell],
		"sinceShift_ = otherModifierPhysicallyDown();", "sinceShift_ = false;", 1)
	if mutated == files[winShell] {
		t.Fatal("变异未生效：找不到其它修饰键守卫")
	}
	problems = nil
	checkShiftToggleAccounting(build(mutated), &problems)
	if !strings.Contains(strings.Join(problems, "\n"), groupShiftAccounting) {
		t.Fatalf("去掉其它修饰键守卫应被捕获，实际：%v", problems)
	}
}
