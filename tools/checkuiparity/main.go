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
//     Windows per-monitor DPI/双拼 mspy 等）；
//  7. 候选窗几何/配色字段静态对齐：mac CandidatePanel.swift ↔ Windows
//     win32.cpp 的布局 pt 常量（字号/padding/gap/键帽/齿轮区/最大宽度）、
//     齿轮矢量参数（圆环半径/线宽、8 齿内/外半径、线宽、颜色绑定）、
//     13 个主题色字段名集合一致。**像素级渲染仍需真机**，但「常量被改一端」
//     会在这里先 FAIL；
//  8. CI 覆盖不退化：key_routing 回归测试必须存在且被 ci.yml 的 macos-build
//     调用（审计 G7：它曾长期是未接入 CI 的「死测试」）；
//  9. shell 脚本里 `$VAR` 不得紧跟非 ASCII 字符（macOS bash 3.2 在 UTF-8
//     locale 下会把多字节字符并入变量名 → unbound variable；2026-10-31
//     key_routing CI 首跑就栽在 `$ARCH）` 上，统一改用 `${VAR}`）。
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
}

const (
	macPanel    = "shims/macos/src/CandidatePanel.swift"
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

	// 6. 候选窗静态几何/配色字段（mac ↔ Windows）。
	checkCandidateWindow(fsys, &problems)

	// 7. CI 覆盖不退化：key_routing 按键路由回归测试必须存在且被 CI 调用。
	checkExists(fsys, &problems, "CI 覆盖", "shims/macos/tests/key_routing/run.sh")
	checkContains(fsys, &problems, "CI 覆盖", ciYML, "key_routing")

	// 8. shell 变量转义（macOS bash 3.2 + UTF-8 locale 的 unbound variable 坑）。
	checkShellVarEscaping(fsys, &problems)

	// 9. ui-parity 关键差异行必须保留（防止文档被删后失去对照基准）。
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
	fmt.Println("UI 一致性契约检查通过（设置窗文案/单例/学习数据 GUI/词库导入/打包安装/候选窗几何/CI 覆盖/shell 转义/文档对照）")
}
