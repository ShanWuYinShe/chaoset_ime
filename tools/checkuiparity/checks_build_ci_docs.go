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
	for _, s := range []string{winBuild, macBuild, linuxSetupBuild} {
		checkContains(fsys, problems, group, s, "version.sh")
		checkExcludes(fsys, problems, group, s, "git describe --tags")
	}
}

type shimModuleBudget struct {
	path     string
	maxLines int
}

var shimModuleBudgets = []shimModuleBudget{
	// win32.cpp 拆分 wave1（2026-11-08）：ModeToast 已迁到 mode_toast.cpp，
	// owner/DPI 判定迁到 winutil；预算从 5300 收紧到 4800 锁定进度。
	{"shims/windows/src/win32.cpp", 4800},
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
	{"shims/windows/src/settings_window.cpp", 1600},
	{"shims/windows/src/settings_window.hpp", 300},
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
	{"shims/macos/src/SettingsWindow.swift", 600},
	{"shims/macos/src/SettingsWindowPages.swift", 700},
	{"shims/macos/src/SettingsWindowTargets.swift", 400},
	{"shims/macos/src/main.swift", 100},
	{"shims/linux/setup/chaoset-setup.c", 400},
	{"shims/linux/setup/setup_page_input.c", 400},
	{"shims/linux/setup/setup_page_dict.c", 400},
	{"shims/linux/setup/setup_page_about.c", 300},
	{"shims/linux/setup/setup_pages.h", 120},
	{"shims/linux/setup/setup_ctl.c", 700},
	{"shims/linux/setup/setup_internal.h", 120},
	{"shims/linux/setup/setup_import.c", 500},
	{"shims/linux/setup/setup_learn.c", 500},
	{"shims/linux/setup/setup_ctl.h", 120},
	{"shims/linux/setup/test_setup_ctl.c", 200},
	// Linux 原生引擎（shims/linux/engine/）：此前不在模块地图与预算内，是
	// 「大文件回潮」的盲区；docs/architecture.md §3.4 同步登记。
	{"shims/linux/engine/main.cpp", 900},
	{"shims/linux/engine/daemon.cpp", 300},
	{"shims/common/ipc/core.cpp", 700},
	{"shims/common/ipc/core.hpp", 300},
}

var shimDirLineLimits = map[string]int{
	"shims/windows/src":  1600,
	"shims/macos/src":    1000,
	"shims/linux/setup":  800,
	"shims/linux/engine": 1000,
	"shims/common/ipc":   800,
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
			case ".cpp", ".hpp", ".swift", ".c", ".h":
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
	for _, want := range []string{"## 2. 方案 A", "## 3. 模块地图", "文件预算", "依赖方向"} {
		if !strings.Contains(text, want) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少关键小节「%s」", group, archDoc, want))
		}
	}
}

// checkPackaging 打包/安装一致性：Linux 设置窗脚本、学习数据模块、小图标都要随包；
// macOS 运行组件必须随 ChaosetIME.app 安装。
func checkPackaging(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupPackaging, makefile, "chaoset-setup", "chaoset-macos.zip")
	checkContains(fsys, problems, groupPackaging, installSh, "chaoset-setup")
	checkContains(fsys, problems, groupPackaging, archScript, "chaoset-setup")
	checkContains(fsys, problems, groupPackaging, releaseYML, "chaoset-setup", "chaoset-macos.zip")
	checkContains(fsys, problems, groupPackaging, linuxComp, "chaoset-setup")
	checkContains(fsys, problems, groupPackaging, ciYML,
		"build.sh", "test_setup_ctl")
	// macOS：运行组件必须随 ChaosetIME.app 安装，且安装器/CLI 都从 app 内启动 daemon。
	checkContains(fsys, problems, groupPackaging, macBuild,
		"$APP/Contents/Resources/", "dict_common.tsv")
	checkContains(fsys, problems, groupPackaging, macSetup,
		"installedResource(\"chaosetd\")", "embeddedResource(\"chaoset-import\")", "installSetupApp")
	checkContains(fsys, problems, groupPackaging, macInstall,
		"$IM/ChaosetIME.app/Contents/Resources/chaosetd")
}

// checkCICoverage CI 覆盖不退化：按键路由/联想 E2E 回归测试必须存在且被 CI 调用；
// 日常 CI 必须跑真实打包链（不能只靠打 tag 时的 release 冒烟）。
func checkCICoverage(fsys fs.FS, problems *[]string) {
	// key_routing 按键路由回归测试必须存在且被 CI 调用。
	checkExists(fsys, problems, groupCICoverage, "shims/macos/tests/key_routing/run.sh")
	checkContains(fsys, problems, groupCICoverage, ciYML, "key_routing")
	// 联想端到端（EngineClient + 真实 daemon）同样必须存在且被 CI 调用。
	checkExists(fsys, problems, groupCICoverage, "shims/macos/tests/associate/run.sh")
	checkContains(fsys, problems, groupCICoverage, ciYML, "associate/run.sh")
	// 安装包自检：日常 CI 必须构建 Linux 包并断言设置窗模块随包，
	// 不能只靠打 tag 时的 release 冒烟。
	checkContains(fsys, problems, groupCICoverage, ciYML, "make package-linux", "Linux 安装包自检")
	// 同理：日常 macos-build 必须跑真实打包链（zip），不能只 build。
	checkContains(fsys, problems, groupCICoverage, ciYML, "make package-macos", "macOS 构建 + 安装包")
	// equivalence.sh B 段用 bin/chaoset-import：macOS job 必须先 make build。
	checkContains(fsys, problems, groupCICoverage, ciYML, "make build")
	// 断言用例数守卫：防止有人删断言让回归测试静默变弱；并校验 expectedChecks
	// 与实际 check( 数一致（加断言忘了同步会 FAIL）。
	checkContains(fsys, problems, groupCICoverage, macKeyTest, "expectedChecks")
	checkKeyRoutingCount(fsys, problems)
}

// checkDocParity ui-parity 关键差异行必须保留（防止文档被删后失去对照基准）；
// 历史阶段记录已移入 CHANGELOG，README 不得残留已过期的现状描述。
func checkDocParity(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupDocParity, uiParityDoc,
		"学习数据管理", "词库导入", groupSettingsSingleton, "Windows per-monitor DPI",
		"双拼 mspy", "无障碍", "鼠标点选候选", "滚轮翻页")
	// 历史阶段记录已移入 CHANGELOG（README 只保留当前文档）；历史条目
	// 不能留下「Linux 仍只有 CLI」这类已过期的现状描述，必须带「阶段 54 已补齐」。
	checkContains(fsys, problems, groupDocParity, changelogDoc, "阶段 54",
		"Linux 设置窗补「词库导入」GUI")
	checkExcludes(fsys, problems, groupDocParity, readmeDoc,
		"Linux 目前仍只有 CLI", "GTK 设置窗导入待后续")
}

// checkPackagingArtifacts 打包安装的「产物存在」段：Linux 设置窗测试/逻辑模块与 hicolor
// 图标必须随包。与 checkPackaging 同组、在注册表里位于原执行位置（daemon 单实例之后），
// 保持既有失败输出顺序不变。
func checkPackagingArtifacts(fsys fs.FS, problems *[]string) {
	checkExists(fsys, problems, groupPackaging, linuxTest)
	checkExists(fsys, problems, groupPackaging, linuxCtl)
	for _, size := range []string{"16x16", "22x22", "24x24", "32x32", "48x48", "64x64", "128x128", "256x256"} {
		checkExists(fsys, problems, groupPackaging,
			fmt.Sprintf("shims/linux/icons/hicolor/%s/apps/chaoset.png", size))
	}
	checkExists(fsys, problems, groupPackaging, "shims/linux/icons/hicolor/scalable/apps/chaoset.svg")
}
