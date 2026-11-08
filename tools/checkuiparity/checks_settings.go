// checks_settings.go：三端设置窗契约（共享文案/单例/设置核心方案 A/Linux 装配）
// 与学习数据 GUI、engine 纯净约束。

package main

import (
	"fmt"
	"io/fs"
	"strings"
)

// checkSettingsWindowContract 锁住 2026-11-06 阶段2 定稿的三端设置窗契约：
//  1. 骨架：客户区 640×560pt、左导航 132pt + 选中态、底部固定页脚版本号
//     （所有页，不再只放关于页）；关于页有「关于」分组标题、版本号不重复；
//  2. Windows 卡片契约：SettingsCardDef / kInputCards / kDictionaryCards /
//     kAboutCards / settingsCardColor；控件背景必须用卡片刷（settingsControlBg
//     + g_settingsCardBrush），防止「卡片上一条窗口底色横条」回归；
//  3. macOS/Linux 不做像素级卡片容器（用原生分组），但页脚/关于页标题/词库
//     导入统一说明必须与契约一致；
//  4. docs/ui-parity.md 必须记录骨架、卡片口径与分组文案。
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
	checkContains(fsys, problems, group, linuxSetup,
		"gtk_window_set_default_size(GTK_WINDOW(window), 640, 560)",
		"Chaoset 版本 %s", "GtkStackSidebar")
	// 页脚版本号固定在最底：Windows 自绘、macOS footerLabel、Linux vbox 页脚。
	checkContains(fsys, problems, group, linuxSetup,
		"gtk_box_pack_start(GTK_BOX(vbox), footer, FALSE, FALSE, 0);")
	// 关于页分组标题（三端）：Windows 卡片标题 / macOS label / Linux markup。
	checkContains(fsys, problems, group, winShell, `L"关于"`)
	checkContains(fsys, problems, group, macSettings, `labelWithString: "关于"`)
	checkContains(fsys, problems, group, linuxSetup, "<b>关于</b>")
	// 词库导入统一说明由 sharedSettingsText 在三端逐字断言（2026-11-06 定稿：
	// Windows 也已改为「导入后自动重启输入法」）；这里只锁文档同步。
	// 文档必须记录骨架/卡片口径/分组文案（实现改了文档不跟会 FAIL）。
	checkContains(fsys, problems, group, uiParityDoc,
		"## 设置窗契约", "640×560pt", "卡片容器是 Windows 的视觉实现",
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
	// CLI 契约面：get/set + 三个键名 + JSON 字段名。
	checkContains(fsys, problems, group, "cmd/chaoset-settingsctl/main.go",
		"get", "set", "associate", "mode-scope", "ui-english")
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		"mode_scope", "ui_english", "ModeScopeSystem", "ModeScopeProcess", "ModeScopeWindow")
	// macOS 域/键名必须与 Swift 壳一致（写错域 = 设置窗读写落到另一个域）。
	checkContains(fsys, problems, group, "internal/settings/store_darwin.go",
		"ime.chaoset.inputmethod.ChaosetIME", "AssociateEnabled", "ChaosetModeScope")
	// 方案 A2 试点（Windows）：设置窗的设置项冷路径必须走 chaoset-settingsctl，
	// 且带 1s 硬超时与 native 回退；打包必须带 CLI。
	checkContains(fsys, problems, group, winShell,
		"chaoset-settingsctl.exe", "settingsRunCtl(", "settingsLoadShared(",
		"settingsWriteShared(", "kSettingsCtlTimeoutMs", "set associate ", "set mode-scope ")
	checkContains(fsys, problems, group, makefile, "chaoset-settingsctl")
	checkContains(fsys, problems, group, winInstaller,
		"chaoset-settingsctl.exe", "InstallRobustFile")
	// Linux 必须复用 appdirs 的 config 路径（与 C 设置窗/chaoset-userdict 同源）。
	checkContains(fsys, problems, group, "internal/settings/store_linux.go", "appdirs.LinuxConfigPath")
	// 架构文档必须记录 CLI 契约与单一真源位置。
	checkContains(fsys, problems, group, archDoc, "chaoset-settingsctl", "internal/settings")
}

// checkLinuxSetupAssembly 锁住 2026-11-06 修复的两个 Linux 设置窗回归：
//  1. GtkStack 页面必须用 gtk_stack_add_titled 挂载（GtkStack 不是 GtkBox；
//     误用 gtk_box_pack_start 会让三个内容页从未挂上、设置窗内容区为空）；
//  2. 中英状态三选一必须共用同一个首 radio 建组（from_widget），不能把
//     GSList 等错误类型传给 GTK_RADIO_BUTTON（否则三个 radio 各自成组、可同时选中）。
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
		"gtk_radio_button_new_with_label_from_widget(",
		"mode_group_first",
	} {
		if !strings.Contains(code, want) {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 装配代码缺少 %q（2026-11-06 Linux 设置窗回归修复）", group, linuxSetup, want))
		}
	}
	for _, bad := range []string{
		"gtk_box_pack_start(GTK_BOX(stack)",
		"GTK_RADIO_BUTTON(mode_group_radio)",
	} {
		if strings.Contains(code, bad) {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 仍包含错误装配 %q（2026-11-06 Linux 设置窗回归，见 docs/architecture.md）",
				group, linuxSetup, bad))
		}
	}
}

// checkSettingsSharedText 三端设置窗共享文案必须逐字一致（改一端即 FAIL）。
func checkSettingsSharedText(fsys fs.FS, problems *[]string) {
	for _, path := range []string{macSettings, winShell, linuxSetup} {
		checkContains(fsys, problems, groupSettingsSharedText, path, sharedSettingsText...)
	}
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
