// checks_tsf.go：Windows TSF 壳专属契约（UI 宿主/托盘/零阻塞/生命周期/安装器/版本信息）。
//
// 文件名刻意不用 checks_windows.go：Go 把 `_windows.go` 后缀当作隐式 GOOS 构建约束，
// 在 macOS/Linux 上该文件会被排除，本机 `go run ./tools/checkuiparity` 直接编译失败。

package main

import (
	"fmt"
	"io/fs"
	"strings"
)

// UILess 能力契约（2026-11-05）：
// 微软《UILess Mode Overview》明确要求 TIP 实现 ITfTextInputProcessorEx，否则
// 在 UILess 线程里**不会被激活**（连日志都不会有，所以「日志里 uiless=0」不是
// 证据）。同时严禁在未实现 ITfUIElementMgr 的情况下注册
// TFCAT_TIPCAP_UIELEMENTENABLED——声明了就必须让所有 UI 走 UI 元素管理器，
// 否则正是 2026-10-30「开始菜单里候选窗完全消失」的事故。
func checkUILessCapability(fsys fs.FS, problems *[]string) {
	const group = groupUILess
	checkContains(fsys, problems, group, winShell,
		"ITfTextInputProcessorEx",      // 接口声明（mingw 缺失，需自备）
		"ActivateEx(",                  // 实现
		"kIID_ITfTextInputProcessorEx", // 用自己的 IID 常量做 QI 匹配
		"TF_TMAE_UIELEMENTENABLEDONLY", // 标志位判定入日志
	)
	// 未实现 ITfUIElementMgr 前不得注册 UIELEMENTENABLED 类别。
	// 只匹配**常量名**（kGUID_TFCAT_TIPCAP_UIELEMENTENABLED），避免把解释性
	// 注释里的字样也算进来；一旦有人真去注册，这条就会红。
	checkExcludes(fsys, problems, group, winShell, "kGUID_TFCAT_TIPCAP_UIELEMENTENABLED")
	// 决策理由必须留在源码里，防止后来者"顺手补上"。
	checkContains(fsys, problems, group, winShell, "不注册 TFCAT_TIPCAP_UIELEMENTENABLED")
}

// 焦点兜底契约（2026-11-05）：Windows 11 开始菜单/搜索**不投递
// OnSetFocus(FALSE)**（实机：关闭开始菜单重开，上一轮拼音与候选窗仍在）。
// 必须有系统级前台事件兜底，且清理只能发生在 TIP 线程的按键入口——
// 前台回调里不得碰 TSF（旧上下文可能已销毁）。
func checkForegroundFallback(fsys fs.FS, problems *[]string) {
	const group = groupForegroundFallback
	checkContains(fsys, problems, group, winShell,
		"SetWinEventHook(EVENT_SYSTEM_FOREGROUND",
		"onForegroundChanged",
		"pendingStaleReset_",
		"applyPendingStaleReset();",                // 必须在按键入口被调用
		"stale state dropped (foreground changed)", // 留证据，便于排障
	)
}

// 输入法激活判据契约（2026-11-05 用户反馈「选定的输入法不是 chaoset 时按 Shift
// 仍有中英提示」）：TSF 在某些宿主里会把按键继续投给未激活的 TIP，因此必须在
// 按键入口查询「本线程激活的输入处理器是不是我们」（
// ITfInputProcessorProfileMgr::GetActiveProfile），不是就整键放行。
// 四个按键入口（Test/Key × 按下/抬起）都要有这道闸门。
func checkActiveProfileGate(fsys fs.FS, problems *[]string) {
	const group = groupActiveGate
	checkContains(fsys, problems, group, winShell,
		"ITfInputProcessorProfileMgr", "GetActiveProfile(kGUID_TFCAT_TIP_KEYBOARD",
		"bool isActiveTip()", "bailOutIfInactive",
		"TF_PROFILETYPE_INPUTPROCESSOR", "CLSID_ChaosetTIP",
	)
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "if (bailOutIfInactive())"); n < 4 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：bailOutIfInactive() 闸门只有 %d 处，应为 4 处"+
					"（OnTestKeyDown/OnTestKeyUp/OnKeyDown/OnKeyUp）", group, n))
		}
	}
}

// 崩溃日志契约（2026-11-05）：crash.log 只允许记录**错误级**异常。
// 曾经什么都记，结果 AppContainer 宿主里堆了 197 条「崩溃」，实际全是宿主
// 自己抛出又接住的 COM/RPC 首次异常（pid 一直没变、进程一直活着）。
// NTSTATUS 严重性位非 0xC0000000 档的一律跳过。
func checkCrashLogFilter(fsys fs.FS, problems *[]string) {
	const group = groupCrashLog
	checkContains(fsys, problems, group, winShell,
		"0xF0000000u) != 0xC0000000u", // 严重性位过滤
		"EPT_S_NOT_REGISTERED",        // 决策理由留档
	)
}

// 中英状态指示器 / 设置窗宿主契约（2026-11-05 用户反馈后的架构定稿）：
//
// 用户反馈「中英文模式提示完全乱套：输入光标、鼠标附近、任务栏都在显示，
// 任务栏甚至同时多个；开始菜单里点设置齿轮没反应」。根因：
//   - TSF 把 TIP 加载进每个宿主进程（实机 explorer.exe 单进程 4 个实例），
//     TIP 进程内自绘托盘 = 每个实例一个图标，且 enMode_ 每实例独立（中/英混显）；
//   - 托盘不可用时的浮动 toast 兜底，加上候选窗锚点会退化成鼠标坐标，
//     就成了「光标附近 + 鼠标附近」两处提示。
//
// 定稿架构：中英模式仍写 TSF 线程隔间（语义/兼容），但**显示只由每用户会话
// 唯一的 UI 宿主进程（chaoset-ui.exe）负责**——它持有唯一托盘图标，TIP 只通过
// 命名管道上报模式；设置窗也由它创建（开始菜单/搜索是 Low IL AppContainer，
// TIP 自己创建窗口会被 z 序 band 压住、也无法 CreateProcess/文件对话框）。
// 不许回退成 TIP 自绘托盘或 toast 兜底。
func checkModeIndicator(fsys fs.FS, problems *[]string) {
	const group = groupModeIndicator
	checkContains(fsys, problems, group, winShell,
		"kGUID_COMPARTMENT_KEYBOARD_OPENCLOSE",
		"syncModeCompartment",
		"IID_ITfCompartmentMgr",
		"VT_I4", // 隔间值类型（非零 = 键盘打开/中文）
	)
	// TIP 侧只上报，不持有任何托盘/浮窗兜底。
	checkExcludes(fsys, problems, group, winShell,
		"modeTray_", "ModeTray", "showModeToast(",
	)
	checkContains(fsys, problems, group, winShell,
		"reportModeToUi", "uiSendCommand", "chaoset-ui.exe",
		"openSettingsViaUi", "openSettingsWindowStandalone",
	)
	// UI 宿主实现（在 DLL 内）：唯一托盘、命令管道、单实例、设置窗入口。
	checkContains(fsys, problems, group, winShell,
		"class UiTray", "Shell_NotifyIconW", "makeGlyphIcon", "TaskbarCreated",
		"g_liveTrayWindows", // 托盘窗口过程在 DLL 内，必须防卸载
		"runUiHost", "ChaosetRunUi", "ChaosetUi-", "ALL APPLICATION PACKAGES",
		"S-1-15-2-1",  // DACL: ALL APPLICATION PACKAGES（AC 宿主才能写）
		"ML;;NW;;;LW", // SACL: 低完整性 NO_WRITE_UP
		"openSettingsWindowStandalone",
	)
	// 初始化/切换/焦点回来都必须上报，否则托盘会停在旧模式。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "reportModeToUi("); n < 3 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：reportModeToUi( 只有 %d 处，应至少 3 处"+
					"（Activate / 切换模式 / 焦点回来）", group, n))
		}
		// 设置窗创建入口只允许出现在 UI 宿主路径里（定义 + 消息处理 + 首启），
		// 且候选栏齿轮只发管道请求。
		if n := strings.Count(string(data), "openSettingsWindowStandalone("); n < 3 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：openSettingsWindowStandalone() 只有 %d 处，应至少 3 处"+
					"（定义 / UI 宿主消息 / 首启 --settings）", group, n))
		}
	}
	// TIP 的齿轮入口必须走管道（AC 宿主内 TIP 不能 CreateProcess）。
	checkContains(fsys, problems, group, winShell,
		"void ChaosetTIP::showSettingsWindow() {", "if (openSettingsViaUi()) return;")
	// UI 宿主退出必须 join 管道线程后才能返回（ui_main 随后 FreeLibrary；
	// 线程若还阻塞在 ConnectNamedPipe/ReadFile 会进已卸载代码——2026-11-05 评审 P0）。
	checkContains(fsys, problems, group, winShell,
		"g_uiStopEvent", "CancelSynchronousIo(", "WaitForSingleObject(pipeThread")
	// 命令管道必须加 PIPE_REJECT_REMOTE_CLIENTS，且服务端读命令有超时
	// （连上不发数据不能永久占住单线程服务）。
	checkContains(fsys, problems, group, winShell,
		"PIPE_REJECT_REMOTE_CLIENTS", "PeekNamedPipe(", "deadline")
	// 设置失败不得静默：必须留日志并给用户可见反馈（否则回归无人发现）。
	checkContains(fsys, problems, group, winShell,
		"open settings failed", "设置窗口打不开：chaoset-ui.exe 未运行")
	// AppContainer 内 UI 未运行时由 daemon 代拉起（IPC "ui"），不能静默失败。
	checkContains(fsys, problems, group, winShell,
		"ensureUiHostStartedByDaemon", "session_.ensureUiHost(settings)")
	checkContains(fsys, problems, group, ipcServer, "case \"ui\":")
	checkContains(fsys, problems, group, "ipc/ui_windows.go", "chaoset-ui.exe")
	checkContains(fsys, problems, group, "ipc/ui_other.go", "ensureUIHost")
	// 构建/打包/安装三条链路都必须带上 UI 宿主，否则托盘与设置窗整体不可用。
	checkContains(fsys, problems, group, winBuild,
		"chaoset-ui.exe", "ui_main.cpp", "ChaosetRunUi")
	checkContains(fsys, problems, group, "Makefile",
		"chaoset-ui.exe", "ChaosetRunUi")
	checkContains(fsys, problems, group, winInstaller,
		"chaoset-ui.exe", "ChaosetUi")
	checkContains(fsys, problems, group, ".github/workflows/release.yml",
		"chaoset-ui.exe", "ChaosetRunUi")
}

// 带 owner 的 TOPMOST 窗口显示时会把 owner 所在窗口组抬到前面，所以 owner
// 必须是**当前前台窗口且属于本进程**，不得用 TSF 上下文推出来的窗口
// （那可能是另一个窗口的，用户会看到「弹窗但焦点不动」）。
func checkPopupOwner(fsys fs.FS, problems *[]string) {
	const group = groupPopupOwner
	// Windows：统一经 popupOwner() 取 owner。
	// 2026-11-08 wave1：owner 判定从 win32.cpp 迁到 winutil.cpp（候选窗/提示条
	// 两个叶子 TU 共用，避免反向依赖 TIP TU）；规则本身不变，防回退断言留在 winShell 聚合上。
	checkContains(fsys, problems, group, winShell,
		"HWND popupOwner()", "candidateWnd_.setOwner(popupOwner());")
	// 不得回退为用上下文窗口当 owner。
	checkExcludes(fsys, problems, group, winShell, "setOwner(hostWindowOf(ctx))")
	// 候选窗创建时不得再兜底取前台窗口当 owner——那会绕过「只给 shell 宿主
	// 设 owner」的规则，让 Chrome/Firefox 多窗口互相顶到前面（实机 2026-11-05）。
	checkExcludes(fsys, problems, group, winShell, "owner = GetForegroundWindow();")
	// 跨进程 owner 必须被排除（否则会把别人进程的窗口带上来）。
	checkContains(fsys, problems, group, winShell, "pid != GetCurrentProcessId()")
	// **只有沉浸式 shell 宿主才需要 owner**：普通桌面宿主带 owner 会让同进程的
	// 另一个窗口被抬到前面（Firefox 多窗口实机复现）。判定必须集中在一处。
	checkContains(fsys, problems, group, winShell,
		"bool shellHostNeedsOwner()", "if (!shellHostNeedsOwner()) return nullptr;",
		"startmenuexperiencehost.exe", "searchapp.exe")
	// 判定实现必须落在叶子模块 winutil.cpp，而不是散在 TIP TU。
	checkContains(fsys, problems, group, "shims/windows/src/winutil.cpp", "HWND popupOwner()")
	// 前台事件回调里不得碰 TSF（ITfContext::GetActiveView 等 COM）：合成窗口
	// HWND 必须在 TSF 线程缓存（composeHostWnd_），回调只做纯 Win32 比较。
	//（2026-11-05 回归审计 P2-3）
	checkContains(fsys, problems, group, winShell, "composeHostWnd_")
	checkExcludes(fsys, problems, group, winShell,
		"compCtx_ != nullptr ? hostWindowOf(compCtx_) : nullptr")
}

// checkWindowsVersionInfo 锁住「安装程序与 Windows 二进制都带版本信息 / app
// 信息」契约（2026-11-06 用户反馈：ChaosetSetup.exe 属性页全空，无法判断
// 装的是哪一版）。版本号必须来自单一真源 git tag——NSIS 用注入的
// ${VERSION}/${VERSION_QUAD}，资源模板用 @VERSION@ 占位符，
// 任何一处写死数字都会被 make check-version 拦下。
func checkWindowsVersionInfo(fsys fs.FS, problems *[]string) {
	const group = groupWindowsVersionInfo
	// 安装器 EXE：VS_FIXEDFILEINFO（数字 4 段）+ 属性页字符串键。
	checkContains(fsys, problems, group, winInstaller,
		"VIProductVersion", "VIFileVersion", "VIAddVersionKey",
		`"ProductName"`, `"CompanyName"`, `"LegalCopyright"`,
		`"FileDescription"`, `"FileVersion"`, `"ProductVersion"`,
		`"OriginalFilename"`, `"InternalName"`, `"Comments"`,
		`"Chaoset 拼音输入法"`, `"ShanWuYinShe"`)
	// 卸载注册表（已安装的应用）：显示版本、安装位置、安装日期、帮助/
	// 主页/更新链接与体积等 app 元数据；InstallDate 必须是标准格式。
	checkContains(fsys, problems, group, winInstaller,
		`"DisplayVersion"`, `"InstallLocation"`, `"InstallDate"`,
		`"HelpLink"`, `"URLInfoAbout"`, `"URLUpdateInfo"`,
		`"EstimatedSize"`, `"NoModify"`, `"NoRepair"`,
		"${GetTime}")
	// 版本号不得写死：全部走注入的 define / 占位符。
	checkContains(fsys, problems, group, winInstaller,
		`"${VERSION}"`, `"${VERSION_QUAD}"`,
		"VIProductVersion \"${VERSION_QUAD}\"")
	checkExcludes(fsys, problems, group, winInstaller,
		`"DisplayVersion" "0.`, `VIProductVersion "0.`)
	// Windows 二进制资源模板：图标 + VS_VERSION_INFO，版本号用占位符。
	checkExists(fsys, problems, group, winResource)
	checkContains(fsys, problems, group, winResource,
		`1 ICON "chaoset.ico"`, "VS_VERSION_INFO VERSIONINFO",
		"FILEVERSION", "PRODUCTVERSION", "StringFileInfo", "VarFileInfo",
		`"CompanyName"`, `"FileDescription"`, `"FileVersion"`,
		`"ProductName"`, `"ProductVersion"`, `"LegalCopyright"`,
		"@VERSION@", "@VERSION_QUAD@", "@VER_MAJOR@",
		"@FILE_DESCRIPTION@", "@ORIGINAL_FILENAME@", "@FILE_TYPE@")
	// 两条构建链都必须：注入占位符、以 UTF-8 代码页编译版本资源、把 UI 宿主
	// 资源链进 exe；且 NSIS 收到 VERSION_QUAD。
	checkContains(fsys, problems, group, makefile,
		"@VERSION@", "@VERSION_QUAD@", "-c 65001",
		"chaoset-ui.rc", "chaoset_ui_res.o",
		"VERSION_QUAD := ", "-DVERSION_QUAD=$(VERSION_QUAD)",
		"-Wl,--export-all-symbols", "makensis -DVERSION=$(VERSION)")
	checkContains(fsys, problems, group, winBuild,
		"@VERSION@", "@VERSION_QUAD@", "-c 65001",
		"ui.gen.rc", "ui_rc.o",
		"gen_rc", "VERSION_QUAD", "-Wl,--export-all-symbols")
	// 发行包自检必须真的验证版本资源，而不只是「文件存在」。
	checkContains(fsys, problems, group, releaseYML,
		"VS_VERSION_INFO", "ChaosetSetup.exe")
}

// checkInactiveNoAction 锁住 2026-11-06 用户反馈的契约：**输入法未激活时不许有
// 任何动作**（不拦按键、不弹窗、不占屏幕、不挂全局钩子），且所有浮窗都不能
// 在别人的应用上面挡鼠标/卡住宿主。
//
// 背景（用户实机反馈）：系统设置、开始菜单等 Win10/11 应用被卡住，鼠标点击
// 打开的应用无效。根因是候选窗（WS_EX_TOPMOST 且要收鼠标点选）在非激活时
// 仍可能留在屏幕上，以及 Activate 在宿主 UI 线程上串了一堆同步阻塞操作
// （CreateProcess、管道重试 3×20ms、全局 SetWinEventHook）。
func checkInactiveNoAction(fsys fs.FS, problems *[]string) {
	const group = groupInactiveNoAction
	// 候选窗必须有「激活门」：非激活时连 show() 都不许走到建窗。
	checkContains(fsys, problems, group, winShell,
		"setActiveGate", "activeGate_")
	// 门必须在 show() 的**最前面**生效（只声明成员不算：变异测试证明过，
	// 删掉 show 里的判断时上面的 checkContains 仍然通过）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		sig := "void CandidateWindow::show(HINSTANCE hinst, POINT pt, int lineH, const chaoset::Comp& comp, CandidateWindowHost* host) {"
		i := strings.Index(text, sig)
		if i < 0 {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少候选窗 show() 入口", group, winShell))
		} else {
			end := i + 300
			if end > len(text) {
				end = len(text)
			}
			if !strings.Contains(text[i:end], "activeGate_") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的候选窗 show() 未先判 activeGate_（非激活不许建/显示 TOPMOST 窗口）",
					group, winShell))
			}
		}
	}
	// 四个按键入口都必须先过激活闸门（顺序即语义：闸门在最前）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		for _, sig := range []string{
			"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyDown(",
			"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyDown(",
			"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyUp(",
			"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyUp(",
		} {
			i := strings.Index(text, sig)
			if i < 0 {
				*problems = append(*problems, fmt.Sprintf("%s：%s 缺少按键入口 %q",
					group, winShell, sig))
				continue
			}
			// 入口函数体前 ~200 字节内必须出现 bailOutIfInactive()。
			end := i + 400
			if end > len(text) {
				end = len(text)
			}
			if !strings.Contains(text[i:end], "bailOutIfInactive()") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 %s 未在入口先调 bailOutIfInactive()（非激活必须整键放行）",
					group, winShell, sig))
			}
		}
	}
	// 渲染/提示同样必须受激活约束（render 有多个非按键调用点）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		for _, sig := range []struct{ entry, gate string }{
			{"void ChaosetTIP::render(ITfContext* pic) {", "inputActive_"},
			{"void ChaosetTIP::showToast(const wchar_t* text, bool allowMouseAnchor) {", "isActiveTip()"},
		} {
			i := strings.Index(text, sig.entry)
			if i < 0 {
				*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 %q", group, winShell, sig.entry))
				continue
			}
			end := i + 600
			if end > len(text) {
				end = len(text)
			}
			if !strings.Contains(text[i:end], sig.gate) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 %q 未先判断 %s（非激活不许渲染/弹窗）",
					group, winShell, sig.entry, sig.gate))
			}
		}
	}
	// 宿主 UI 线程上不许有阻塞：Activate/OnSetFocus 里的 daemon 预热必须走
	// 后台线程，设置窗拉起不许长 Sleep。
	checkContains(fsys, problems, group, winShell,
		"warmDaemonInBackgroundOnce", "uiSendCommandFast")
	checkExcludes(fsys, problems, group, winShell,
		"for (int i = 0; i < 50; ++i) {")
	// 全局前台钩子必须按需注册/可撤销（不能每个 TIP 实例无条件挂）。
	checkContains(fsys, problems, group, winShell,
		"shouldWatchForeground", "ensureForegroundHook", "unhookForeground")
	// 「彻底退场」：失焦/停用不是只 hide()，而是销毁窗口 + 撤钩子。
	checkContains(fsys, problems, group, winShell,
		"retireUiCompletely", "candidateWnd_.destroy()", "toast_.destroy()")
	// 激活判定必须 fail-closed：查询不到就不许有动作（旧实现 return true，
	// 会让「不确定」时的后台实例继续建窗/挂钩子/处理按键）。
	checkExcludes(fsys, problems, group, winShell,
		"if (profileMgr_ == nullptr) return true;")
	// 拉起进程的副作用（daemon/UI 宿主预热）只能在「真正激活」时发生，且每
	// 实例只发一次（旧实现每次焦点回来都发 → worker 反复 spawn chaosetd，
	// 2026-11-07 用户看到 tsf.log 大量 daemon lazy start）。
	checkContains(fsys, problems, group, winShell,
		"updateInputActive(", "void warmDaemonInBackgroundOnce()", "if (warmed_) return;")
	// 设置窗拉起**不许轮询**：daemon 侧用 --settings 拉起，UI 宿主自己弹窗 /
	// 自己转发（2026-11-06 用户质疑「5×20ms 轮询在干什么」）。
	checkContains(fsys, problems, group, winShell,
		"ensureUiHostStartedByDaemon(/*settings=*/true)")
	checkContains(fsys, problems, group, ipcServer, `"settings"`)
	checkContains(fsys, problems, group, "ipc/ui_windows.go", "--settings")
	checkExcludes(fsys, problems, group, winShell,
		"if (uiSendCommand(\"settings\", 1)) return;")
	// 失焦分支必须走「彻底退场」而不是只 hide（用户要求：失去焦点后
	// 候选窗之类全部消失、取消任何钩子）。变异测试证明只查 retireUiCompletely
	// 是否存在不够——它可能被定义但失焦路径没调。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		sig := "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetFocus(BOOL fForeground) {"
		i := strings.Index(text, sig)
		if i < 0 {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 OnSetFocus", group, winShell))
		} else {
			end := i + 1200
			if end > len(text) {
				end = len(text)
			}
			body := text[i:end]
			if !strings.Contains(body, "retireUiCompletely()") &&
				!strings.Contains(body, "setActiveGate(false)") &&
				!strings.Contains(body, "updateInputActive(false)") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 OnSetFocus 失焦分支未彻底退场（必须调 retireUiCompletely()）",
					group, winShell))
			}
		}
	}
}

// checkTrayAndModeScope 锁住 2026-11-06 用户要求的三件事，以及 2026-11-11
// 两档作用域/TSF 焦点重构（spec §2.2–§2.5）：
//  1. 托盘左键 = 切换中英、右键 = 弹菜单（旧实现左键打开设置、右键无处理）；
//  2. 中英状态只有两档作用域（global/window），且状态同步只在获得焦点/
//     激活时做一次——按键热路径不许做跨进程查询（性能要求）；
//  3. 设置窗改卡片式布局（左导航三页 + 右侧分组卡片 + 页脚版本号 + 滚动）。
//  4. TSF 焦点原语：ITfThreadFocusSink（线程 UI 焦点）+
//     ITfActiveLanguageProfileNotifySink（输入法之间切换），focused_ 判定不再
//     用 GetForegroundWindow 的 pid 启发式。
//  5. 托盘可见性由 TSF 信号事件驱动：active 1/0 <pid> 命令（去掉 worker 前台
//     pid 门限），UI 宿主只维护单活跃 owner（无心跳/轮询定时器）。
func checkTrayAndModeScope(fsys fs.FS, problems *[]string) {
	const group = groupTrayModeScope
	checkContains(fsys, problems, group, winShell,
		"kUiToggleModeMsg", "showTrayMenu", "TrackPopupMenu",
		"WM_RBUTTONUP", "WM_LBUTTONUP", "打开设置…", "关于 Chaoset")
	// 左键必须切中英：托盘回调里投递 toggle 消息。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		i := strings.Index(text, "if (ev == WM_LBUTTONUP) {")
		if i < 0 {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少左键分支", group, winShell))
		} else {
			end := i + 400
			if end > len(text) {
				end = len(text)
			}
			if !strings.Contains(text[i:end], "kUiToggleModeMsg") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的左键分支未切换中英（应投递 kUiToggleModeMsg）", group, winShell))
			}
		}
	}
	// 作用域只有两档（System/Process）+ 持久化；旧值只在读取路径归一化。
	checkContains(fsys, problems, group, winShell,
		"enum class ModeScope", "ModeScope::System", "ModeScope::Process",
		"uiLoadModeScope", "uiSaveModeScope")
	checkExcludes(fsys, problems, group, winShell,
		"ModeScope::Global", "ModeScope::Window",
		"windowModes()", "focusWindowKey()", "ITfThreadMgrEventSink",
		"IID_ITfThreadMgrEventSink")
	// 旧存储值迁移必须真的留在读取路径（window→Process；未知/空→System）。
	checkContains(fsys, problems, group, winShell,
		`_wcsicmp(buf, L"system") == 0`, `_wcsicmp(buf, L"process") == 0`,
		`_wcsicmp(buf, L"window") == 0`, "return ModeScope::Process;",
		"return ModeScope::System;")
	// 热路径只读本地缓存：currentEnglish() 内不许出现跨进程查询（uiQueryMode）；
	// System 档 300ms 权威 TTL 复读（readHkcuEnglish）、Process 档进程级状态
	// （processEnglish）都必须存在。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		if body, line, ok := cppFunctionBody(text, "bool ChaosetTIP::currentEnglish()"); ok {
			code := stripCppComments(body)
			if strings.Contains(code, "uiQueryMode") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 currentEnglish()（约第 %d 行）在按键热路径做跨进程查询（应在焦点/激活时同步）",
					group, winShell, line))
			}
			for _, need := range []string{"readHkcuEnglish", "300", "processEnglish"} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 currentEnglish()（约第 %d 行）缺少 %q——System 档需按 TTL 复读 HKCU 镜像 / Process 档读进程状态",
						group, winShell, line, need))
				}
			}
		} else {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 currentEnglish()", group, winShell))
		}
	}
	checkContains(fsys, problems, group, winShell,
		"syncEnglishFromAuthority", "PIPE_ACCESS_DUPLEX", "uiQueryMode",
		"static bool& processEnglish()")
	// TSF 焦点/输入法切换原语：两个 sink + ITfSource Advise/Unadvise 成对。
	// ITfThreadFocusSink 判线程 UI 焦点（替换 GetForegroundWindow pid 启发式）；
	// ITfActiveLanguageProfileNotifySink 在「输入法之间切换」时通知（宿主常常
	// 不调 Deactivate，靠它立即收口）。
	checkContains(fsys, problems, group, winShell,
		"ITfThreadFocusSink", "ITfActiveLanguageProfileNotifySink", "IID_ITfSource",
		"IID_ITfThreadFocusSink", "IID_ITfActiveLanguageProfileNotifySink",
		"AdviseSink(IID_ITfThreadFocusSink", "AdviseSink(IID_ITfActiveLanguageProfileNotifySink",
		"UnadviseSink(threadFocusCookie_)", "UnadviseSink(languageProfileCookie_)",
		"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetThreadFocus()",
		"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKillThreadFocus()",
		"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnActivated(",
	)
	// Advise/Unadvise 生命周期必须成对：Deactivate 与析构都调 unadviseThreadSinks。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		for _, sig := range []string{
			"HRESULT STDMETHODCALLTYPE ChaosetTIP::Deactivate()",
			"~ChaosetTIP()",
		} {
			if body, line, ok := cppFunctionBody(text, sig); ok {
				if !strings.Contains(stripCppComments(body), "unadviseThreadSinks()") {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 %s（约第 %d 行）未 Unadvise TSF sink（ITfSource cookie 必须成对摘除）",
						group, winShell, sig, line))
				}
			} else {
				*problems = append(*problems, fmt.Sprintf("%s：%s 找不到 %s 定义", group, winShell, sig))
			}
		}
		// OnActivated：Chaoset 激活 → 进入在用并按作用域应用模式；切走 → 立即
		// 收口（focused_/inputActive_/postActiveReport/retireUiCompletely）。
		if body, line, ok := cppFunctionBody(text, "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnActivated("); !ok {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 OnActivated 定义", group, winShell))
		} else {
			code := stripCppComments(body)
			for _, need := range []string{
				"CLSID_ChaosetTIP", "focused_ = true", "updateInputActive(true)",
				"applyModeForCurrentScope()", "postActiveReport(false", "retireUiCompletely()",
			} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 OnActivated（约第 %d 行）缺少 %q（输入法切换通知必须收口托盘/激活状态）",
						group, winShell, line, need))
				}
			}
		}
		// 焦点 sink 回调必须真的驱动激活状态迁移（只断言存在性不够：删掉
		// updateInputActive 调用后托盘/激活态就不再跟随线程焦点）。
		for _, cb := range []struct{ sig, need string }{
			{"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetThreadFocus()", "updateInputActive(true)"},
			{"HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKillThreadFocus()", "updateInputActive(false)"},
		} {
			if body, line, ok := cppFunctionBody(text, cb.sig); !ok {
				*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 %s 定义", group, winShell, cb.sig))
			} else if !strings.Contains(stripCppComments(body), cb.need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 %s（约第 %d 行）缺少 %q（焦点 sink 必须驱动激活状态迁移）",
					group, winShell, cb.sig, line, cb.need))
			}
		}
		// 同进程多实例：失活上报必须有 wasActive 守卫（非 owner 实例的 active 0
		// 会误隐藏 owner 的托盘）；OnActivated(false) 还要与 switchToInactiveProfile
		// 一样挂起 Shift 记账，避免切输入法后 Shift 会话残留。
		if body, line, ok := cppFunctionBody(text, "HRESULT STDMETHODCALLTYPE ChaosetTIP::Deactivate()"); ok {
			code := stripCppComments(body)
			for _, need := range []string{"const bool wasActive = inputActive_", "if (wasActive) postActiveReport(false"} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 Deactivate（约第 %d 行）缺少 %q（同进程多实例下非激活实例不得发 active 0）",
						group, winShell, line, need))
				}
			}
		}
		if body, line, ok := cppFunctionBody(text, "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnActivated("); ok {
			code := stripCppComments(body)
			for _, need := range []string{
				"const bool wasActive = inputActive_", "if (wasActive)",
				"postActiveReport(false", "suspendShiftHoldOnFocusLost()",
			} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 OnActivated（约第 %d 行）缺少 %q（切走输入法必须只对激活实例收口）",
						group, winShell, line, need))
				}
			}
		}
		// QueryInterface 必须真的暴露两个新 sink 接口（AdviseSink 的字面量会
		// 被其它文本满足，只有 body 级断言能抓住 QI 分支被删）。
		if body, line, ok := cppFunctionBody(text, "HRESULT STDMETHODCALLTYPE ChaosetTIP::QueryInterface(REFIID riid, void** ppv)"); !ok {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 QueryInterface 定义", group, winShell))
		} else {
			code := stripCppComments(body)
			for _, need := range []string{"IID_ITfThreadFocusSink", "IID_ITfActiveLanguageProfileNotifySink"} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 QueryInterface（约第 %d 行）未暴露 %s（TSF AdviseSink 会 QI 该接口）",
						group, winShell, line, need))
				}
			}
		}
	}
	// 两档同步方向：System 从权威同步（不上报 mode）；Process 取进程级状态并
	// 上报 mode。防回抖记账（本地切换 500ms / 权威同步 300ms）必须存在。
	checkContains(fsys, problems, group, winShell,
		"void ChaosetTIP::applyModeForCurrentScope()",
		"lastAuthoritySyncTick_", "lastLocalToggleTick_",
		"void ChaosetTIP::onWorkerModeAuthority()")
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		if body, _, ok := cppFunctionBody(text, "void ChaosetTIP::onWorkerModeAuthority()"); ok {
			if !strings.Contains(stripCppComments(body), "lastLocalToggleTick_") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 onWorkerModeAuthority 未用本地切换防回抖窗（异步校准会把刚切的模式翻回去）",
					group, winShell))
			}
		}
		// 两档语义必须真的落在函数体里（防「只改枚举名/只改注释」）。
		if body, line, ok := cppFunctionBody(text, "void ChaosetTIP::applyModeForCurrentScope()"); !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 applyModeForCurrentScope()（两档同步方向唯一出口）", group, winShell))
		} else {
			code := stripCppComments(body)
			for _, need := range []string{"syncEnglishFromAuthority", "processEnglish", "reportModeToUi("} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 applyModeForCurrentScope（约第 %d 行）缺少 %q（System 从权威同步 / Process 取进程级状态并上报）",
						group, winShell, line, need))
				}
			}
		}
		if body, line, ok := cppFunctionBody(text, "void ChaosetTIP::updateInputActive(bool fromFocusEvent)"); !ok {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 updateInputActive()", group, winShell))
		} else {
			code := stripCppComments(body)
			for _, need := range []string{"IsThreadFocus", "applyModeForCurrentScope()", "postActiveReport(true, mode, true)"} {
				if !strings.Contains(code, need) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 updateInputActive（约第 %d 行）缺少 %q（TSF 焦点判定/按作用域重应用/激活上报）",
						group, winShell, line, need))
				}
			}
		}
	}
	// 托盘可见性由 TSF 信号事件驱动：命令带 pid 与当前模式；worker 不再用前台
	// 窗口 pid 门限（UWP/ApplicationFrameHost 宿主的前台窗口 pid != TIP 进程 pid）。
	checkContains(fsys, problems, group, winShell,
		"bool reportModeToUi(bool english, bool allowSpawn) {",
		`"active 1 %d %lu"`, `"active 0 %lu"`, `"mode %d %lu"`,
		"GetCurrentProcessId()")
	// 必须保留「激活迁移发一次 active 1」：不能因为去定时器而丢掉可见性上报。
	checkContains(fsys, problems, group, winShell, "postActiveReport(true, mode, true)")
	// 防回潮：禁止再引入激活档案轮询定时器 / 托盘心跳 prune / 前台 pid 门限。
	checkExcludes(fsys, problems, group, winShell,
		"workerWaitForegroundSelf", "GetNamedPipeClientProcessId",
		"kActiveWatchTimerId", "onActiveWatchTimer", "refreshActiveWatchTimer",
		"kUiPruneTimerId", "kUiActiveStaleMs", "g_uiActivePids")

	// 设置窗卡片式布局：左导航三页 + 卡片 + 页脚 + 滚动。
	checkContains(fsys, problems, group, winShell,
		"enum class SettingsPage", "SettingsPage::Input", "SettingsPage::Dictionary",
		"SettingsPage::About", "kNavWidth", "settingsPaint", "settingsSwitchPage",
		"settingsNavItemRect", "kCardRadius", "WM_MOUSEWHEEL")
	// 「关于」跳设置窗关于页（不再另开 MessageBox 对话框）。
	checkContains(fsys, problems, group, winShell,
		"kUiOpenSettingsAboutMsg", "openSettingsWindowStandalone(SettingsPage::About)")
}

// checkTrayLazyLifecycle 锁住 2026-11-06 用户要求：托盘中英图标不随开机显示，
// TIP 首次激活（切到输入法）时带 --mode 懒启动 UI 宿主，UI 宿主收到首条模式
// 上报后才第一次 Shell_NotifyIcon；安装器/便携包升级必须清掉旧版 ChaosetUi
// Run 项，且不得再写登录 Run 项。
func checkTrayLazyLifecycle(fsys fs.FS, problems *[]string) {
	const group = groupTrayLifecycle
	// 托盘可见性由 active 命令控制（2026-11-07）；启动/模式上报都不得无条件
	checkContains(fsys, problems, group, winShell,
		"ensureUiHostStarted(const wchar_t* args)",
		`ensureUiHostStarted(english ? L"--mode 1" : L"--mode 0")`,
		"--mode 1", "--mode 0", "visible_")
	// 安装器：ChaosetUi 只能出现在 DeleteRegValue（清旧 Run 项）行。
	if data, err := fs.ReadFile(fsys, winInstaller); err == nil {
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "ChaosetUi") {
				continue
			}
			if strings.Contains(line, "WriteRegStr") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s:%d 仍写入 ChaosetUi Run 项（UI 宿主不随登录启动）", group, winInstaller, i+1))
			} else if !strings.Contains(line, "DeleteRegValue") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s:%d 出现 ChaosetUi 但不是 DeleteRegValue 清理（升级必须清旧 Run 项）", group, winInstaller, i+1))
			}
		}
	}
	checkContains(fsys, problems, group, winInstaller, "DeleteRegValue", "ChaosetUi")
	// 便携包 install.bat 也必须幂等清理旧 Run 项。
	if data, err := fs.ReadFile(fsys, winInstallBat); err == nil {
		cleaned := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "reg delete") && strings.Contains(line, "ChaosetUi") {
				cleaned = true
			}
		}
		if !cleaned {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 reg delete ... ChaosetUi（便携包升级清旧 Run 项）", group, winInstallBat))
		}
	}
	// 文档必须保留新的 UI 宿主生命周期描述。
	checkContains(fsys, problems, group, uiParityDoc,
		"不写登录 Run 项", "TIP 首次激活带 --mode 懒启动",
		"active 1", "active 0", "输入法未激活时隐藏")
}

// checkTsfZeroBlockContract 锁住 2026-11-06 用户要求（「输入法在任何情况下都
// 不应阻塞任何进程或 app」）+ AGENTS「零阻塞契约（Windows）」：
// TSF 把 TIP 加载进每个宿主进程，所有 TSF 回调都在宿主 UI 线程同步执行，
// 因此回调体内禁止任何跨进程等待/建进程/同步写盘——副作用统一交给每进程一个
// ShellWorker 后台线程（任务队列 + message-only 窗口回投结果）。
//
// 断言与 .workwork/impl/phase1-report.md §4 的 a–e 对应：
//
//	a) 18 个关键回调函数体不含阻塞/跨进程/同步写盘 API 与慢连接路径；
//	   tsf.log 只走 AsyncLog（diagPrintf → enqueueLogLine，不回退同步写）。
//	b) 零阻塞基础设施存在：ShellWorker/ShellTask/AsyncLog/message-only 窗口/
//	   overlapped 管道/熔断。
//	c) 慢连接 ensureDaemonConnected 只允许 worker；TSF 调用点用 ensureDaemonFast。
//	d) 前台钩子回调只 PostMessage；TIP 线程处理前台变化时日志必须限流。
//	e) DllCanUnloadNow 计入 worker 线程与 message-only 窗口。
func checkTsfZeroBlockContract(fsys fs.FS, problems *[]string) {
	const group = groupTsfZeroBlock
	data, err := fs.ReadFile(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return
	}
	src := string(data)

	// a) 关键 TSF 回调函数体：禁止阻塞/跨进程/同步写盘 API 与慢连接路径。
	// wave2（TIP 类拆分）后这些方法定义在 tip_core/keys/composition/candidates.cpp，
	// 类内只剩声明；needle 必须是**类外定义**的完整签名（cppFunctionBody 遇到声明
	// 的 ';' 会跳过，再匹配不到就报「签名变了」，等于静默失去零阻塞守卫）。
	callbacks := []struct{ name, sig string }{
		{"Activate", "HRESULT STDMETHODCALLTYPE ChaosetTIP::Activate(ITfThreadMgr* ptim, TfClientId tid)"},
		{"ActivateEx", "HRESULT STDMETHODCALLTYPE ChaosetTIP::ActivateEx(ITfThreadMgr* ptim, TfClientId tid,"},
		{"OnSetFocus", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetFocus(BOOL fForeground)"},
		// 2026-11-11 新增的 TSF 焦点/输入法切换 sink 回调同样在宿主 UI 线程
		// 同步执行：只允许内存操作 + 入队（零阻塞契约）。
		{"OnSetThreadFocus", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetThreadFocus()"},
		{"OnKillThreadFocus", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKillThreadFocus()"},
		{"OnActivated", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnActivated(REFCLSID clsid,"},
		{"OnTestKeyDown", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnKeyDown", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnTestKeyUp", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnKeyUp", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"render", "void ChaosetTIP::render(ITfContext* pic)"},
		{"handleKey", "bool ChaosetTIP::handleKey(WPARAM vk, ITfContext* pic)"},
		{"showSettingsWindow", "void ChaosetTIP::showSettingsWindow()"},
		{"onForegroundChanged", "void ChaosetTIP::onForegroundChanged(HWND fg)"},
		{"selectCandidateByIndex", "void ChaosetTIP::selectCandidateByIndex(int index)"},
		{"deleteCandidateByIndex", "void ChaosetTIP::deleteCandidateByIndex(int index)"},
		{"flipPage", "void ChaosetTIP::flipPage(bool next)"},
		{"CandidateWindow::show",
			"void CandidateWindow::show(HINSTANCE hinst, POINT pt, int lineH, const chaoset::Comp& comp, CandidateWindowHost* host)"},
	}
	banned := []string{
		"Sleep(", "WaitNamedPipeW", "WaitForSingleObject", "CreateProcessW",
		"CreateFileW", "WriteFile(", "MessageBoxW", "GetOpenFileNameW",
		"RegSetValueExW", "ensureDaemonConnected(",
	}
	for _, cb := range callbacks {
		body, line, ok := cppFunctionBody(src, cb.sig)
		if !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 找不到 %s 的函数体（签名变了？）", group, winShell, cb.name))
			continue
		}
		code := stripCppComments(body)
		for _, bad := range banned {
			if strings.Contains(code, bad) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 %s（约第 %d 行）函数体出现 %q——TSF 回调不得跨进程等待/"+
						"建进程/同步写盘（应入队 ShellWorker）", group, winShell, cb.name, line, bad))
			}
		}
	}

	// a2) tsf.log 必须异步：diagPrintf 对 tsf.log 只 enqueueLogLine；同步写盘只
	// 留给崩溃路径的 diagWrite（crash.log）。
	if body, line, ok := cppFunctionBody(src, "void diagPrintf(const wchar_t* name, const char* fmt, ...)"); ok {
		code := stripCppComments(body)
		for _, need := range []string{"enqueueLogLine(", `wcscmp(name, L"tsf.log")`} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 diagPrintf（约第 %d 行）缺少 %q——tsf.log 必须走 AsyncLog", group, winShell, line, need))
			}
		}
		if strings.Contains(code, "WriteFile(") || strings.Contains(code, "CreateFileW") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 diagPrintf（约第 %d 行）出现同步写盘——TSF 线程不得写盘（崩溃路径另有 diagWrite）",
				group, winShell, line))
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：%s 找不到 diagPrintf 定义", group, winShell))
	}

	// b) 零阻塞基础设施必须存在（worker 任务队列 / AsyncLog / message-only 窗口 /
	// overlapped 管道 + 截止时间 / 熔断）。
	checkContains(fsys, problems, group, winShell,
		"class ShellWorker", "ShellTask", "enqueueLogLine", "kTipForegroundMsg",
		"HWND_MESSAGE", "FILE_FLAG_OVERLAPPED", "circuitOpen", "setDeadlineMs",
	)

	// c) 慢连接 ensureDaemonConnected 只允许 worker；TSF 调用点用 ensureDaemonFast
	// （单次非阻塞探测 + fail-open）。
	checkContains(fsys, problems, group, winShell, "ensureDaemonFast(")
	if body, line, ok := cppFunctionBody(src, "bool ensureDaemonFast(PipeTransport& transport)"); ok {
		if strings.Contains(stripCppComments(body), "ensureDaemonConnected(") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 ensureDaemonFast（约第 %d 行）又回退到 ensureDaemonConnected（会阻塞）",
				group, winShell, line))
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：%s 找不到 ensureDaemonFast 定义", group, winShell))
	}

	// d) 前台钩子：系统级回调体内只 PostMessage（不 hide、不写盘、不等待）；
	// TIP 线程处理前台变化时日志必须限流（前台风暴不能每事件刷盘）。
	if body, line, ok := cppFunctionBody(src, "void CALLBACK ChaosetTIP::fgHookProc(HWINEVENTHOOK /*hook*/"); ok {
		code := stripCppComments(body)
		for _, need := range []string{"PostMessageW(", "kTipForegroundMsg"} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的前台钩子回调（约第 %d 行）缺少 %q——钩子回调只能 PostMessage",
					group, winShell, line, need))
			}
		}
		for _, bad := range banned {
			if strings.Contains(code, bad) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的前台钩子回调（约第 %d 行）出现 %q（回调体内只许 PostMessage）",
					group, winShell, line, bad))
			}
		}
		if strings.Contains(code, "diagPrintf") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的前台钩子回调（约第 %d 行）出现 diagPrintf（不得每事件写盘）",
				group, winShell, line))
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：%s 找不到 fgHookProc 定义", group, winShell))
	}
	if body, line, ok := cppFunctionBody(src, "void ChaosetTIP::onForegroundChanged(HWND fg)"); ok {
		code := stripCppComments(body)
		if strings.Contains(code, "diagPrintf") && !strings.Contains(code, "lastLog") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 onForegroundChanged（约第 %d 行）写诊断日志但没有限流（前台风暴会刷盘）",
				group, winShell, line))
		}
	}

	// e) DLL 卸载：worker 线程与 message-only 窗口活着时不得卸载 DLL。
	if body, line, ok := cppFunctionBody(src, "STDAPI DllCanUnloadNow()"); ok {
		code := stripCppComments(body)
		for _, need := range []string{"g_liveTipMsgWindows", "shellWorkerAlive()", "shellWorkerStop()"} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 DllCanUnloadNow（约第 %d 行）缺少 %q——worker/消息窗口活着时不得卸载",
					group, winShell, line, need))
			}
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：%s 找不到 DllCanUnloadNow", group, winShell))
	}

	// 文档必须记录契约（防止实现与 docs/ui-parity.md 脱节）。
	checkContains(fsys, problems, group, uiParityDoc,
		"## 零阻塞契约", "TSF 回调", "ShellWorker", "fail-open",
		"FILE_FLAG_OVERLAPPED", "DllCanUnloadNow", "checkuiparity")
}

// checkWindowsInstallSafety 固定安装器的两个用户可见契约（2026-11-03）：
//  1. 安装/升级不得强杀加载旧 DLL 的用户程序或系统输入宿主——用户未保存的
//     工作不能因为装输入法丢失；被占用文件由 InstallRobustFile/重启替换兜底。
//  2. daemon 不能由提升后的安装器以管理员身份启动（LOCALAPPDATA/管道 ACL
//     可能属于另一个账户，用户侧因此打不出拼音）；改为 Run 项（REG_EXPAND_SZ
//     + %LOCALAPPDATA%）与 TSF 首次输入懒启动（-pipe + -seeddict）。
func checkWindowsInstallSafety(fsys fs.FS, problems *[]string) {
	const group = groupWindowsInstall
	// 禁止任何按模块名批量杀进程或强停系统输入宿主的执行语句；注释里提到
	// 历史行为不算（只匹配实际执行片段）。
	for _, path := range []string{winInstaller, winInstallBat} {
		checkExcludes(fsys, problems, group, path,
			"Stop-Process -Id $_",
			"taskkill /F /IM ctfmon.exe",
			"taskkill /F /IM TextInputHost.exe",
			"taskkill /F /IM SearchHost.exe",
			"taskkill /F /IM StartMenuExperienceHost.exe",
			"taskkill /F /IM SearchApp.exe",
			"taskkill /F /IM SearchUI.exe",
		)
	}
	// Run 项必须按用户展开 LOCALAPPDATA，并交给 daemon 首次初始化种子词库。
	checkContains(fsys, problems, group, winInstaller,
		"WriteRegExpandStr", "%LOCALAPPDATA%\\Chaoset\\dict.tsv",
		"-pipe", "-seeddict")
	// TSF 壳必须在当前用户上下文懒启动（-seeddict 首次初始化用户词库）。
	checkContains(fsys, problems, group, winShell,
		"ensureDaemonStarted", "ensureDaemonConnected", "-seeddict")
	// 设置窗重启 daemon 必须用 detached 启动；settingsRunProcess 会等 120s
	// 再 TerminateProcess，曾把 daemon 当普通导入器进程杀掉。
	checkContains(fsys, problems, group, winShell, "settingsStartProcessDetached")
	checkExcludes(fsys, problems, group, winShell, `settingsRunProcess(L"", run`)
	// 词库刷新必须走 Go 导入器（保留用户导入词 + 备份 + 原子替换），不能
	// 用 CopyFiles 直接覆盖用户词库。
	checkContains(fsys, problems, group, winInstaller,
		"chaoset-import.exe", "-seed", "-backup")
	checkContains(fsys, problems, group, winInstallBat, "chaoset-import.exe")
	checkExcludes(fsys, problems, group, winInstaller,
		`CopyFiles /SILENT "$INSTDIR\dict.tsv"`)
	// 安装器不得自己启动 daemon（管理员上下文会写错用户数据/管道）。
	checkExcludes(fsys, problems, group, winInstaller,
		`Exec '"$INSTDIR\chaosetd.exe"`)
	// UI 宿主（托盘/设置窗）同样不能在提升后的安装器里启动：它必须是当前登录
	// 用户的普通完整性进程，否则托盘图标与设置窗落在错误的会话/权限上下文。
	checkExcludes(fsys, problems, group, winInstaller,
		`Exec '"$INSTDIR\chaoset-ui.exe"`)
	// 升级/卸载必须结束本输入法自己的旧 UI 宿主，否则单例互斥体与命令管道
	// 仍被旧进程占用，新版本托盘/设置窗行为不生效。
	checkContains(fsys, problems, group, winInstaller, "taskkill /IM chaoset-ui.exe")

	// 三端都必须有「发现 daemon 没起来就拉起它」的懒启动路径：自启动（Run 项 /
	// LaunchAgent）是常态，但被用户关掉、被安全软件拦截、安装后未重新登录等
	// 情况都要能自愈。Windows/Linux 早就有了，macOS 曾缺失（只依赖
	// LaunchAgent），这里固定三端都存在。
	checkContains(fsys, problems, group, winShell,
		"ensureDaemonStarted", "CreateProcessW")
	checkContains(fsys, problems, group, linuxEngineMain, "spawnDaemon")
	checkContains(fsys, problems, group, macEngineClient, "Process()", "chaosetd")
}

// checkInactiveZeroSideEffects 锁住 2026-11-07 用户要求「输入法没激活时不做任何
// 操作、不拦截按键、不获取鼠标事件」：
//  1. bailOutIfInactive 是唯一闸门且体内零副作用（不清理/不查询/不写盘/不动钩子）；
//  2. 四个按键入口在闸门后立即 return，修饰键永不消费（KeyUp 恒 FALSE）；
//  3. 整个 Windows 壳不得安装键盘/鼠标钩子或注入输入；TSF 回调里不得读鼠标；
//  4. active 上报与 worker 前台校验必须存在；daemon/UI 副作用只在 worker +
//     非 AppContainer 时发生。
func checkInactiveZeroSideEffects(fsys fs.FS, problems *[]string) {
	const group = groupInactiveZeroSide
	data, err := fs.ReadFile(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return
	}
	src := string(data)
	if body, line, ok := cppFunctionBody(src, "bool ChaosetTIP::bailOutIfInactive()"); !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 bailOutIfInactive()", group, winShell))
	} else {
		code := stripCppComments(body)
		if !strings.Contains(code, "inputActive_") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 bailOutIfInactive（约第 %d 行）未判 inputActive_（激活闸门失效）",
				group, winShell, line))
		}
		// 每键复核激活档案：部分宿主切换输入法后不调用 Deactivate，只有这里
		// 能保证「已经换到别的输入法后不再吃键/不再显示托盘」。
		if !strings.Contains(code, "isActiveTip()") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 bailOutIfInactive（约第 %d 行）缺少 isActiveTip() 兜底（切输入法不调 Deactivate 的宿主会漏判）",
				group, winShell, line))
		}
		for _, bad := range []string{
			"retireUiCompletely", "candidateWnd_.hide", "toast_.hide",
			"resetShiftTracking", "GetForegroundWindow",
			"GetAsyncKeyState", "GetCursorPos", "PostMessageW",
		} {
			if strings.Contains(code, bad) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 bailOutIfInactive（约第 %d 行）出现 %q——非激活必须零动作（清理只在 active→inactive 迁移时做）",
					group, winShell, line, bad))
			}
		}
	}
	// 修饰键的抬起永不消费（否则 OS/宿主会认为修饰键仍按着）。
	callbacks := []struct{ name, sig string }{
		{"OnTestKeyUp", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnKeyUp", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM"},
	}
	for _, cb := range callbacks {
		body, line, ok := cppFunctionBody(src, cb.sig)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 %s", group, winShell, cb.name))
			continue
		}
		code := stripCppComments(body)
		if !strings.Contains(code, "*pfEaten = FALSE;") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 %s（约第 %d 行）未显式放行（修饰键 KeyUp 必须恒 FALSE）", group, winShell, cb.name, line))
		}
		if strings.Contains(code, "*pfEaten = TRUE;") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 %s（约第 %d 行）出现 *pfEaten = TRUE——KeyUp 绝不许消费（会造成 OS 修饰键卡住）",
				group, winShell, cb.name, line))
		}
	}
	// 全壳：禁止安装键盘/鼠标钩子、禁止注入输入（用户 2026-11-07 明确要求）。
	full := stripCppComments(src)
	for _, bad := range []string{
		"SetWindowsHookEx(", "SetWindowsHookExW(", "WH_MOUSE", "WH_KEYBOARD",
		"SendInput(", "keybd_event(", "SetKeyboardState(", "BlockInput(",
		"SetCapture(", "AttachThreadInput(",
	} {
		if strings.Contains(full, bad) {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 出现 %q——输入法不得安装键鼠钩子或注入输入（只用 TSF 回调）",
				group, winShell, bad))
		}
	}
	// TSF 回调体内不得读鼠标 / 全局键盘状态（GetKeyState 只在 handleKey 等激活路径用）。
	for _, cb := range []struct{ name, sig string }{
		{"Activate", "HRESULT STDMETHODCALLTYPE ChaosetTIP::Activate(ITfThreadMgr* ptim, TfClientId tid)"},
		{"OnSetFocus", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetFocus(BOOL fForeground)"},
		{"OnTestKeyDown", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnKeyDown", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnTestKeyUp", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM"},
		{"OnKeyUp", "HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM"},
	} {
		body, line, ok := cppFunctionBody(src, cb.sig)
		if !ok {
			continue
		}
		code := stripCppComments(body)
		for _, bad := range []string{"GetCursorPos(", "GetAsyncKeyState(", "GetKeyState("} {
			if strings.Contains(code, bad) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 %s（约第 %d 行）出现 %q——TSF 回调不得读鼠标/全局键态",
					group, winShell, cb.name, line, bad))
			}
		}
	}
	// active 生命周期（托盘只在真正激活时显示）：命令带 pid + 当前模式，
	// 且不带旧的前台窗口 pid 门限（UWP/ApplicationFrameHost 宿主会误判）。
	// 纯 TSF 信号事件驱动：不允许回退到轮询定时器/心跳。
	checkContains(fsys, problems, group, winShell,
		"case ShellTask::Kind::kReportActive:",
		`"active 1 %d %lu"`, `"active 0 %lu"`, "postActiveReport(",
		"updateInputActive(", "inputActive_", "GetCurrentProcessId()",
		// 输入法之间切换：宿主常不调 Deactivate，靠 TSF 通知立即收口。
		"ITfActiveLanguageProfileNotifySink", "OnActivated")
	checkExcludes(fsys, problems, group, winShell,
		"workerWaitForegroundSelf", "kActiveWatchTimerId", "onActiveWatchTimer",
		"refreshActiveWatchTimer", "kUiPruneTimerId")
	// daemon spawn 只在 worker 且非 AppContainer（修 2026-11-07 重复 spawn）。
	checkContains(fsys, problems, group, winShell,
		"const bool canSpawn = !isAppContainer() && !daemonMutexPresent();",
		"if (!isAppContainer() && !daemonMutexPresent()) ensureDaemonStarted();")
}

// checkTrayActiveLifecycle 锁住 2026-11-07 用户要求（bug1/bug3）+ 2026-11-11
// TSF 事件驱动 + 单活跃 owner（用户明确要求「不要定时器轮询」）：
//  1. 托盘可见性只由 TIP 的 active 上行控制：active 1 <0|1> <pid> 设 owner 并
//     显示；active 0 <pid> 仅当 sender==owner 才在防抖后 NIM_DELETE；mode 命令
//     只更新 owner 的图标、绝不显示；
//  2. 启动参数 --mode 不得点亮托盘（开机不显示）；
//  3. 切换输入法但 TSF 未调用 Deactivate 的宿主，靠 TSF 信号
//     （OnKillThreadFocus / OnActivated(false)）+ bailOutIfInactive 按键复核兜底，
//     **没有任何轮询定时器/心跳**；
//  4. Process 档托盘跟随当前进程（mode 命令不写 HKCU），System 档托盘是权威
//     （写 HKCU）；Process 档托盘左键不反向改进程状态；
//  5. owner 进程崩溃用 OpenProcess+RegisterWaitForSingleObject 事件清理
//     （不是定时器 prune）。
func checkTrayActiveLifecycle(fsys fs.FS, problems *[]string) {
	const group = groupTrayActive
	data, err := fs.ReadFile(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return
	}
	src := string(data)
	checkContains(fsys, problems, group, winShell,
		"kUiActiveMsg", "kUiHideTimerId", "void setMode(bool english)", "NIM_DELETE",
		"switchToInactiveProfile", "postActiveReport", "updateInputActive(",
		// 单活跃 owner（无心跳/轮询）+ owner 进程退出事件清理。
		"g_uiOwnerPid", "g_uiOwnerEnglish", "uiCommandPid(", "uiDisplayEnglish()",
		"kUiOwnerExitedMsg", "uiOwnerExitedCallback",
		"OpenProcess(SYNCHRONIZE", "RegisterWaitForSingleObject",
		"UnregisterWaitEx", "uiSetOwnerProcess(", "uiClearOwner()")
	// 两档枚举必须精确（防止加回第三档）；300ms 防抖数值与 owner 退出清理必须
	// 是真实代码（纯 checkContains 可被注释满足，下面补 body 级断言）。
	checkContains(fsys, problems, group, winShell,
		"enum class ModeScope { System, Process };",
		"static constexpr UINT kUiHideDelayMs = 300;")
	if body, line, ok := cppFunctionBody(src, "static void uiSetOwnerProcess(DWORD pid, HWND wnd)"); !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 uiSetOwnerProcess 定义", group, winShell))
	} else {
		code := stripCppComments(body)
		for _, need := range []string{"OpenProcess(SYNCHRONIZE", "RegisterWaitForSingleObject", "uiOwnerExitedCallback"} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 uiSetOwnerProcess（约第 %d 行）缺少 %q（owner 崩溃清理必须事件驱动）",
					group, winShell, line, need))
			}
		}
	}
	if body, line, ok := cppFunctionBody(src, "static void uiClearOwner()"); !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 uiClearOwner 定义", group, winShell))
	} else {
		code := stripCppComments(body)
		for _, need := range []string{"UnregisterWaitEx", "CloseHandle"} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 uiClearOwner（约第 %d 行）缺少 %q（wait/进程句柄必须成对释放）",
					group, winShell, line, need))
			}
		}
	}
	// 事件驱动的 active 命令必须带 pid 与当前模式。
	checkContains(fsys, problems, group, winShell,
		`"active 1 %d %lu"`, `"active 0 %lu"`,
		"postActiveReport(true, mode, true)")
	// 线程池 wait 回调只能 PostMessage（UI 状态/托盘只能由主线程修改）。
	if body, line, ok := cppFunctionBody(src, "static VOID CALLBACK uiOwnerExitedCallback("); !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 owner 进程退出回调", group, winShell))
	} else {
		code := stripCppComments(body)
		if !strings.Contains(code, "PostMessageW(") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 owner 进程退出回调（约第 %d 行）未 PostMessage（跨线程不得碰 UI 状态）",
				group, winShell, line))
		}
		for _, bad := range []string{"g_uiTray", "->hide(", "->show(", "Shell_NotifyIcon"} {
			if strings.Contains(code, bad) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 owner 进程退出回调（约第 %d 行）出现 %q——线程池回调只能 PostMessage",
					group, winShell, line, bad))
			}
		}
	}
	// 托盘可见性只能由 active 分支 show：mode/启动路径必须只调 setMode。
	if body, line, ok := cppFunctionBody(src, "static void uiApplyEnglish(bool english)"); ok {
		code := stripCppComments(body)
		if strings.Contains(code, "->show(") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 uiApplyEnglish（约第 %d 行）仍调用 ->show(（mode 命令不得显示托盘）",
				group, winShell, line))
		}
		if !strings.Contains(code, "setMode(") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 uiApplyEnglish（约第 %d 行）未调 setMode（只更新图标）", group, winShell, line))
		}
	}
	// active 1 必须设 owner 并 show（取消隐藏定时器）；active 0 只有 owner 才
	// 防抖 hide（后台实例的 active 0 不得误隐藏）；owner 退出事件清理。
	if body, line, ok := cppFunctionBody(src, "static LRESULT CALLBACK uiHostWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam)"); ok {
		code := stripCppComments(body)
		for _, need := range []string{
			"kUiActiveMsg", "SetTimer(hwnd, kUiHideTimerId", "->show(", "->hide()",
			"g_uiOwnerPid", "uiSetOwnerProcess(pid, hwnd)",
			"pid == g_uiOwnerPid", "uiClearOwner()", "kUiOwnerExitedMsg",
		} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 uiHostWndProc（约第 %d 行）缺少 %q（active 事件显示/隐藏/单 owner 契约）",
					group, winShell, line, need))
			}
		}
	}
	// mode 命令分支只走 setMode（不显示）。
	if body, line, ok := cppFunctionBody(src, "static LRESULT CALLBACK uiHostWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam)"); ok {
		code := stripCppComments(body)
		if !strings.Contains(code, "kUiSetModeMsg") || !strings.Contains(code, "setMode(") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 uiHostWndProc（约第 %d 行）mode 分支未走 setMode（不得显示托盘）",
				group, winShell, line))
		}
	}
	// 托盘图标取值：System 档用权威 g_uiEnglish，Process 档用当前 pid 条目。
	if body, line, ok := cppFunctionBody(src, "static bool uiDisplayEnglish()"); !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 uiDisplayEnglish()（两档显示取值）", group, winShell))
	} else {
		code := stripCppComments(body)
		for _, need := range []string{"ModeScope::System", "g_uiEnglish", "g_uiOwnerEnglish"} {
			if !strings.Contains(code, need) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 uiDisplayEnglish（约第 %d 行）缺少 %q（System 权威 / Process 取 owner 上报值）",
					group, winShell, line, need))
			}
		}
	}
	// Process 档托盘左键不得反向改进程状态：toggle 分支必须只在 System 档生效。
	if i := strings.Index(src, "case kUiToggleModeMsg:"); i < 0 {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 kUiToggleModeMsg 分支", group, winShell))
	} else {
		end := i + 600
		if end > len(src) {
			end = len(src)
		}
		branch := stripCppComments(src[i:end])
		if !strings.Contains(branch, "ModeScope::System") || !strings.Contains(branch, "uiApplyEnglish(!g_uiEnglish)") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的托盘左键 toggle 分支未限定 System 档（Process 档只显示、不切换）", group, winShell))
		}
	}
	// 防回潮：用户明确要求「有 TSF 信号就不要定时器轮询」——禁止再引入激活
	// 档案轮询定时器 / 托盘心跳 prune 定时器。
	checkExcludes(fsys, problems, group, winShell,
		"kActiveWatchTimerId", "onActiveWatchTimer", "refreshActiveWatchTimer",
		"kUiPruneTimerId", "kUiActiveStaleMs", "g_uiActivePids", "g_uiLastActivePid")
	// 文档必须记录 active 生命周期与 scope 对齐语义。
	checkContains(fsys, problems, group, uiParityDoc,
		"active 1", "active 0", "输入法未激活时隐藏")
}

// checkDaemonSpawnGuard 锁住 2026-11-07 审计确认的「重复 spawn chaosetd」根因修复：
//  1. 管道探测三态：ERROR_PIPE_BUSY/ERROR_ACCESS_DENIED 说明 daemon 在，只有
//     真正没有管道才算不存在（go-winio 同一时刻只有一个监听实例，实测 80% busy）；
//  2. spawn 前先查 Local\\ChaosetDaemon 互斥体；
//  3. chaosed 先把单实例锁放在词库加载之前（重复实例不再白载 65k 词典）；
//  4. 后台预激活实例不创建 worker/消息窗口（延迟到真正激活）。
func checkDaemonSpawnGuard(fsys fs.FS, problems *[]string) {
	const group = groupDaemonSpawnGuard
	data, err := fs.ReadFile(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return
	}
	src := string(data)
	checkContains(fsys, problems, group, "shims/windows/src/transport.cpp",
		"ERROR_PIPE_BUSY", "ERROR_ACCESS_DENIED",
		"只有「根本没有这个管道」才算不存在")
	checkContains(fsys, problems, group, winShell,
		"daemonMutexPresent()", "OpenMutexW(SYNCHRONIZE, FALSE, L\"Local\\\\ChaosetDaemon\")",
		"const bool canSpawn = !isAppContainer() && !daemonMutexPresent();")
	if data, err := fs.ReadFile(fsys, "cmd/chaosetd/main.go"); err == nil {
		text := string(data)
		early := strings.Index(text, "var winLock *lockHandle")
		load := strings.Index(text, "engine.LoadDictFilesLenient(dicts)")
		if early < 0 || load < 0 || early > load {
			*problems = append(*problems, fmt.Sprintf(
				"%s：cmd/chaosetd/main.go 的 Windows 单实例锁必须先于词库加载（early=%d load=%d）",
				group, early, load))
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：读取 cmd/chaosetd/main.go 失败：%v", group, err))
	}
	// 后台预激活实例不建 worker/窗口：Activate 体里不得直接 shellWorkerStart。
	if body, line, ok := cppFunctionBody(src, "HRESULT STDMETHODCALLTYPE ChaosetTIP::Activate(ITfThreadMgr* ptim, TfClientId tid)"); ok {
		code := stripCppComments(body)
		if strings.Contains(code, "shellWorkerStart()") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 Activate（约第 %d 行）仍无条件创建 worker 线程（延迟到 updateInputActive 激活时）",
				group, winShell, line))
		}
	}
}

// checkDllUnloadSafety 锁住 2026-11-07 排查到的宿主进程崩溃根因：
// 本 DLL 在宿主进程里注册了 VEH（崩溃取证）与若干窗口类；TSF 动态卸载 DLL 时
// 若不清掉 VEH，宿主下一次任意异常会跳进已卸载内存（WER: ChaosetIME.dll_unloaded）。
// 因此 DllMain(DLL_PROCESS_DETACH) 必须先 removeCrashHandler() 再卸载。
func checkDllUnloadSafety(fsys fs.FS, problems *[]string) {
	const group = groupDllUnload
	checkContains(fsys, problems, group, "shims/windows/src/diagnostics.cpp",
		"RemoveVectoredExceptionHandler", "void removeCrashHandler()", "g_vehHandle")
	// VEH 只允许在 UI 宿主进程安装（我们拥有其生命周期，不会 FreeLibrary）。
	if data, err := fs.ReadFile(fsys, "shims/windows/src/ui_host.cpp"); err == nil {
		if !strings.Contains(string(data), "ensureCrashHandler()") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：UI 宿主未安装 VEH（crash.log 诊断会丢失；TSF 宿主禁止安装）", group))
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：读取 ui_host.cpp 失败：%v", group, err))
	}
	checkContains(fsys, problems, group, "shims/windows/src/diagnostics.hpp",
		"void removeCrashHandler();")
	// TSF 宿主进程不得安装 VEH（只有 UI 宿主可以）：宿主里注册的 VEH 在 DLL
	// 卸载后会悬空，也会给宿主每个 first-chance 异常增加回调开销。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		src := string(data)
		if body, line, ok := cppFunctionBody(src, "HRESULT STDMETHODCALLTYPE ChaosetTIP::Activate(ITfThreadMgr* ptim, TfClientId tid)"); ok {
			if strings.Contains(stripCppComments(body), "ensureCrashHandler()") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 Activate（约第 %d 行）仍安装 VEH——TSF 宿主进程不得注册全局异常回调（会悬空/增负担）",
					group, winShell, line))
			}
		}
		if body, line, ok := cppFunctionBody(src, "void runUiHost(LPWSTR cmdLine)"); ok {
			_ = body
			_ = line
		}
		// 析构必须幂等摘除线程注册表：宿主不调 Deactivate 直接 Release 时，
		// g_tlFgTips 会留下悬垂指针（500ms 定时器/前台消息解引用 → 宿主崩溃），
		// message-only 窗口也会因计数不归零让 DllCanUnloadNow 恒 S_FALSE。
		// 该断言锁住 2026-11-08 生命周期修复，禁止回退。
		if body, line, ok := cppFunctionBody(src, "~ChaosetTIP()"); ok {
			if !strings.Contains(stripCppComments(body), "unregisterTipFromThread(this)") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 ~ChaosetTIP（约第 %d 行）未调用 unregisterTipFromThread（宿主未调 Deactivate 直接 Release 会留悬垂指针/窗口不注销）",
					group, winShell, line))
			}
		} else {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 ~ChaosetTIP 析构定义", group, winShell))
		}
		if body, line, ok := cppFunctionBody(src, "BOOL WINAPI DllMain(HINSTANCE hinstDLL, DWORD fdwReason, LPVOID reserved)"); ok {
			code := stripCppComments(body)
			if !strings.Contains(code, "removeCrashHandler()") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 DllMain（约第 %d 行）动态卸载分支未撤 VEH（removeCrashHandler）",
					group, winShell, line))
			}
			if !strings.Contains(code, "UnregisterClassW(") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 DllMain（约第 %d 行）未注销窗口类（回调会指向已卸载内存）",
					group, winShell, line))
			}
		} else {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 DllMain", group, winShell))
		}
	} else {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
	}
	if data, err := fs.ReadFile(fsys, "shims/windows/src/diagnostics.cpp"); err == nil {
		_ = data
	}
}
