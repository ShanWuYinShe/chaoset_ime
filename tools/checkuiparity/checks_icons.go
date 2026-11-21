// checks_icons.go：「图标规范」组。
//
// 契约（docs/ui-parity.md「设置窗契约」+ AGENTS.md「设置 UI」）：
//   - 页面图标只用 SVG（shims/settings/ui/icons/*.svg），禁止 emoji/Unicode 符号当图标；
//   - 关于页品牌图用真实 SVG（app.svg），不再是文字字母占位；
//   - 三端 GUI app 必须有图标：macOS 设置中心打包为独立 ChaosetSettings.app（Info.plist+icns）、
//     Windows 设置中心 exe 嵌入 ico（shims/settings/build.rs）、Linux 提供 .desktop + hicolor 图标。
package main

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// emojiOrGlyphIcon 匹配「当图标用」的 emoji/符号：emoji 区、杂项符号、装饰符号、几何图形、
// 箭头与变体选择符。正常中文标点（…、·、×）不在范围内。
// 说明：✓/✗（2713/2717）与 →（2192）是常规文本状态符/箭头，不算「emoji 当图标」，
// 因此从杂项符号区间里排除 2713–2717、并排除整个箭头区。
var emojiOrGlyphIcon = regexp.MustCompile(`[\x{1F000}-\x{1FAFF}\x{2600}-\x{2712}\x{2718}-\x{27BF}\x{2B00}-\x{2BFF}\x{25A0}-\x{25FF}\x{2100}-\x{214F}\x{FE0F}]`)

var slintSvgRef = regexp.MustCompile(`@image-url\("([^"]+\.svg)"\)`)

func checkIconRules(fsys fs.FS, problems *[]string) {
	const group = groupIconRules

	// 1) Slint 页面不得把 emoji/符号当图标。
	slintFiles := []string{slintApp, slintComponents, slintTokens}
	for _, f := range slintFiles {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			addProblem(problems, group, f, 0, "读取失败: "+err.Error())
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if m := emojiOrGlyphIcon.FindString(line); m != "" {
				addProblem(problems, group, f, i+1, fmt.Sprintf("页面图标禁止使用 emoji/Unicode 符号 %q，请改用 icons/*.svg", m))
			}
		}
	}

	// 2) Slint 引用的 SVG 必须真实存在（按引用文件所在目录解析）。
	refs := map[string]bool{}
	for _, f := range slintFiles {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			continue
		}
		for _, m := range slintSvgRef.FindAllStringSubmatch(string(data), -1) {
			ref := m[1]
			if !strings.HasPrefix(ref, "icons/") {
				addProblem(problems, group, f, 0, fmt.Sprintf("页面图标应放在 ui/icons/ 下，实际引用 %q", ref))
				continue
			}
			refs[ref] = true
			if _, err := fs.Stat(fsys, path.Join(path.Dir(f), ref)); err != nil {
				addProblem(problems, group, f, 0, fmt.Sprintf("引用的 SVG 不存在：%s", ref))
			}
		}
	}
	if len(refs) < 9 {
		addProblem(problems, group, "shims/settings/ui", 0, fmt.Sprintf("页面图标引用过少（%d 个 SVG），导航/卡片/候选预览/关于页应覆盖", len(refs)))
	}

	// 3) 关于页品牌图 = 真实 SVG，不是文字字母。
	checkContains(fsys, problems, group, slintApp, `icon: @image-url("icons/app.svg")`)
	checkContains(fsys, problems, group, slintApp, `source: @image-url("icons/app.svg")`)

	// 4) macOS：设置中心必须有独立 app bundle（Info.plist + icns），启动器指向 bundle。
	checkContains(fsys, problems, group, macBuild,
		"ChaosetSettings.app", "ChaosetSettings.icns", "CFBundleIconFile", "CFBundleIdentifier", "plutil -lint")
	checkContains(fsys, problems, group, macLauncher, "ChaosetSettings.app/Contents/MacOS/")
	checkContains(fsys, problems, group, makefile, "ChaosetSettings.app", "ChaosetSettings.icns")

	// 5) Windows：设置中心 exe 的图标由 build.rs 以 rc.exe/windres 嵌入（release 必须生效）。
	checkContains(fsys, problems, group, "shims/settings/build.rs", "chaoset.rc", "chaoset.ico", "rustc-link-arg")
	checkContains(fsys, problems, group, "shims/windows/resources/chaoset.rc", "1 ICON")
	checkContains(fsys, problems, group, winInstaller, "MUI_ICON")

	// 6) Linux：设置中心在应用菜单里要有带图标的桌面入口。
	checkExists(fsys, problems, group, "shims/linux/chaoset-settings.desktop")
	checkContains(fsys, problems, group, "shims/linux/chaoset-settings.desktop",
		"[Desktop Entry]", "Icon=chaoset", "Exec=/usr/bin/chaoset-settings", "Terminal=false")
	checkContains(fsys, problems, group, installSh, "APPLICATIONS_DIR", "chaoset-settings.desktop")
	checkContains(fsys, problems, group, makefile, "chaoset-settings.desktop")
	checkContains(fsys, problems, group, linuxArchPkg, "chaoset-settings.desktop")
	checkExists(fsys, problems, group, "shims/linux/icons/hicolor/scalable/apps/chaoset.svg")

	// 7) macOS 两个 app 都要有 icns（输入法 + 安装器）。
	checkContains(fsys, problems, group, macBuild, "ChaosetIME.icns", "SETUP/Contents/Resources")

	// 8) 桌面 GUI 文案不得再用 emoji（含 Setup.app 状态文案），避免页面出现彩色 emoji。
	if data, err := fs.ReadFile(fsys, "shims/macos/setup/main.swift"); err == nil {
		for i, line := range strings.Split(string(data), "\n") {
			if emojiOrGlyphIcon.MatchString(line) {
				addProblem(problems, group, "shims/macos/setup/main.swift", i+1,
					"GUI 文案禁止 emoji，请改用纯文本标记")
			}
		}
	}
}
