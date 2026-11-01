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

// userDelete 协议往返：daemon 共享 UserDict 按词删除即时生效，返回
// deleted 标志；重复删除返回 false（不是错误）。
func TestServerUserDelete(t *testing.T) {
	u := exportTestUserDict(t, 0, 0, 0)
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	if err := u.BumpPhrase("单位", []string{"dan", "wei"}); err != nil {
		t.Fatalf("BumpPhrase: %v", err)
	}
	if err := u.BumpWord("今天", []string{"jin", "tian"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	c, _ := exportTestServer(t, u)

	deleted, err := c.UserDelete("你好", []string{"ni", "hao"})
	if err != nil {
		t.Fatalf("UserDelete 失败: %v", err)
	}
	if !deleted {
		t.Fatal("删除已学习的词应返回 deleted=true")
	}
	if got := u.Score("你好", []string{"ni", "hao"}, ""); got != 0 {
		t.Fatalf("daemon 内存态删除后「你好」Score = %d", got)
	}

	// 重复删除：返回 false、无错误（幂等）。
	if deleted, err := c.UserDelete("你好", []string{"ni", "hao"}); err != nil || deleted {
		t.Fatalf("重复删除应返回 (false, nil)，实际 (%v, %v)", deleted, err)
	}

	// 删除词组同样生效。
	if deleted, err := c.UserDelete("单位", []string{"dan", "wei"}); err != nil || !deleted {
		t.Fatalf("删除词组应成功，实际 (%v, %v)", deleted, err)
	}
	if got := u.PhraseCount("单位", []string{"dan", "wei"}); got != 0 {
		t.Fatalf("词组计数 = %d，期望 0", got)
	}

	// 其它词不受影响，导出里也不应再出现被删词。
	if got := u.Score("今天", []string{"jin", "tian"}, ""); got == 0 {
		t.Fatal("删除「你好/单位」不应影响「今天」的学习记录")
	}
	tsv, err := c.UserExport(false)
	if err != nil {
		t.Fatalf("UserExport 失败: %v", err)
	}
	if strings.Contains(tsv, "你好") || strings.Contains(tsv, "单位") {
		t.Fatalf("导出不应再含被删词：\n%s", tsv)
	}
}

// userDelete 参数兼容：syllables 字符串（' 或空格分隔）与只给词两种形态。
func TestServerUserDeleteParamForms(t *testing.T) {
	u := exportTestUserDict(t, 0, 0, 0)
	if err := u.BumpWord("今天", []string{"jin", "tian"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	if err := u.BumpWord("长", []string{"zhang"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	c, _ := exportTestServer(t, u)

	var out struct {
		Deleted bool `json:"deleted"`
	}
	// macOS EngineClient 形态：syllables 为 ' 分隔字符串。
	if err := c.call("userDelete", map[string]string{
		"word": "今天", "syllables": "jin'tian",
	}, &out); err != nil {
		t.Fatalf("syllables=' 分隔调用失败: %v", err)
	}
	if !out.Deleted {
		t.Fatal("syllables 字符串形态应删除成功")
	}

	// CLI 形态：space 分隔字符串。
	if err := c.call("userDelete", map[string]string{
		"word": "长", "syllables": "zhang",
	}, &out); err != nil || !out.Deleted {
		t.Fatalf("syllables=空格分隔调用失败: (%+v, %v)", out, err)
	}

	// 只给词（CLI 省略音节）：按词文本删全部记录。
	if err := u.BumpWord("长", []string{"chang"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	if err := c.call("userDelete", map[string]string{"word": "长"}, &out); err != nil || !out.Deleted {
		t.Fatalf("只给词调用失败: (%+v, %v)", out, err)
	}
	if got := u.Score("长", []string{"chang"}, ""); got != 0 {
		t.Fatalf("只给词删除后「长 chang」仍有调频分 %d", got)
	}
}

// 缺少 word 参数：返回业务错误，不静默成功。
func TestServerUserDeleteMissingWord(t *testing.T) {
	u := exportTestUserDict(t, 0, 0, 0)
	c, _ := exportTestServer(t, u)
	var out struct {
		Deleted bool `json:"deleted"`
	}
	err := c.call("userDelete", map[string]string{"syllables": "ni hao"}, &out)
	if err == nil {
		t.Fatal("缺少 word 参数应返回错误")
	}
	if !strings.Contains(err.Error(), "word") {
		t.Fatalf("错误信息应说明缺少 word，实际: %v", err)
	}
}

// 未启用用户词典的 daemon：userDelete 与其他 user* 方法一样返回业务错误。
func TestServerUserDeleteWithoutUserDict(t *testing.T) {
	dir, err := os.MkdirTemp("", "csipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "nouser.sock")
	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, nil)
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })
	c, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.UserDelete("你好", []string{"ni", "hao"}); err == nil {
		t.Fatal("未启用用户词典时 userDelete 应返回错误")
	} else if !strings.Contains(err.Error(), "用户词典") {
		t.Fatalf("错误信息应说明未启用用户词典，实际: %v", err)
	}
}

// 并发：多连接同时 userDelete/userStats 不 panic（配合 -race）。
func TestServerUserDeleteConcurrent(t *testing.T) {
	u := exportTestUserDict(t, 0, 0, 0)
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	dir, err := os.MkdirTemp("", "csipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "conc.sock")
	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, u)
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })

	const clients = 4
	done := make(chan error, clients)
	for i := 0; i < clients; i++ {
		go func(i int) {
			c, derr := waitForDial(sock)
			if derr != nil {
				done <- derr
				return
			}
			defer c.Close()
			for j := 0; j < 20; j++ {
				if _, err := c.UserDelete("你好", []string{"ni", "hao"}); err != nil {
					done <- fmt.Errorf("client %d delete: %w", i, err)
					return
				}
				if _, err := c.UserStats(); err != nil {
					done <- fmt.Errorf("client %d stats: %w", i, err)
					return
				}
			}
			done <- nil
		}(i)
	}
	for i := 0; i < clients; i++ {
		if err := <-done; err != nil {
			t.Fatalf("并发删除出错: %v", err)
		}
	}
}
