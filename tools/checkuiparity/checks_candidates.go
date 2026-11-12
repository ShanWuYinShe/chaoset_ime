// checks_candidates.go：候选窗契约（几何/齿轮/分割线/配色/每页数量/点选/右键删除）。

package main

import (
	"fmt"
	"io/fs"
	"math"
	"regexp"
	"sort"
	"strings"
)

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
	// Windows 侧锚定齿轮绘制块：拆分后 winShell 是聚合文本，别的模块也有
	// for (int i = 0; i < N; ++i)（如 daemon 重试 40 次），不能取全文第一个。
	{"辐条数", `for i in 0\.\.<([0-9]+)`, `(?s)const COLORREF gearColor = theme\.chipDigit;.*?for \(int i = 0; i < ([0-9]+); \+\+i\)`},
	{"辐条内半径", `spokes\.move\(to: NSPoint\(x: center\.x \+ dx \* ([0-9.]+)`, `(?s)MoveToEx\(memDC,\s*static_cast<int>\(cx \+ dx \* ([0-9.]+) \* u`},
	{"辐条外半径", `spokes\.line\(to: NSPoint\(x: center\.x \+ dx \* ([0-9.]+)`, `(?s)LineTo\(memDC,\s*static_cast<int>\(cx \+ dx \* ([0-9.]+) \* u`},
	{"辐条线宽", `spokes\.lineWidth = ([0-9.]+)`, `createRoundCapPen\(static_cast<int>\(([0-9.]+) \* u \+ 0\.5\), gearColor\)`},
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

func allPresent(values map[string]float64, names []string) bool {
	for _, n := range names {
		if _, ok := values[n]; !ok {
			return false
		}
	}
	return true
}

// checkCandidateDelete 候选窗右键「删除该词」三端/全链路静态契约（2026-11-01）。
//
// 为什么静态锁：删除是「写学习数据」的 UI 入口，漏接线不会编译失败——mac
// 忘记设 onDelete 闭包、Windows 忘记映射 WM_RBUTTONUP、daemon 忘记分发
// userDelete、CLI 没接 delete 子命令，都会「看起来有菜单但点了没反应」。
// 这里把各层入口关键字与三端文案锁定，改一端漏同步先 FAIL；实机点击/焦点
// 行为（菜单不抢焦点、右键不上屏）仍需人工验收。
func checkCandidateDelete(fsys fs.FS, problems *[]string) {
	const group = groupCandidateDelete
	// macOS：候选面板右键菜单 + 控制器接线 + EngineClient 写调用。
	checkContains(fsys, problems, group, macPanel,
		"var onDelete", "menu(for event: NSEvent)", "删除该词")
	checkContains(fsys, problems, group, macController,
		"onDelete", "deleteCandidate", "deleteAssociateCandidate", "userDelete",
		"recompose", "associateSylls")
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
	// 三端文案逐字一致：菜单文案在两端壳内；失败提示保留，**删除成功静默**
	// （用户反馈「已删除该词」成功提示多余）：只刷新候选窗，不弹 toast。
	for _, text := range []string{"未找到该词的学习记录", "删除失败：daemon 不可达"} {
		checkContains(fsys, problems, group, macController, text)
		checkContains(fsys, problems, group, winShell, text)
	}
	checkExcludes(fsys, problems, group, macController, "已删除该词")
	checkExcludes(fsys, problems, group, winShell, "已删除该词")
	// Linux：ibus 面板不适用，必须记录理由与替代入口，禁止假装已支持。
	checkContains(fsys, problems, group, linuxReadme,
		"右键删除", "不适用", "chaoset-userdict delete")
	// 文档：语义/入口/文案必须有对照，防止文档悄悄变成「三端都支持」。
	checkContains(fsys, problems, group, uiParityDoc,
		"学习数据按词删除", "右键删除", "删除该词", "chaoset-userdict delete")

	// ---- issue #1（2026-11-12）：右键菜单 owner 安全契约 ----
	// 根因：对 WS_EX_NOACTIVATE 候选窗调用 SetForegroundWindow 会程序化激活它
	//（微软文档明确），候选窗与宿主文档同进程/同线程，宿主文档因此失去 TSF 键盘
	// 焦点 → 按键闸门关闭（打字变英文、Shift 失效）、合成被外部终止（拼音留在
	// 文档）。锁死：菜单 owner 必须来自 TIP 返回的**本进程宿主窗口**（前台窗口
	// 优先）；onRButtonUp 内不得再对候选窗自身 SetForegroundWindow；菜单返回后
	// 必须重校验 tip_/窗口/index 再删除（B1 重入防护）。
	checkContains(fsys, problems, group, winShell,
		"virtual HWND menuOwnerWindow() = 0;",
		"HWND menuOwnerWindow() override;",
		"HWND ChaosetTIP::menuOwnerWindow()",
		"GetForegroundWindow()",
		"GetWindowThreadProcessId(",
		"pid == GetCurrentProcessId()",
		"contextHostWindow(",
		"composeHostWnd_")
	winSrc, winErr := readText(fsys, winShell)
	if winErr != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, winErr))
	} else if body, _, ok := cppFunctionBody(winSrc, "void CandidateWindow::onRButtonUp(int x, int y)"); !ok {
		*problems = append(*problems, group+"：无法抽取 CandidateWindow::onRButtonUp 函数体（实现可能已重构，需同步更新检查器）")
	} else {
		code := noSpace(stripCppComments(body))
		if strings.Contains(code, "SetForegroundWindow(hwnd_)") {
			*problems = append(*problems, group+"：onRButtonUp 不得对候选窗自身调用 SetForegroundWindow（会激活 WS_EX_NOACTIVATE 候选窗、夺走宿主 TSF 焦点，issue #1 根因）")
		}
		for _, needle := range []string{
			"host->menuOwnerWindow()",
			"SetForegroundWindow(owner)",
			"TrackPopupMenu(",
			"PostMessageW(owner,WM_NULL",
			"host!=tip_",
			"IsWindow(hwnd_)",
			"deleteCandidateByIndex(index)",
		} {
			if !strings.Contains(code, needle) {
				*problems = append(*problems, group+"：onRButtonUp 缺少 "+needle)
			}
		}
		nullIdx := strings.Index(code, "owner==nullptr")
		delIdx := strings.Index(code, "deleteCandidateByIndex(index)")
		if nullIdx < 0 {
			*problems = append(*problems, group+"：onRButtonUp 必须处理 menuOwnerWindow() 返回空（不弹菜单、不删除）")
		} else if delIdx >= 0 && nullIdx > delIdx {
			*problems = append(*problems, group+"：onRButtonUp 的空 owner 分支必须在删除调用之前（安全优先）")
		}
	}

	// ---- 独立 review 加固（M1/M3）：函数体级断言（只查聚合串会漏掉「整段删除/优先级反转」）----
	if winErr == nil {
		if body, _, ok := cppFunctionBody(winSrc, "HWND ChaosetTIP::menuOwnerWindow()"); !ok {
			*problems = append(*problems, group+"：无法抽取 ChaosetTIP::menuOwnerWindow 函数体（issue #1 菜单 owner 契约失效）")
		} else {
			code := noSpace(stripCppComments(body))
			fgIdx := strings.Index(code, "GetForegroundWindow()")
			ctxIdx := strings.Index(code, "contextHostWindow(")
			composeIdx := strings.Index(code, "composeHostWnd_")
			if fgIdx < 0 || ctxIdx < 0 || composeIdx < 0 || !(fgIdx < ctxIdx && ctxIdx < composeIdx) {
				*problems = append(*problems, group+"：menuOwnerWindow 的 owner 优先级必须是「本进程前台窗口 → TSF 上下文宿主窗口 → composeHostWnd_」（顺序反转/缺项会让菜单抢宿主焦点，issue #1 根因）")
			}
			if !strings.Contains(code, "candidateWnd_.hwnd()") {
				*problems = append(*problems, group+"：menuOwnerWindow 必须显式排除候选窗自身（candidateWnd_.hwnd()）")
			}
			if !strings.Contains(code, "returnnullptr") {
				*problems = append(*problems, group+"：menuOwnerWindow 取不到本进程宿主窗口时必须返回 nullptr（不弹菜单，安全优先）")
			}
		}
	}
	// M1：OnCompositionTerminated 的残留拼音清理是 issue#1 症状 b 的最后保护，必须
	// 「先 GetText 比对、内容一致才 SetText 保留已确认段」，整段删除/判据反转都要 FAIL。
	if winErr == nil {
		if body, _, ok := cppFunctionBody(winSrc, "ChaosetTIP::OnCompositionTerminated("); !ok {
			*problems = append(*problems, group+"：无法抽取 OnCompositionTerminated 函数体（合成终止清理契约失效）")
		} else {
			code := noSpace(stripCppComments(body))
			getIdx := strings.Index(code, "GetText(")
			setIdx := strings.Index(code, "SetText(ec,0,keep.c_str()")
			if getIdx < 0 || setIdx < 0 || getIdx > setIdx {
				*problems = append(*problems, group+"：OnCompositionTerminated 必须先 GetText 取合成文本、内容一致后再 SetText 保留已确认段")
			}
			for _, needle := range []string{"GetRange(", "actual==wideExpected", "pComposition==composition_", "confirmedPreedit_"} {
				if !strings.Contains(code, needle) {
					*problems = append(*problems, group+"：OnCompositionTerminated 清理缺少 "+needle+"（内容比对/指针身份守卫/已确认段保留）")
				}
			}
			if strings.Contains(code, "EndComposition(") {
				*problems = append(*problems, group+"：OnCompositionTerminated 不得调用 EndComposition（合成已被 TSF 终止）")
			}
		}
	}
}

// checkCandidateGearMouseUp 锁住 2026-11-06 用户要求：候选栏设置齿轮必须
// 鼠标抬起（mouseUp）才打开设置窗，按下（mouseDown）只记录命中；候选词点选
// 仍是按下即选。按下即弹窗属误触，禁止回归。Linux ibus 面板无齿轮，必须在
// README 记录不适用理由与替代入口。
func checkCandidateGearMouseUp(fsys fs.FS, problems *[]string) {
	const group = groupCandidateGearMouseUp
	checkContains(fsys, problems, group, macPanel,
		"settingsPressed", "override func mouseUp(with event: NSEvent)", "hitArea -3")
	checkContains(fsys, problems, group, winShell,
		"gearPressed_", "void CandidateWindow::onLButtonUp(",
		"void onLButtonUp(int x, int y);")
	checkContains(fsys, problems, group, linuxReadme,
		"候选栏设置齿轮", "齿轮不适用")
	checkContains(fsys, problems, group, uiParityDoc,
		"齿轮触发时机", "mouseUp 仍在齿轮区才打开设置窗")

	// macOS：mouseDown 函数体内不允许出现 onSettings?()（必须留给 mouseUp）。
	if data, err := fs.ReadFile(fsys, macPanel); err == nil {
		text := string(data)
		start := strings.Index(text, "override func mouseDown(with event: NSEvent) {")
		end := strings.Index(text, "override func mouseUp(with event: NSEvent) {")
		if start < 0 || end < 0 || end <= start {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 mouseDown/mouseUp 覆盖（齿轮必须先记录后抬起触发）", group, macPanel))
		} else if strings.Contains(text[start:end], "onSettings?()") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 mouseDown 仍直接调用 onSettings?()——齿轮必须在 mouseUp 才打开设置", group, macPanel))
		}
	}
	// Windows：onLButtonDown 函数体内不允许 showSettingsWindow（只允许 onLButtonUp）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		start := strings.Index(text, "void CandidateWindow::onLButtonDown(int x, int y) {")
		end := strings.Index(text, "void CandidateWindow::onLButtonUp(int x, int y) {")
		if start < 0 || end < 0 || end <= start {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 CandidateWindow::onLButtonDown/onLButtonUp 定义", group, winShell))
		} else if strings.Contains(text[start:end], "showSettingsWindow(") {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的 onLButtonDown 仍直接 showSettingsWindow()——齿轮必须在 mouseUp 才打开设置", group, winShell))
		}
	}
}

// checkCandidateMouseUpSemantics 锁住 2026-11-06 用户要求：候选窗左键的全部
// 动作（候选上屏 / 翻页 / 打开设置）都必须在鼠标抬起时触发；按下只记录命中，
// 抬起点仍在同一目标上才执行（按下后拖走再松开不触发）。此前候选词是按下即
// 上屏，用户明确要求改为鼠标抬起。
func checkCandidateMouseUpSemantics(fsys fs.FS, problems *[]string) {
	const group = groupCandidateMouseUp
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		down, _, ok := cppFunctionBody(text, "void CandidateWindow::onLButtonDown(int x, int y)")
		if !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 CandidateWindow::onLButtonDown", group, winShell))
		} else {
			body := stripCppComments(down)
			for _, bad := range []string{
				"selectCandidateByIndex(", "flipPage(", "flipAssociateFromPanel(", "showSettingsWindow(",
			} {
				if strings.Contains(body, bad) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 onLButtonDown 仍直接调用 %s（左键动作必须在 mouseUp 触发）",
						group, winShell, bad))
				}
			}
			if !strings.Contains(body, "pressedItemIndex_") || !strings.Contains(body, "gearPressed_") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 onLButtonDown 未记录 pressedItemIndex_/gearPressed_", group, winShell))
			}
		}
		up, _, ok := cppFunctionBody(text, "void CandidateWindow::onLButtonUp(int x, int y)")
		if !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 CandidateWindow::onLButtonUp", group, winShell))
		} else {
			body := stripCppComments(up)
			for _, want := range []string{
				"selectCandidateByIndex(", "showSettingsWindow(", "flipPage(", "flipAssociateFromPanel(",
			} {
				if !strings.Contains(body, want) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 onLButtonUp 未执行 %s（左键动作须在抬起时触发）",
						group, winShell, want))
				}
			}
		}
	}
	if data, err := fs.ReadFile(fsys, macPanel); err == nil {
		text := string(data)
		region := func(signature string) (string, bool) {
			i := strings.Index(text, signature)
			if i < 0 {
				return "", false
			}
			rest := text[i+len(signature):]
			if j := strings.Index(rest, "\n    override func "); j >= 0 {
				return rest[:j], true
			}
			return rest, true
		}
		if body, ok := region("override func mouseDown(with event: NSEvent) {"); !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 mouseDown", group, macPanel))
		} else {
			for _, bad := range []string{"onSelect?(", "onPageDown?()", "onPageUp?()", "onSettings?()"} {
				if strings.Contains(body, bad) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 mouseDown 仍直接调用 %s（左键动作必须在 mouseUp 触发）",
						group, macPanel, bad))
				}
			}
			if !strings.Contains(body, "pressedIndex") || !strings.Contains(body, "settingsPressed") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 mouseDown 未记录 pressedIndex/settingsPressed", group, macPanel))
			}
		}
		if body, ok := region("override func mouseUp(with event: NSEvent) {"); !ok {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 mouseUp", group, macPanel))
		} else {
			for _, want := range []string{"onSelect?(", "onSettings?()", "onPageDown?()", "onPageUp?()"} {
				if !strings.Contains(body, want) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 mouseUp 未派发 %s", group, macPanel, want))
				}
			}
		}
	}
}

// checkCandidateClick 鼠标点选候选：mac/Windows 自绘点选 + Linux ibus CandidateClicked
// 回引擎；linux-e2e 必须真实验证 CandidateClicked（不只是单测）。
func checkCandidateClick(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupCandidateClick, macPanel, "mouseDown", "mouseUp", "pressedIndex")
	checkContains(fsys, problems, groupCandidateClick, winShell, "WM_LBUTTONDOWN", "WM_LBUTTONUP", "pressedItemIndex_")
	checkContains(fsys, problems, groupCandidateClick, linuxEngine, "candidate_clicked")
	checkContains(fsys, problems, groupCandidateClick, uiParityDoc, "鼠标点选候选")
	// linux-e2e 必须真实验证 CandidateClicked（不只是单测）：防止 E2E 用例被删。
	checkContains(fsys, problems, groupCandidateClick, linuxE2E, "CandidateClicked", "CommitText")
}

// checkCandidateGeometry 候选窗布局 pt 常量、行高/内容上限公式、多行折行与无障碍（UIA）基础能力。
func checkCandidateGeometry(fsys fs.FS, problems *[]string) {
	const group = groupCandidateGeometry
	values := checkNumericContracts(fsys, problems, group, layoutContracts)
	macText := mustRead(fsys, macPanel)
	winText := mustRead(fsys, winShell)
	if fontSize, ok := values["正文字号"]; ok {
		if factor, ok := firstFloat(macText, `static let textH: CGFloat = ceil\(fontSize \* ([0-9.]+)\)`); ok {
			want := math.Ceil(fontSize * factor)
			if got, ok := firstFloat(winText, `static_cast<int>\(tmText\.tmHeight\), pt\(([0-9.]+), dpi\)`); ok {
				if math.Abs(got-want) > 1e-6 {
					*problems = append(*problems, fmt.Sprintf(group+"：行高 textH mac=ceil(%g*%g)=%g Windows=%g 不一致", fontSize, factor, want, got))
				}
			} else {
				*problems = append(*problems, group+"：无法从 Windows 解析 textH 行高常量")
			}
		} else {
			*problems = append(*problems, group+"：无法从 mac 解析 textH 公式")
		}
	}
	// Windows 的 kMaxContentPt 是内联表达式（kMaxWidthPt - 7*2 - 36 - 26 - 2*2 - 2），
	// 不是字面量；这里按 mac 常量重建表达式并做「去空白子串」比对。
	need := []string{"内边距", "翻页区宽", "齿轮区宽", "首选胶囊外扩"}
	if allPresent(values, need) {
		expect := fmt.Sprintf("constexprintkMaxContentPt=kMaxWidthPt-%g*2-%g-%g-%g*2-2;",
			values["内边距"], values["翻页区宽"], values["齿轮区宽"], values["首选胶囊外扩"])
		if !strings.Contains(noSpace(winText), expect) {
			*problems = append(*problems, fmt.Sprintf(group+"：Windows 内容上限公式与 mac 常量不一致（期望 %s）", expect))
		}
	}
	// 多行折行：Windows 2026-10-31 起与 mac 同算法（纯布局函数可在 macOS 单测），
	// 断言头文件被 win32.cpp 引用，防止退回单行布局。
	checkContains(fsys, problems, groupCandidateGeometry, winShell, "candidate_layout.hpp",
		"wrapRows(boxes, maxContent, gap, contentH, kMaxRows)",
		"const int lineSpacing = pt(4, dpi)")
	// 无障碍基础能力不能退化：候选窗必须处理 WM_GETOBJECT 并动态加载 UIA。
	checkContains(fsys, problems, groupCandidateGeometry, winShell,
		"WM_GETOBJECT", "UiaRootObjectId", "uiautomationcore.dll")
	// UIA 必须动态加载：构建脚本断言 DLL 没有 uiautomationcore 静态导入。
	checkContains(fsys, problems, groupCandidateGeometry, winBuild,
		"uiautomationcore", "静态导入")
}

// checkCandidateGearGeometry 齿轮矢量参数（圆环/8 齿半径/线宽）与颜色绑定两端一致。
func checkCandidateGearGeometry(fsys fs.FS, problems *[]string) {
	checkNumericContracts(fsys, problems, groupCandidateGear, gearContracts)
	checkContains(fsys, problems, groupCandidateGear, macPanel, "color: theme.chipDigit")
	checkContains(fsys, problems, groupCandidateGear, winShell, "const COLORREF gearColor = theme.chipDigit;")
}

// checkCandidateDivider 候选窗分割线位置两端一致（textH * 55 / 100）。
func checkCandidateDivider(fsys fs.FS, problems *[]string) {
	checkContains(fsys, problems, groupCandidateDivider, macPanel, "Layout.textH * 55 / 100")
	checkContains(fsys, problems, groupCandidateDivider, winShell, "textH * 55 / 100")
}

// checkThemeFieldParity 候选窗 13 个主题色字段名必须两端一致（任一新增/删除都会 FAIL）。
func checkThemeFieldParity(fsys fs.FS, problems *[]string) {
	const group = groupCandidateTheme
	macText, err := readText(fsys, macPanel)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, macPanel, err))
		return
	}
	winText, err := readText(fsys, winShell)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, winShell, err))
		return
	}
	macFields := structFields(macText, `(?s)struct CandidateTheme \{.*?\n\}`, `(?m)^\s*let ([A-Za-z0-9_]+): NSColor`)
	winFields := structFields(winText, `(?s)struct CandidateColors \{.*?\n\};`, `(?m)^\s*COLORREF ([A-Za-z0-9_]+);`)
	sort.Strings(macFields)
	sort.Strings(winFields)
	if strings.Join(macFields, ",") != strings.Join(winFields, ",") {
		*problems = append(*problems, fmt.Sprintf("%s：主题色字段集合不一致（mac=%v Windows=%v）", group, macFields, winFields))
	}
	if len(macFields) != 13 {
		*problems = append(*problems, fmt.Sprintf("%s：mac 主题色字段数 = %d，期望 13", group, len(macFields)))
	}
}

// checkCandidateWindow 候选窗静态几何/配色字段一致性（mac ↔ Windows）组合入口。
// 像素级渲染仍需真机；这里锁住「常量被改一端」与主题色字段集合漂移。
//
// Run 通过注册表分别执行四个子组（候选窗几何/齿轮/分割线/配色），让成功输出能逐组
// 列出实际运行内容；本组合入口保留给变异测试，避免单测跟着注册表改动。
func checkCandidateWindow(fsys fs.FS, problems *[]string) {
	checkCandidateGeometry(fsys, problems)
	checkCandidateGearGeometry(fsys, problems)
	checkCandidateDivider(fsys, problems)
	checkThemeFieldParity(fsys, problems)
}

// checkCandidatePageSize 「每页候选数」三端与引擎一致（2026-11-08）。
//
// 唯一真源是 engine/session.go 的 pageSize（引擎按它分页，PageSize() 导出该口径；
// engine/api_contract_test.go 另有一道硬断言）。三端壳为了「联想候选本地分页 +
// 数字键 1..N 指位」各自维护了页容量常量，都是显式字面量、可静态精确定位：
//   - macOS  shims/macos/src/ChaosetController.swift  associatePageSize（KeyRouter 复用）；
//   - Windows shims/windows/src/win32.cpp             kPageSize；
//   - Linux  shims/linux/engine/main.cpp              kPageSize（ibus LookupTable 页大小）。
//
// 任一端与引擎漂移都会让数字键越界后「既不选词也不进文档」，或联想翻页错位
// （pageSize 曾由 9 改成 6，当时壳仍接受 1-9）。三端因此全部断言；若将来某端改成
// 运行时从引擎 PageSize() 推导（不再有本地常量），应把断言改成「该端不得再定义
// 本地页容量常量」而不是删除本组。
func checkCandidatePageSize(fsys fs.FS, problems *[]string) {
	const group = groupCandidatePageSize
	session := mustRead(fsys, "engine/session.go")
	engineSize, ok := firstInt(session, `(?m)^\s*pageSize = ([0-9]+)\s*$`)
	if !ok {
		*problems = append(*problems, group+"：无法从 engine/session.go 解析 pageSize 常量（实现可能已重构，需同步更新检查器）")
		return
	}
	if !strings.Contains(session, "func PageSize() int { return pageSize }") {
		*problems = append(*problems, group+"：engine/session.go 缺少 func PageSize() int { return pageSize }（壳依赖该导出口径判定数字键范围）")
	}
	ends := []struct {
		name string
		path string
		re   string
	}{
		{"macOS", macController, `static let associatePageSize = ([0-9]+)`},
		{"Windows", winShell, `constexpr int kPageSize = ([0-9]+);`},
		{"Linux", linuxEngine, `constexpr guint kPageSize = ([0-9]+);`},
	}
	for _, e := range ends {
		got, ok := firstInt(mustRead(fsys, e.path), e.re)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s：无法从 %s 解析 %s 每页候选数（实现可能已重构，需同步更新检查器）", group, e.path, e.name))
			continue
		}
		if got != engineSize {
			*problems = append(*problems, fmt.Sprintf("%s：%s 每页候选数 = %d，但引擎 pageSize = %d（数字键指位/联想翻页会错位）", group, e.name, got, engineSize))
		}
	}
}

// checkCandidatePageArrows 双翻页箭头契约（issue#2，2026-11-12）：
//  1. 首页只显示/可用下箭头、末页只显示/可用上箭头、中间页两枚都有；
//     单页没有翻页动作（mac/Windows 都画置灰双箭头且不注册命中区）；
//  2. 两枚箭头命中区分开：Windows chevronUpRect_/chevronDownRect_，
//     macOS hitAreas -2（上）/ -1（下），点击分别翻上一页/下一页；
//  3. 禁止退回旧「单箭头 dirDown = hasNext || !hasPrev 二选一」实现——
//     那正是用户抱怨「中间页看不到上箭头」的根因。
//
// 像素级渲染仍需真机；这里锁住可用性判断被删、命中区合并、单箭头回归。
func checkCandidatePageArrows(fsys fs.FS, problems *[]string) {
	const group = groupCandidatePageArrows
	// Windows：双箭头命中矩形 + 可用性条件 + 两方向分别绘制。
	checkContains(fsys, problems, group, winShell,
		"chevronUpRect_", "chevronDownRect_",
		"if (hasPrev) drawChevron(false, upCy);",
		"if (hasNext) drawChevron(true, downCy);",
		"comp_.page > 0", "comp_.page + 1 < comp_.pageCount",
		"chevronPressed_ = -1", "chevronPressed_ = 1",
	)
	// Windows：onLButtonUp 必须按记录的箭头方向翻页，不能又合并成单方向。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		if body, _, ok := cppFunctionBody(text, "void CandidateWindow::onLButtonUp(int x, int y)"); ok {
			code := stripCppComments(body)
			if !strings.Contains(code, "wasChevron > 0") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 onLButtonUp 未按上/下箭头区分翻页方向（chevronPressed_ 需区分 -1/+1）",
					group, winShell))
			}
		} else {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 CandidateWindow::onLButtonUp", group, winShell))
		}
	}
	// macOS：两枚箭头 + 可用性 + 分开的命中区（-2 上 / -1 下）。
	checkContains(fsys, problems, group, macPanel,
		"if hasPrev {", "if hasNext {",
		// 多页分支两枚箭头必须一上一下（单页置灰分支用 dimColor，不参与）。
		"color: theme.chevron, dirDown: false",
		"color: theme.chevron, dirDown: true",
		"height: half), -2", "height: blockH - half), -1",
	)
	// 旧单箭头实现不得回归（两端都查）。
	checkExcludes(fsys, problems, group, winShell, "dirDown = hasNext || !hasPrev")
	checkExcludes(fsys, problems, group, macPanel, "let dirDown = hasNext || !hasPrev")
	// 单页必须显式处理（两端都要有分支，不允许「单页也画可点箭头」）。
	checkContains(fsys, problems, group, winShell, "pageCount <= 1")
	checkContains(fsys, problems, group, macPanel, "pageCount <= 1")
	// Linux 用 ibus 原生面板，不自绘箭头：README 必须记录「双箭头可用性不可控」
	// 与方向键语义，禁止假装三端自绘一致。
	checkContains(fsys, problems, group, linuxReadme,
		"双箭头可用性", "ibus 面板", "方向键预选")
	// 独立 review 加固（M2）：只查「dirDown: true/false 字符串存在」会漏掉首页/末页
	// 语义反转（把 if hasPrev 改成 if hasNext 仍能通过）。这里断言箭头绘制与可用性
	// 守卫的**关联**：上箭头由 hasPrev 守卫且 dirDown:false；下箭头由 hasNext 守卫且
	// dirDown:true。
	if data, err := fs.ReadFile(fsys, macPanel); err == nil {
		text := string(data)
		upArrow := regexp.MustCompile(`(?s)if hasPrev \{\s*drawChevron\([^\n]*dirDown: false`)
		downArrow := regexp.MustCompile(`(?s)if hasNext \{\s*drawChevron\([^\n]*dirDown: true`)
		if !upArrow.MatchString(text) {
			*problems = append(*problems, group+"：macOS 上箭头必须由 hasPrev 守卫且 dirDown: false（首页只有下箭头、末页只有上箭头，语义反转/删除会被抓）")
		}
		if !downArrow.MatchString(text) {
			*problems = append(*problems, group+"：macOS 下箭头必须由 hasNext 守卫且 dirDown: true（首页只有下箭头、末页只有上箭头）")
		}
		if !strings.Contains(text, "? arrowH * 2 + pageH : arrowH * 2") {
			*problems = append(*problems, group+"：macOS 单页也须按两枚箭头合计 12pt 居中（blockH 单页分支为 arrowH * 2，与 Windows 同公式）")
		}
	}
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if !strings.Contains(string(data), "? arrowH * 2 + flipPageH_ : arrowH * 2") {
			*problems = append(*problems, group+"：Windows 单页也须按两枚箭头合计 12pt 居中（flipBlockMetrics 单页分支为 arrowH * 2，与 macOS 同公式）")
		}
	}
}

// checkCandidateDirectionKeys 方向键契约（issue#2 + 用户反馈调整）：
//  1. 组词有候选 / 联想有候选时，**上/下 = 翻页**（上一页/下一页，翻页后
//     预选归零，边界页保持不动且仍消费）；**左/右 = 页内预选移动**
//     （前一项/后一项，页边界保持不动）；
//  2. 无组词/无候选时方向键必须放行宿主（绝不消费）——组词分支必须带
//     hasCandidates() 守卫，联想分支只在联想候选非空时消费；
//  3. 空格确认**页内预选项**（不再固定第 0 项），数字键仍按序号直选；
//  4. 预选状态生命周期：新输入/翻页/选词后归零（候选窗/面板 show 侧负责）；
//  5. 三端都要有：Windows TIP 按键路由、macOS KeyRouter、Linux ibus lookup cursor。
func checkCandidateDirectionKeys(fsys fs.FS, problems *[]string) {
	const group = groupCandidateDirectionKeys
	// Windows：键位常量、消费/放行判定、上/下翻页与左/右页内移动、空格确认预选项。
	checkContains(fsys, problems, group, winShell,
		"kVkLeft", "kVkUp", "kVkRight", "kVkDown",
		"bool ChaosetTIP::isArrowKey(WPARAM vk)",
		"isArrowKey(vk) && hasCandidates()",
		"isArrowKey(vk) && isComposing() && hasCandidates()",
		"moveCompositionSelection(", "moveAssociateSelection(",
		"flipCompositionPage(", "flipAssociatePage(",
		"flipCompositionPage(vk == kVkDown, pic)",
		"flipAssociatePage(vk == kVkDown)",
		"candidateWnd_.moveSelection(", "candidateWnd_.setSelectedIndex(",
		"item.index == selectedIndex_",
	)
	// Windows：handleKey 的空格分支必须读取预选序号，不能写死 0；
	// 预选 pill 必须按 selectedIndex_ 判定（不再固定 item.isFirst）。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		text := string(data)
		if body, _, ok := cppFunctionBody(text, "bool ChaosetTIP::handleKey(WPARAM vk, ITfContext* pic)"); ok {
			code := stripCppComments(body)
			if n := strings.Count(code, "candidateWnd_.selectedIndex()"); n < 2 {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 handleKey 中 candidateWnd_.selectedIndex() 只有 %d 处（联想态与组词态空格都要确认预选项）",
					group, winShell, n))
			}
			for _, bad := range []string{"selectPrefix(0, &rest)", "session_.select(0, &committed)"} {
				if strings.Contains(code, bad) {
					*problems = append(*problems, fmt.Sprintf(
						"%s：%s 的 handleKey 仍固定选第 0 项（%s）——空格应确认预选项",
						group, winShell, bad))
				}
			}
		} else {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 缺少 ChaosetTIP::handleKey", group, winShell))
		}
		// 无候选/无组词时方向键必须放行：组词分支的消费条件必须同时带
		// hasCandidates()（只有联想分支可以只看 assoc_.candidates 非空）。
		if body, _, ok := cppFunctionBody(text, "bool ChaosetTIP::wouldHandle(WPARAM vk) const"); ok {
			code := stripCppComments(body)
			if !strings.Contains(code, "isArrowKey(vk) && hasCandidates()") {
				*problems = append(*problems, fmt.Sprintf(
					"%s：%s 的 wouldHandle 未把方向键消费限制在 hasCandidates()（无候选时必须放行宿主）",
					group, winShell))
			}
		}
	}
	// 独立 review 加固（L2）：候选窗/面板不可见（全屏免打扰/建窗失败/渲染失败）时
	// 方向键不得被吞并触发不可见的翻页——消费条件必须带 itemCount 可见性守卫。
	if data, err := fs.ReadFile(fsys, winShell); err == nil {
		if n := strings.Count(string(data), "candidateWnd_.itemCount() > 0"); n < 2 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的方向键消费只有 %d 处带 candidateWnd_.itemCount() > 0（组词/联想两处都要：面板不可见时必须放行）",
				group, winShell, n))
		}
	}
	if data, err := fs.ReadFile(fsys, macPanel); err == nil {
		if n := strings.Count(string(data), "CandidatePanelController.itemCount > 0"); n < 2 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 的方向键消费只有 %d 处带 CandidatePanelController.itemCount > 0（组词/联想两处都要：面板不可见时必须放行）",
				group, macPanel, n))
		}
	}
	// macOS：KeyRouter 方向键路由（上/下翻页、左/右页内移动）+ 空格确认预选项 +
	// 候选面板的预选状态与 pill 跟随。
	checkContains(fsys, problems, group, macPanel,
		"arrowDirection", "ArrowDirection",
		"flipCompositionPage", "flipAssociatePage",
		"static var selectedIndex", "static func moveSelection",
		"CandidatePanelController.moveSelection(", "CandidatePanelController.setSelectedIndex(",
		"frame.index == selectedIndex",
		"isComposing, hasCandidates",
		"case 126: return .prevPage", "case 125: return .nextPage",
		"case 123: return .prevItem", "case 124: return .nextItem",
	)
	// 联想态方向键必须由联想分支消费（两个 arrowDirection 调用点：组词 + 联想）。
	if data, err := fs.ReadFile(fsys, macPanel); err == nil {
		if n := strings.Count(string(data), "Self.arrowDirection(event.keyCode)"); n < 2 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 中 Self.arrowDirection(event.keyCode) 只有 %d 处（组词态与联想态都要消费方向键）",
				group, macPanel, n))
		}
	}
	// Linux：ibus lookup cursor 预选 + 空格确认 cursor 候选 + README 记录。
	checkContains(fsys, problems, group, linuxEngine,
		"kKeysymLeft", "kKeysymUp", "kKeysymRight", "kKeysymDown",
		"moveLookup(", "flipPage(", "flipAssociate(",
		"ibus_lookup_table_new(kPageSize, cursor, TRUE, FALSE)",
		"ibus_lookup_table_set_cursor_pos(table, cursor)",
		"selectCandidate(lookupIndex_)",
	)
	if data, err := fs.ReadFile(fsys, linuxEngine); err == nil {
		if n := strings.Count(string(data), "selectCandidate(lookupIndex_)"); n < 2 {
			*problems = append(*problems, fmt.Sprintf(
				"%s：%s 中 selectCandidate(lookupIndex_) 只有 %d 处（联想态与组词态空格都要确认 lookup cursor 候选）",
				group, linuxEngine, n))
		}
	}
	checkContains(fsys, problems, group, linuxReadme, "方向键预选", "lookup cursor")
}
