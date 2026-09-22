// Package renderer 把组词状态渲染为候选窗 PNG——「daemon 渲染、壳贴图」
// 的统一候选窗方案：三端壳显示同一张图，皮肤与布局只收敛在 daemon 一处。
// 纯 Go（x/image 栅格化 + stdlib PNG），渲染结果可单测、可人工目检。
package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"runtime"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"chaoset.ime/engine"
)

// Skin 是候选窗皮肤。零值不可用：用 DefaultSkin() 并按需覆盖字段。
type Skin struct {
	FontPath      string      // CJK 字体（TTF/TTC）；空串则按平台常见路径探测
	FontSize      float64     // 字号（pt）
	Padding       int         // 内边距（px）
	LineSpacing   int         // 行间距（px）
	BgColor       color.NRGBA // 背景半透明
	BorderColor   color.NRGBA
	PreeditColor  color.NRGBA // 预编辑行文字
	FgColor       color.NRGBA // 候选文字
	HighlightBg   color.NRGBA // 首选候选底色
	UserMarkColor color.NRGBA // 用户学习标记（*）颜色
}

// DefaultSkin 返回按当前平台探测字体路径的默认皮肤。
func DefaultSkin() Skin {
	return Skin{
		FontPath:      probeFontPath(),
		FontSize:      16,
		Padding:       10,
		LineSpacing:   6,
		BgColor:       color.NRGBA{0xFA, 0xFA, 0xFA, 0xF2},
		BorderColor:   color.NRGBA{0xC8, 0xC8, 0xC8, 0xFF},
		PreeditColor:  color.NRGBA{0x88, 0x88, 0x88, 0xFF},
		FgColor:       color.NRGBA{0x22, 0x22, 0x22, 0xFF},
		HighlightBg:   color.NRGBA{0x3B, 0x82, 0xF6, 0x33},
		UserMarkColor: color.NRGBA{0x16, 0x96, 0x50, 0xFF},
	}
}

// 探测顺序：常见 CJK 字体的平台惯例路径，首个存在的即用。
func probeFontPath() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/System/Library/Fonts/STHeiti Medium.ttc",
		}
	case "windows":
		candidates = []string{
			`C:\Windows\Fonts\msyh.ttc`,
			`C:\Windows\Fonts\simsun.ttc`,
		}
	default: // linux 及其他
		candidates = []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
			"/usr/share/fonts/wenquanyi/wqy-microhei/wqy-microhei.ttc",
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ---- 字体缓存：daemon 每次击键都可能渲染，TTF/TTC 只解析一次 ----

type faceKey struct {
	path string
	size float64
}

var (
	faceMu  sync.Mutex
	faceMon = map[faceKey]font.Face{}
)

func loadFace(s Skin) (font.Face, error) {
	if s.FontPath == "" {
		return nil, fmt.Errorf("未找到 CJK 字体（请用 -font 指定路径）")
	}
	if s.FontSize <= 0 {
		return nil, fmt.Errorf("字号必须为正")
	}
	faceMu.Lock()
	defer faceMu.Unlock()
	key := faceKey{s.FontPath, s.FontSize}
	if f, ok := faceMon[key]; ok {
		return f, nil
	}
	data, err := os.ReadFile(s.FontPath)
	if err != nil {
		return nil, fmt.Errorf("读取字体失败: %w", err)
	}
	// TTC 字体集取第一个 face；解析失败则按单字体 TTF 再试
	faceFont, ferr := parseFont(data)
	if ferr != nil {
		return nil, fmt.Errorf("解析字体 %s 失败: %w", s.FontPath, ferr)
	}
	face, err := opentype.NewFace(faceFont, &opentype.FaceOptions{
		Size:    s.FontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("创建字体面失败: %w", err)
	}
	faceMon[key] = face
	return face, nil
}

func parseFont(data []byte) (*opentype.Font, error) {
	if coll, err := opentype.ParseCollection(data); err == nil {
		return coll.Font(0)
	}
	return opentype.Parse(data)
}

var renderMu sync.Mutex // 字体面并发不安全；渲染串行化（耗时微秒级）

// Render 把组词状态画成候选窗 PNG。
// 无候选时返回 (nil, nil)（壳应隐藏窗口）；字体不可用时返回错误，
// 壳应回退到各平台 v1 候选 UI。
func Render(comp engine.Composition, skin Skin) ([]byte, error) {
	face, err := loadFace(skin)
	if err != nil {
		return nil, err
	}
	if len(comp.Candidates) == 0 {
		return nil, nil
	}

	renderMu.Lock()
	defer renderMu.Unlock()

	lineH := int(face.Metrics().Ascent.Ceil()+face.Metrics().Descent.Ceil()) + skin.LineSpacing
	pad := skin.Padding
	if pad <= 0 {
		pad = 8
	}

	// 布局：第 1 行预编辑（可选），之后每候选一行
	type line struct {
		text      string
		color     color.NRGBA
		highlight bool
		userMark  bool
	}
	var lines []line
	if comp.Preedit != "" {
		lines = append(lines, line{text: comp.Preedit, color: skin.PreeditColor})
	}
	for i, cand := range comp.Candidates {
		text := fmt.Sprintf("%d.%s", (i+1)%10, cand.Text)
		if cand.FromUser {
			text += " *"
		}
		lines = append(lines, line{
			text:      text,
			color:     skin.FgColor,
			highlight: i == 0,
			userMark:  cand.FromUser,
		})
	}

	// 测量：宽度 = 最宽行；高度 = 行数 × lineH - 末行 spacing + 2×pad
	maxW := 0
	for _, ln := range lines {
		w := font.MeasureString(face, ln.text).Ceil()
		if w > maxW {
			maxW = w
		}
	}
	if maxW == 0 {
		return nil, nil
	}
	width := maxW + pad*2
	height := pad*2 + len(lines)*lineH - skin.LineSpacing
	if width <= 0 || height <= 0 {
		return nil, nil
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	// 背景 + 边框
	draw.Draw(img, img.Bounds(), &image.Uniform{skin.BgColor}, image.Point{}, draw.Src)
	drawBorder(img, skin.BorderColor)

	// 逐行绘制
	d := &font.Drawer{Dst: img, Face: face}
	y := pad + face.Metrics().Ascent.Ceil()
	for _, ln := range lines {
		if ln.highlight {
			// 首选高亮：整行底色（从行顶到行底，宽度铺满内区）
			rect := image.Rect(pad/2, y-face.Metrics().Ascent.Ceil(), width-pad/2, y+face.Metrics().Descent.Ceil())
			draw.Draw(img, rect, &image.Uniform{skin.HighlightBg}, image.Point{}, draw.Over)
		}
		d.Src = image.NewUniform(ln.color)
		d.Dot = fixed.P(pad, y)
		d.DrawString(ln.text)
		if ln.userMark {
			// 学习标记：绿色 * 覆盖行尾（文本本身已含 *，换色强调）
			markX := pad + font.MeasureString(face, ln.text).Ceil() - font.MeasureString(face, "*").Ceil()
			d.Src = image.NewUniform(skin.UserMarkColor)
			d.Dot = fixed.P(markX, y)
			d.DrawString("*")
		}
		y += lineH
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码 PNG 失败: %w", err)
	}
	return buf.Bytes(), nil
}

func drawBorder(img *image.NRGBA, c color.NRGBA) {
	b := img.Bounds()
	for x := b.Min.X; x < b.Max.X; x++ {
		img.Set(x, b.Min.Y, c)
		img.Set(x, b.Max.Y-1, c)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		img.Set(b.Min.X, y, c)
		img.Set(b.Max.X-1, y, c)
	}
}
