// checks_settings.go：设置窗契约（唯一实现 = Slint 统一设置中心）、三端启动入口、
// 设置核心方案 A（Go internal/settings + chaoset-settingsctl）、学习数据/词库导入的
// CLI 契约，以及「Go 引擎已移除」纯净约束。
//
// 架构基线：三端壳不再实现原生设置窗，只保留启动器把 chaoset-settings 拉起来
// （macOS SettingsLauncher.swift / Windows settings_launcher.cpp / Linux POSIX
// 启动器 chaoset-setup）。视觉/文案/交互的唯一实现是 shims/settings（Slint）。

package main

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// checkSettingsCenterContract 锁定设置 UI 的唯一实现与三端入口：
//  1. Slint 统一设置中心存在且具备 640×560 骨架、三页、页脚版本号、关于页；
//  2. 三端只允许「启动 chaoset-settings」的启动器，禁止回退旧原生设置窗；
//  3. 缺设置中心时启动器必须明确报错（不得静默/降级成残缺设置）；
//  4. 三端发行包与构建链必须携带设置中心（含 macOS 依赖的 chaosetctl）。
func checkSettingsCenterContract(fsys fs.FS, problems *[]string) {
	const group = groupSettingsContract
	// Slint UI 契约（视觉/文案唯一实现）。
	checkContains(fsys, problems, group, slintApp,
		"preferred-width: 640px;", "preferred-height: 560px;", "width: 148px;",
		`title: "关于"`, "root.version_text",
		`title: "输入行为"`, `title: "中英状态"`, "学习数据", "词库扩展",
	)
	// macOS：Shell 只拉起 app 内 chaoset-settings；设置项读写经同目录 chaosetctl。
	checkContains(fsys, problems, group, macLauncher,
		"chaoset-settings", "Contents/Resources", "未找到统一设置中心")
	checkContains(fsys, problems, group, macBuild, "chaoset-settings", "chaosetctl")
	// Windows：UI 宿主经 settings_launcher 起进程；TIP 只发命令（不在 TIP 内建进程）。
	checkContains(fsys, problems, group, winLauncher,
		`kSettingsExeName[] = L"chaoset-settings.exe"`, "CreateProcessW")
	checkContains(fsys, problems, group, winLauncherHpp, "LaunchSettingsCenter")
	checkContains(fsys, problems, group, winUiHost, "LaunchSettingsCenter")
	checkContains(fsys, problems, group, winInstaller, "chaoset-settings.exe")
	// Linux：chaoset-setup 是纯 POSIX 启动器（ibus setup 入口不变）。
	checkContains(fsys, problems, group, linuxSetup,
		"chaoset-settings", "未找到统一设置中心", "execv")
	checkContains(fsys, problems, group, linuxComp, "chaoset-setup")
	checkContains(fsys, problems, group, installSh, "chaoset-settings")
	checkContains(fsys, problems, group, linuxArchPkg, "chaoset-settings")
	// 旧原生设置窗不得回归（文件必须不存在）：出现即说明又走回双实现。
	for _, gone := range []string{
		"shims/macos/src/SettingsWindow.swift",
		"shims/macos/src/SettingsWindowPages.swift",
		"shims/macos/src/SettingsWindowTargets.swift",
		"shims/macos/src/SettingsTheme.swift",
		"shims/windows/src/settings_window.cpp",
		"shims/windows/src/settings_window.hpp",
		"shims/linux/setup/setup_page_input.c",
		"shims/linux/setup/setup_page_dict.c",
		"shims/linux/setup/setup_page_about.c",
		"shims/linux/setup/setup_ctl.c",
		"shims/linux/setup/setup_learn.c",
		"shims/linux/setup/setup_import.c",
	} {
		checkAbsent(fsys, problems, group, gone)
	}
	// 构建/打包链必须携带设置中心（缺了用户点设置没有 UI）。
	checkContains(fsys, problems, group, makefile,
		"build-settings", "build-settings-universal", "build-settings-windows",
		"chaoset-settings")
	checkContains(fsys, problems, group, macInstall, "ChaosetIME.app")
	// 文档：设置窗契约必须以 Slint 统一设置中心为口径。
	checkContains(fsys, problems, group, uiParityDoc,
		"## 设置窗契约", "Slint", "640×560pt", "chaoset-settings")
}

// settingsCenterText 是 Slint 统一设置中心的必备文案（唯一实现，无需三端同步）。
var settingsCenterText = []string{
	"Chaoset 设置",
	"输入行为",
	"词组联想",
	"学习数据",
	"清空学习数据",
	"导出学习数据",
	"学习数据仅保存在本机",
	"设置项会随版本持续补充",
	"Chaoset 版本",
	"https://github.com/ShanWuYinShe/chaoset_ime",
	"中英状态",
	"全局共享",
	"每进程独立",
	"跟随系统",
	"浅色",
	"深色",
	"模糊音",
	"双拼方案",
	"全拼",
	"自然码",
	"微软",
	"小鹤",
	"跨平台中文输入法",
	"代码 MIT · 词库 CC BY-SA 4.0",
	"支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。",
}

// checkSettingsSharedText 设置文案只存在一份实现（Slint）：这里锁住关键文案与
// 仓库链接，防止设置中心改文案时把功能入口删掉。
func checkSettingsSharedText(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupSettingsSharedText, slintApp, settingsCenterText...)
}

// checkSettingsCore 锁住方案 A：设置项 schema/默认值/持久化唯一真源是 Go
// internal/settings，CLI 是 chaoset-settingsctl；Slint 设置中心冷路径经
// chaosetctl（multicall 包含 settingsctl 子命令）读写，输入热路径仍读本机存储。
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
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		"windowsRegistrySubKey", "Software\\Chaoset")
	checkContains(fsys, problems, group, "cmd/chaoset-settingsctl/main.go",
		"get", "set", "associate", "fuzzy", "shuangpin", "mode-scope", "theme", "ui-english",
		"system|process", "system|light|dark", "full|zrm|mspy|flypy")
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		"mode_scope", "ui_english", "theme", "fuzzy", "shuangpin",
		"ModeScopeSystem", "ModeScopeProcess",
		"ThemeSystem", "ThemeLight", "ThemeDark",
		"ShuangpinFull", "ShuangpinZrm", "ShuangpinMspy", "ShuangpinFlypy",
		`{"associate":true,"fuzzy":false,"shuangpin":"","mode_scope":"system","ui_english":false,"theme":"system"}`)
	checkContains(fsys, problems, group, "internal/settings/settings.go",
		`case ModeScopeProcess, "window":`, "parseStoredModeScope")
	checkContains(fsys, problems, group, uiParityDoc,
		"两档", "全局共享", "每进程独立", "window→process", "未知→system")
	checkContains(fsys, problems, group, "internal/settings/store_darwin.go",
		"ime.chaoset.inputmethod.ChaosetIME", "AssociateEnabled",
		"ChaosetFuzzy", "ChaosetShuangpin", "ChaosetModeScope", "ChaosetTheme")
	// Slint 设置中心的 CLI 契约：所有读写都经 chaosetctl（与设置中心同目录）。
	checkContains(fsys, problems, group, slintBridge,
		"find_chaoset_bin", "CHAOS_BIN",
		`"config"`, `"get"`, `"set"`,
		`"userdict"`, `"import"`, "restart_daemon")
	checkContains(fsys, problems, group, "internal/settings/store_linux.go",
		"appdirs.LinuxConfigPath", "linuxKeyTheme")
	// Windows 安装包必须同时带设置中心与它依赖的 chaosetctl。
	checkContains(fsys, problems, group, winInstaller,
		"chaoset-settings.exe", "chaosetctl.exe")
	checkContains(fsys, problems, group, archDoc, "chaoset-settingsctl", "internal/settings")

	// Rust daemon 与后续 Rust CLI 共用的设置/路径实现（Go CLI 迁移完成后
	// internal/settings 删除，这里的存在性断言会转成唯一真源断言）。
	for _, path := range []string{
		"crates/chaoset-config/src/lib.rs",
		"crates/chaoset-config/src/model.rs",
		"crates/chaoset-config/src/parse.rs",
		"crates/chaoset-config/src/store/mod.rs",
		"crates/chaoset-config/src/store/darwin.rs",
		"crates/chaoset-config/src/store/linux.rs",
		"crates/chaoset-config/src/store/windows.rs",
		"crates/chaoset-config/tests/constants_lock.rs",
		"crates/chaoset-config/tests/linux_store.rs",
	} {
		checkExists(fsys, problems, group, path)
	}
	checkContains(fsys, problems, group, "crates/chaoset-config/src/constants.rs",
		"WINDOWS_REG_SUBKEY", "MAC_DEFAULTS_DOMAIN", "CLI_ITEM_MODE_SCOPE", "DEFAULTS_JSON")
	checkContains(fsys, problems, group, "crates/chaosetd/src/main.rs", "chaoset_config::behavior::load()")
	checkContains(fsys, problems, group, "shims/settings/src/bridge.rs", "mode-scope")
	checkNotContains(fsys, problems, group, "shims/settings/src/bridge.rs", `"config", "set", "mode_scope"`)
}

// checkLearningDataGUI 学习数据管理只有 Slint 设置中心一份 GUI；实现走
// chaosetctl userdict（本地文件模式），壳内不再重复学习数据界面。
func checkLearningDataGUI(fsys fs.FS, problems *[]string) {
	const group = groupLearningGUI
	checkContains(fsys, problems, group, slintBridge,
		"get_userdict_stats", "clear_userdict", "export_userdict")
	checkContains(fsys, problems, group, slintEvents,
		"on_clear_userdict_clicked", "on_export_userdict_clicked")
	checkContains(fsys, problems, group, slintApp,
		"清空学习数据", "导出学习数据…")
	checkExists(fsys, problems, group, userdictCLI)
	checkContains(fsys, problems, group, "internal/tools/userdict/local.go",
		"func (u *localUserDict) Save() error", "func (u *localUserDict) Stats()")
}

// checkNoGoEngine 锁定「Go 引擎已删除、Rust 是唯一实现」：
//  1. engine/ 目录不得重新出现（回退成双实现）；
//  2. 任何 Go 源码不得 import "chaoset.ime/engine"（工具层不得再绑回引擎）。
func checkNoGoEngine(fsys fs.FS, problems *[]string) {
	const group = groupNoGoEngine
	if _, err := fs.Stat(fsys, "engine"); err == nil {
		*problems = append(*problems, group+"：engine/ 目录不应存在（Go 引擎已删除，Rust 是唯一实现）")
	}
	skip := map[string]bool{".git": true, ".agents": true, ".workwork": true,
		"node_modules": true, "target": true, "bin": true, "build": true, "dist": true}
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "\"chaoset.ime/engine\"" {
				*problems = append(*problems, group+"："+path+" 仍 import chaoset.ime/engine（Go 引擎已删除，工具应使用 internal/dict 等独立包）")
				break
			}
		}
		return nil
	})
}

// settingsTokenNames 是设置窗视觉令牌表的必备令牌名（只防「删表行」；
// 具体色值不写死，唯一事实源是 docs/ui-parity.md 的表格）。
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

// checkSettingsVisualTokens 锁住设置窗视觉令牌：docs/ui-parity.md 的表格是唯一
// 事实源，Slint 实现必须逐值包含；只改实现不改令牌表、或删表行都会 FAIL。
func checkSettingsVisualTokens(fsys fs.FS, problems *[]string) {
	const group = groupSettingsVisual
	doc, err := readText(fsys, uiParityDoc)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, uiParityDoc, err))
		return
	}
	tokens := parseSettingsTokenTable(doc)
	slintText, slintErr := readText(fsys, slintTokens)
	if slintErr != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, slintTokens, slintErr))
		return
	}
	upper := strings.ToUpper(slintText)
	for _, name := range settingsTokenNames {
		vals, ok := tokens[name]
		if !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 令牌表缺少「%s」行（视觉令牌是单一事实源，删行即失去机器断言）",
				group, uiParityDoc, name))
			continue
		}
		if name == "shadow" {
			continue
		}
		for _, v := range vals {
			if !strings.Contains(upper, "#"+v) {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s（Slint 设计令牌）缺少令牌 %s 色值 #%s（解码自 %s）",
					group, slintTokens, name, v, uiParityDoc))
			}
		}
	}
	// 几何令牌（pt/px）：改尺寸要同时改文档令牌表与实现。
	checkContains(fsys, problems, group, slintTokens,
		"card_radius: 14px", "card_pad: 18px", "card_gap: 16px",
		"row_height: 36px", "nav_width: 148px",
		"switch_width: 38px", "switch_height: 22px",
		"button_height: 32px")
	// 控件形态：卡片 + 开关 + 胶囊选择 + 主/次按钮（components.slint）。
	checkContains(fsys, problems, group, slintComponents,
		"Card", "ChaosSwitch", "ChoicePill", "ChaosButton")
	// 禁止色：系统蓝/冷灰/旧橙不得回到设置中心。
	forbidden := []string{"0A84FF", "F5EFE6", "FFFCF7", "C9703F", "A85427", "E8DCCC", "F6E6D9", "D16B29", "994014"}
	clean := strings.ToUpper(stripCppComments(slintText))
	for _, bad := range forbidden {
		if strings.Contains(clean, bad) {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 仍包含禁止色 %s（系统蓝/冷灰/旧橙与暖陶纸感冲突，改用设置窗视觉令牌）",
				group, slintTokens, bad))
		}
	}
}
