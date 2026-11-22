// checks_rust_cli.go：「Rust CLI 发行面」契约。
//
// 背景：发行面（引擎/daemon/设置/IPC/导入/用户词典/CLI）已全部 Rust 化，Go 只保留
// 构建期词库生成 cmd/chaoset-dictgen 与静态检查 tools/check*。这一组锁住「构建/打包
// 链只从 crates/chaosetctl 产出 chaosetctl、三端安装/升级统一走 chaosetctl import、
// 已删除的 Go 运行期实现不得回归」，防止迁移后又被悄悄接回 Go 工具。
//
// 与「打包安装」组的分工：packaging 组管「包里必须有什么」（设置中心/词库/图标），
// 本组管「这些产物由谁构建、由谁调用」——Rust 是唯一构建者与调用入口。
package main

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// goBuildCmdRe 从 go build 命令行里提取 Go 命令包（cmd/<名字>）。
var goBuildCmdRe = regexp.MustCompile(`cmd/[A-Za-z0-9_-]+`)

// checkGoBuildCommands 扫描构建脚本里的 go build：
//   - 只允许构建 allowed 里的 Go 命令（发行面只剩 cmd/chaoset-dictgen）；
//   - 注释行（# / ; 开头）里提到 go build 不算（历史说明）；
//   - 其它 cmd/<名字> 一律报错——它们已迁移到 Rust，再出现即回退双实现。
//
// 为什么按「提取 cmd/ 包名」而不是整串匹配：cmd/chaoset 是 cmd/chaoset-dictgen 的
// 前缀，按子串匹配会误伤；反过来精确到包名，新增已删命令也能直接命中。
func checkGoBuildCommands(fsys fs.FS, problems *[]string, group, path string, allowed map[string]bool) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, err))
		return
	}
	allowedNames := make([]string, 0, len(allowed))
	for name := range allowed {
		allowedNames = append(allowedNames, "cmd/"+name)
	}
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if !strings.Contains(line, "go build") {
			continue
		}
		for _, ref := range goBuildCmdRe.FindAllString(line, -1) {
			name := strings.TrimPrefix(ref, "cmd/")
			if allowed[name] {
				continue
			}
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s:%d 仍在 go build 已迁移到 Rust 的 Go 命令 %s（发行面只允许构建 %s）",
				group, path, i+1, ref, strings.Join(allowedNames, "、")))
		}
	}
}

// checkRustCLIShipping 锁住发行面 Rust 化后的构建/调用链：
//  1. Makefile / macOS build.sh / Windows package_msvc.sh / Arch PKGBUILD 都由
//     cargo 构建 crates/chaosetctl（--bin chaosetctl），不得再 go build 已删命令；
//  2. macOS app 内保留 chaoset-import 同名副本（multicall 分发，Setup.app 与
//     等价性脚本调用不变）；
//  3. Windows 安装器/便携包用 chaosetctl import 刷新词库（-seed/-backup 安全参数）；
//  4. 版本信息自检脚本覆盖发行包内全部 Rust 二进制与图标资源；
//  5. 两个 Rust build.rs 都从 shims/windows/resources/chaoset.rc 模板注入版本资源；
//  6. 已删除的 Go 运行期实现路径不得回归。
func checkRustCLIShipping(fsys fs.FS, problems *[]string) {
	const group = groupRustCLIShipping
	// 1) Makefile：发行面 CLI 是 Rust 单二进制（multicall），Go 构建只留 dictgen。
	checkContains(fsys, problems, group, makefile, "--bin chaosetctl", "package_msvc.sh")
	checkGoBuildCommands(fsys, problems, group, makefile,
		map[string]bool{"chaoset-dictgen": true})
	// 2) macOS：universal 构建 chaosetctl，并在 app 内生成 chaoset-import 副本。
	checkContains(fsys, problems, group, macBuild,
		"rust_universal", "cp build/chaosetctl build/chaoset-import")
	checkGoBuildCommands(fsys, problems, group, macBuild, nil)
	// 3) Windows 原生打包链：cargo 构建 chaosetctl.exe（不再 go build）。
	checkContains(fsys, problems, group, winPackageMsvc,
		"--bin chaosetctl", "chaosetctl.exe")
	checkGoBuildCommands(fsys, problems, group, winPackageMsvc, nil)
	// 4) Linux 发行包（Arch PKGBUILD）：同样 cargo 构建，禁止 go build。
	checkContains(fsys, problems, group, linuxArchPkg, "--bin chaosetctl", "chaosetctl")
	checkGoBuildCommands(fsys, problems, group, linuxArchPkg, nil)
	// 5) 安装/升级入口：词库刷新必须走 chaosetctl 的 import 子命令（保留 -seed/-backup）。
	checkContains(fsys, problems, group, winInstaller,
		"chaosetctl.exe", " import ", "-seed", "-backup")
	checkContains(fsys, problems, group, winInstallBat, "chaosetctl.exe", " import ")
	// 6) 版本信息自检：发行包内 Rust 二进制都要有 VS_VERSION_INFO + 图标组。
	checkContains(fsys, problems, group, winVersionCheck,
		"chaosetctl.exe", "chaosetd.exe", "chaoset-settings.exe", "RT_GROUP_ICON")
	// 7) Windows 资源注入：两个 Rust build.rs 都引用同一份 rc 模板与四段版本号。
	for _, path := range []string{rustCtlBuildScript, rustDaemonBuildScript} {
		checkContains(fsys, problems, group, path, "chaoset.rc", "CHAOSET_VERSION_QUAD")
	}
	// 9) 版本可追溯：两个 Rust 二进制都从 build.rs 注入 git rev/构建时间/脏标记，
	//    daemon 的 -version 不能再显示 Cargo.toml 里的静态版本。
	for _, path := range []string{rustCtlBuildScript, rustDaemonBuildScript} {
		checkContains(fsys, problems, group, path, "CHAOSET_BUILD_REV", "CHAOSET_BUILD_TIME", "CHAOSET_BUILD_DIRTY")
	}
	checkContains(fsys, problems, group, "crates/chaosetctl/src/buildinfo.rs", "CHAOSET_BUILD_REV")
	checkContains(fsys, problems, group, "crates/chaosetd/src/main.rs", `option_env!("CHAOSET_BUILD_VERSION")`)
	// 10) macOS 升级安全：先停旧 IME/daemon 再替换 app（防 TIS 重复输入源与旧 daemon 写回），
	//     并在升级前备份用户词典。
	checkContains(fsys, problems, group, macInstall,
		"升级/覆盖安装前先停旧进程", "userdict-", "用户词典已备份")

	// 8) 已删除的 Go 运行期实现不得回归（Rust 是唯一实现）。
	for _, gone := range []string{
		"cmd/chaosetctl",
		"cmd/chaoset",
		"cmd/chaoset-cli",
		"cmd/chaoset-import",
		"cmd/chaoset-userdict",
		"cmd/chaoset-settingsctl",
		"internal/settings",
		"internal/tools",
		"internal/appdirs",
		"internal/buildinfo",
		"internal/cliutil",
		"ipc/client.go",
	} {
		checkAbsent(fsys, problems, group, gone)
	}
}
