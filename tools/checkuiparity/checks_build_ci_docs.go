// checks_build_ci_docs.go：构建/打包/CI 覆盖/文档对照/模块布局与架构文档契约。

package main

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// checkKeyRoutingCount 校验 key_routing 的 expectedChecks 与实际 check( 断言数
// 一致：加/删断言却忘了同步 expectedChecks 时，静态检查先 FAIL（运行时守卫是
// 第二道，两道都保留）。
func checkKeyRoutingCount(fsys fs.FS, problems *[]string) {
	const group = groupCICoverage
	text, err := readText(fsys, macKeyTest)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, macKeyTest, err))
		return
	}
	expected, ok := firstFloat(text, `let expectedChecks = ([0-9]+)`)
	if !ok {
		*problems = append(*problems, fmt.Sprintf("%s：%s 缺少可解析的 expectedChecks", group, macKeyTest))
		return
	}
	callRe := regexp.MustCompile(`(?m)^check\(`)
	calls := len(callRe.FindAllStringIndex(text, -1))
	if int(expected) != calls {
		*problems = append(*problems, fmt.Sprintf("%s：key_routing expectedChecks=%d 与实际 check( 断言数 %d 不一致（加/删断言需同步更新）", group, int(expected), calls))
	}
}

// checkVersionSource 锁住 2026-11-06 用户规则：只看一个环境变量
// CHAOS_VERSION_TAG——CI 在 push tag 触发时设成 tag 版本，其余构建不设，
// version.sh 读不到就生成「年月日 + commit hash」。禁止回退到 git describe --tags。
func checkVersionSource(fsys fs.FS, problems *[]string) {
	const group = groupVersionSource
	checkExists(fsys, problems, group, "scripts/version.sh")
	checkContains(fsys, problems, group, "scripts/version.sh",
		"CHAOS_VERSION_TAG", "rev-parse", "date +%Y%m%d", "--quad", "--pkg",
		"date-hash")
	// tag 版本只由 release.yml 顶层 env 注入，且限定 push tag 触发。
	// 发版包必须走与日常 CI 相同的 Makefile 打包链（`make package-macos`）：
	// release 手写复制品曾弱于 Makefile 的 universal/内容自检（2026-11-08 审计 P1-4）。
	checkContains(fsys, problems, group, releaseYML,
		"CHAOS_VERSION_TAG", "github.event_name == 'push'", "make package-macos")
	checkExcludes(fsys, problems, group, releaseYML, "git describe --tags")
	checkContains(fsys, problems, group, makefile,
		"VERSION_SCRIPT", "VERSION_QUAD", "PKG_VERSION",
		"s/@VERSION@/$(PKG_VERSION)/g", "version.sh", "CHAOS_VERSION_TAG")
	for _, s := range []string{winBuild, macBuild} {
		checkContains(fsys, problems, group, s, "version.sh")
		checkExcludes(fsys, problems, group, s, "git describe --tags")
	}
}

type shimModuleBudget struct {
	path     string
	maxLines int
}

var shimModuleBudgets = []shimModuleBudget{
	// win32.cpp 拆分 wave2（TIP 类拆分完成）：wave1 续拆出 CandidateWindow/ShellWorker/
	// TIP 注册/CLSID-GUID 常量（预算 4800→3200），wave2 把 class ChaosetTIP 的 2564 行
	// 类内定义按职责拆成 tip.hpp + tip_core/keys/composition/candidates.cpp，win32.cpp
	// 只剩类厂/导出/DllMain/DllGetClassObject/DllCanUnloadNow（≤2000 目标达成）。
	// 新文件预算按实测行数留余量，防止回搬大文件。
	{"shims/windows/src/win32.cpp", 1200},
	{"shims/windows/src/tip.hpp", 500},
	{"shims/windows/src/tip_core.cpp", 1100},
	// tip_profiles.cpp：2026-11-13 从 tip_core.cpp 按「TSF 信号 sink 与激活档案」
	// 职责拆出（键盘布局切换只有 ITfInputProcessorProfileActivationSink 通知，
	// 托盘常显收口新增）。
	{"shims/windows/src/tip_profiles.cpp", 300},
	{"shims/windows/src/tip_keys.cpp", 1100},
	{"shims/windows/src/tip_composition.cpp", 900},
	{"shims/windows/src/tip_candidates.cpp", 500},
	{"shims/windows/src/candidate_window.cpp", 1200},
	{"shims/windows/src/candidate_window.hpp", 300},
	{"shims/windows/src/shell_worker.cpp", 600},
	{"shims/windows/src/shell_worker.hpp", 250},
	{"shims/windows/src/tip_registration.cpp", 300},
	{"shims/windows/src/tip_registration.hpp", 120},
	{"shims/windows/src/tip_ids.hpp", 150},
	// tip_thread.cpp：wave2 迁入窗口过程/按线程 TIP 注册表/前台钩子表（68→191 行）。
	{"shims/windows/src/tip_thread.cpp", 350},
	{"shims/windows/src/tip_thread.hpp", 200},
	{"shims/windows/src/mode_toast.hpp", 150},
	{"shims/windows/src/mode_toast.cpp", 400},
	{"shims/windows/src/diagnostics.cpp", 700},
	{"shims/windows/src/diagnostics.hpp", 250},
	{"shims/windows/src/winutil.cpp", 500},
	{"shims/windows/src/winutil.hpp", 300},
	{"shims/windows/src/theme.cpp", 500},
	{"shims/windows/src/theme.hpp", 250},
	{"shims/windows/src/config.cpp", 700},
	{"shims/windows/src/config.hpp", 300},
	{"shims/windows/src/transport.cpp", 500},
	{"shims/windows/src/transport.hpp", 300},
	{"shims/windows/src/daemon.cpp", 500},
	{"shims/windows/src/daemon.hpp", 300},
	{"shims/windows/src/ui_client.cpp", 700},
	{"shims/windows/src/ui_client.hpp", 300},
	{"shims/windows/src/ui_host.cpp", 1000},
	{"shims/windows/src/ui_host.hpp", 300},
	{"shims/windows/src/candidate_layout.hpp", 200},
	{"shims/windows/src/ui_main.cpp", 120},
	{"shims/macos/src/ChaosetController.swift", 900},
	{"shims/macos/src/KeyRouter.swift", 700},
	{"shims/macos/src/CandidatePanel.swift", 400},
	{"shims/macos/src/CandidatePanelView.swift", 900},
	{"shims/macos/src/CandidatePanelWindow.swift", 400},
	{"shims/macos/src/CandidateTheme.swift", 500},
	{"shims/macos/src/CandidateSettings.swift", 200},
	{"shims/macos/src/EngineClient.swift", 600},
	{"shims/macos/src/DaemonTransport.swift", 500},
	// 2026-11-16「暖陶纸感」视觉升级把主题令牌与自绘控件抽成独立文件
	// （三端共享设计系统 + 可被 checkuiparity 逐值断言）。
	{"shims/macos/src/main.swift", 100},
	{"shims/linux/setup/chaoset-setup.c", 200},
	// 统一设置中心启动器（三端只保留入口；唯一 UI 是 Slint 设置中心）。

	{"shims/windows/src/settings_launcher.cpp", 200},

	{"shims/windows/src/settings_launcher.hpp", 100},

	{"shims/macos/src/SettingsLauncher.swift", 200},

	{"shims/settings/src/single_instance.rs", 250},

	// Linux 原生引擎（shims/linux/engine/）：此前不在模块地图与预算内，是
	// 「大文件回潮」的盲区；docs/architecture.md §3.4 同步登记。
	{"shims/linux/engine/main.cpp", 900},
	{"shims/linux/engine/daemon.cpp", 300},
	{"shims/common/ipc/core.cpp", 700},
	{"shims/common/ipc/core.hpp", 300},
	// Slint 跨平台设置中心（shims/settings/）：声明式 UI，三端令牌对齐。
	{"shims/settings/ui/tokens.slint", 100},
	{"shims/settings/ui/components.slint", 400},
	{"shims/settings/ui/app.slint", 500},
	{"shims/settings/src/bridge.rs", 300},
	{"shims/settings/src/events.rs", 150},
	{"shims/settings/src/lib.rs", 150},
	{"shims/settings/src/main.rs", 150},
}

var shimDirLineLimits = map[string]int{
	"shims/windows/src":  1600,
	"shims/macos/src":    1000,
	"shims/linux/setup":  200,
	"shims/linux/engine": 1000,
	"shims/common/ipc":   800,
	"shims/settings/src": 800,
	"shims/settings/ui":  500,
}

func checkShimModuleLayout(fsys fs.FS, problems *[]string) {
	const group = groupModuleLayout
	registered := make(map[string]bool, len(shimModuleBudgets))
	for _, m := range shimModuleBudgets {
		registered[m.path] = true
		data, err := fs.ReadFile(fsys, m.path)
		if err != nil {
			*problems = append(*problems, fmt.Sprintf(
				"%s：缺少模块文件 %s（架构拆分不许回退；模块地图见 %s）", group, m.path, archDoc))
			continue
		}
		if n := lineCount(data); n > m.maxLines {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 有 %d 行，超过预算 %d 行（按 %s 模块地图拆分）", group, m.path, n, m.maxLines, archDoc))
		}
	}
	for dir, limit := range shimDirLineLimits {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			*problems = append(*problems, fmt.Sprintf("%s：无法读取模块目录 %s：%v", group, dir, err))
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := dir + "/" + e.Name()
			if registered[p] {
				continue
			}
			switch path.Ext(e.Name()) {
			case ".cpp", ".hpp", ".swift", ".c", ".h", ".rs", ".slint":
			default:
				continue
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				continue
			}
			if n := lineCount(data); n > limit {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 有 %d 行，超过目录单文件上限 %d；新增模块必须登记到 %s 与 checker 模块清单",
					group, p, n, limit, archDoc))
			}
		}
	}
}

func checkArchitectureDoc(fsys fs.FS, problems *[]string) {
	const group = groupArchitectureDoc
	data, err := fs.ReadFile(fsys, archDoc)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：缺少 %s", group, archDoc))
		return
	}
	text := string(data)
	// 发行面 Rust 化后按 P3 文档同步口径锁稳定关键词（不锁整句，避免文档改写即红）。
	for _, want := range []string{
		"## 1. 分层与依赖方向",
		"## 2. 设置与 CLI 单一真源",
		"## 3. 模块地图",
		"## 4. 架构检查清单",
		"## 5. 验证",
		"文件预算", "依赖方向", "crates/chaoset-config", "chaosetctl",
	} {
		if !strings.Contains(text, want) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少关键小节/内容「%s」", group, archDoc, want))
		}
	}
}

// checkPackaging 打包/安装一致性：Linux 设置窗脚本、学习数据模块、小图标都要随包；
// macOS 运行组件必须随 ChaosetIME.app 安装；发行面 CLI 由 Rust 单二进制承载。
func checkPackaging(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupPackaging, makefile, "chaoset-setup", "chaoset-macos.zip", "build-settings", "chaoset-settings", "dict.bin", "--bin chaosetctl")
	checkContains(fsys, problems, groupPackaging, installSh, "chaoset-setup", "chaoset-settings", "dict.bin")
	checkContains(fsys, problems, groupPackaging, archScript, "chaoset-setup", "chaoset-settings", "dict.bin")
	checkContains(fsys, problems, groupPackaging, releaseYML, "chaoset-setup", "chaoset-macos.zip", "chaoset-settings")
	checkContains(fsys, problems, groupPackaging, linuxComp, "chaoset-setup")
	checkContains(fsys, problems, groupPackaging, ciYML, "shims/linux/setup/build.sh", "chaoset-settings", "data/dict.bin")
	// macOS：运行组件与统一设置中心（含其依赖的 chaosetctl）必须随 app 安装。
	checkContains(fsys, problems, groupPackaging, macBuild, "$APP/Contents/Resources/", "dict_common.tsv", "dict_common.bin", "chaoset-settings", "chaosetctl")
	checkContains(fsys, problems, groupPackaging, macSetup, `installedResource("chaosetd")`, `embeddedResource("chaoset-import")`, "installSetupApp")
	// 发行面 CLI：三端包内都是 Rust chaosetctl（macOS app 另出 chaoset-import 副本）。
	checkContains(fsys, problems, groupPackaging, macBuild, "build/chaosetctl")
	checkContains(fsys, problems, groupPackaging, winPackageMsvc, "chaosetctl.exe")
	checkContains(fsys, problems, groupPackaging, winInstaller, "chaosetctl.exe")
	checkContains(fsys, problems, groupPackaging, linuxArchPkg, "chaosetctl")
	// 发布词库二进制化：TSV 仍是仓库源，打包期编译 CSDB；daemon 优先加载缓存。
	checkExists(fsys, problems, groupPackaging, "crates/chaoset-engine/src/dict_binary.rs")
	checkExists(fsys, problems, groupPackaging, "crates/chaoset-engine/src/bin/dictc.rs")
	checkContains(fsys, problems, groupPackaging, "crates/chaosetd/src/main.rs", "load_with_cache")
	checkContains(fsys, problems, groupPackaging, macInstall, "$IM/ChaosetIME.app/Contents/Resources/chaosetd")
}

// checkCICoverage CI 覆盖不退化：按键路由/联想 E2E 回归测试必须存在且被 CI 调用；
// 日常 CI 必须跑真实打包链（不能只靠打 tag 时的 release 冒烟）；Rust 发行面后
// Go 工具链只在 lint/test/race 出现，设置存储单测改跑 chaoset-config。
func checkCICoverage(fsys fs.FS, problems *[]string) {
	checkExists(fsys, problems, groupCICoverage, "shims/macos/tests/key_routing/run.sh")
	checkContains(fsys, problems, groupCICoverage, ciYML, "key_routing")
	checkExists(fsys, problems, groupCICoverage, "shims/macos/tests/associate/run.sh")
	checkContains(fsys, problems, groupCICoverage, ciYML, "associate/run.sh")
	// 安装包自检：日常 CI 必须构建 Linux 包并断言设置入口与 Slint 设置中心随包。
	checkContains(fsys, problems, groupCICoverage, ciYML, "make package-linux", "Linux 安装包自检")
	// 日常 macos-build 必须跑真实打包链（zip），不能只 build。
	checkContains(fsys, problems, groupCICoverage, ciYML, "make package-macos", "macOS 构建 + 安装包")
	checkContains(fsys, problems, groupCICoverage, ciYML, "make build")
	// Slint 设置中心：单元测试/类型检查 + 三端构建（mac universal / Windows）。
	checkContains(fsys, problems, groupCICoverage, ciYML,
		"cargo test --manifest-path shims/settings/Cargo.toml",
		"cargo check --manifest-path shims/settings/Cargo.toml",
		"make build-settings-universal",
		"make build-settings-windows")
	// 启动器冒烟：Windows 交叉编译产物必须内嵌设置中心文件名；Linux 启动器
	// 编译/execv 冒烟步骤必须在 CI 里存在。
	checkContains(fsys, problems, groupCICoverage, ciYML, "设置入口启动器编译与冒烟")
	// Rust 发行面：Go 只在 lint/test/race 安装；设置存储单测跑 chaoset-config
	// （macOS defaults 与 Windows 注册表真跑），macOS universal 自检包含 chaosetctl。
	if data, err := fs.ReadFile(fsys, ciYML); err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", groupCICoverage, ciYML, err))
	} else if n := strings.Count(string(data), "actions/setup-go"); n != 3 {
		*problems = append(*problems, fmt.Sprintf(
			"%s：%s 中 actions/setup-go 出现 %d 次，应只保留 lint/test/race 3 处（其余 job 装 Go 会掩盖 Rust 发行面）",
			groupCICoverage, ciYML, n))
	}
	checkContains(fsys, problems, groupCICoverage, ciYML,
		"cargo test --manifest-path crates/Cargo.toml -p chaoset-config -- --nocapture",
		"cargo test --manifest-path crates/Cargo.toml -p chaoset-config --test windows_store -- --nocapture",
		"设置存储单测（macOS defaults）",
		"Windows 设置存储单测（注册表往返）",
		"build/chaosetctl")
	checkExcludes(fsys, problems, groupCICoverage, ciYML,
		"go test ./internal/settings", "go test ./internal/tools",
		"go test ./cmd/chaoset")
	// release：Go 只在 test job（workflow 结构校验/单测），package-* 全走 Rust 打包链。
	if data, err := fs.ReadFile(fsys, releaseYML); err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", groupCICoverage, releaseYML, err))
	} else if n := strings.Count(string(data), "actions/setup-go"); n != 1 {
		*problems = append(*problems, fmt.Sprintf(
			"%s：%s 中 actions/setup-go 出现 %d 次，应只保留 test job 1 处", groupCICoverage, releaseYML, n))
	}
	checkExcludes(fsys, problems, groupCICoverage, releaseYML, "构建 Go 工具")
	checkContains(fsys, problems, groupCICoverage, macKeyTest, "expectedChecks")
	checkKeyRoutingCount(fsys, problems)
}

// checkDocParity ui-parity 关键差异行必须保留（防止文档被删后失去对照基准）；
// 历史阶段记录已移入 CHANGELOG，README 不得残留已过期的现状描述。
// 发行面 Rust 化后只锁稳定关键词：阶段编号等会被 P3 文档同步重写的串不再锁死。
func checkDocParity(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupDocParity, uiParityDoc,
		"学习数据", "词库导入", "Slint", "chaoset-settings",
		"Windows per-monitor DPI", "双拼 mspy", "无障碍", "鼠标点选候选", "滚轮翻页")
	checkContains(fsys, problems, groupDocParity, changelogDoc, "## v", "chaosetctl")
	checkExcludes(fsys, problems, groupDocParity, readmeDoc,
		"Linux 目前仍只有 CLI", "GTK 设置窗导入待后续")
}

// checkPackagingArtifacts 打包安装的「产物存在」段：Linux 设置窗测试/逻辑模块与 hicolor
// 图标必须随包。与 checkPackaging 同组、在注册表里位于原执行位置（daemon 单实例之后），
// 保持既有失败输出顺序不变。
func checkPackagingArtifacts(fsys fs.FS, problems *[]string) {
	checkExists(fsys, problems, groupPackaging, "shims/linux/setup/chaoset-setup.c")
	checkExists(fsys, problems, groupPackaging, "shims/linux/setup/build.sh")
	checkExists(fsys, problems, groupPackaging, "shims/windows/src/settings_launcher.cpp")
	checkExists(fsys, problems, groupPackaging, "shims/macos/src/SettingsLauncher.swift")
	for _, size := range []string{"16x16", "22x22", "24x24", "32x32", "48x48", "64x64", "128x128", "256x256"} {
		checkExists(fsys, problems, groupPackaging,
			fmt.Sprintf("shims/linux/icons/hicolor/%s/apps/chaoset.png", size))
	}
	checkExists(fsys, problems, groupPackaging, "shims/linux/icons/hicolor/scalable/apps/chaoset.svg")
}
