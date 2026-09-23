package renderer

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"chaoset.ime/engine"
)

func sampleComp() engine.Composition {
	return engine.Composition{
		Input:   "jianpanshubiao",
		Preedit: "jian'pan'shu'biao",
		Candidates: []engine.Candidate{
			{Text: "键盘鼠标", Syllables: "jian'pan'shu'biao", Score: 1500, FromUser: true},
			{Text: "键盘", Syllables: "jian'pan", Score: 558},
			{Text: "鼠标", Syllables: "shu'biao", Score: 556},
		},
	}
}

func TestRenderPNG(t *testing.T) {
	skin := DefaultSkin()
	if skin.FontPath == "" {
		t.Skip("本机无探测得到的 CJK 字体")
	}
	pngBytes, err := Render(sampleComp(), skin)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if len(pngBytes) == 0 {
		t.Fatal("有候选时应产出 PNG")
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("产出不是合法 PNG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() < 100 || b.Dy() < 40 {
		t.Fatalf("图像尺寸过小: %dx%d", b.Dx(), b.Dy())
	}
}

// 多页组成时预编辑行附加页码指示，渲染图因此更宽。
func TestRenderPageIndicator(t *testing.T) {
	skin := DefaultSkin()
	if skin.FontPath == "" {
		t.Skip("本机无探测得到的 CJK 字体")
	}
	withPage := sampleComp()
	withPage.Page, withPage.PageCount = 0, 2
	a, err := Render(sampleComp(), skin)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	b, err := Render(withPage, skin)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	imgA, _ := png.Decode(bytes.NewReader(a))
	imgB, _ := png.Decode(bytes.NewReader(b))
	if imgB.Bounds().Dx() <= imgA.Bounds().Dx() {
		t.Fatalf("页码指示应使图像更宽: %d vs %d", imgB.Bounds().Dx(), imgA.Bounds().Dx())
	}
}

func TestRenderEmptyCandidates(t *testing.T) {
	comp := engine.Composition{Input: "nihao", Preedit: "ni'hao"}
	pngBytes, err := Render(comp, DefaultSkin())
	if err != nil {
		t.Fatalf("无候选渲染不应报错: %v", err)
	}
	if pngBytes != nil {
		t.Fatalf("无候选应返回 nil PNG，得到 %d 字节", len(pngBytes))
	}
}

func TestRenderFontError(t *testing.T) {
	skin := DefaultSkin()
	skin.FontPath = "/nonexistent/font.ttf"
	if _, err := Render(sampleComp(), skin); err == nil {
		t.Fatal("字体不可用应报错（壳据此回退 v1 UI）")
	}
}

// 供人工目检：渲染样例写入 .workwork/（gitignored）。
func TestWriteVisualSample(t *testing.T) {
	skin := DefaultSkin()
	if skin.FontPath == "" {
		t.Skip("本机无 CJK 字体")
	}
	paged := sampleComp()
	paged.Page, paged.PageCount = 1, 2
	samples := map[string]engine.Composition{
		"render-sample.png":       sampleComp(),
		"render-sample-paged.png": paged,
	}
	for name, comp := range samples {
		pngBytes, err := Render(comp, skin)
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join("..", ".workwork", name)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, pngBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("样例已写入 %s", out)
	}
}
