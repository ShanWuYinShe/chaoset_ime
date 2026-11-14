// checks_linux_mac.go：macOS/Linux 壳与跨端行为契约（词库导入/退格/修饰键/Shift/reset/daemon 单实例）。

package main

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// shellVarCJKRe 匹配「$VAR 紧跟非 ASCII 字符」。
var shellVarCJKRe = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*[^\x00-\x7F]`)

// checkShellVarEscaping 扫描 shims 下所有 .sh：macOS 自带 bash 3.2 在 UTF-8
// locale 下会把 `$ARCH）` 的多字节字符并入变量名，运行时报 `ARCH�: unbound
// variable`（2026-10-31 key_routing CI 首跑的真实故障）。统一要求 `${VAR}`。
func checkShellVarEscaping(fsys fs.FS, problems *[]string) {
	const group = groupShellEscaping
	err := fs.WalkDir(fsys, "shims", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sh") {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, rerr))
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if shellVarCJKRe.MatchString(line) {
				*problems = append(*problems, fmt.Sprintf("%s：%s:%d 中 shell 变量紧跟非 ASCII 字符（bash 3.2 + UTF-8 locale 会并入变量名 → unbound variable；请改用 ${VAR}）", group, path, i+1))
			}
		}
		return nil
	})
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：遍历 shims 失败：%v", group, err))
	}
}

// checkImportSafety 词库导入安全流程静态断言。
//
// 安全点都是历史审查修出来的（2026-10-26 / 2026-10-31 / 2026-11-01）：
//  1. 必须检查导入器退出码，不能只看产物文件存在（导入器会写出残缺产物）；
//  2. 必须先备份旧词库，再原子替换，不能 remove+move；
//  3. 失败要留下可排查的日志（Windows import.log / mac 返回 log 文本）；
//  4. 文件合并/备份/原子替换属于工具逻辑，必须由 Go `chaoset-import` 实现；
//     三端壳只调用工具（-seed/-backup），不再自己 temp/copy/replace。
//
// 这里锁住实现关键片段，防止重构时把安全流程删掉。
func checkImportSafety(fsys fs.FS, problems *[]string) {
	const group = groupImportSafety
	// mac Setup.app（2026-11-01 起）：只调 Go 工具，退出码检查 + seed/backup 参数。
	checkContains(fsys, problems, group, macSetup,
		"r.status == 0",
		`"-seed"`,
		`"-backup"`,
	)
	checkExcludes(fsys, problems, group, macSetup, "replaceItemAt")
	// Windows 设置窗（2026-11-01 起）：只调 Go 工具，退出码检查 + backup 参数 +
	// 失败日志；不再自己备份/替换。
	checkContains(fsys, problems, group, winShell,
		"code != 0",
		"-backup",
		"import.log",
	)
	checkExcludes(fsys, problems, group, winShell, "MoveFileExW(",
		"MOVEFILE_REPLACE_EXISTING")
	// Linux 设置窗（2026-11-01 起）：只调用 Go 工具并检查退出码；合并/备份/
	// 原子替换由 chaoset-import 负责（-o 直接指向目标 + -backup）。
	checkContains(fsys, problems, group, linuxDictImport,
		"WIFEXITED",
		`"-o"`,
		`"-backup"`,
	)
	checkExcludes(fsys, problems, group, linuxDictImport,
		"os.replace", "shutil.copy2", `target + ".tmp"`,
	)
	// Go 工具是这些安全语义的唯一实现：seed/backup/原子替换都在这里。
	checkContains(fsys, problems, group, chaosetImport,
		`"seed"`, `"backup"`, "backupFile(", "os.Rename(",
	)
}

// checkShiftToggleAccounting 锁住三端 Shift 空按的「记账」契约：
//   - Windows 必须用幂等位掩码（shiftHeld_）而不是 ++/-- 计数器；
//   - Windows 的 Deactivate 与 OnSetFocus(FALSE) 必须调 resetShiftTracking；
//   - 必须有物理状态自愈（GetAsyncKeyState），兑现 README 承诺的兜底；
//   - Linux 必须保留 shift_.reset() 的 focusIn/focusOut 复位；
//   - macOS 必须保留 shiftArmed 复位路径；
//   - ui-parity 必须保留「Shift 空按」行。
//
// 这些断言都对应真实发生过的缺陷（Windows 计数器只增不减 → 永久失效）。
func checkShiftToggleAccounting(fsys fs.FS, problems *[]string) {
	const group = groupShiftAccounting
	// Windows：必须是幂等位掩码 + 焦点/停用复位 + 物理状态自愈。
	checkContains(fsys, problems, group, winShell,
		"shiftHeld_",
		"resetShiftTracking",
		"shiftPhysicallyDown",
		"GetAsyncKeyState",
		"reconcileShiftState",
	)
	// 必须**纯事件驱动**：输入法不该靠常驻定时器轮询键盘状态。每个宿主进程
	// 一个 40ms 定时器开销实打实，还会误判快速轻按与 Shift+点击。
	// 2026-11-05：曾加过 GetKeyState 轮询兜底，确认真因是「焦点抖动吃掉按住
	// 状态」后已撤除，这里锁死不得回归。
	checkExcludes(fsys, problems, group, winShell,
		"onShiftPollTick", "ensureShiftPollWindow", "g_livePollWindows")
	// 焦点抖动只能挂起、不能丢弃按住状态（否则浏览器里 Shift 永远切不了）。
	checkContains(fsys, problems, group, winShell,
		"suspendShiftHoldOnFocusLost",
		"shiftHoldSuspended_",
		"beginShiftHold", "endShiftHold", "finishShiftHold",
	)
	// 失焦「挂起」不得被焦点分支末尾无条件清掉：必须在获得焦点分支里清
	//（2026-11-05 回归审计 P1-1：把 2676 行的无条件下移进 TRUE 分支）。
	checkContains(fsys, problems, group, winShell,
		"shiftHoldSuspended_ = false;")
	// 丢 KeyUp 的自愈必须在 Test 与 Key 两个阶段都可达（Chrome/conhost 等内容键
	// 只进 Key 阶段；2026-11-05 回归审计 P2-11）。定义 1 + 调用 2 = 3 处。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "reconcileShiftState("); n < 3 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：reconcileShiftState( 只有 %d 处（含定义），应在 Test 与 Key 两个阶段都调用",
				group, n))
		}
	}
	// 旧实现的致命写法不得回归：按事件累加的计数器。
	checkExcludes(fsys, problems, group, winShell,
		"++shiftDown_", "--shiftDown_")
	// **两阶段都必须跟踪**（2026-11-05 实机 tsf.log 实证）：宿主分两类——
	// Firefox 把修饰键送进 Test 阶段，Chrome/conhost/Windows Terminal 只送
	// Key 阶段（OnKeyDown/OnKeyUp 有、OnTestKeyDown 无）。只挂一边等于对
	// 另一类宿主彻底失效，表现为「能打字但 Shift 切不了中英」。
	// 状态机幂等，两阶段各投递一次不会双计。
	// 英文模式的放行判断必须同时出现在**预告点**（OnTestKeyDown）与
	// **实际处理点**（handleKey）：Chrome/conhost 等宿主的内容键也走 Key 阶段，
	// 只写预告点等于英文模式对它们无效（实机：切换后仍在出拼音候选）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "if (currentEnglish())"); n < 2 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 中 if (currentEnglish()) 只有 %d 处，应在 OnTestKeyDown 与 handleKey 各一处",
				group, winShell, n))
		}
	}
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		// 4 处调用（Test/Key × 按下/释放）+ 1 处函数定义
		if n := strings.Count(string(data), "onShiftEvent("); n < 5 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 中 onShiftEvent 只有 %d 处（含定义），应为 5 处——"+
					"Test 与 Key 两个按键阶段都必须跟踪修饰键", group, winShell, n))
		}
	}
	// Linux：焦点进出必须复位 Shift 跟踪状态。
	checkContains(fsys, problems, group, linuxEngine,
		"void reset() { shiftDown = 0; sinceShift = false; }",
		"void focusIn() { shift_.reset(); }")
	// macOS：出现其它按键必须清 shiftArmed（否则 Shift+字母会误切换）。
	checkContains(fsys, problems, group, "shims/macos/src/ChaosetController.swift",
		"shiftArmed = false", "shiftArmed = true")
	// 文档基准行不得被删。
	checkContains(fsys, problems, group, uiParityDoc, "Shift 空按")
}

// checkResetSemantics 锁住「继续打字」与「会话结束」两类收尾在三端的分层：
//   - daemon 必须同时提供 reset（清上下文）与 resetComposition（保留）；
//   - 三端壳在切中英/透传等继续打字的路径必须能走到轻量版；
//   - 会话结束（失焦/停用/focusOut）仍必须用完整 reset。
func checkResetSemantics(fsys fs.FS, problems *[]string) {
	const group = groupResetSemantics
	// daemon 侧两条链路都要在。
	checkContains(fsys, problems, group, ipcServer,
		"case \"reset\":", "case \"resetComposition\":")
	// Windows：两个入口都在，轻量版被真正调用。
	checkContains(fsys, problems, group, winShell,
		"void resetSession()", "void resetComposition()",
		"session_.resetComposition()")
	// macOS：EngineClient 暴露两个方法，控制器在继续打字路径用轻量版。
	checkContains(fsys, problems, group, "shims/macos/src/EngineClient.swift",
		"func reset()", "func resetComposition()", "\"resetComposition\"")
	checkContains(fsys, problems, group, "shims/macos/src/ChaosetController.swift",
		"engine.resetComposition()")
	// Linux：同样两个入口。
	checkContains(fsys, problems, group, linuxEngine,
		"void sessionReset()", "void sessionResetComposition()",
		"resetComposition()")
	// 文档必须记录这个区别，否则后来者会误用。
	checkContains(fsys, problems, group, readmeDoc, "resetComposition")
	checkContains(fsys, problems, group, uiParityDoc, "resetComposition")
}

// checkImportEntries 词库导入入口：mac Setup.app + Windows 设置窗有 GUI；Linux 设置窗调
// chaoset-import 写用户词库，ibus 引擎启动时 -imported-dict 自动加载。
func checkImportEntries(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupImportEntries, macSetup, "导入词库", "chaoset-import")
	// mac 输入法设置窗也有「导入词库…」入口（打开 Setup.app 并传 --import），
	// 与 Windows 设置窗「词库」区对齐；实现仍只有 Setup.app 一份。
	checkContains(fsys, problems, groupImportEntries, macSettings, "导入词库", "安装与设置")
	checkContains(fsys, problems, groupImportEntries, macSetup, "--import")
	checkContains(fsys, problems, groupImportEntries, winShell, "导入词库", "chaoset-import.exe")
	// Linux（2026-10-31 起）：原生 GTK3 设置窗「导入词库…」→ setup_ctl 调
	// chaoset-import 写用户词库 → ibus 引擎启动时 -imported-dict 自动加载。
	checkContains(fsys, problems, groupImportEntries, linuxSetup, "导入词库")
	checkExists(fsys, problems, groupImportEntries, linuxDictImport)
	checkContains(fsys, problems, groupImportEntries, linuxDictImport,
		"chaoset-import", "setup_import_dicts", "restart_engines")
	checkContains(fsys, problems, groupImportEntries, linuxEngine,
		"-imported-dict", "importedDict")
	// 默认路径同口径：C 侧优先调用 Go 工具 -print-paths（唯一来源
	// internal/appdirs），原生引擎 daemon.cpp 也走 XDG 默认路径；
	// C 仍保留 XDG 回退供源码树未构建时使用。
	checkContains(fsys, problems, groupImportEntries, linuxCtl,
		"-print-paths", "tool_path")
	checkContains(fsys, problems, groupImportEntries, linuxDictImport,
		`"imported_dict"`, `"dict.tsv"`)
	checkContains(fsys, problems, groupImportEntries, linuxEngineMain,
		"defaultLinuxPaths", "importedDict")
	// linux-e2e 必须真实验证导入词参与候选，而不只是日志里有加载路径。
	checkContains(fsys, problems, groupImportEntries, linuxE2E, "用户导入词库", "测试词", "UpdateLookupTable")
}

// checkAssociateImmediacy 关闭联想的即时性：mac 通知立即收起；Windows 跨进程广播
// （候选窗转发 TIP）立即收起；Linux 仍为下次按键生效（ui-parity 记录为已知差异）。
func checkAssociateImmediacy(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupAssociateImmediacy, macSettings, "chaosetAssociateDisabled")
	checkContains(fsys, problems, groupAssociateImmediacy, winShell,
		"Chaoset.AssociateChanged", "HWND_BROADCAST", "clearAssociateFromSettings")
	checkContains(fsys, problems, groupAssociateImmediacy, uiParityDoc, "下一次按键")
}

// checkBackspaceSemantics Option/Alt+Backspace 三端统一为按音节回退（2026-10-31）：
// mac 壳、Windows 壳（含 core 层）、Linux ibus 壳都必须调用引擎 backspaceWord。
func checkBackspaceSemantics(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupBackspaceSemantics, "shims/macos/src/ChaosetController.swift", "backspaceWord")
	checkContains(fsys, problems, groupBackspaceSemantics, winShell, "backspaceWord", "kVkDelete")
	checkContains(fsys, problems, groupBackspaceSemantics, winCoreHpp, "backspaceWord")
	checkContains(fsys, problems, groupBackspaceSemantics, linuxEngine, "backspaceWord", "kKeysymDelete")
	checkContains(fsys, problems, groupBackspaceSemantics, uiParityDoc, "按音节回退")
}

// checkModifierMapping 修饰键映射：macOS Command↔Windows Win↔Linux Super、
// Control↔Ctrl、Option↔Alt，三端壳必须识别对应修饰键，且 ui-parity 必须保留
// 映射表（防止文档漂移后习惯不可迁移）。另锁住 macOS「非 Shift 修饰键零捕获」：
// AppKit 只允许在 keyDown/keyUp 上访问 charactersIgnoringModifiers，对 flagsChanged
// 访问会抛 NSInternalInconsistencyException——修饰键事件在 IMK handle 里抛异常会中断
// 本次事件处理，远程桌面（RDP/ToDesk）客户端因此可能收不到 Ctrl keyUp，远端 Windows
// 表现为 Ctrl 卡住；同时主流程 passThrough 会同步 daemon IPC 并清组词。因此非 Shift
// 修饰键必须早退原样放行（与 Windows 壳「修饰键永不消费」契约对齐）。
func checkModifierMapping(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupModifierMapping, "shims/macos/src/ChaosetController.swift",
		".command", ".control", ".option")
	checkContains(fsys, problems, groupModifierMapping, winShell,
		"VK_CONTROL", "VK_MENU", "VK_LWIN")
	checkContains(fsys, problems, groupModifierMapping, linuxEngine,
		"IBUS_CONTROL_MASK", "IBUS_MOD1_MASK", "IBUS_MOD4_MASK")
	checkContains(fsys, problems, groupModifierMapping, uiParityDoc, groupModifierMapping, "Command")

	// macOS：非 Shift 修饰键（Cmd/Option/Ctrl/CapsLock/Fn）的 flagsChanged 必须早退
	// 原样放行，且在早退之前不得访问 charactersIgnoringModifiers。
	const macKeyRouter = "shims/macos/src/KeyRouter.swift"
	kr, err := readText(fsys, macKeyRouter)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", groupModifierMapping, macKeyRouter, err))
		return
	}
	code := stripCppComments(kr)
	early := strings.Index(code, "event.type == .flagsChanged")
	chars := strings.Index(code, "charactersIgnoringModifiers")
	if early < 0 {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少「非 Shift 修饰键 flagsChanged 早退放行」——修饰键状态事件会落进组词主流程", groupModifierMapping, macKeyRouter))
	} else {
		end := early + 320
		if end > len(code) {
			end = len(code)
		}
		if !strings.Contains(code[early:end], "return false") {
			*problems = append(*problems, fmt.Sprintf("%s：%s 的修饰键早退分支没有立即 return false（仍会走主流程）", groupModifierMapping, macKeyRouter))
		} else if chars >= 0 && chars < early {
			*problems = append(*problems, fmt.Sprintf("%s：%s 在修饰键早退之前访问 charactersIgnoringModifiers（flagsChanged 上会抛异常）", groupModifierMapping, macKeyRouter))
		}
	}
	if !strings.Contains(code, "nonShiftModifierKeyCodes") {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 nonShiftModifierKeyCodes 集合（Cmd/Option/Ctrl/CapsLock/Fn 必须全覆盖）", groupModifierMapping, macKeyRouter))
	}
	checkContains(fsys, problems, groupModifierMapping, uiParityDoc, "修饰键状态事件")
}

// checkDaemonSingleton daemon 单实例：安装器不能和 LaunchAgent 同时拉起 daemon。旧实现会在
// LaunchAgent 慢启动时手动再 exec 一个 chaosetd，Activity Monitor 出现两个。
func checkDaemonSingleton(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupDaemonSingleton, macSetup,
		"waitLaunchdGone", "waitProcessGone", "只保留 launchd 一个 owner")
	checkExcludes(fsys, problems, groupDaemonSingleton, macSetup,
		"已用已安装 app 内的 daemon 启动")
	checkContains(fsys, problems, groupDaemonSingleton, macInstall,
		"killall chaosetd", "避免出现两个进程")
	checkContains(fsys, problems, groupDaemonSingleton, "cmd/chaosetd/main.go", "runtime.GOOS")
	checkContains(fsys, problems, groupDaemonSingleton, "cmd/chaosetd/singleton_windows.go",
		"CreateMutex", `Local\ChaosetDaemon`)
	checkContains(fsys, problems, groupDaemonSingleton, linuxE2E, "chaosetd 单实例")
}

// checkMacTisRegisterIdempotent 锁住「安装/升级不得重复登记输入源」的 macOS 契约。
//
// 背景：TIS 没有公开的反注册 API，同一 bundle 每多调用一次 TISRegisterInputSource，
// 当前登录会话的输入源表就追加一条记录——系统设置/输入源菜单出现多行「Chaoset」，
// 而且只有注销重登才能重建表。因此登记必须幂等：先按 bundle id 查 TIS 表，已登记
// 绝不重复注册；CLI 安装器、GUI 安装器（Setup.app）与 reregister 统一走随包
// `tis register`（幂等实现）。禁止用 `tis list | grep` 预判是否已登记：TIS 缓存按
// 进程一份，受限/沙盒进程读到的表可能为空，会误判成未登记而重复注册。
func checkMacTisRegisterIdempotent(fsys fs.FS, problems *[]string) {
	const group = groupMacTisIdempotent

	tis, err := readText(fsys, macTis)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, macTis, err))
		return
	}
	regStart := strings.Index(tis, `case "register":`)
	if regStart < 0 {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 register 子命令", group, macTis))
		return
	}
	reg := tis[regStart:]
	if end := strings.Index(reg, `case "enable":`); end > 0 {
		reg = reg[:end]
	}
	if !strings.Contains(reg, "kTISPropertyBundleID") {
		*problems = append(*problems, fmt.Sprintf("%s：%s 的 register 没有按 bundle id 查 TIS 表——重复安装/升级会累积重复输入源条目", group, macTis))
	}
	if !strings.Contains(reg, "if existing {") {
		*problems = append(*problems, fmt.Sprintf("%s：%s 的 register 缺少「已登记则跳过」分支（幂等）", group, macTis))
	}
	regCode := stripCppComments(reg)
	alreadyIdx := strings.Index(regCode, "已登记")
	registerIdx := strings.Index(regCode, "TISRegisterInputSource")
	if alreadyIdx < 0 {
		*problems = append(*problems, fmt.Sprintf("%s：%s 的 register 缺少「已登记则跳过」分支（幂等）", group, macTis))
	} else if registerIdx < 0 {
		*problems = append(*problems, fmt.Sprintf("%s：%s 的 register 不再调用 TISRegisterInputSource——首次安装无法登记", group, macTis))
	} else if registerIdx < alreadyIdx {
		*problems = append(*problems, fmt.Sprintf("%s：%s 的 register 先注册后判重——顺序反了，重复注册无法避免", group, macTis))
	}

	// GUI 安装器（唯一「一键安装/更新」入口）必须走随包幂等 register；
	// 内嵌兜底也必须在查表判重之后，不得再无条件直接注册。
	setup, err := readText(fsys, macSetup)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, macSetup, err))
	} else if instBody, ok := swiftFunctionBody(setup, "func installIME() -> String"); !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 installIME()", group, macSetup))
	} else {
		body := stripCppComments(instBody)
		if !strings.Contains(body, `["register", dst]`) || !strings.Contains(body, `res("tis")`) {
			*problems = append(*problems, fmt.Sprintf(`%s：%s 的 installIME 没有走随包 tis register（应先 runTool(res("tis"), ["register", dst])）`, group, macSetup))
		}
		guardIdx := strings.Index(body, "tisSourceRegistered(")
		regIdx := strings.Index(body, "TISRegisterInputSource")
		if regIdx >= 0 && (guardIdx < 0 || guardIdx > regIdx) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 的 installIME 仍有无条件 TISRegisterInputSource（必须先用 tisSourceRegistered 判重）", group, macSetup))
		}
	}

	// CLI 安装器 / reregister：无条件走幂等 register，禁止再用 tis list 预判。
	checkContains(fsys, problems, group, macInstall, `build/tis register "$IM/ChaosetIME.app"`, "幂等")
	checkExcludes(fsys, problems, group, macInstall, `if ! build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then`)
	checkContains(fsys, problems, group, macReregister, `build/tis register "$APP"`, "幂等")
	checkExcludes(fsys, problems, group, macReregister, `if build/tis list 2>/dev/null | grep -q "$ID"; then`)

	// 文档契约：重复安装的产品约束与兜底（注销重登）必须写在 macOS README。
	checkContains(fsys, problems, group, macReadme, "幂等", "TISRegisterInputSource", "注销重登")
}

// swiftFunctionBody 取 Swift 函数体（签名后的第一个 { 到配对 }）。
func swiftFunctionBody(src, signature string) (string, bool) {
	start := strings.Index(src, signature)
	if start < 0 {
		return "", false
	}
	i := start + len(signature)
	for i < len(src) && src[i] != '{' {
		i++
	}
	if i >= len(src) {
		return "", false
	}
	depth := 0
	begin := i
	for ; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[begin : i+1], true
			}
		}
	}
	return "", false
}
