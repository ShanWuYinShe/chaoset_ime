package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
)

// newIPCClient 起一个真实 Unix socket server + client（覆盖 client 便捷方法
// 与 Server Option 的协议往返；测试结束自动清理）。
func newIPCClient(t *testing.T, dictSrc string, opts ...Option) *Client {
	t.Helper()
	// 用短路径：macOS Unix socket 路径上限 104 字节，t.TempDir() 目录名
	// 含完整测试名，长测试名会直接 bind: invalid argument。
	dir, err := os.MkdirTemp("", "ipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	dict, err := engine.ParseDict(strings.NewReader(dictSrc))
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
	srv := NewServer(ln, dict, user, opts...)
	go func() { _ = srv.Serve() }()
	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接 daemon 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = srv.Close()
	})
	return client
}

// TestClientNavigationMethods：client 的 Backspace / Reset / NextPage /
// PrevPage 便捷方法与 daemon dispatch 分支（此前 ipc 包覆盖率审计发现
// 这四个客户端方法 0% 覆盖）。
func TestClientNavigationMethods(t *testing.T) {
	var src strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&src, "你%d\tni\t%d\n", i, 90-i)
	}
	c := newIPCClient(t, src.String())

	comp, err := c.Input("ni")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) != engine.PageSize() {
		t.Fatalf("第 1 页应有 %d 条候选，实际 %d", engine.PageSize(), len(comp.Candidates))
	}

	comp, err = c.NextPage()
	if err != nil {
		t.Fatalf("NextPage 失败: %v", err)
	}
	if comp.Page != 1 || len(comp.Candidates) == 0 {
		t.Fatalf("NextPage 后应为第 2 页且有候选，实际 page=%d candidates=%d", comp.Page, len(comp.Candidates))
	}

	comp, err = c.PrevPage()
	if err != nil {
		t.Fatalf("PrevPage 失败: %v", err)
	}
	if comp.Page != 0 {
		t.Fatalf("PrevPage 后应回到第 1 页，实际 page=%d", comp.Page)
	}

	comp, err = c.Backspace()
	if err != nil {
		t.Fatalf("Backspace 失败: %v", err)
	}
	if comp.Input != "n" {
		t.Fatalf("Backspace 后 input 应为 n，实际 %q", comp.Input)
	}

	comp, err = c.Reset()
	if err != nil {
		t.Fatalf("Reset 失败: %v", err)
	}
	if comp.Input != "" || len(comp.Candidates) != 0 {
		t.Fatalf("Reset 后应为空，实际 %+v", comp)
	}
}

// TestServerWithTraditionalOption：daemon 的 -traditional 选项必须对每个
// 新会话生效（此前 WithTraditional 0% 覆盖）。
func TestServerWithTraditionalOption(t *testing.T) {
	c := newIPCClient(t, "国\tguo\t99\n", WithTraditional(true))
	comp, err := c.Input("guo")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "國" {
		t.Fatalf("繁体模式首选应为「國」，实际 %+v", comp.Candidates)
	}
}

// TestServerWithFuzzyOption：daemon 的 -fuzzy 选项必须对每个新会话生效
// （此前 WithFuzzy 0% 覆盖）。
func TestServerWithFuzzyOption(t *testing.T) {
	c := newIPCClient(t, "中国\tzhong guo\t99\n", WithFuzzy(true))
	comp, err := c.Input("zongguo")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	found := false
	for _, cand := range comp.Candidates {
		if cand.Text == "中国" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("模糊音模式 zongguo 应出「中国」，实际 %+v", comp.Candidates)
	}
}
