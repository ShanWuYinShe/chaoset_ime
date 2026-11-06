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
	winText := readRepo(t, winShell)
	buildText := readRepo(t, winBuild)
	fsys := fstest.MapFS{
		macPanel: &fstest.MapFile{Data: macText},
		winShell: &fstest.MapFile{Data: winText},
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
		macPanel: &fstest.MapFile{Data: macText},
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
		uiParityDoc, macSettings, linuxSetup}
	fsys := fstest.MapFS{}
	for _, p := range paths {
		data := readRepo(t, p)
		fsys[p] = &fstest.MapFile{Data: data}
	}
	var problems []string
	checkSettingsWindowContract(fsys, &problems)
	checkCandidateGearMouseUp(fsys, &problems)
	checkTrayLazyLifecycle(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实源码未通过阶段2契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 变异 1：mac mouseDown 直接打开设置（用户明确禁止的「按下即弹窗」）。
	macText := string(fsys[macPanel].Data)
	mutatedMac := strings.Replace(macText, "settingsPressed = true", "onSettings?()", 1)
	if mutatedMac == macText {
		t.Fatal("变异未生效：mac 源码里找不到 settingsPressed = true")
	}
	problems = nil
	checkCandidateGearMouseUp(fstest.MapFS{
		macPanel: &fstest.MapFile{Data: []byte(mutatedMac)},
		winShell: fsys[winShell], linuxReadme: fsys[linuxReadme], uiParityDoc: fsys[uiParityDoc],
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "mouseDown") {
		t.Fatalf("mac mouseDown 直接 onSettings 应被捕获，实际：%v", problems)
	}

	// 变异 2：Windows onLButtonDown 直接 showSettingsWindow（同一回归）。
	winText := string(fsys[winShell].Data)
	mutatedWin := strings.Replace(winText, "gearPressed_ = true;", "tip_->showSettingsWindow();", 1)
	if mutatedWin == winText {
		t.Fatal("变异未生效：win32.cpp 里找不到 gearPressed_ = true;")
	}
	problems = nil
	checkCandidateGearMouseUp(fstest.MapFS{
		macPanel: fsys[macPanel], winShell: &fstest.MapFile{Data: []byte(mutatedWin)},
		linuxReadme: fsys[linuxReadme], uiParityDoc: fsys[uiParityDoc],
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "onLButtonDown") {
		t.Fatalf("Windows onLButtonDown 直接 showSettingsWindow 应被捕获，实际：%v", problems)
	}

	// 变异 3：安装器重新写 ChaosetUi Run 项（开机就启动/显示托盘）。
	instText := string(fsys[winInstaller].Data)
	mutatedInst := instText + "\n    WriteRegStr HKLM \"Software\\Microsoft\\Windows\\CurrentVersion\\Run\" \"ChaosetUi\" \"x\"\n"
	problems = nil
	checkTrayLazyLifecycle(fstest.MapFS{
		winShell: fsys[winShell], winInstaller: &fstest.MapFile{Data: []byte(mutatedInst)},
		winInstallBat: fsys[winInstallBat], uiParityDoc: fsys[uiParityDoc],
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "ChaosetUi") {
		t.Fatalf("安装器重写 ChaosetUi Run 项应被捕获，实际：%v", problems)
	}

	// 变异 4：设置窗尺寸回退（mac 640×480）应被捕获。
	settingsText := string(fsys[macSettings].Data)
	mutatedSettings := strings.Replace(settingsText, "width: 640, height: 560", "width: 640, height: 480", 1)
	if mutatedSettings == settingsText {
		t.Fatal("变异未生效：mac SettingsWindow 里找不到 width: 640, height: 560")
	}
	problems = nil
	checkSettingsWindowContract(fstest.MapFS{
		macPanel: fsys[macPanel], winShell: fsys[winShell], linuxReadme: fsys[linuxReadme],
		winInstaller: fsys[winInstaller], winInstallBat: fsys[winInstallBat],
		uiParityDoc: fsys[uiParityDoc],
		macSettings: &fstest.MapFile{Data: []byte(mutatedSettings)}, linuxSetup: fsys[linuxSetup],
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "640, height: 560") {
		t.Fatalf("mac 设置窗尺寸回退应被捕获，实际：%v", problems)
	}
}

// TestCheckTsfZeroBlockContract 用真实 win32.cpp + ui-parity 文档验证零阻塞契约，
// 并做 4 个变异（任一回归都必须被捕获）：
//  1. TSF 回调（Activate/OnSetFocus 共享调用点）体内注入 Sleep(1)——宿主 UI 线程睡眠；
//  2. diagPrintf 去掉 enqueueLogLine——tsf.log 回退同步写；
//  3. 前台钩子回调注入 Sleep(1)——钩子回调不再只 PostMessage；
//  4. DllCanUnloadNow 去掉 g_liveTipMsgWindows——worker/消息窗口活着时卸载 DLL。
func TestCheckTsfZeroBlockContract(t *testing.T) {
	winText := readRepo(t, winShell)
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

// TestCheckImportSafety 用真实两端源码验证导入安全契约，并做一次变异：
// 去掉 Windows 的原子替换标志应被捕获。
func TestCheckImportSafety(t *testing.T) {
	macText := readRepo(t, macSetup)
	winText := readRepo(t, winShell)
	linuxText := readRepo(t, linuxDictImport)
	importText := readRepo(t, chaosetImport)
	fsys := fstest.MapFS{
		macSetup:        &fstest.MapFile{Data: macText},
		winShell:        &fstest.MapFile{Data: winText},
		linuxDictImport: &fstest.MapFile{Data: linuxText},
		chaosetImport:   &fstest.MapFile{Data: importText},
	}
	var problems []string
	checkImportSafety(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实导入实现未通过安全契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	mutated := strings.ReplaceAll(string(winText), "-backup", "-nobackup")
	if mutated == string(winText) {
		t.Fatal("变异未生效：Windows 源码里找不到 -backup")
	}
	problems = nil
	checkImportSafety(fstest.MapFS{
		macSetup:        &fstest.MapFile{Data: macText},
		winShell:        &fstest.MapFile{Data: []byte(mutated)},
		linuxDictImport: &fstest.MapFile{Data: linuxText},
		chaosetImport:   &fstest.MapFile{Data: importText},
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "-backup") {
		t.Fatalf("Windows 去掉 -backup 应被捕获，实际：%v", problems)
	}

	// Linux 现在只调 Go 工具：去掉 -backup 参数应被捕获（备份语义不能丢）。
	linuxMutated := strings.Replace(string(linuxText), "\"-backup\"", "\"-nobackup\"", 1)
	if linuxMutated == string(linuxText) {
		t.Fatal("变异未生效：Linux 导入模块里找不到 \"-backup\"")
	}
	problems = nil
	checkImportSafety(fstest.MapFS{
		macSetup:        &fstest.MapFile{Data: macText},
		winShell:        &fstest.MapFile{Data: winText},
		linuxDictImport: &fstest.MapFile{Data: []byte(linuxMutated)},
		chaosetImport:   &fstest.MapFile{Data: importText},
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "-backup") {
		t.Fatalf("Linux 去掉 -backup 应被捕获，实际：%v", problems)
	}
}

// TestCheckWindowsVersionInfo 用真实安装器/模板/构建脚本验证版本信息契约，
// 并做变异：删掉安装器的 VIProductVersion 或写死版本号都应被捕获。
func TestCheckWindowsVersionInfo(t *testing.T) {
	paths := []string{winInstaller, winResource, makefile, winBuild, releaseYML, ciYML, winShell, winInstallBat}
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
	paths := []string{makefile, winBuild, macBuild, linuxSetupBuild, releaseYML}
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

	// 变异 4：release.yml 回退到「最近的 tag」。
	ymlorig := string(fsys[releaseYML].Data)
	problems = nil
	fsys[releaseYML] = &fstest.MapFile{Data: []byte(
		strings.Replace(ymlorig, "mkdir -p dist", "git describe --tags", 1))}
	checkVersionSource(fsys, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "不应包含") {
		t.Fatalf("release.yml 回退到 git describe --tags 应被捕获，实际：%v", problems)
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
	winText := readRepo(t, winShell)
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
// GtkStack 页面误用 gtk_box_pack_start、radio 组类型混用都必须被捕获。
func TestCheckLinuxSetupAssembly(t *testing.T) {
	text := string(readRepo(t, linuxSetup))
	fsys := fstest.MapFS{linuxSetup: &fstest.MapFile{Data: []byte(text)}}
	var problems []string
	checkLinuxSetupAssembly(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实 Linux 设置窗装配未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	// 变异 1：把 gtk_stack_add_titled 换回 gtk_box_pack_start(GTK_BOX(stack), ...)。
	mutated := strings.Replace(text,
		"gtk_stack_add_titled(GTK_STACK(stack), input_page, \"input\", \"输入\");",
		"gtk_box_pack_start(GTK_BOX(stack), input_page, TRUE, TRUE, 0);", 1)
	if mutated == text {
		t.Fatal("变异 1 未生效：找不到 gtk_stack_add_titled(input_page)")
	}
	problems = nil
	checkLinuxSetupAssembly(fstest.MapFS{linuxSetup: &fstest.MapFile{Data: []byte(mutated)}}, &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "gtk_box_pack_start(GTK_BOX(stack)") {
		t.Fatalf("GtkStack 页面误挂载应被捕获，实际：%v", problems)
	}

	// 变异 2：radio 首成员建组被删（模拟组类型混用回归）。
	mutated = strings.Replace(text,
		"rb = gtk_radio_button_new_with_label_from_widget(",
		"rb = gtk_radio_button_new_with_label(", 1)
	if mutated == text {
		t.Fatal("变异 2 未生效：找不到 from_widget")
	}
	problems = nil
	checkLinuxSetupAssembly(fstest.MapFS{linuxSetup: &fstest.MapFile{Data: []byte(mutated)}}, &problems)
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "gtk_radio_button_new_with_label_from_widget") {
		t.Fatalf("radio 建组回归应被捕获，实际：%v", problems)
	}
}

// TestCheckDllUnloadSafety 锁住 DLL 动态卸载安全：删掉 removeCrashHandler() 或
// UnregisterClassW 必须被捕获（否则宿主会崩在已卸载模块里）。
func TestCheckDllUnloadSafety(t *testing.T) {
	winText := readRepo(t, winShell)
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
