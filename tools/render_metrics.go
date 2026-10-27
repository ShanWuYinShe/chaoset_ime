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
	Colors map[string]string `json:"colors"`
}

func main() {
	m := metrics{Colors: map[string]string{}}

	s := skin.DefaultSkin()
	m.Skin.FontSize = s.FontSize
	m.Skin.Padding = s.Padding
	m.Skin.LineSpacing = s.LineSpacing
	m.Skin.MaxWidth = s.MaxWidth
	m.Skin.CornerRadius = s.CornerRadius

	// 配色（浅色皮肤，对应 Swift CandidateTheme.theme(dark: false)）
	rgba := func(c color.NRGBA) string {
		return fmt.Sprintf("#%02X%02X%02X%02X", c.R, c.G, c.B, c.A)
	}
	m.Colors["panelBg"] = rgba(s.BgColor)
	m.Colors["panelBorder"] = rgba(s.BorderColor)
	m.Colors["text"] = rgba(s.FgColor)
	m.Colors["highlightBg"] = rgba(s.HighlightBg)
	m.Colors["chipBg"] = rgba(s.IndexChipBg)
	m.Colors["chipBorder"] = rgba(s.IndexChipBorder)
	m.Colors["chipDigit"] = rgba(s.IndexColor)
	m.Colors["pageColor"] = rgba(s.PreeditColor)
	m.Colors["userMark"] = rgba(s.UserMarkColor)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
}
