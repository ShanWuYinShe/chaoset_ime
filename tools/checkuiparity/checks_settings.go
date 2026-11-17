// checks_settings.go：三端设置窗契约（共享文案/单例/设置核心方案 A/Linux 装配）
// 与学习数据 GUI、engine 纯净约束。

package main

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// checkSettingsWindowContract 锁住三端设置窗契约（2026-11-06 定稿，2026-11-16
// 「暖陶纸感」升级为三端同构卡片）：
//  1. 骨架：客户区 640×560pt、左导航 148pt + 选中态、底部固定页脚版本号
//     （所有页，不再只放关于页）；关于页有「关于」分组标题、版本号不重复；
//  2. Windows 卡片契约：SettingsCardDef / kInputCards / kDictionaryCards /
//     kAboutCards / settingsCardColor；控件背景必须用卡片刷（settingsControlBg
//     + g_settingsCardBrush），防止「卡片上一条窗口底色横条」回归；
//  3. 卡片容器三端都有（mac SettingsCardView / Windows 自绘 / Linux .card CSS），
//     视觉令牌逐值一致由「设置窗视觉令牌」组断言；页脚/关于页标题/词库导入
//     统一说明必须与契约一致；
//  4. docs/ui-parity.md 必须记录骨架、视觉令牌表、几何与分组文案。
func checkSettingsWindowContract(fsys fs.FS, problems *[]string) {
	const group = groupSettingsContract
	// Windows 卡片契约（2026-11-06 修复的回归锁）。
	checkContains(fsys, problems, group, winShell,
		"struct SettingsCardDef", "kInputCards", "kDictionaryCards", "kAboutCards",
		"settingsCardColor", "g_settingsCardBrush", "settingsControlBg",
	)
	// 尺寸：三端客户区 640×560pt；Windows 页脚 34pt。
	checkContains(fsys, problems, group, winShell,
		"constexpr int kWinW = 640;", "constexpr int kWinH = 560;",
		"constexpr int kFooterH = 34;")
	checkContains(fsys, problems, group, macSettings,
		"width: 640, height: 560", "footerLabel", "PageSwitchTarget", "styleNavButton")
	// 2026-11-16：Linux 左导航由 GtkStackSidebar 换成与 macOS/Windows 同形态的
	// 自绘胶囊导航（互斥切换 stack 页）；三端分组标题/页脚/版本串保持不变。
	checkContains(fsys, problems, group, linuxSetup,
		"gtk_window_set_default_size(GTK_WINDOW(window), 640, 560)",
		"Chaoset 版本 %s", "gtk_stack_set_visible_child")
	// 页脚版本号固定在最底：Windows 自绘、macOS footerLabel、Linux vbox 页脚。
	checkContains(fsys, problems, group, linuxSetup,
		"gtk_box_pack_start(GTK_BOX(vbox), footer, FALSE, FALSE, 0);")
	// 关于页分组标题（三端）：Windows 卡片标题 / macOS label / Linux markup。
	checkContains(fsys, problems, group, winShell, `L"关于"`)
	checkContains(fsys, problems, group, macSettings, `title: "关于"`)
	checkContains(fsys, problems, group, linuxSetup, "<b>关于</b>")
	// 词库导入统一说明由 sharedSettingsText 在三端逐字断言（2026-11-06 定稿：
	// Windows 也已改为「导入后自动重启输入法」）；这里只锁文档同步。
	// 文档必须记录骨架/卡片口径/分组文案（实现改了文档不跟会 FAIL）。
	checkContains(fsys, problems, group, uiParityDoc,
		"## 设置窗契约", "640×560pt", "卡片容器三端都有",
		"支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。")
}

// checkSettingsCore 锁住方案 A 的 Go 单一真源与 CLI 契约（2026-11-06）：
// internal/settings 是设置 schema/默认值/持久化的唯一真源；三端设置窗冷路径
// 通过 chaoset-settingsctl 读写。文件缺失、存储键名被改、CLI 子命令被删都会 FAIL。
func checkSettingsCore(fsys fs.FS, problems *[]string) {
	const group = groupSettingsCore
	for _, path := range []string{
		"internal/settings/settings.go",
		"internal/settings/store_windows.go",
		"internal/settings/store_darwin.go",
		"internal/settings/store_linux.go",
		"internal/settings/settings_test.go",
		"internal/settings/store_linux_test.go",
		"cmd/chaoset-settingsctl/main.go",
		"cmd/chaoset-settingsctl/main_test.go",
	} {
		checkExists(fsys, problems, group, path)
	}
	// Windows 注册表子键必须逐字等于 Software\Chaoset（raw string 里只有一个
	// 反斜杠；2026-11-06 曾因生成脚本吞反斜杠写成 SoftwareChaoset）。
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		"windowsRegistrySubKey", "Software\\Chaoset")
	// CLI 契约面：get/set + 四个键名 + JSON 字段名 + 两档作用域 + 三档主题。
	checkContains(fsys, problems, group, "cmd/chaoset-settingsctl/main.go",
		"get", "set", "associate", "mode-scope", "theme", "ui-english", "system|process", "system|light|dark")
	// 单一真源：两档枚举名与缺省 JSON 契约值固定为 system。
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		"mode_scope", "ui_english", "theme", "ModeScopeSystem", "ModeScopeProcess",
		"ThemeSystem", "ThemeLight", "ThemeDark",
		`{"associate":true,"mode_scope":"system","ui_english":false,"theme":"system"}`)
	// 旧三档值的读取归一化必须保留：window→process、未知→system
	// （老用户存储里仍有 window，删掉迁移映射会让 get/设置窗拿到非法值）。
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		`case ModeScopeProcess, "window":`,
		"parseStoredModeScope")
	// ui-parity 文档必须记录两档语义与旧值迁移规则（实现/文档同步）。
	checkContains(fsys, problems, group, uiParityDoc,
		"两档", "全局共享", "每进程独立",
		"window→process", "未知→system")
	// macOS 域/键名必须与 Swift 壳一致（写错域 = 设置窗读写落到另一个域）。
	checkContains(fsys, problems, group, "internal/settings/store_darwin.go",
		"ime.chaoset.inputmethod.ChaosetIME", "AssociateEnabled", "ChaosetModeScope", "ChaosetTheme")
	// 方案 A2 试点（Windows）：设置窗的设置项冷路径必须走 chaoset-settingsctl，
	// 且带 1s 硬超时与 native 回退；打包必须带 CLI。
	checkContains(fsys, problems, group, winShell,
		"chaoset-settingsctl.exe", "settingsRunCtl(", "settingsLoadShared(",
		"settingsWriteShared(", "kSettingsCtlTimeoutMs", "set associate ", "set mode-scope ", "set theme ")
	checkContains(fsys, problems, group, makefile, "chaoset-settingsctl")
	checkContains(fsys, problems, group, winInstaller,
		"chaoset-settingsctl.exe", "InstallRobustFile")
	// Linux 必须复用 appdirs 的 config 路径（与 C 设置窗/chaoset-userdict 同源）。
	checkContains(fsys, problems, group, "internal/settings/store_linux.go", "appdirs.LinuxConfigPath", "linuxKeyTheme")
	// 架构文档必须记录 CLI 契约与单一真源位置。
	checkContains(fsys, problems, group, archDoc, "chaoset-settingsctl", "internal/settings")
}

// checkLinuxSetupAssembly 锁住 2026-11-06 修复的两个 Linux 设置窗回归：
//  1. GtkStack 页面必须用 gtk_stack_add_titled 挂载（GtkStack 不是 GtkBox；
//     误用 gtk_box_pack_start 会让三个内容页从未挂上、设置窗内容区为空）；
//  2. 中英状态二选一必须共用同一个首 radio 建组（from_widget），不能把
//     GSList 等错误类型传给 GTK_RADIO_BUTTON（否则两个 radio 各自成组、可同时选中）。
func checkLinuxSetupAssembly(fsys fs.FS, problems *[]string) {
	const group = groupLinuxSetupAssembly
	text, err := readText(fsys, linuxSetup)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, linuxSetup, err))
		return
	}
	// 正向/负向扫描都必须去注释：修复说明注释里同时会提到新旧两种写法，
	// 用注释内容判断会两边都「命中」。
	code := stripCppComments(text)
	for _, want := range []string{
		"gtk_stack_add_titled(GTK_STACK(stack)",
		"\"input\"", "\"dict\"", "\"about\"",
		// 2026-11-16：左导航改自绘胶囊（三个 toggle 互斥切 stack 页），
		// 中英状态改两个 choice 胶囊；设置写入仍走 setup_config_save_mode_scope。
		"gtk_stack_set_visible_child",
		"setup_config_save_mode_scope",
	} {
		if !strings.Contains(code, want) {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 装配代码缺少 %q（设置窗装配回归，见 docs/architecture.md）", group, linuxSetup, want))
		}
	}
	for _, bad := range []string{
		"gtk_box_pack_start(GTK_BOX(stack)",
	} {
		if strings.Contains(code, bad) {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 仍包含错误装配 %q（GtkStack 不是 GtkBox，见 docs/architecture.md）",
				group, linuxSetup, bad))
		}
	}
}

// checkSettingsSharedText 三端设置窗共享文案必须逐字一致（改一端即 FAIL）。
func checkSettingsSharedText(fsys fs.FS, problems *[]string) {
	for _, path := range []string{macSettings, winShell, linuxSetup} {
		checkContains(fsys, problems, groupSettingsSharedText, path, sharedSettingsText...)
	}
	// 两档作用域的按钮文案 → 存储值映射不得接反：macOS/Linux 设置窗都必须是
	// 「全局共享」→system、「每进程独立」→process。
	checkContains(fsys, problems, groupSettingsSharedText, macSettings,
		`case .system: title = "全局共享"`,
		`case .process: title = "每进程独立"`)
	checkContains(fsys, problems, groupSettingsSharedText, linuxSetup,
		`{"全局共享", "system"}`,
		`{"每进程独立", "process"}`)
	// Windows 设置窗按钮标题必须真的是两档文案（注释里出现不算）。
	checkContains(fsys, problems, groupSettingsSharedText, winShell,
		`L"全局共享"`, `L"每进程独立"`)
	// 外观主题三档文案三端一致。
	for _, path := range []string{macSettings, winShell, linuxSetup} {
		checkContains(fsys, problems, groupSettingsSharedText, path,
			"跟随系统", "浅色", "深色")
	}
	// Windows 121/122 → System/Process 映射不得接反（默认 System；旧的 123 档已删）。
	checkContains(fsys, problems, groupSettingsSharedText, winShell,
		"(LOWORD(wParam) == 121 || LOWORD(wParam) == 122)",
		"ModeScope scope = ModeScope::System;",
		"if (LOWORD(wParam) == 122) scope = ModeScope::Process;")
}

// checkSettingsSingleton 三端设置窗单例实现必须存在（mac 静态 window / Windows g_settingsWnd / Linux Gtk.Application）。
func checkSettingsSingleton(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupSettingsSingleton, macSettings, "private static var window: NSWindow?")
	checkContains(fsys, problems, groupSettingsSingleton, winShell, "g_settingsWnd")
	checkContains(fsys, problems, groupSettingsSingleton, linuxSetup,
		"gtk_application_new", `org.chaoset.Setup`)
}

// checkLearningDataGUI 学习数据管理三端都有 GUI；Linux 侧只调 chaoset-userdict 本地文件模式。
func checkLearningDataGUI(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupLearningGUI, macEngine,
		"func userStats", "func userClear", "func userExportAll")
	checkContains(fsys, problems, groupLearningGUI, macSettings,
		"userStats", "userClear", "userExportAll")
	checkContains(fsys, problems, groupLearningGUI, winShell,
		"userStats", "userClear", "userExportAll")
	checkContains(fsys, problems, groupLearningGUI, linuxSetup,
		"setup_userdict_stats", "setup_userdict_clear", "setup_userdict_export",
		"g_idle_add")
	checkContains(fsys, problems, groupLearningGUI, linuxCtl,
		"chaoset-userdict", "-userdict", "setup_userdict_clear", "ibus")
}

// checkEnginePurity engine 包纯净约束：Linux 设置窗只能走 chaoset-userdict 本地文件模式，
// 不允许重新引入 DBus / ibus 引擎管理接口（2026-10-31 用户明确要求）。
func checkEnginePurity(fsys fs.FS, problems *[]string) {
	for _, path := range []string{linuxSetup, linuxCtl, linuxDictImport} {
		checkExcludes(fsys, problems, groupEnginePurity, path,
			"dbus-send", "org.freedesktop.IBus", "IBusEngine")
	}
}

// settingsTokenNames 是三端设置窗视觉令牌表的必备令牌名（只防「删表行」；
// 具体色值不在 Go 里写死，唯一事实源是 docs/ui-parity.md 的表格）。
var settingsTokenNames = []string{
	"windowBg", "navBg", "cardBg", "cardBorder", "divider",
	"textPrimary", "textSecondary", "textTertiary",
	"accent", "accentStrong", "accentPressed", "accentSoft", "accentText",
	"controlBg", "controlHover", "controlBorder", "controlText",
	"knob", "danger", "onAccent", "shadow",
}

// 令牌表行：| windowBg | #F5EFE6 | #1C1916 | 用途 |
var settingsTokenRowRe = regexp.MustCompile(
	`(?m)^\s*\|\s*([A-Za-z][A-Za-z0-9]*)\s*\|\s*#([0-9A-Fa-f]{6})\s*\|\s*#([0-9A-Fa-f]{6})\s*\|`)

func parseSettingsTokenTable(doc string) map[string][2]string {
	out := make(map[string][2]string)
	for _, m := range settingsTokenRowRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = [2]string{strings.ToUpper(m[2]), strings.ToUpper(m[3])}
	}
	return out
}

// settingsRGBLiteral 把 RRGGBB 转成 Windows 源码里的 RGB(0xRR, 0xGG, 0xBB) 写法。
func settingsRGBLiteral(hex string) string {
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return fmt.Sprintf("RGB(0x%02X, 0x%02X, 0x%02X)", r, g, b)
}

// checkSettingsVisualTokens 锁住 2026-11-16「暖陶纸感」三端设置窗视觉系统。
//
// docs/ui-parity.md「设置窗视觉令牌」表是**唯一事实源**：这里解析表格，逐值断言
// macOS/Windows/Linux 三端实现包含同一色值（书写形式不同：macOS 0xRRGGBB、
// Windows RGB(0xRR, 0xGG, 0xBB)、Linux CSS #RRGGBB），并锁几何令牌、三端卡片/
// 控件形态与禁止色。只改一端、或改了实现不改文档令牌表都会 FAIL。
//
// shadow 令牌只对 macOS 严格比对：阴影的 alpha/blur 按平台绘制能力落地
// （GDI 无高斯模糊、GTK 用 box-shadow），逐值比对只会逼出假实现。
func checkSettingsVisualTokens(fsys fs.FS, problems *[]string) {
	const group = groupSettingsVisual
	const macTheme = "shims/macos/src/SettingsTheme.swift"
	const winSettings = "shims/windows/src/settings_window.cpp"

	doc, err := readText(fsys, uiParityDoc)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, uiParityDoc, err))
		return
	}
	tokens := parseSettingsTokenTable(doc)

	macText, macErr := readText(fsys, macTheme)
	winText, winErr := readText(fsys, winSettings)
	linuxText, linuxErr := readText(fsys, linuxSetup)
	for _, one := range []struct {
		path string
		err  error
	}{{macTheme, macErr}, {winSettings, winErr}, {linuxSetup, linuxErr}} {
		if one.err != nil {
			*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, one.path, one.err))
		}
	}
	if macErr != nil || winErr != nil || linuxErr != nil {
		return
	}

	for _, name := range settingsTokenNames {
		vals, ok := tokens[name]
		if !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 令牌表缺少「%s」行（视觉令牌是单一事实源，删行即失去机器断言）",
				group, uiParityDoc, name))
			continue
		}
		for _, v := range vals {
			// macOS：0xF5EFE6（大写）
			if !strings.Contains(macText, "0x"+v) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 缺少令牌 %s 色值 0x%s（macOS 解码自 %s）", group, macTheme, name, v, uiParityDoc))
			}
			if name == "shadow" {
				continue
			}
			// Windows：RGB(0xF5, 0xEF, 0xE6)
			if !strings.Contains(winText, settingsRGBLiteral(v)) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 缺少令牌 %s 色值 %s（Windows 解码自 %s）",
					group, winSettings, name, settingsRGBLiteral(v), uiParityDoc))
			}
			// Linux：CSS #F5EFE6（大小写不敏感）
			if !strings.Contains(strings.ToUpper(linuxText), "#"+v) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s（Linux 设置窗聚合源）缺少令牌 %s 色值 #%s（解码自 %s）",
					group, linuxSetup, name, v, uiParityDoc))
			}
		}
	}

	// 几何令牌（pt）：三端同一节奏，改尺寸要三端一起改。
	checkContains(fsys, problems, group, macTheme,
		"cardRadius: CGFloat = 14", "cardPad: CGFloat = 18", "cardGap: CGFloat = 16",
		"rowHeight: CGFloat = 36", "navWidth: CGFloat = 148",
		"switchWidth: CGFloat = 38", "switchHeight: CGFloat = 22",
		"buttonHeight: CGFloat = 32")
	checkContains(fsys, problems, group, winSettings,
		"kCardRadius = 14", "kCardPad = 18", "kCardGap = 16",
		"kRowHeight = 36", "kNavWidth = 148")
	checkContains(fsys, problems, group, linuxSetup,
		"border-radius: 14px", "padding: 18px", ", 148, -1)")

	// 三端形态同构：卡片 + 暖陶开关 + 软胶囊（导航/二选一）+ 主/次按钮。
	checkContains(fsys, problems, group, macTheme,
		"SettingsCardView", "WarmSwitch", "WarmPillButton", "WarmButton")
	checkContains(fsys, problems, group, winSettings,
		"SettingsCardDef", "settingsDrawSwitch", "settingsDrawChoice", "settingsDrawButton")
	checkContains(fsys, problems, group, linuxSetup,
		".card", "switch:checked", ".choice", ".primary", ".secondary", ".nav")

	// 禁止色：系统蓝/冷灰/旧橙是「突兀」的主要来源（C 源码先剥注释再查，
	// 允许注释里记录历史色值，但代码里出现即 FAIL）。
	forbidden := []string{"0A84FF", "F5EFE6", "FFFCF7", "C9703F", "A85427", "E8DCCC", "F6E6D9", "D16B29", "994014"}
	checkForbidden := func(path, text string) {
		clean := strings.ToUpper(stripCppComments(text))
		for _, bad := range forbidden {
			if strings.Contains(clean, bad) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 仍包含禁止色 %s（系统蓝/冷灰/旧橙与暖陶纸感冲突，改用设置窗视觉令牌）",
					group, path, bad))
			}
		}
	}
	checkForbidden(macTheme, macText)
	checkForbidden(winSettings, winText)
	checkForbidden(linuxSetup, linuxText)
}
