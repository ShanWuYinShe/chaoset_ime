package ipc

import (
	"bytes"
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
	"chaoset.ime/renderer"
)

func TestCandidateImage(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "img.sock")

	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析词库失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, mustMemUser(t), WithSkin(renderer.DefaultSkin()))
	go srv.Serve()
	defer srv.Close()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer client.Close()

	if _, err := client.Input("nihao"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	w, h, data, err := client.CandidateImage()
	if err != nil {
		t.Fatalf("CandidateImage 失败: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("有候选时应返回 PNG 数据")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("返回的不是合法 PNG: %v", err)
	}
	if w != cfg.Width || h != cfg.Height {
		t.Fatalf("宽高字段与图像不符: %dx%d vs %dx%d", w, h, cfg.Width, cfg.Height)
	}

	// 无候选：png 为空但调用成功（壳应隐藏窗口）
	if _, err := client.Reset(); err != nil {
		t.Fatalf("Reset 失败: %v", err)
	}
	_, _, data2, err := client.CandidateImage()
	if err != nil {
		t.Fatalf("无候选时 CandidateImage 不应报错: %v", err)
	}
	if len(data2) != 0 {
		t.Fatalf("无候选应返回空 PNG，得到 %d 字节", len(data2))
	}
}

func TestCandidateImageDisabled(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "ns.sock")

	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln, dict, mustMemUser(t)) // 未配皮肤
	go srv.Serve()
	defer srv.Close()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, _, _, err := client.CandidateImage(); err == nil {
		t.Fatal("未启用渲染时 CandidateImage 应返回错误")
	}
}

// newSkinServer 起一个带皮肤的 server 并完成一次输入，返回已连上的 client。
func newSkinServer(t *testing.T, skin renderer.Skin) *Client {
	t.Helper()
	// t.TempDir() 的路径里含测试名，Unix socket 路径上限 104 字节会超限；
	// 用短前缀的临时目录，保证 socket 路径足够短。
	dir, err := os.MkdirTemp("", "ipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")

	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析词库失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, mustMemUser(t), WithSkin(skin))
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	if _, err := client.Input("nihao"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	return client
}

func decodeImage(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("返回的不是合法 PNG: %v", err)
	}
	return img
}

// candidateImage 的 dark/scale 可选参数：深色换配色、scale=2 尺寸翻倍，
// 且每次请求只影响本次响应（Skin 是值类型，不得改全局状态）。
func TestCandidateImageDarkScale(t *testing.T) {
	skin := renderer.DefaultSkin()
	if skin.FontPath == "" {
		t.Skip("本机无探测得到的 CJK 字体")
	}
	client := newSkinServer(t, skin)

	// 不带参数：浅色、scale=1（与旧行为一致）
	w1, h1, light, scale1, err := client.CandidateImageDetailed(CandidateImageOptions{})
	if err != nil {
		t.Fatalf("无参 CandidateImage 失败: %v", err)
	}
	if scale1 != 1 {
		t.Fatalf("无参 scale = %d, 期望 1", scale1)
	}
	if len(light) == 0 {
		t.Fatal("有候选时应返回 PNG")
	}
	lightImg := decodeImage(t, light)
	wantLight := renderer.DefaultSkinFor(false).BgColor
	if got := lightImg.At(lightImg.Bounds().Dx()/2, h1-2); got != wantLight {
		t.Fatalf("默认皮肤背景像素 = %v, 期望 %v", got, wantLight)
	}

	// dark=true：深色背景
	_, hd, dark, _, err := client.CandidateImageDetailed(CandidateImageOptions{Dark: true})
	if err != nil {
		t.Fatalf("dark 参数 CandidateImage 失败: %v", err)
	}
	darkImg := decodeImage(t, dark)
	wantDark := renderer.DefaultSkinFor(true).BgColor
	if got := darkImg.At(darkImg.Bounds().Dx()/2, hd-2); got != wantDark {
		t.Fatalf("深色皮肤背景像素 = %v, 期望 %v", got, wantDark)
	}

	// scale=2：宽高约翻倍，返回的 scale 字段为 2
	w2, h2, hi, scale2, err := client.CandidateImageDetailed(CandidateImageOptions{Scale: 2})
	if err != nil {
		t.Fatalf("scale=2 CandidateImage 失败: %v", err)
	}
	if scale2 != 2 {
		t.Fatalf("scale 字段 = %d, 期望 2", scale2)
	}
	if r := float64(w2) / float64(w1); r < 1.9 || r > 2.1 {
		t.Fatalf("scale=2 宽度应约翻倍: %d -> %d (x%.2f)", w1, w2, r)
	}
	if r := float64(h2) / float64(h1); r < 1.9 || r > 2.1 {
		t.Fatalf("scale=2 高度应约翻倍: %d -> %d (x%.2f)", h1, h2, r)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(hi))
	if err != nil {
		t.Fatalf("scale=2 返回的不是合法 PNG: %v", err)
	}
	if cfg.Width != w2 || cfg.Height != h2 {
		t.Fatalf("scale=2 宽高字段与图像不符: %dx%d vs %dx%d", w2, h2, cfg.Width, cfg.Height)
	}

	// 不支持的 scale 按 1 处理
	_, _, _, scaleBad, err := client.CandidateImageDetailed(CandidateImageOptions{Scale: 3})
	if err != nil {
		t.Fatalf("scale=3 CandidateImage 不应报错: %v", err)
	}
	if scaleBad != 1 {
		t.Fatalf("scale=3 应回落到 1，得到 %d", scaleBad)
	}

	// 关键：前面的 dark/scale 请求不得污染后续请求（Skin 是值类型副本）
	_, _, lightAgain, scaleAgain, err := client.CandidateImageDetailed(CandidateImageOptions{})
	if err != nil {
		t.Fatalf("后续无参 CandidateImage 失败: %v", err)
	}
	if scaleAgain != 1 {
		t.Fatalf("后续请求 scale = %d, 期望 1（全局皮肤被污染）", scaleAgain)
	}
	againImg := decodeImage(t, lightAgain)
	if got := againImg.At(againImg.Bounds().Dx()/2, againImg.Bounds().Dy()-2); got != wantLight {
		t.Fatalf("后续请求背景像素 = %v, 期望仍是浅色（全局皮肤被污染）", got)
	}
}

// daemon 用 -font/-fontsize 定制皮肤时，dark 只换配色、保留字号。
func TestCandidateImageDarkKeepsFontSize(t *testing.T) {
	skin := renderer.DefaultSkin()
	if skin.FontPath == "" {
		t.Skip("本机无探测得到的 CJK 字体")
	}
	client := newSkinServer(t, skin)
	_, h1, _, _, err := client.CandidateImageDetailed(CandidateImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, h2, _, _, err := client.CandidateImageDetailed(CandidateImageOptions{Dark: true})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("深色皮肤不应改变行高: %d vs %d", h1, h2)
	}
}

func mustMemUser(t *testing.T) *engine.UserDict {
	t.Helper()
	u, err := engine.NewUserDict("")
	if err != nil {
		t.Fatal(err)
	}
	return u
}
