// checkuiparity 校验 chaoset_ime 的 UI/架构契约（在 make vet / CI lint 里跑）。
//
// 核心前提：设置 UI 只有一份实现——Slint 统一设置中心（shims/settings，
// 产物 chaoset-settings）。macOS / Windows / Linux 壳只保留「启动设置中心」的
// 启动器（SettingsLauncher.swift / settings_launcher.cpp / chaoset-setup POSIX
// 启动器），不再各自实现设置窗。候选窗仍为 mac AppKit / Windows GDI 双实现、
// Linux 用 ibus 面板。
//
// 检查范围：
//  1. 设置中心契约：Slint 骨架/页面/文案/视觉令牌 + 三端启动入口 + 打包必带；
//  2. 设置核心（Rust 单一真源）：crates/chaoset-config 的 schema/默认值/三端持久化
//     与 crates/chaosetctl 的 config 子命令契约；
//  3. 学习数据/词库导入：只有设置中心 GUI，实现走 Rust crates/chaoset-tools /
//     crates/chaosetctl，导入安全（退出码/备份/原子替换）由 Rust 工具承担；
//  4. 发行面 Rust 化：构建/打包链只从 crates/chaosetctl 产出 chaosetctl，三分发
//     入口统一 chaosetctl import，已删除的 Go 工具路径不得回归；
//  5. 候选窗几何/配色字段与按键语义（mac/Windows 静态对齐，Linux 记录差异）；
//  6. 打包/安装/CI/文档一致性、模块布局与文件预算、Go 引擎移除锁；
//  7. Windows 零阻塞/动态卸载/UI 宿主等平台硬约束（见各 check 注释）。
//
// 检查组注册：组名唯一表在 groups.go，注册表在 run.go（checkGroups）。Run 只遍历
// 注册表执行，成功提示由注册表生成——新增检查组只需「加组名常量 + 加注册 + 实现
// 函数」，不会出现「跑了但没打印」的清单漂移。
//
// 用法：go run ./tools/checkuiparity <仓库根>
// 退出码：0 = 通过，1 = 发现违规，2 = 用法错误。
package main

import (
	"fmt"
	"os"
	"strings"
)

// daemon 命名管道服务端（Windows）与平台无关的监听池骨架；单独成块，避免
// 挤动下方常量块的对齐。
const (
	rustDaemonServer = "crates/chaosetd/src/server.rs"
	rustDaemonPipe   = "crates/chaosetd/src/server/pipe.rs"
)

const (
	macPanel        = "shims/macos/src/CandidatePanel.swift"
	macEngine       = "shims/macos/src/EngineClient.swift"
	macController   = "shims/macos/src/ChaosetController.swift"
	macEngineClient = "shims/macos/src/EngineClient.swift"
	macSetup        = "shims/macos/setup/main.swift"
	macKeyTest      = "shims/macos/tests/key_routing/main.swift"
	macBuild        = "shims/macos/build.sh"
	macInstall      = "shims/macos/install.sh"
	macTis          = "shims/macos/tools/tis.swift"
	macReregister   = "shims/macos/reregister.sh"
	macReadme       = "shims/macos/README.md"
	winShell        = "shims/windows/src/win32.cpp"
	winBuild        = "shims/windows/build.sh"
	winInstaller    = "shims/windows/installer/chaoset.nsi"
	archDoc         = "docs/architecture.md"
	winInstallBat   = "shims/windows/install.bat"
	winResource     = "shims/windows/resources/chaoset.rc"
	winPackageMsvc  = "shims/windows/package_msvc.sh"
	winVersionCheck = "scripts/check_windows_version_info.py"
	linuxSetupBuild = "shims/linux/setup/build.sh"
	winCoreHpp      = "shims/common/ipc/core.hpp"
	winCoreCpp      = "shims/common/ipc/core.cpp"
	winTransport    = "shims/windows/src/transport.hpp"
	winTipCore      = "shims/windows/src/tip_core.cpp"
	winTipDesync    = "shims/windows/src/tip_desync.cpp"
	winCoreTest     = "shims/windows/tests/core_test.cpp"
	macTransport    = "shims/macos/src/DaemonTransport.swift"
	linuxEngine     = "shims/linux/engine/main.cpp"
	linuxEngineMain = "shims/linux/engine/daemon.cpp"
	linuxReadme     = "shims/linux/README.md"
	linuxSetup      = "shims/linux/setup/chaoset-setup.c"
	linuxE2E        = "shims/linux/test-ci.sh"
	linuxComp       = "shims/linux/component/chaoset.xml"
	// 发行面工具已全部 Rust 化（Go 只保留 dictgen/静态检查）：导入/用户词典/
	// 设置/CLI 的实现都在这几个 crate 里。
	chaosetImport    = "crates/chaoset-tools/src/dictimport.rs"
	rustImportFs     = "crates/chaoset-tools/src/fsutil.rs"
	rustUserdictCLI  = "crates/chaoset-tools/src/userdict.rs"
	rustCtlMain      = "crates/chaosetctl/src/main.rs"
	rustCtlMulticall = "crates/chaosetctl/src/multicall.rs"
	rustCtlSettings  = "crates/chaosetctl/src/settingsctl.rs"
	// daemon 已由 Rust 实现（crates/chaosetd）；IPC 方法分发在 ipc.rs，
	// 单实例/延迟拉起等 daemon 侧契约看 main.rs/singleton.rs/ui.rs。
	ipcServer    = "crates/chaosetd/src/ipc.rs"
	ipcClient    = "crates/chaoset-ipc/src/client.rs"
	rustUserdict = "crates/chaoset-engine/src/userdict.rs"
	rustSession  = "crates/chaoset-engine/src/session.rs"
	userdictCLI  = "crates/chaosetctl/src/userdictcmd.rs"
	// Rust 发行面构建/版本资源（Windows 二进制版本信息由 build.rs 注入）。
	rustCtlBuildScript    = "crates/chaosetctl/build.rs"
	rustDaemonBuildScript = "crates/chaosetd/build.rs"
	makefile              = "Makefile"
	installSh             = "shims/linux/install.sh"
	archScript            = "shims/linux/arch/package.sh"
	releaseYML            = ".github/workflows/release.yml"
	ciYML                 = ".github/workflows/ci.yml"
	uiParityDoc           = "docs/ui-parity.md"
	readmeDoc             = "README.md"
	changelogDoc          = "CHANGELOG.md"
	slintTokens           = "shims/settings/ui/tokens.slint"
	slintComponents       = "shims/settings/ui/components.slint"
	slintBridge           = "shims/settings/src/bridge.rs"
	slintEvents           = "shims/settings/src/events.rs"
	slintOpenUrl          = "shims/settings/src/open_url.rs"
	macLauncher           = "shims/macos/src/SettingsLauncher.swift"
	winLauncher           = "shims/windows/src/settings_launcher.cpp"
	winLauncherHpp        = "shims/windows/src/settings_launcher.hpp"
	winUiHost             = "shims/windows/src/ui_host.cpp"
	winModeHotkey         = "shims/windows/src/mode_hotkey.cpp"
	linuxArchPkg          = "shims/linux/arch/PKGBUILD"
	linuxArchPkgBin       = "shims/linux/arch/PKGBUILD-bin"
	linuxArchInstall      = "shims/linux/arch/chaoset-ime.install"
	slintApp              = "shims/settings/ui/app.slint"
)

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
	// 成功输出由注册表生成：打印的组就是实际执行的组，不再有第二份清单。
	fmt.Printf("UI 一致性契约检查通过（%d 组：%s）\n", len(groupNames()), strings.Join(groupNames(), "、"))
}
