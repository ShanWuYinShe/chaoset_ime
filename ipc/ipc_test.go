package ipc

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
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

// userStats/userClear/userExport 协议往返：统计、清空、导出全链路。
func TestServerUserDictMgmt(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "ud.sock")

	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	user, err := engine.NewUserDict(filepath.Join(dir, "user.json"))
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}
	// 预置学习数据：经同一对象写入（daemon 共享该指针）
	if err := user.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := user.BumpBigram("你好", "你好"); err != nil {
		t.Fatalf("BumpBigram 失败: %v", err)
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

	st, err := client.UserStats()
	if err != nil {
		t.Fatalf("UserStats 失败: %v", err)
	}
	if st.Words != 1 || st.Bigrams != 1 || st.Phrases != 0 {
		t.Fatalf("UserStats = %+v, 期望 words=1 bigrams=1 phrases=0", st)
	}

	tsv, err := client.UserExport(false)
	if err != nil {
		t.Fatalf("UserExport 失败: %v", err)
	}
	// 空段（词组）按设计省略
	if !strings.Contains(tsv, "你好\tni hao\t1\t") || strings.Contains(tsv, "# 词组") {
		t.Fatalf("导出内容不符：\n%s", tsv)
	}

	if err := client.UserClear(); err != nil {
		t.Fatalf("UserClear 失败: %v", err)
	}
	if st, err = client.UserStats(); err != nil || st.Words != 0 {
		t.Fatalf("清空后 UserStats = %+v err=%v", st, err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("关闭 server 失败: %v", err)
	}
}

// 多客户端并发访问共享 UserDict：打字、统计、导出交错执行。
// 配合 go test -race 验证 Server 共享 UserDict 的并发安全。
func TestServerConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "conc.sock")

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

	clients := make([]*Client, 0, 4)
	for i := 0; i < 4; i++ {
		c, err := waitForDial(sock)
		if err != nil {
			t.Fatalf("客户端 %d 连接失败: %v", i, err)
		}
		defer c.Close()
		clients = append(clients, c)
	}

	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *Client) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c.Input("nihao")
				c.Select(0)
				c.Input("shi")
				c.Punct(",")
				c.Input("zhong")
				c.Select(0)
				_, _ = c.UserStats()
				_, _ = c.UserExport(j%2 == 0)
			}
		}(i, c)
	}
	wg.Wait()

	if st, err := clients[0].UserStats(); err != nil || st.Words == 0 {
		t.Fatalf("并发后 UserStats = %+v err=%v", st, err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("关闭 server 失败: %v", err)
	}
}

func TestCommitRaw(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

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
	defer srv.Close()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	defer client.Close()

	if _, err := client.Input("nizaiganma"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}

	raw, err := client.CommitRaw()
	if err != nil {
		t.Fatalf("CommitRaw 失败: %v", err)
	}
	if raw != "nizaiganma" {
		t.Errorf("CommitRaw = %q, 期望 nizaiganma", raw)
	}
}

func TestBackspaceWord(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

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
	defer srv.Close()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	defer client.Close()

	if _, err := client.Input("nihaoma"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}

	comp, err := client.BackspaceWord()
	if err != nil {
		t.Fatalf("BackspaceWord 失败: %v", err)
	}
	if comp.Input != "nihao" {
		t.Errorf("BackspaceWord 后 input = %q, 期望 nihao", comp.Input)
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

// TestSelectPrefixProgressive：渐进上屏——selectPrefix 只消耗候选音节
// 对应的 input 前缀，剩余拼音回灌继续组词（committed=已上屏段，
// input/preedit=剩余拼音，candidates=剩余候选）。2026-10-29 用户场景：
// 打长串 jin'tian'wo'chi… 选「今天」不清空整个拼音，剩余继续组词。
func TestSelectPrefixProgressive(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	dict, err := engine.ParseDict(strings.NewReader(`
今天	jin tian	99
我	wo	88
吃	chi	85
了	le	84
一	yi	83
大	da	82
碗	wan	81
一大碗	yi da wan	70
`))
	if err != nil {
		t.Fatalf("解析词库失败: %v", err)
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
	defer srv.Close()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer client.Close()

	// 打长串 jin'tian'wo'chi'le'yi'da'wan，渐进选「今天」（词条路径）
	comp, err := client.Input("jintianwochi leyidawan")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	idx := -1
	for i, c := range comp.Candidates {
		if c.Text == "今天" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("前置失败：候选中应有「今天」")
	}

	comp, _, err = client.SelectPrefix(idx)
	if err != nil {
		t.Fatalf("SelectPrefix 失败: %v", err)
	}
	if comp.Committed != "今天" {
		t.Fatalf("committed 应为「今天」，实际 %q", comp.Committed)
	}
	if comp.Preedit != "wo'chi'le'yi'da'wan" {
		t.Fatalf("剩余拼音应为 wo'chi'le'yi'da'wan，实际 %q", comp.Preedit)
	}
	if len(comp.Candidates) == 0 {
		t.Fatal("剩余拼音应有继续组词的候选")
	}

	// 渐进第二步：剩余拼音 wo'chi'le'yi'da'wan 已组出整句「我吃了一大碗」
	// 排第一——SelectPrefix(0) 选它，剩余耗尽后正常收尾
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "我吃了一大碗" {
		t.Fatalf("剩余候选首选应为整句「我吃了一大碗」：%+v", comp.Candidates)
	}
	comp2, _, err := client.SelectPrefix(0)
	if err != nil {
		t.Fatalf("SelectPrefix(0) 失败: %v", err)
	}
	if comp2.Committed != "我吃了一大碗" {
		t.Fatalf("第二段 committed 应为「我吃了一大碗」，实际 %q", comp2.Committed)
	}
	if comp2.Input != "" || len(comp2.Candidates) != 0 {
		t.Fatalf("剩余耗尽后应无剩余拼音：input=%q candidates=%d", comp2.Input, len(comp2.Candidates))
	}
}
