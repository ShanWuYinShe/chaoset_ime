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
//  4. 词库导入：三端都有 GUI 入口（Linux 2026-10-31 起：设置窗调用 chaoset-import
//     写用户词库，ibus 引擎启动时用 -imported-dict 自动加载）；
//  5. 打包/安装一致性：Linux 原生设置窗 chaoset-setup / setup_ctl.c 必须被 Makefile/install.sh/Arch 打包/
//     release 冒烟引用；hicolor 必须有小尺寸图标；setup 脚本必须被组件 XML
//     与 release 冒烟引用；
//  6. ui-parity 文档保留关键差异行（学习数据管理/词库导入/设置窗单例/
//     Windows per-monitor DPI/双拼 mspy 等）；
//  7. 候选窗几何/配色字段静态对齐：mac CandidatePanel.swift ↔ Windows
//     win32.cpp 的布局 pt 常量（字号/padding/gap/键帽/齿轮区/最大宽度/最大行数）、
//     齿轮矢量参数（圆环半径/线宽、8 齿内/外半径、线宽、颜色绑定）、
//     13 个主题色字段名集合一致，Windows 必须使用多行折行布局函数
//     candidate_layout.hpp。**像素级渲染仍需真机**，但「常量被改一端」会先 FAIL；
//  8. CI 覆盖不退化：key_routing 回归测试必须存在且被 ci.yml 的 macos-build
//     调用（审计 G7：它曾长期是未接入 CI 的「死测试」）；expectedChecks 必须与
//     实际 check( 断言数一致（加/删断言忘了同步会先在这里 FAIL）；
//  9. shell 脚本里 `$VAR` 不得紧跟非 ASCII 字符（macOS bash 3.2 在 UTF-8
//     locale 下会把多字节字符并入变量名 → unbound variable；2026-10-31
//     key_routing CI 首跑就栽在 `$ARCH）` 上，统一改用 `${VAR}`）；
//  10. 词库导入安全流程不能退化：mac Setup.app 与 Windows 设置窗都必须保留
//     「检查导入器退出码 / 旧词库备份 / 同卷原子替换」关键实现（只看产物文件
//     存在会把部分解析失败当成功；直接 remove+move 会出现丢词库中间态）；
//  11. 关闭联想的即时性不能退化：mac 设置窗 post 通知、Windows 设置窗跨进程广播
//     （候选窗转发 TIP），Linux 仍为下次按键生效且必须在 ui-parity 记录；
//  12. Option/Alt+Backspace 三端语义统一为按音节回退（2026-10-31）：mac/Windows/
//     Linux 都必须走引擎 `backspaceWord`，不能退回「放行/整段清空」旧行为；
//  13. 修饰键映射（2026-10-31 用户要求）：macOS Command↔Windows Win↔Linux Super、
//     Control↔Ctrl、Option↔Alt；组词中 Backspace/Delete 按该映射统一语义，
//     ui-parity 必须保留映射表；
//  14. 候选窗右键「删除该词」（2026-11-01）：macOS NSMenu / Windows TrackPopupMenu
//     / Go UserDict.DeleteWord + IPC userDelete + CLI delete 各层入口必须在位，
//     三端 toast 文案逐字一致；Linux ibus 面板不适用并记录替代入口；
//     ui-parity 必须保留「学习数据按词删除」语义与入口。
//  15. 中英状态指示器/设置窗只能在单例 UI 宿主进程（chaoset-ui.exe）里实现
//     （2026-11-05 用户反馈定稿）：TSF 把 TIP 加载进每个宿主进程，TIP 自绘托盘
//     = 任务栏多个图标且各实例模式不一致；设置窗在 AC 宿主（开始菜单/搜索）
//     内创建会被 z 序 band 压住且无法启动子进程。TIP 只许经命名管道上报模式/
//     请求设置；构建（build.sh/Makefile）、安装器（Run 项 + 文件）与发行包
//     必须包含 chaoset-ui.exe，DLL 必须导出 ChaosetRunUi。
//
// 用法：go run ./tools/checkuiparity <仓库根>
// 退出码：0 = 通过，1 = 发现违规，2 = 用法错误。
package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
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
	winShell        = "shims/windows/src/win32.cpp"
	winBuild        = "shims/windows/build.sh"
	winInstaller    = "shims/windows/installer/chaoset.nsi"
	winInstallBat   = "shims/windows/install.bat"
	winResource     = "shims/windows/resources/chaoset.rc"
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

// ---- 候选窗（mac CandidatePanel.swift ↔ Windows win32.cpp）----

// numericContract 是两端同名 pt 常量的提取正则（第一个分组必须是数字）。
type numericContract struct {
	name  string
	macRe string
	winRe string
}

// 候选窗布局 pt 常量（与 docs/ui-parity.md 的「布局常量」一致）。
var layoutContracts = []numericContract{
	{"正文字号", `static let fontSize: CGFloat = ([0-9.]+)`, `int fontH = -MulDiv\(([0-9.]+), dpi, 72\);`},
	{"序号字号", `static let indexFontSize: CGFloat = ([0-9.]+)`, `int idxFontH = -MulDiv\(([0-9.]+), dpi, 72\);`},
	{"内边距", `static let padding: CGFloat = ([0-9.]+)`, `const int pad = pt\(([0-9.]+), dpi\);`},
	{"首项左内边距", `static let firstPadLeft: CGFloat = ([0-9.]+)`, `const int firstPadLeft = pt\(([0-9.]+), dpi\);`},
	{"候选内边距", `static let itemPadX: CGFloat = ([0-9.]+)`, `const int itemPadX = pt\(([0-9.]+), dpi\);`},
	{"候选间距", `static let gap: CGFloat = ([0-9.]+)`, `const int gap = pt\(([0-9.]+), dpi\);`},
	{"键帽横向内边距", `static let chipPadX: CGFloat = ([0-9.]+)`, `const int chipPadX = pt\(([0-9.]+), dpi\);`},
	{"键帽纵向内边距", `static let chipPadY: CGFloat = ([0-9.]+)`, `const int chipPadY = pt\(([0-9.]+), dpi\);`},
	{"首选胶囊外扩", `static let pillMargin: CGFloat = ([0-9.]+)`, `const int pillMargin = pt\(([0-9.]+), dpi\);`},
	{"翻页区宽", `static let chevronBoxW: CGFloat = ([0-9.]+)`, `const int chevronBoxW = pt\(([0-9.]+), dpi\);`},
	{"齿轮区宽", `static let boxW: CGFloat = ([0-9.]+)`, `const int gearBoxW = pt\(([0-9.]+), dpi\);`},
	{"面板最大宽度", `static let maxWidth: CGFloat = ([0-9.]+)`, `constexpr int kMaxWidthPt = ([0-9.]+);`},
	{"最大行数", `static let maxRows = ([0-9]+)`, `constexpr int kMaxRows = ([0-9]+);`},
	{"行距", `static let lineSpacing: CGFloat = ([0-9.]+)`, `const int lineSpacing = pt\(([0-9.]+), dpi\);`},
}

// 齿轮矢量参数（2026-10-28 用户拍板：两端同算法同几何同色）。
var gearContracts = []numericContract{
	{"圆环半径", `center\.x - ([0-9.]+)`, `const double rr = ([0-9.]+) \* u;`},
	{"圆环线宽", `ring\.lineWidth = ([0-9.]+)`, `CreatePen\(PS_SOLID, static_cast<int>\(([0-9.]+) \* u \+ 0\.5\), gearColor\)`},
	{"辐条数", `for i in 0\.\.<([0-9]+)`, `for \(int i = 0; i < ([0-9]+); \+\+i\)`},
	{"辐条内半径", `spokes\.move\(to: NSPoint\(x: center\.x \+ dx \* ([0-9.]+)`, `(?s)MoveToEx\(memDC,\s*static_cast<int>\(cx \+ dx \* ([0-9.]+) \* u`},
	{"辐条外半径", `spokes\.line\(to: NSPoint\(x: center\.x \+ dx \* ([0-9.]+)`, `(?s)LineTo\(memDC,\s*static_cast<int>\(cx \+ dx \* ([0-9.]+) \* u`},
	{"辐条线宽", `spokes\.lineWidth = ([0-9.]+)`, `createRoundCapPen\(static_cast<int>\(([0-9.]+) \* u \+ 0\.5\), gearColor\)`},
}

func readText(fsys fs.FS, path string) (string, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mustRead(fsys fs.FS, path string) string {
	text, err := readText(fsys, path)
	if err != nil {
		return ""
	}
	return text
}

// firstFloat 用正则从文本中提取第一个分组为数字的值。
func firstFloat(text, pattern string) (float64, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, false
	}
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// checkNumericContracts 对比 mac/Windows 两端的同名 pt 常量，返回成功提取的值。
func checkNumericContracts(fsys fs.FS, problems *[]string, group string, contracts []numericContract) map[string]float64 {
	macText, err := readText(fsys, macPanel)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, macPanel, err))
		return nil
	}
	winText, err := readText(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return nil
	}
	values := make(map[string]float64, len(contracts))
	for _, c := range contracts {
		mv, ok := firstFloat(macText, c.macRe)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s：无法从 %s 解析「%s」（mac 端模式未命中，实现可能已重构，需同步更新检查器）", group, macPanel, c.name))
			continue
		}
		wv, ok := firstFloat(winText, c.winRe)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s：无法从 %s 解析「%s」（Windows 端模式未命中，实现可能已重构，需同步更新检查器）", group, winShell, c.name))
			continue
		}
		values[c.name] = mv
		if math.Abs(mv-wv) > 1e-6 {
			*problems = append(*problems, fmt.Sprintf("%s：「%s」mac=%g Windows=%g 不一致", group, c.name, mv, wv))
		}
	}
	return values
}

// checkCandidateWindow 候选窗静态几何/配色字段一致性（mac ↔ Windows）。
// 像素级渲染仍需真机；这里锁住「常量被改一端」与主题色字段集合漂移。
func checkCandidateWindow(fsys fs.FS, problems *[]string) {
	values := checkNumericContracts(fsys, problems, "候选窗几何", layoutContracts)
	macText := mustRead(fsys, macPanel)
	winText := mustRead(fsys, winShell)
	if fontSize, ok := values["正文字号"]; ok {
		if factor, ok := firstFloat(macText, `static let textH: CGFloat = ceil\(fontSize \* ([0-9.]+)\)`); ok {
			want := math.Ceil(fontSize * factor)
			if got, ok := firstFloat(winText, `static_cast<int>\(tmText\.tmHeight\), pt\(([0-9.]+), dpi\)`); ok {
				if math.Abs(got-want) > 1e-6 {
					*problems = append(*problems, fmt.Sprintf("候选窗几何：行高 textH mac=ceil(%g*%g)=%g Windows=%g 不一致", fontSize, factor, want, got))
				}
			} else {
				*problems = append(*problems, "候选窗几何：无法从 Windows 解析 textH 行高常量")
			}
		} else {
			*problems = append(*problems, "候选窗几何：无法从 mac 解析 textH 公式")
		}
	}
	// Windows 的 kMaxContentPt 是内联表达式（kMaxWidthPt - 7*2 - 36 - 26 - 2*2 - 2），
	// 不是字面量；这里按 mac 常量重建表达式并做「去空白子串」比对。
	need := []string{"内边距", "翻页区宽", "齿轮区宽", "首选胶囊外扩"}
	if allPresent(values, need) {
		expect := fmt.Sprintf("constexprintkMaxContentPt=kMaxWidthPt-%g*2-%g-%g-%g*2-2;",
			values["内边距"], values["翻页区宽"], values["齿轮区宽"], values["首选胶囊外扩"])
		if !strings.Contains(noSpace(winText), expect) {
			*problems = append(*problems, fmt.Sprintf("候选窗几何：Windows 内容上限公式与 mac 常量不一致（期望 %s）", expect))
		}
	}
	// 多行折行：Windows 2026-10-31 起与 mac 同算法（纯布局函数可在 macOS 单测），
	// 断言头文件被 win32.cpp 引用，防止退回单行布局。
	checkContains(fsys, problems, "候选窗几何", winShell, "candidate_layout.hpp",
		"wrapRows(boxes, maxContent, gap, contentH, kMaxRows)",
		"const int lineSpacing = pt(4, dpi)")
	// 无障碍基础能力不能退化：候选窗必须处理 WM_GETOBJECT 并动态加载 UIA。
	checkContains(fsys, problems, "候选窗几何", winShell,
		"WM_GETOBJECT", "UiaRootObjectId", "uiautomationcore.dll")
	// UIA 必须动态加载：构建脚本断言 DLL 没有 uiautomationcore 静态导入。
	checkContains(fsys, problems, "候选窗几何", winBuild,
		"uiautomationcore", "静态导入")
	checkNumericContracts(fsys, problems, "候选窗齿轮", gearContracts)
	checkContains(fsys, problems, "候选窗齿轮", macPanel, "color: theme.chipDigit")
	checkContains(fsys, problems, "候选窗齿轮", winShell, "const COLORREF gearColor = theme.chipDigit;")
	checkContains(fsys, problems, "候选窗分割线", macPanel, "Layout.textH * 55 / 100")
	checkContains(fsys, problems, "候选窗分割线", winShell, "textH * 55 / 100")
	checkThemeFieldParity(fsys, problems)
}

// noSpace 去掉全部空白，便于比对源码里被换行/缩进拆开的表达式。
func noSpace(text string) string { return strings.Join(strings.Fields(text), "") }

func allPresent(values map[string]float64, names []string) bool {
	for _, n := range names {
		if _, ok := values[n]; !ok {
			return false
		}
	}
	return true
}

// checkThemeFieldParity 13 个主题色字段名必须两端一致（任一新增/删除都会 FAIL）。
func checkThemeFieldParity(fsys fs.FS, problems *[]string) {
	macText, err := readText(fsys, macPanel)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("候选窗配色：读取 %s 失败：%v", macPanel, err))
		return
	}
	winText, err := readText(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("候选窗配色：读取 %s 失败：%v", winShell, err))
		return
	}
	macFields := structFields(macText, `(?s)struct CandidateTheme \{.*?\n\}`, `(?m)^\s*let ([A-Za-z0-9_]+): NSColor`)
	winFields := structFields(winText, `(?s)struct CandidateColors \{.*?\n\};`, `(?m)^\s*COLORREF ([A-Za-z0-9_]+);`)
	sort.Strings(macFields)
	sort.Strings(winFields)
	if strings.Join(macFields, ",") != strings.Join(winFields, ",") {
		*problems = append(*problems, fmt.Sprintf("候选窗配色：主题色字段集合不一致（mac=%v Windows=%v）", macFields, winFields))
	}
	if len(macFields) != 13 {
		*problems = append(*problems, fmt.Sprintf("候选窗配色：mac 主题色字段数 = %d，期望 13", len(macFields)))
	}
}

// structFields 在匹配 struct 区域内按字段模式提取字段名。
func structFields(text, structRe, fieldRe string) []string {
	sre, err := regexp.Compile(structRe)
	if err != nil {
		return nil
	}
	region := sre.FindString(text)
	if region == "" {
		return nil
	}
	fre, err := regexp.Compile(fieldRe)
	if err != nil {
		return nil
	}
	var fields []string
	for _, m := range fre.FindAllStringSubmatch(region, -1) {
		fields = append(fields, m[1])
	}
	return fields
}

// shellVarCJKRe 匹配「$VAR 紧跟非 ASCII 字符」。
var shellVarCJKRe = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*[^\x00-\x7F]`)

// checkShellVarEscaping 扫描 shims 下所有 .sh：macOS 自带 bash 3.2 在 UTF-8
// locale 下会把 `$ARCH）` 的多字节字符并入变量名，运行时报 `ARCH�: unbound
// variable`（2026-10-31 key_routing CI 首跑的真实故障）。统一要求 `${VAR}`。
func checkShellVarEscaping(fsys fs.FS, problems *[]string) {
	const group = "shell 变量转义"
	err := fs.WalkDir(fsys, "shims", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sh") {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, rerr))
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if shellVarCJKRe.MatchString(line) {
				*problems = append(*problems, fmt.Sprintf("%s：%s:%d 中 shell 变量紧跟非 ASCII 字符（bash 3.2 + UTF-8 locale 会并入变量名 → unbound variable；请改用 ${VAR}）", group, path, i+1))
			}
		}
		return nil
	})
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：遍历 shims 失败：%v", group, err))
	}
}

// checkImportSafety 词库导入安全流程静态断言。
//
// 安全点都是历史审查修出来的（2026-10-26 / 2026-10-31 / 2026-11-01）：
//  1. 必须检查导入器退出码，不能只看产物文件存在（导入器会写出残缺产物）；
//  2. 必须先备份旧词库，再原子替换，不能 remove+move；
//  3. 失败要留下可排查的日志（Windows import.log / mac 返回 log 文本）；
//  4. 文件合并/备份/原子替换属于工具逻辑，必须由 Go `chaoset-import` 实现；
//     三端壳只调用工具（-seed/-backup），不再自己 temp/copy/replace。
//
// 这里锁住实现关键片段，防止重构时把安全流程删掉。
func checkImportSafety(fsys fs.FS, problems *[]string) {
	const group = "词库导入安全"
	// mac Setup.app（2026-11-01 起）：只调 Go 工具，退出码检查 + seed/backup 参数。
	checkContains(fsys, problems, group, macSetup,
		"r.status == 0",
		`"-seed"`,
		`"-backup"`,
	)
	checkExcludes(fsys, problems, group, macSetup, "replaceItemAt")
	// Windows 设置窗（2026-11-01 起）：只调 Go 工具，退出码检查 + backup 参数 +
	// 失败日志；不再自己备份/替换。
	checkContains(fsys, problems, group, winShell,
		"code != 0",
		"-backup",
		"import.log",
	)
	checkExcludes(fsys, problems, group, winShell, "MoveFileExW(",
		"MOVEFILE_REPLACE_EXISTING")
	// Linux 设置窗（2026-11-01 起）：只调用 Go 工具并检查退出码；合并/备份/
	// 原子替换由 chaoset-import 负责（-o 直接指向目标 + -backup）。
	checkContains(fsys, problems, group, linuxDictImport,
		"WIFEXITED",
		`"-o"`,
		`"-backup"`,
	)
	checkExcludes(fsys, problems, group, linuxDictImport,
		"os.replace", "shutil.copy2", `target + ".tmp"`,
	)
	// Go 工具是这些安全语义的唯一实现：seed/backup/原子替换都在这里。
	checkContains(fsys, problems, group, chaosetImport,
		`"seed"`, `"backup"`, "backupFile(", "os.Rename(",
	)
}

// checkCandidateDelete 候选窗右键「删除该词」三端/全链路静态契约（2026-11-01）。
//
// 为什么静态锁：删除是「写学习数据」的 UI 入口，漏接线不会编译失败——mac
// 忘记设 onDelete 闭包、Windows 忘记映射 WM_RBUTTONUP、daemon 忘记分发
// userDelete、CLI 没接 delete 子命令，都会「看起来有菜单但点了没反应」。
// 这里把各层入口关键字与三端文案锁定，改一端漏同步先 FAIL；实机点击/焦点
// 行为（菜单不抢焦点、右键不上屏）仍需人工验收。
func checkCandidateDelete(fsys fs.FS, problems *[]string) {
	const group = "候选右键删除"
	// macOS：候选面板右键菜单 + 控制器接线 + EngineClient 写调用。
	checkContains(fsys, problems, group, macPanel,
		"var onDelete", "menu(for event: NSEvent)", "删除该词")
	checkContains(fsys, problems, group, macController,
		"onDelete", "deleteCandidate", "deleteAssociateCandidate", "userDelete",
		"recompose", "associateSylls", "已删除该词")
	checkContains(fsys, problems, group, macEngine, "func userDelete", "retryOnFailure: false")
	// Windows：候选窗右键消息 + TrackPopupMenu + TIP 删除/刷新 + core 封装。
	checkContains(fsys, problems, group, winShell,
		"WM_RBUTTONUP", "TrackPopupMenu", "删除该词",
		"deleteCandidateByIndex", "userDelete", "assocSylls_")
	checkContains(fsys, problems, group, winCoreHpp, "userDelete")
	checkContains(fsys, problems, group, winCoreCpp, "userDelete")
	// Go：引擎删除 API + IPC 分发/客户端 + CLI 子命令（daemon 与本地文件两模式）。
	checkContains(fsys, problems, group, userdictGo,
		"func (u *UserDict) DeleteWord", "u.file.Phrases")
	checkContains(fsys, problems, group, ipcServer, "case \"userDelete\":", "DeleteWord")
	checkContains(fsys, problems, group, ipcClient, "func (c *Client) UserDelete")
	checkContains(fsys, problems, group, userdictCLI,
		"case \"delete\":", "DeleteWord", "UserDelete", "已删除「%s」的学习记录")
	// 三端文案逐字一致：菜单文案在两端壳内，状态提示在 mac/Windows 壳内。
	for _, text := range []string{"已删除该词", "未找到该词的学习记录", "删除失败：daemon 不可达"} {
		checkContains(fsys, problems, group, macController, text)
		checkContains(fsys, problems, group, winShell, text)
	}
	// Linux：ibus 面板不适用，必须记录理由与替代入口，禁止假装已支持。
	checkContains(fsys, problems, group, linuxReadme,
		"右键删除", "不适用", "chaoset-userdict delete")
	// 文档：语义/入口/文案必须有对照，防止文档悄悄变成「三端都支持」。
	checkContains(fsys, problems, group, uiParityDoc,
		"学习数据按词删除", "右键删除", "删除该词", "chaoset-userdict delete")
}

// UILess 能力契约（2026-11-05）：
// 微软《UILess Mode Overview》明确要求 TIP 实现 ITfTextInputProcessorEx，否则
// 在 UILess 线程里**不会被激活**（连日志都不会有，所以「日志里 uiless=0」不是
// 证据）。同时严禁在未实现 ITfUIElementMgr 的情况下注册
// TFCAT_TIPCAP_UIELEMENTENABLED——声明了就必须让所有 UI 走 UI 元素管理器，
// 否则正是 2026-10-30「开始菜单里候选窗完全消失」的事故。
func checkUILessCapability(fsys fs.FS, problems *[]string) {
	const group = "UILess 能力"
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
	const group = "焦点兜底"
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
	const group = "输入法激活判据"
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
	const group = "崩溃日志"
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
	const group = "模式指示器/UI 宿主"
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
		if n := strings.Count(string(data), "reportModeToUi(enMode_"); n < 3 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：reportModeToUi(enMode_) 只有 %d 处，应至少 3 处"+
					"（Activate / 切换模式 / 焦点回来）", group, n))
		}
		// 设置窗创建入口只允许出现在 UI 宿主路径里（定义 + 消息处理 + 首启），
		// 且候选栏齿轮只发管道请求。
		if n := strings.Count(string(data), "openSettingsWindowStandalone()"); n < 3 {
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
		"ensureUiHostStartedByDaemon", "session_.ensureUiHost()")
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
	const group = "浮窗 owner"
	// Windows：统一经 popupOwner() 取 owner。
	checkContains(fsys, problems, group, winShell, "static HWND popupOwner()", "candidateWnd_.setOwner(popupOwner());")
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
		"static bool shellHostNeedsOwner()", "if (!shellHostNeedsOwner()) return nullptr;",
		"startmenuexperiencehost.exe", "searchapp.exe")
	// 前台事件回调里不得碰 TSF（ITfContext::GetActiveView 等 COM）：合成窗口
	// HWND 必须在 TSF 线程缓存（composeHostWnd_），回调只做纯 Win32 比较。
	//（2026-11-05 回归审计 P2-3）
	checkContains(fsys, problems, group, winShell, "composeHostWnd_")
	checkExcludes(fsys, problems, group, winShell,
		"compCtx_ != nullptr ? hostWindowOf(compCtx_) : nullptr")
}

// checkKeyRoutingCount 校验 key_routing 的 expectedChecks 与实际 check( 断言数
// 一致：加/删断言却忘了同步 expectedChecks 时，静态检查先 FAIL（运行时守卫是
// 第二道，两道都保留）。
func checkKeyRoutingCount(fsys fs.FS, problems *[]string) {
	const group = "CI 覆盖"
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

// checkWindowsVersionInfo 锁住「安装程序与 Windows 二进制都带版本信息 / app
// 信息」契约（2026-11-06 用户反馈：ChaosetSetup.exe 属性页全空，无法判断
// 装的是哪一版）。版本号必须来自单一真源 git tag——NSIS 用注入的
// ${VERSION}/${VERSION_QUAD}，资源模板用 @VERSION@ 占位符，
// 任何一处写死数字都会被 make check-version 拦下。
func checkWindowsVersionInfo(fsys fs.FS, problems *[]string) {
	const group = "Windows 版本信息"
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

// checkWindowsInstallSafety 固定安装器的两个用户可见契约（2026-11-03）：
//  1. 安装/升级不得强杀加载旧 DLL 的用户程序或系统输入宿主——用户未保存的
//     工作不能因为装输入法丢失；被占用文件由 InstallRobustFile/重启替换兜底。
//  2. daemon 不能由提升后的安装器以管理员身份启动（LOCALAPPDATA/管道 ACL
//     可能属于另一个账户，用户侧因此打不出拼音）；改为 Run 项（REG_EXPAND_SZ
//     + %LOCALAPPDATA%）与 TSF 首次输入懒启动（-pipe + -seeddict）。
func checkWindowsInstallSafety(fsys fs.FS, problems *[]string) {
	const group = "Windows 安装安全"
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
		"gtk_application_new", `org.chaoset.Setup`)

	// 3. 学习数据 GUI 三端存在；Linux 设置窗不得引入引擎 DBus 管理接口。
	checkContains(fsys, &problems, "学习数据 GUI", macEngine,
		"func userStats", "func userClear", "func userExportAll")
	checkContains(fsys, &problems, "学习数据 GUI", macSettings,
		"userStats", "userClear", "userExportAll")
	checkContains(fsys, &problems, "学习数据 GUI", winShell,
		"userStats", "userClear", "userExportAll")
	checkContains(fsys, &problems, "学习数据 GUI", linuxSetup,
		"setup_userdict_stats", "setup_userdict_clear", "setup_userdict_export",
		"g_idle_add")
	checkContains(fsys, &problems, "学习数据 GUI", linuxCtl,
		"chaoset-userdict", "-userdict", "setup_userdict_clear", "ibus")
	// engine 包纯净约束：Linux 设置窗只能走 chaoset-userdict 本地文件模式，
	// 不允许重新引入 DBus / ibus 引擎管理接口（2026-10-31 用户明确要求）。
	for _, path := range []string{linuxSetup, linuxCtl, linuxDictImport} {
		checkExcludes(fsys, &problems, "engine 纯净约束", path,
			"dbus-send", "org.freedesktop.IBus", "IBusEngine")
	}

	// 4. 词库导入入口：mac Setup.app + Windows 设置窗有 GUI；Linux 仅 CLI 且
	//    ui-parity 必须明确记录（避免文档悄悄变成「已支持」）。
	checkContains(fsys, &problems, "词库导入入口", macSetup, "导入词库", "chaoset-import")
	// mac 输入法设置窗也有「导入词库…」入口（打开 Setup.app 并传 --import），
	// 与 Windows 设置窗「词库」区对齐；实现仍只有 Setup.app 一份。
	checkContains(fsys, &problems, "词库导入入口", macSettings, "导入词库", "安装与设置")
	checkContains(fsys, &problems, "词库导入入口", macSetup, "--import")
	checkContains(fsys, &problems, "词库导入入口", winShell, "导入词库", "chaoset-import.exe")
	// Linux（2026-10-31 起）：原生 GTK3 设置窗「导入词库…」→ setup_ctl 调
	// chaoset-import 写用户词库 → ibus 引擎启动时 -imported-dict 自动加载。
	checkContains(fsys, &problems, "词库导入入口", linuxSetup, "导入词库")
	checkExists(fsys, &problems, "词库导入入口", linuxDictImport)
	checkContains(fsys, &problems, "词库导入入口", linuxDictImport,
		"chaoset-import", "setup_import_dicts", "restart_engines")
	checkContains(fsys, &problems, "词库导入入口", linuxEngine,
		"-imported-dict", "importedDict")
	// 默认路径同口径：C 侧优先调用 Go 工具 -print-paths（唯一来源
	// internal/appdirs），原生引擎 daemon.cpp 也走 XDG 默认路径；
	// C 仍保留 XDG 回退供源码树未构建时使用。
	checkContains(fsys, &problems, "词库导入入口", linuxCtl,
		"-print-paths", "tool_path")
	checkContains(fsys, &problems, "词库导入入口", linuxDictImport,
		`"imported_dict"`, `"dict.tsv"`)
	checkContains(fsys, &problems, "词库导入入口", linuxEngineMain,
		"defaultLinuxPaths", "importedDict")
	// linux-e2e 必须真实验证导入词参与候选，而不只是日志里有加载路径。
	checkContains(fsys, &problems, "词库导入入口", linuxE2E, "用户导入词库", "测试词", "UpdateLookupTable")

	// 4b. 词库导入安全流程（备份/原子替换/退出码检查）不能退化。
	checkImportSafety(fsys, &problems)

	// 4c. 关闭联想即时性：mac 通知立即收起；Windows 跨进程广播（候选窗转发
	// TIP）立即收起；Linux 仍为下次按键生效（ui-parity 记录为已知差异）。
	checkContains(fsys, &problems, "关闭联想即时性", macSettings, "chaosetAssociateDisabled")
	checkContains(fsys, &problems, "关闭联想即时性", winShell,
		"Chaoset.AssociateChanged", "HWND_BROADCAST", "clearAssociateFromSettings")
	checkContains(fsys, &problems, "关闭联想即时性", uiParityDoc, "下一次按键")

	// 4d. Option/Alt+Backspace 三端统一为按音节回退（2026-10-31）：mac 壳、
	// Windows 壳（含 core 层）、Linux ibus 壳都必须调用引擎 backspaceWord。
	checkContains(fsys, &problems, "退格键语义", "shims/macos/src/ChaosetController.swift", "backspaceWord")
	checkContains(fsys, &problems, "退格键语义", winShell, "backspaceWord", "kVkDelete")
	checkContains(fsys, &problems, "退格键语义", winCoreHpp, "backspaceWord")
	checkContains(fsys, &problems, "退格键语义", linuxEngine, "backspaceWord", "kKeysymDelete")
	checkContains(fsys, &problems, "退格键语义", uiParityDoc, "按音节回退")

	// 4e. 修饰键映射（2026-10-31 用户要求）：macOS Command↔Windows Win↔
	// Linux Super、Control↔Ctrl、Option↔Alt。三端壳必须识别对应修饰键，
	// 且 ui-parity 必须保留映射表（防止文档漂移后习惯不可迁移）。
	checkContains(fsys, &problems, "修饰键映射", "shims/macos/src/ChaosetController.swift",
		".command", ".control", ".option")
	checkContains(fsys, &problems, "修饰键映射", winShell,
		"VK_CONTROL", "VK_MENU", "VK_LWIN")
	checkContains(fsys, &problems, "修饰键映射", linuxEngine,
		"IBUS_CONTROL_MASK", "IBUS_MOD1_MASK", "IBUS_MOD4_MASK")
	checkContains(fsys, &problems, "修饰键映射", uiParityDoc, "修饰键映射", "Command")

	// 4f. Shift 空按切中英的记账契约（2026-11-05）：
	// 三端都必须是「按住会话」语义，且必须能在焦点切换时复位——否则任何一次
	// 丢失的 Shift KeyUp（按住 Shift 切窗口时 KeyUp 交给新前台窗口，本线程
	// 收不到）都会让状态机永久卡死。Windows 曾用 ++/-- 计数器、且
	// Deactivate/OnSetFocus 完全不复位，实机表现为 Terminal/Chrome/开始菜单
	// 里 Shift 永久失效（只能重启应用恢复）。
	checkShiftToggleAccounting(fsys, &problems)

	// 4g. reset 语义分层（2026-11-05）：三端壳都必须区分「继续打字」与
	// 「会话结束」两种收尾。用错会让引擎忘记刚上屏的词，后续候选失去
	// bigram 上下文加成（实测：学过「我们→时」后输入 shi，带 prev 首候选
	// 是「时」，清掉后退回「是」）。
	checkResetSemantics(fsys, &problems)

	// 4h. 浮窗 owner 契约（2026-11-05）：候选窗/提示条只能以**本进程的前台窗口**
	// 为 owner。带 owner 的 TOPMOST 窗口会把 owner 所在窗口组抬到前面，用
	// TSF 上下文推出来的窗口当 owner 时，用户会看到「弹到别的窗口但焦点不动」。
	checkPopupOwner(fsys, &problems)

	// 4i. UILess 能力（2026-11-05）：必须实现 ITfTextInputProcessorEx，
	// 否则 UILess 宿主里 TIP 完全不被激活；但在实现 ITfUIElementMgr 之前
	// 不得注册 UIELEMENTENABLED 类别。
	checkUILessCapability(fsys, &problems)

	// 4j. 焦点兜底（2026-11-05）：开始菜单/搜索不投递 OnSetFocus(FALSE)，
	// 必须用前台窗口事件兜底清理，否则重开后上一轮拼音与候选窗还在。
	checkForegroundFallback(fsys, &problems)

	// 4k. 模式指示器（2026-11-05）：中英模式必须同步到 TSF 线程隔间，
	// 由 Windows 输入指示器显示「中/英」，而不是自建托盘图标。
	checkModeIndicator(fsys, &problems)

	// 4l. 崩溃日志只记错误级异常（2026-11-05）：RPC/COM 首次异常不算崩溃。
	checkCrashLogFilter(fsys, &problems)

	// 4m. 输入法激活判据（2026-11-05）：非激活时必须整键放行。
	checkActiveProfileGate(fsys, &problems)

	// 5. 打包/安装一致性：Linux 设置窗脚本、学习数据模块、小图标都要随包。
	checkContains(fsys, &problems, "打包安装", makefile, "chaoset-setup", "chaoset-macos.zip")
	checkContains(fsys, &problems, "打包安装", installSh, "chaoset-setup")
	checkContains(fsys, &problems, "打包安装", archScript, "chaoset-setup")
	checkContains(fsys, &problems, "打包安装", releaseYML, "chaoset-setup", "chaoset-macos.zip")
	checkContains(fsys, &problems, "打包安装", linuxComp, "chaoset-setup")
	checkContains(fsys, &problems, "打包安装", ciYML,
		"build.sh", "test_setup_ctl")
	// macOS：运行组件必须随 ChaosetIME.app 安装，且安装器/CLI 都从 app 内启动 daemon。
	checkContains(fsys, &problems, "打包安装", macBuild,
		"$APP/Contents/Resources/", "dict_common.tsv")
	checkContains(fsys, &problems, "打包安装", macSetup,
		"installedResource(\"chaosetd\")", "embeddedResource(\"chaoset-import\")", "installSetupApp")
	checkContains(fsys, &problems, "打包安装", macInstall,
		"$IM/ChaosetIME.app/Contents/Resources/chaosetd")
	checkWindowsInstallSafety(fsys, &problems)
	// 5b. 安装程序与 Windows 二进制的版本信息 / app 信息（2026-11-06）。
	checkWindowsVersionInfo(fsys, &problems)

	// daemon 单实例：安装器不能和 LaunchAgent 同时拉起 daemon。旧实现会在
	// LaunchAgent 慢启动时手动再 exec 一个 chaosetd，Activity Monitor 出现两个。
	checkContains(fsys, &problems, "daemon 单实例", macSetup,
		"waitLaunchdGone", "waitProcessGone", "只保留 launchd 一个 owner")
	checkExcludes(fsys, &problems, "daemon 单实例", macSetup,
		"已用已安装 app 内的 daemon 启动")
	checkContains(fsys, &problems, "daemon 单实例", macInstall,
		"killall chaosetd", "避免出现两个进程")
	checkContains(fsys, &problems, "daemon 单实例", "cmd/chaosetd/main.go", "runtime.GOOS")
	checkContains(fsys, &problems, "daemon 单实例", "cmd/chaosetd/singleton_windows.go",
		"CreateMutex", `Local\ChaosetDaemon`)
	checkContains(fsys, &problems, "daemon 单实例", linuxE2E, "chaosetd 单实例")

	checkExists(fsys, &problems, "打包安装", linuxTest)
	checkExists(fsys, &problems, "打包安装", linuxCtl)
	for _, size := range []string{"16x16", "22x22", "24x24", "32x32", "48x48", "64x64", "128x128", "256x256"} {
		checkExists(fsys, &problems, "打包安装",
			fmt.Sprintf("shims/linux/icons/hicolor/%s/apps/chaoset.png", size))
	}
	checkExists(fsys, &problems, "打包安装", "shims/linux/icons/hicolor/scalable/apps/chaoset.svg")

	// 6. 候选窗静态几何/配色字段（mac ↔ Windows）。
	checkCandidateWindow(fsys, &problems)

	// 7. CI 覆盖不退化：key_routing 按键路由回归测试必须存在且被 CI 调用。
	checkExists(fsys, &problems, "CI 覆盖", "shims/macos/tests/key_routing/run.sh")
	checkContains(fsys, &problems, "CI 覆盖", ciYML, "key_routing")
	// 联想端到端（EngineClient + 真实 daemon）同样必须存在且被 CI 调用。
	checkExists(fsys, &problems, "CI 覆盖", "shims/macos/tests/associate/run.sh")
	checkContains(fsys, &problems, "CI 覆盖", ciYML, "associate/run.sh")
	// 安装包自检：日常 CI 必须构建 Linux 包并断言设置窗模块随包，
	// 不能只靠打 tag 时的 release 冒烟。
	checkContains(fsys, &problems, "CI 覆盖", ciYML, "make package-linux", "Linux 安装包自检")
	// 同理：日常 macos-build 必须跑真实打包链（zip），不能只 build。
	checkContains(fsys, &problems, "CI 覆盖", ciYML, "make package-macos", "macOS 构建 + 安装包")
	// equivalence.sh B 段用 bin/chaoset-import：macOS job 必须先 make build。
	checkContains(fsys, &problems, "CI 覆盖", ciYML, "make build")
	// 断言用例数守卫：防止有人删断言让回归测试静默变弱；并校验 expectedChecks
	// 与实际 check( 数一致（加断言忘了同步会 FAIL）。
	checkContains(fsys, &problems, "CI 覆盖", macKeyTest, "expectedChecks")
	checkKeyRoutingCount(fsys, &problems)

	// 8. 鼠标点选候选：mac/Windows 自绘点选 + Linux ibus CandidateClicked 回引擎。
	// 此前 Linux 没有 CandidateClicked，面板点候选无反应（2026-10-31 补齐）。
	checkContains(fsys, &problems, "候选点选", macPanel, "mouseDown")
	checkContains(fsys, &problems, "候选点选", winShell, "WM_LBUTTONDOWN")
	checkContains(fsys, &problems, "候选点选", linuxEngine, "candidate_clicked")
	checkContains(fsys, &problems, "候选点选", uiParityDoc, "鼠标点选候选")
	// linux-e2e 必须真实验证 CandidateClicked（不只是单测）：防止 E2E 用例被删。
	checkContains(fsys, &problems, "候选点选", linuxE2E, "CandidateClicked", "CommitText")

	// 8b. 候选窗右键「删除该词」：各层入口与文案一致（2026-11-01）。
	checkCandidateDelete(fsys, &problems)

	// 9. shell 变量转义（macOS bash 3.2 + UTF-8 locale 的 unbound variable 坑）。
	checkShellVarEscaping(fsys, &problems)

	// 10. ui-parity 关键差异行必须保留（防止文档被删后失去对照基准）。
	checkContains(fsys, &problems, "文档对照", uiParityDoc,
		"学习数据管理", "词库导入", "设置窗单例", "Windows per-monitor DPI",
		"双拼 mspy", "无障碍", "鼠标点选候选", "滚轮翻页")
	// 历史阶段记录已移入 CHANGELOG（README 只保留当前文档）；历史条目
	// 不能留下「Linux 仍只有 CLI」这类已过期的现状描述，必须带「阶段 54 已补齐」。
	checkContains(fsys, &problems, "文档对照", changelogDoc, "阶段 54",
		"Linux 设置窗补「词库导入」GUI")
	checkExcludes(fsys, &problems, "文档对照", readmeDoc,
		"Linux 目前仍只有 CLI", "GTK 设置窗导入待后续")

	return problems
}

// checkShiftToggleAccounting 锁住三端 Shift 空按的「记账」契约：
//   - Windows 必须用幂等位掩码（shiftHeld_）而不是 ++/-- 计数器；
//   - Windows 的 Deactivate 与 OnSetFocus(FALSE) 必须调 resetShiftTracking；
//   - 必须有物理状态自愈（GetAsyncKeyState），兑现 README 承诺的兜底；
//   - Linux 必须保留 shift_.reset() 的 focusIn/focusOut 复位；
//   - macOS 必须保留 shiftArmed 复位路径；
//   - ui-parity 必须保留「Shift 空按」行。
//
// 这些断言都对应真实发生过的缺陷（Windows 计数器只增不减 → 永久失效）。
func checkShiftToggleAccounting(fsys fs.FS, problems *[]string) {
	const group = "Shift 记账"
	// Windows：必须是幂等位掩码 + 焦点/停用复位 + 物理状态自愈。
	checkContains(fsys, problems, group, winShell,
		"shiftHeld_",
		"resetShiftTracking",
		"shiftPhysicallyDown",
		"GetAsyncKeyState",
		"reconcileShiftState",
	)
	// 必须**纯事件驱动**：输入法不该靠常驻定时器轮询键盘状态。每个宿主进程
	// 一个 40ms 定时器开销实打实，还会误判快速轻按与 Shift+点击。
	// 2026-11-05：曾加过 GetKeyState 轮询兜底，确认真因是「焦点抖动吃掉按住
	// 状态」后已撤除，这里锁死不得回归。
	checkExcludes(fsys, problems, group, winShell,
		"onShiftPollTick", "ensureShiftPollWindow", "g_livePollWindows")
	// 焦点抖动只能挂起、不能丢弃按住状态（否则浏览器里 Shift 永远切不了）。
	checkContains(fsys, problems, group, winShell,
		"suspendShiftHoldOnFocusLost",
		"shiftHoldSuspended_",
		"beginShiftHold", "endShiftHold", "finishShiftHold",
	)
	// 失焦「挂起」不得被焦点分支末尾无条件清掉：必须在获得焦点分支里清
	//（2026-11-05 回归审计 P1-1：把 2676 行的无条件下移进 TRUE 分支）。
	checkContains(fsys, problems, group, winShell,
		"if (fForeground != FALSE) {\n            shiftHoldSuspended_ = false;")
	// 丢 KeyUp 的自愈必须在 Test 与 Key 两个阶段都可达（Chrome/conhost 等内容键
	// 只进 Key 阶段；2026-11-05 回归审计 P2-11）。定义 1 + 调用 2 = 3 处。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "reconcileShiftState("); n < 3 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：reconcileShiftState( 只有 %d 处（含定义），应在 Test 与 Key 两个阶段都调用",
				group, n))
		}
	}
	// 旧实现的致命写法不得回归：按事件累加的计数器。
	checkExcludes(fsys, problems, group, winShell,
		"++shiftDown_", "--shiftDown_")
	// **两阶段都必须跟踪**（2026-11-05 实机 tsf.log 实证）：宿主分两类——
	// Firefox 把修饰键送进 Test 阶段，Chrome/conhost/Windows Terminal 只送
	// Key 阶段（OnKeyDown/OnKeyUp 有、OnTestKeyDown 无）。只挂一边等于对
	// 另一类宿主彻底失效，表现为「能打字但 Shift 切不了中英」。
	// 状态机幂等，两阶段各投递一次不会双计。
	// 英文模式的放行判断必须同时出现在**预告点**（OnTestKeyDown）与
	// **实际处理点**（handleKey）：Chrome/conhost 等宿主的内容键也走 Key 阶段，
	// 只写预告点等于英文模式对它们无效（实机：切换后仍在出拼音候选）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "if (enMode_)"); n < 2 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 中 if (enMode_) 只有 %d 处，应在 OnTestKeyDown 与 handleKey 各一处",
				group, winShell, n))
		}
	}
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		// 4 处调用（Test/Key × 按下/释放）+ 1 处函数定义
		if n := strings.Count(string(data), "onShiftEvent("); n < 5 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 中 onShiftEvent 只有 %d 处（含定义），应为 5 处——"+
					"Test 与 Key 两个按键阶段都必须跟踪修饰键", group, winShell, n))
		}
	}
	// Linux：焦点进出必须复位 Shift 跟踪状态。
	checkContains(fsys, problems, group, linuxEngine,
		"void reset() { shiftDown = 0; sinceShift = false; }",
		"void focusIn() { shift_.reset(); }")
	// macOS：出现其它按键必须清 shiftArmed（否则 Shift+字母会误切换）。
	checkContains(fsys, problems, group, "shims/macos/src/ChaosetController.swift",
		"shiftArmed = false", "shiftArmed = true")
	// 文档基准行不得被删。
	checkContains(fsys, problems, group, uiParityDoc, "Shift 空按")
}

// checkResetSemantics 锁住「继续打字」与「会话结束」两类收尾在三端的分层：
//   - daemon 必须同时提供 reset（清上下文）与 resetComposition（保留）；
//   - 三端壳在切中英/透传等继续打字的路径必须能走到轻量版；
//   - 会话结束（失焦/停用/focusOut）仍必须用完整 reset。
func checkResetSemantics(fsys fs.FS, problems *[]string) {
	const group = "reset 语义"
	// daemon 侧两条链路都要在。
	checkContains(fsys, problems, group, ipcServer,
		"case \"reset\":", "case \"resetComposition\":")
	// Windows：两个入口都在，轻量版被真正调用。
	checkContains(fsys, problems, group, winShell,
		"void resetSession()", "void resetComposition()",
		"session_.resetComposition()")
	// macOS：EngineClient 暴露两个方法，控制器在继续打字路径用轻量版。
	checkContains(fsys, problems, group, "shims/macos/src/EngineClient.swift",
		"func reset()", "func resetComposition()", "\"resetComposition\"")
	checkContains(fsys, problems, group, "shims/macos/src/ChaosetController.swift",
		"engine.resetComposition()")
	// Linux：同样两个入口。
	checkContains(fsys, problems, group, linuxEngine,
		"void sessionReset()", "void sessionResetComposition()",
		"resetComposition()")
	// 文档必须记录这个区别，否则后来者会误用。
	checkContains(fsys, problems, group, readmeDoc, "resetComposition")
	checkContains(fsys, problems, group, uiParityDoc, "resetComposition")
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
	fmt.Println("UI 一致性契约检查通过（设置窗文案/单例/学习数据 GUI/词库导入/导入安全/联想即时性/退格键语义/修饰键映射/候选点选/候选右键删除/单例 UI 宿主与模式指示器/打包安装/Windows 版本信息/候选窗几何/CI 覆盖/shell 转义/文档对照）")
}
