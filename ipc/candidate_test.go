package ipc

import (
	"bytes"
	"image/png"
	"net"
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

func mustMemUser(t *testing.T) *engine.UserDict {
	t.Helper()
	u, err := engine.NewUserDict("")
	if err != nil {
		t.Fatal(err)
	}
	return u
}
