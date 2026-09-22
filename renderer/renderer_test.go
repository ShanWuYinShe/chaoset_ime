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
	pngBytes, err := Render(sampleComp(), skin)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join("..", ".workwork", "render-sample.png")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("样例已写入 %s", out)
}
