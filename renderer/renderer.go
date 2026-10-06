// Package renderer 把组词状态渲染为候选窗 PNG——「daemon 渲染、壳贴图」
// 的统一候选窗方案：三端壳显示同一张图，皮肤与布局只收敛在 daemon 一处。
// 纯 Go（x/image 栅格化 + stdlib PNG），渲染结果可单测、可人工目检。
//
// 默认皮肤取中国传统色与中国印章的双框语汇：宣纸底（#F7F2E7）、
// 朱红外框 + 米金内框、浓墨候选、淡墨预编辑、朱红首选高亮、石青学习标记。
// 三端共用同一张图，故视觉风格在一处收敛、一处调整。
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
	FontPath          string      // CJK 字体（TTF/TTC）；空串则按平台常见路径探测
	FontSize          float64     // 字号（pt）
	Padding           int         // 内边距（px）
	LineSpacing       int         // 行间距（px）
	BgColor           color.NRGBA // 背景半透明
	BorderColor       color.NRGBA // 外框
	InnerBorderColor  color.NRGBA // 内框细线（宣纸/印章双框语汇）；Alpha=0 时不画
	PreeditColor      color.NRGBA // 预编辑行文字
	FgColor           color.NRGBA // 候选文字
	HighlightBg       color.NRGBA // 首选候选底色
	HighlightBarColor color.NRGBA // 首选候选左侧「朱批」竖记；Alpha=0 时不画
	UserMarkColor     color.NRGBA // 用户学习标记（*）颜色
}

// DefaultSkin 返回按当前平台探测字体路径的默认皮肤。
// 配色取中国传统色：宣纸底、朱红外框 + 米金内框、浓墨候选、淡墨预编辑、
// 朱红首选高亮、石青学习标记（见 renderer 包注释）。
func DefaultSkin() Skin {
	return Skin{
		FontPath:          probeFontPath(),
		FontSize:          16,
		Padding:           10,
		LineSpacing:       6,
		BgColor:           color.NRGBA{0xF7, 0xF2, 0xE7, 0xF2}, // 宣纸
		BorderColor:       color.NRGBA{0xB2, 0x3A, 0x2E, 0xFF}, // 朱红
		InnerBorderColor:  color.NRGBA{0xE0, 0xCF, 0xA8, 0xCC}, // 米金细内框
		PreeditColor:      color.NRGBA{0x9A, 0x8F, 0x7D, 0xFF}, // 淡墨
		FgColor:           color.NRGBA{0x2B, 0x2A, 0x26, 0xFF}, // 浓墨
		HighlightBg:       color.NRGBA{0xC1, 0x27, 0x2D, 0x33}, // 朱红高亮
		HighlightBarColor: color.NRGBA{0xB2, 0x3A, 0x2E, 0xFF}, // 朱批竖记
		UserMarkColor:     color.NRGBA{0x1B, 0x6B, 0x8C, 0xFF}, // 石青
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
		preeditText := comp.Preedit
		if comp.PageCount > 1 {
			// 多页时在预编辑行尾附页码指示（与 CLI 输出格式一致）
			preeditText += fmt.Sprintf("  [%d/%d]", comp.Page+1, comp.PageCount)
		}
		lines = append(lines, line{text: preeditText, color: skin.PreeditColor})
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
			// 朱批：首选行左侧一道朱红竖记（中国传统批注记号）
			if skin.HighlightBarColor.A != 0 && rect.Dy() >= 3 {
				bar := image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+3, rect.Max.Y)
				draw.Draw(img, bar, &image.Uniform{skin.HighlightBarColor}, image.Point{}, draw.Over)
			}
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

	// 内框细线后画，保证在朱红高亮之上仍清晰（双框 = 印章/宣纸装帧语汇）
	drawInnerBorder(img, skin.InnerBorderColor, 3)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码 PNG 失败: %w", err)
	}
	return buf.Bytes(), nil
}

// drawInnerBorder 在距边缘 inset 像素处画一圈 1px 细线（Alpha=0 时跳过）。
func drawInnerBorder(img *image.NRGBA, c color.NRGBA, inset int) {
	if c.A == 0 || inset <= 0 {
		return
	}
	b := img.Bounds()
	if b.Dx() <= inset*2+1 || b.Dy() <= inset*2+1 {
		return
	}
	u := &image.Uniform{c}
	draw.Draw(img, image.Rect(b.Min.X+inset, b.Min.Y+inset, b.Max.X-inset, b.Min.Y+inset+1), u, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(b.Min.X+inset, b.Max.Y-inset-1, b.Max.X-inset, b.Max.Y-inset), u, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(b.Min.X+inset, b.Min.Y+inset+1, b.Min.X+inset+1, b.Max.Y-inset-1), u, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(b.Max.X-inset-1, b.Min.Y+inset+1, b.Max.X-inset, b.Max.Y-inset-1), u, image.Point{}, draw.Over)
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
