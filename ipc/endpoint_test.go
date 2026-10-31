package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
)

// TestEndpointUnixRoundTrip：Endpoint 抽象在 Unix socket 上跑通完整协议往返
// （后续 Windows 命名管道复用同一 Client/Server，只换 Listen/Dial 实现）。
func TestEndpointUnixRoundTrip(t *testing.T) {
	// macOS Unix socket 路径上限 104 字节：用短临时目录。
	dir, err := os.MkdirTemp("", "ipc-ep")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	ln, err := Listen(Endpoint{Network: "unix", Address: sock})
	if err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	srv := NewServer(ln, dict, nil)
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })

	client, err := DialEndpoint(Endpoint{Network: "unix", Address: sock})
	if err != nil {
		t.Fatalf("DialEndpoint 失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	comp, err := client.Input("nihao")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("候选不符预期: %+v", comp.Candidates)
	}
}

// TestEndpointRejectsUnknownNetwork：未知传输必须明确报错，不能静默连错。
func TestEndpointRejectsUnknownNetwork(t *testing.T) {
	if _, err := Listen(Endpoint{Network: "bogus", Address: "x"}); err == nil {
		t.Fatal("Listen 未知传输应报错")
	}
	if _, err := DialEndpoint(Endpoint{Network: "bogus", Address: "x"}); err == nil {
		t.Fatal("DialEndpoint 未知传输应报错")
	}
}
