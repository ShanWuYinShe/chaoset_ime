//go:build ignore

// render_metrics 输出 Go 侧候选窗渲染的几何参数（JSON），供
// tests/visual-parity.sh 与 macOS 原生端 CandidatePanel 的布局做机器比对。
//
// 为什么需要它（2026-10-26）：renderer/renderer.go（Windows/Linux 贴图端）与
// shims/macos/src/CandidatePanel.swift（macOS 原生端）是同一套视觉的两份独立
// 实现，色值/间距全靠人工手抄同步，没有任何自动断言。审查已实证 3 处漂移
// （分栏线 y、字号、内容间距）。本工具把 Go 端的可测量属性导出成 JSON，
// 配合 Swift 侧同类导出做差分，让漂移在 CI 里就暴露，而不是等用户看出来。
//
// 用法：go run tools/render_metrics.go > /tmp/go-metrics.json
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"chaoset.ime/engine"
	"chaoset.ime/renderer"
)

// metrics 是候选窗的几何与配色参数（全部为 scale=1 的像素值）。
type metrics struct {
	Skin struct {
		FontSize     float64 `json:"fontSize"`
		Padding      int     `json:"padding"`
		LineSpacing  int     `json:"lineSpacing"`
		MaxWidth     int     `json:"maxWidth"`
		CornerRadius int     `json:"cornerRadius"`
	} `json:"skin"`
	// Scene 是用于对比的固定输入场景渲染结果
	Scene struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"scene"`
	Colors map[string]string `json:"colors"`
}

func main() {
	m := metrics{Colors: map[string]string{}}

	// 固定场景：与 Swift 侧 panel_preview 相同的候选集
	comp := engine.Composition{
		Input:     "jintian",
		Preedit:   "jin'tian",
		Page:      0,
		PageCount: 3,
		Candidates: []engine.Candidate{
			{Text: "今天", Syllables: "jin'tian", Score: 90, FromUser: true},
			{Text: "今天时间", Syllables: "jin'tian'shi'jian", Score: 80},
			{Text: "明天", Syllables: "ming'tian", Score: 70},
			{Text: "昨天", Syllables: "zuo'tian", Score: 60},
			{Text: "半天", Syllables: "ban'tian", Score: 50},
			{Text: "白天", Syllables: "bai'tian", Score: 40},
		},
	}

	skin := renderer.DefaultSkin()
	m.Skin.FontSize = skin.FontSize
	m.Skin.Padding = skin.Padding
	m.Skin.LineSpacing = skin.LineSpacing
	m.Skin.MaxWidth = skin.MaxWidth
	m.Skin.CornerRadius = skin.CornerRadius

	_, w, h, err := renderer.RenderWithBounds(comp, skin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "渲染失败: %v\n", err)
		os.Exit(1)
	}
	m.Scene.Width = w
	m.Scene.Height = h

	hex := func(c [4]uint8) string {
		return fmt.Sprintf("#%02X%02X%02X%02X", c[0], c[1], c[2], c[3])
	}
	_ = hex

	// 配色（浅色皮肤，对应 Swift CandidateTheme.theme(dark: false)）
	m.Colors["panelBg"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.BgColor.R, skin.BgColor.G, skin.BgColor.B, skin.BgColor.A)
	m.Colors["panelBorder"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.BorderColor.R, skin.BorderColor.G, skin.BorderColor.B, skin.BorderColor.A)
	m.Colors["text"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.FgColor.R, skin.FgColor.G, skin.FgColor.B, skin.FgColor.A)
	m.Colors["highlightBg"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.HighlightBg.R, skin.HighlightBg.G, skin.HighlightBg.B, skin.HighlightBg.A)
	m.Colors["chipBg"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.IndexChipBg.R, skin.IndexChipBg.G, skin.IndexChipBg.B, skin.IndexChipBg.A)
	m.Colors["chipBorder"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.IndexChipBorder.R, skin.IndexChipBorder.G, skin.IndexChipBorder.B, skin.IndexChipBorder.A)
	m.Colors["chipDigit"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.IndexColor.R, skin.IndexColor.G, skin.IndexColor.B, skin.IndexColor.A)
	m.Colors["pageColor"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.PreeditColor.R, skin.PreeditColor.G, skin.PreeditColor.B, skin.PreeditColor.A)
	m.Colors["userMark"] = fmt.Sprintf("#%02X%02X%02X%02X", skin.UserMarkColor.R, skin.UserMarkColor.G, skin.UserMarkColor.B, skin.UserMarkColor.A)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
}
