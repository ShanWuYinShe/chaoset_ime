// checkuiparity 校验三端（macOS/Windows/Linux）UI 一致性契约（2026-10-31）。
//
// 为什么需要它：候选窗/设置窗是三套独立实现（AppKit / Win32 GDI / GTK3），
// 共享的只有「应该一致」的文字与功能契约。此前靠人肉检查：改一端忘另一端
// （2026-10-28 齿轮五轮迭代漏同步 Windows 的教训）或文案漂移只能等真机发现。
// 本工具把可静态验证的契约固定下来，在 make vet / CI lint 里跑：
//
//  1. 设置窗共享文案三端逐字一致（标题/分组/联想/学习数据/底部提示）；
//
//  2. 设置窗单例三端都有实现（mac 静态 window、Windows g_settingsWnd、
//     Linux Gtk.Application）；
//
//  3. 学习数据管理三端都有 GUI 实现；Linux 侧只调 chaoset-userdict 本地文件
//     模式，**不得**引入引擎 DBus 管理接口（engine 包纯净约束）；
//
//  4. 词库导入：三端都有 GUI 入口（Linux 2026-10-31 起：设置窗调用 chaoset-import
//     写用户词库，ibus 引擎启动时用 -imported-dict 自动加载）；
//
//  5. 打包/安装一致性：Linux 原生设置窗 chaoset-setup / setup_ctl.c 必须被 Makefile/install.sh/Arch 打包/
//     release 冒烟引用；hicolor 必须有小尺寸图标；setup 脚本必须被组件 XML
//     与 release 冒烟引用；
//
//  6. ui-parity 文档保留关键差异行（学习数据管理/词库导入/设置窗单例/
//     Windows per-monitor DPI/双拼 mspy 等）；
//
//  7. 候选窗几何/配色字段静态对齐：mac CandidatePanel.swift ↔ Windows
//     win32.cpp 的布局 pt 常量（字号/padding/gap/键帽/齿轮区/最大宽度/最大行数）、
//     齿轮矢量参数（圆环半径/线宽、8 齿内/外半径、线宽、颜色绑定）、
//     13 个主题色字段名集合一致，Windows 必须使用多行折行布局函数
//     candidate_layout.hpp。**像素级渲染仍需真机**，但「常量被改一端」会先 FAIL；
//
//  8. CI 覆盖不退化：key_routing 回归测试必须存在且被 ci.yml 的 macos-build
//     调用（审计 G7：它曾长期是未接入 CI 的「死测试」）；expectedChecks 必须与
//     实际 check( 断言数一致（加/删断言忘了同步会先在这里 FAIL）；
//
//  9. shell 脚本里 `$VAR` 不得紧跟非 ASCII 字符（macOS bash 3.2 在 UTF-8
//     locale 下会把多字节字符并入变量名 → unbound variable；2026-10-31
//     key_routing CI 首跑就栽在 `$ARCH）` 上，统一改用 `${VAR}`）；
//
//  10. 词库导入安全流程不能退化：mac Setup.app 与 Windows 设置窗都必须保留
//     「检查导入器退出码 / 旧词库备份 / 同卷原子替换」关键实现（只看产物文件
//     存在会把部分解析失败当成功；直接 remove+move 会出现丢词库中间态）；
//
//  11. 关闭联想的即时性不能退化：mac 设置窗 post 通知、Windows 设置窗跨进程广播
//     （候选窗转发 TIP），Linux 仍为下次按键生效且必须在 ui-parity 记录；
//
//  12. Option/Alt+Backspace 三端语义统一为按音节回退（2026-10-31）：mac/Windows/
//     Linux 都必须走引擎 `backspaceWord`，不能退回「放行/整段清空」旧行为；
//
//  13. 修饰键映射（2026-10-31 用户要求）：macOS Command↔Windows Win↔Linux Super、
//     Control↔Ctrl、Option↔Alt；组词中 Backspace/Delete 按该映射统一语义，
//     ui-parity 必须保留映射表；
//
//  14. 候选窗右键「删除该词」（2026-11-01）：macOS NSMenu / Windows TrackPopupMenu
//     / Go UserDict.DeleteWord + IPC userDelete + CLI delete 各层入口必须在位，
//     三端 toast 文案逐字一致；Linux ibus 面板不适用并记录替代入口；
//     ui-parity 必须保留「学习数据按词删除」语义与入口。
//
//  15. 中英状态指示器/设置窗只能在单例 UI 宿主进程（chaoset-ui.exe）里实现
//     （2026-11-05 用户反馈定稿）：TSF 把 TIP 加载进每个宿主进程，TIP 自绘托盘
//     = 任务栏多个图标且各实例模式不一致；设置窗在 AC 宿主（开始菜单/搜索）
//     内创建会被 z 序 band 压住且无法启动子进程。TIP 只许经命名管道上报模式/
//     请求设置；构建（build.sh/Makefile）、安装器（Run 项 + 文件）与发行包
//     必须包含 chaoset-ui.exe，DLL 必须导出 ChaosetRunUi。
//
//  16. 设置窗阶段2（2026-11-06）：客户区 640×560pt、左导航 132pt + 选中态、
//     页脚版本号所有页、关于页正文（含「关于」分组标题）；Windows 卡片契约
//     （SettingsCardDef / kInputCards / kDictionaryCards / kAboutCards /
//     settingsCardColor，控件背景必须用卡片刷防深色横条回归）；候选栏齿轮
//     必须 mouseUp 才打开设置（按下只记录，Linux README 记录齿轮不适用）；
//     托盘中英图标不随开机显示（UI 宿主不写登录 Run 项，TIP 首次激活带
//     --mode 懒启动，首条模式上报后才 Shell_NotifyIcon）。
//
//  17. 零阻塞契约（Windows，2026-11-06 用户要求「任何情况下都不阻塞任何进程」）：
//     TSF 回调在宿主 UI 线程同步执行，函数体内禁止 Sleep/WaitNamedPipeW/
//     WaitForSingleObject/CreateProcessW/CreateFileW/WriteFile/MessageBoxW/
//     GetOpenFileNameW/RegSetValueExW/慢连接 ensureDaemonConnected；副作用统一
//     入队 ShellWorker（任务队列 + message-only 窗口回投）；tsf.log 走异步
//     enqueueLogLine（不回退同步写）；管道 FILE_FLAG_OVERLAPPED + 截止时间 +
//     熔断；daemon 未就绪按键 fail-open；前台钩子回调只 PostMessage；
//     DllCanUnloadNow 计入 worker 线程与消息窗口。
//
//  18. 每页候选数引擎与三端壳一致（2026-11-08）：唯一真源是 engine/session.go 的 pageSize；
//     macOS `associatePageSize` / Windows `kPageSize` / Linux ibus `kPageSize` 必须
//     与之相同。任一端漂移会让数字键 1..N 指位与翻页错位（pageSize 曾由 9 改 6，
//     壳仍接受 1-9 时按 7/8/9 既不选词也不进文档）。
//
// 检查组注册：组名唯一表在 groups.go，注册表在 run.go（checkGroups）。Run 只遍历
// 注册表执行，成功提示由注册表生成——新增检查组只需「加组名常量 + 加注册 + 实现
// 函数」，不会出现「跑了但没打印」的清单漂移（旧实现把组清单硬编码在成功信息里，
// 曾漏掉 11 个实际运行的组）。
//
// 用法：go run ./tools/checkuiparity <仓库根>
// 退出码：0 = 通过，1 = 发现违规，2 = 用法错误。
package main

import (
	"fmt"
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
	"Chaoset 版本",
	"中英状态",
	// 中英状态作用域只有两档（全局共享 / 每进程独立）；三端按钮文案逐字一致。
	"全局共享",
	"每进程独立",
	// 2026-11-06 阶段2：关于页正文与词库导入统一说明（三端逐字一致）。
	// 词库导入统一句三端一致（macOS 在统一句之后另补 Setup.app 括注，不替换）。
	"跨平台中文输入法",
	"代码 MIT · 词库 CC BY-SA 4.0",
	"支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。",
}

const (
	macPanel        = "shims/macos/src/CandidatePanel.swift"
	macSettings     = "shims/macos/src/SettingsWindow.swift"
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
	linuxSetupBuild = "shims/linux/setup/build.sh"
	winCoreHpp      = "shims/common/ipc/core.hpp"
	winCoreCpp      = "shims/common/ipc/core.cpp"
	linuxEngine     = "shims/linux/engine/main.cpp"
	linuxEngineMain = "shims/linux/engine/daemon.cpp"
	linuxReadme     = "shims/linux/README.md"
	linuxSetup      = "shims/linux/setup/chaoset-setup.c"
	linuxCtl        = "shims/linux/setup/setup_ctl.c"
	linuxDictImport = "shims/linux/setup/setup_ctl.c"
	linuxE2E        = "shims/linux/test-ci.sh"
	linuxTest       = "shims/linux/setup/test_setup_ctl.c"
	linuxComp       = "shims/linux/component/chaoset.xml"
	chaosetImport   = "cmd/chaoset-import/main.go"
	ipcServer       = "ipc/server.go"
	ipcClient       = "ipc/client.go"
	userdictGo      = "engine/userdict.go"
	userdictCLI     = "cmd/chaoset-userdict/main.go"
	makefile        = "Makefile"
	installSh       = "shims/linux/install.sh"
	archScript      = "shims/linux/arch/package.sh"
	releaseYML      = ".github/workflows/release.yml"
	ciYML           = ".github/workflows/ci.yml"
	uiParityDoc     = "docs/ui-parity.md"
	readmeDoc       = "README.md"
	changelogDoc    = "CHANGELOG.md"
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
