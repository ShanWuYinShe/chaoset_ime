// checkuiparity 校验三端（macOS/Windows/Linux）UI 一致性契约（2026-10-31）。
//
// 为什么需要它：候选窗/设置窗是三套独立实现（AppKit / Win32 GDI / GTK3），
// 共享的只有「应该一致」的文字与功能契约。此前靠人肉检查：改一端忘另一端
// （2026-10-28 齿轮五轮迭代漏同步 Windows 的教训）或文案漂移只能等真机发现。
// 本工具把可静态验证的契约固定下来，在 make vet / CI lint 里跑：
//
//  1. 设置窗共享文案三端逐字一致（标题/分组/联想/学习数据/底部提示）；
//  2. 设置窗单例三端都有实现（mac 静态 window、Windows g_settingsWnd、
//     Linux Gtk.Application）；
//  3. 学习数据管理三端都有 GUI 实现；Linux 侧只调 chaoset-userdict 本地文件
//     模式，**不得**引入引擎 DBus 管理接口（engine 包纯净约束）；
//  4. 词库导入：mac Setup.app 与 Windows 设置窗都有 GUI 入口；Linux 仍为 CLI，
//     ui-parity 文档必须明确记录（不允许悄悄当作已支持）；
//  5. 打包/安装一致性：userdict_ctl.py 必须被 Makefile/install.sh/Arch 打包/
//     release 冒烟引用；hicolor 必须有小尺寸图标；setup 脚本必须被组件 XML
//     与 release 冒烟引用；
//  6. ui-parity 文档保留关键差异行（学习数据管理/词库导入/设置窗单例/
//     Windows per-monitor DPI/双拼 mspy 等）。
//
// 用法：go run ./tools/checkuiparity <仓库根>
// 退出码：0 = 通过，1 = 发现违规，2 = 用法错误。
package main

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// 三端设置窗共享文案：必须逐字一致（改文案要三端一起改，本检查会失败）。
var sharedSettingsText = []string{
	"Chaoset 设置",
	"输入行为",
	"词组联想",
	"学习数据",
	"清空学习数据",
	"导出学习数据",
	"学习数据仅保存在本机",
	"更多设置将在后续版本提供",
}

const (
	macSettings = "shims/macos/src/SettingsWindow.swift"
	macEngine   = "shims/macos/src/EngineClient.swift"
	macSetup    = "shims/macos/setup/main.swift"
	winShell    = "shims/windows/src/win32.cpp"
	linuxSetup  = "shims/linux/setup/chaoset-setup.py"
	linuxCtl    = "shims/linux/setup/userdict_ctl.py"
	linuxTest   = "shims/linux/setup/test_userdict_ctl.py"
	linuxComp   = "shims/linux/component/chaoset.xml"
	makefile    = "Makefile"
	installSh   = "shims/linux/install.sh"
	archScript  = "shims/linux/arch/package.sh"
	releaseYML  = ".github/workflows/release.yml"
	ciYML       = ".github/workflows/ci.yml"
	uiParityDoc = "docs/ui-parity.md"
)

// checkContains 断言文件包含全部子串；每个缺失项记一条问题。
func checkContains(fsys fs.FS, problems *[]string, group, path string, needles ...string) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, err))
		return
	}
	text := string(data)
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 %q", group, path, needle))
		}
	}
}

// checkExcludes 断言文件**不包含**子串（用于固定「不允许回退」的架构约束）。
func checkExcludes(fsys fs.FS, problems *[]string, group, path string, needles ...string) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, err))
		return
	}
	text := string(data)
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 不应包含 %q（架构/一致性约束）", group, path, needle))
		}
	}
}

// checkExists 断言路径存在（文件或目录）。
func checkExists(fsys fs.FS, problems *[]string, group, path string) {
	if _, err := fs.Stat(fsys, path); err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：缺少 %s", group, path))
	}
}

// Run 对给定文件树执行全部检查，返回问题列表（空 = 通过）。
func Run(fsys fs.FS) []string {
	var problems []string

	// 1. 设置窗共享文案三端一致。
	for _, path := range []string{macSettings, winShell, linuxSetup} {
		checkContains(fsys, &problems, "设置窗共享文案", path, sharedSettingsText...)
	}

	// 2. 设置窗单例三端实现。
	checkContains(fsys, &problems, "设置窗单例", macSettings, "private static var window: NSWindow?")
	checkContains(fsys, &problems, "设置窗单例", winShell, "g_settingsWnd")
	checkContains(fsys, &problems, "设置窗单例", linuxSetup,
		"Gtk.Application", `application_id="org.chaoset.Setup"`)

	// 3. 学习数据 GUI 三端存在；Linux 侧不得引入引擎 DBus 管理接口。
	checkContains(fsys, &problems, "学习数据 GUI", macEngine,
		"func userStats", "func userClear", "func userExportAll")
	checkContains(fsys, &problems, "学习数据 GUI", macSettings,
		"userStats", "userClear", "userExportAll")
	checkContains(fsys, &problems, "学习数据 GUI", winShell,
		"userStats", "userClear", "userExportAll")
	checkContains(fsys, &problems, "学习数据 GUI", linuxSetup,
		"userdict_ctl.stats", "userdict_ctl.clear_learning_data", "userdict_ctl.export")
	checkContains(fsys, &problems, "学习数据 GUI", linuxCtl,
		"chaoset-userdict", "-userdict", "clear_learning_data", "ibus")
	// engine 包纯净约束：Linux 设置窗只能走 chaoset-userdict 本地文件模式，
	// 不允许重新引入 DBus / ibus 引擎管理接口（2026-10-31 用户明确要求）。
	for _, path := range []string{linuxSetup, linuxCtl} {
		checkExcludes(fsys, &problems, "engine 纯净约束", path,
			"dbus-send", "org.freedesktop.IBus", "IBusEngine")
	}

	// 4. 词库导入入口：mac Setup.app + Windows 设置窗有 GUI；Linux 仅 CLI 且
	//    ui-parity 必须明确记录（避免文档悄悄变成「已支持」）。
	checkContains(fsys, &problems, "词库导入入口", macSetup, "导入词库", "chaoset-import")
	checkContains(fsys, &problems, "词库导入入口", winShell, "导入词库", "chaoset-import.exe")
	checkContains(fsys, &problems, "词库导入入口", uiParityDoc, "Linux 目前仅 CLI")
	checkExcludes(fsys, &problems, "词库导入入口", linuxSetup, "导入词库")

	// 5. 打包/安装一致性：Linux 设置窗脚本、学习数据模块、小图标都要随包。
	checkContains(fsys, &problems, "打包安装", makefile, "userdict_ctl.py")
	checkContains(fsys, &problems, "打包安装", installSh, "userdict_ctl.py")
	checkContains(fsys, &problems, "打包安装", archScript, "userdict_ctl.py")
	checkContains(fsys, &problems, "打包安装", releaseYML,
		"userdict_ctl.py", "chaoset-setup.py")
	checkContains(fsys, &problems, "打包安装", linuxComp, "chaoset-setup.py")
	checkContains(fsys, &problems, "打包安装", ciYML, "test_userdict_ctl.py", "py_compile")
	checkExists(fsys, &problems, "打包安装", linuxTest)
	checkExists(fsys, &problems, "打包安装", linuxCtl)
	for _, size := range []string{"16x16", "22x22", "24x24", "32x32", "48x48", "64x64", "128x128", "256x256"} {
		checkExists(fsys, &problems, "打包安装",
			fmt.Sprintf("shims/linux/icons/hicolor/%s/apps/chaoset.png", size))
	}
	checkExists(fsys, &problems, "打包安装", "shims/linux/icons/hicolor/scalable/apps/chaoset.svg")

	// 6. ui-parity 关键差异行必须保留（防止文档被删后失去对照基准）。
	checkContains(fsys, &problems, "文档对照", uiParityDoc,
		"学习数据管理", "词库导入", "设置窗单例", "Windows per-monitor DPI",
		"双拼 mspy", "无障碍")

	return problems
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./tools/checkuiparity <仓库根>")
		os.Exit(2)
	}
	problems := Run(os.DirFS(os.Args[1]))
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "FAIL:", p)
		}
		fmt.Fprintf(os.Stderr, "UI 一致性契约检查失败（%d 项）\n", len(problems))
		os.Exit(1)
	}
	fmt.Println("UI 一致性契约检查通过（设置窗文案/单例/学习数据 GUI/词库导入/打包安装/文档对照）")
}
