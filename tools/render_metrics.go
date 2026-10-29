//go:build ignore

// render_metrics 输出 Go 侧候选窗视觉参数（JSON），供
// tests/visual-parity.sh 与 macOS 原生端 CandidatePanel 的布局做机器比对。
//
// 为什么需要它（2026-10-26）：skin/skin.go（视觉参数真源）与
// shims/macos/src/CandidatePanel.swift（macOS 原生端）是同一套视觉的两份
// 独立实现，色值/间距全靠人工手抄同步，没有任何自动断言。审查已实证 3 处
// 漂移（分栏线 y、字号、内容间距）。本工具把 Go 端的可测量属性导出成 JSON，
// 配合 Swift 侧同类导出做差分，让漂移在 CI 里就暴露，而不是等用户看出来。
//
// 用法：go run tools/render_metrics.go > /tmp/go-metrics.json
package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"

	"chaoset.ime/skin"
)

// metrics 是候选窗的几何与配色参数。
type metrics struct {
	Skin struct {
		FontSize     float64 `json:"fontSize"`
		Padding      int     `json:"padding"`
		LineSpacing  int     `json:"lineSpacing"`
		MaxWidth     int     `json:"maxWidth"`
		CornerRadius int     `json:"cornerRadius"`
	} `json:"skin"`
	// Colors 浅色皮肤（对应 Swift CandidateTheme.theme(dark: false)），
	// ColorsDark 深色皮肤（theme(dark: true)）。两套都做 Go↔Swift 逐色比对
	//（2026-10-30 审计 G2：此前只导出浅色 9 色且 Swift 端根本未导出）。
	Colors     map[string]string `json:"colors"`
	ColorsDark map[string]string `json:"colorsDark"`
}

// skinColors 把 skin.Skin 的 13 个颜色字段映射为与 Swift CandidateTheme
// 同名的 key（名称即 visual-parity 比对表：一一对应，缺一即脚本报错）。
func skinColors(s skin.Skin) map[string]string {
	rgba := func(c color.NRGBA) string {
		return fmt.Sprintf("#%02X%02X%02X%02X", c.R, c.G, c.B, c.A)
	}
	return map[string]string{
		"panelBg":         rgba(s.BgColor),
		"panelBorder":     rgba(s.BorderColor),
		"text":            rgba(s.FgColor),
		"highlightBg":     rgba(s.HighlightBg),
		"highlightText":   rgba(s.HighlightFg),
		"chipBg":          rgba(s.IndexChipBg),
		"chipBorder":      rgba(s.IndexChipBorder),
		"chipDigit":       rgba(s.IndexColor),
		"highlightChipBg": rgba(s.HighlightIndexChipBg),
		"pageColor":       rgba(s.PreeditColor),
		"userMark":        rgba(s.UserMarkColor),
		"divider":         rgba(s.DividerColor),
		"chevron":         rgba(s.ChevronColor),
	}
}

func main() {
	m := metrics{Colors: map[string]string{}, ColorsDark: map[string]string{}}

	s := skin.DefaultSkin()
	m.Skin.FontSize = s.FontSize
	m.Skin.Padding = s.Padding
	m.Skin.LineSpacing = s.LineSpacing
	m.Skin.MaxWidth = s.MaxWidth
	m.Skin.CornerRadius = s.CornerRadius

	m.Colors = skinColors(skin.DefaultSkinFor(false))
	m.ColorsDark = skinColors(skin.DefaultSkinFor(true))

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
}
