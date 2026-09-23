package ipc

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaoset.ime/engine"
)

const ipcTestDict = `
你好	ni hao	99
你	ni	80
好	hao	85
实施	shi shi	60
试试	shi shi	10
`

func TestServerClientRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	udPath := filepath.Join(dir, "user.json")
	user, err := engine.NewUserDict(udPath)
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, user)
	go srv.Serve()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接 daemon 失败: %v", err)
	}
	defer client.Close()

	if err := client.Ping(); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}

	comp, err := client.Input("nihao")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if comp.Preedit != "ni'hao" || len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("组词结果不符预期: %+v", comp)
	}

	committed, err := client.Select(0)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if committed != "你好" {
		t.Fatalf("上屏 = %q, 期望 你好", committed)
	}

	// Select 越界应返回业务错误而非崩溃
	if _, err := client.Select(9); err == nil {
		t.Fatal("越界 Select 应返回错误")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("关闭 server 失败: %v", err)
	}

	// 用户词典应已落盘：重新加载可见学习记录（新鲜记录得分 = 单次加权）
	user2, err := engine.NewUserDict(udPath)
	if err != nil {
		t.Fatalf("重载用户词典失败: %v", err)
	}
	if got := user2.Score("你好", []string{"ni", "hao"}, ""); got != engine.UserBoostPerCount {
		t.Fatalf("用户词典得分 = %d, 期望 %d", got, engine.UserBoostPerCount)
	}
}

// punct 方法协议往返：组词中合并上屏、空组词直映、未知键报业务错误。
func TestServerPunct(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "punct.sock")

	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	user, err := engine.NewUserDict(filepath.Join(dir, "user.json"))
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, user)
	go srv.Serve()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接 daemon 失败: %v", err)
	}
	defer client.Close()

	if _, err := client.Input("nihao"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	committed, err := client.Punct(",")
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if committed != "你好，" {
		t.Fatalf("组词中 Punct = %q, 期望 你好，", committed)
	}

	if committed, err = client.Punct("."); err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if committed != "。" {
		t.Fatalf("空组词 Punct = %q, 期望 。", committed)
	}

	if _, err := client.Punct("@"); err == nil {
		t.Fatal("未知标点应返回业务错误")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("关闭 server 失败: %v", err)
	}
}

func waitForDial(sock string) (*Client, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := Dial("unix", sock)
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("连接 daemon 超时: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
