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

	comp, err := client.Input("nihao", false)
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

	if _, err := client.Input("nihao", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	committed, err := client.Punct(",", false)
	if err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if committed != "你好，" {
		t.Fatalf("组词中 Punct = %q, 期望 你好，", committed)
	}

	if committed, err = client.Punct(".", false); err != nil {
		t.Fatalf("Punct 失败: %v", err)
	}
	if committed != "。" {
		t.Fatalf("空组词 Punct = %q, 期望 。", committed)
	}

	if _, err := client.Punct("@", false); err == nil {
		t.Fatal("未知标点应返回业务错误")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("关闭 server 失败: %v", err)
	}
}

// input 的 half 字段：双拼 dpPending==0 时 ';' 的标点宽度必须用本次下发的
// 壳状态（2026-10-31 修复 1 键瞬态）；省略 half 的旧客户端保持会话内旧状态。
func TestServerInputHalfWidth(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "half.sock")

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
	srv := NewServer(ln, dict, user, WithDoublePinyin(engine.DPSchemeMSPY))
	go srv.Serve()

	client, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接 daemon 失败: %v", err)
	}
	defer client.Close()

	// 半角：'；' 键应原样上屏 ASCII ';'
	comp, err := client.Input(";", true)
	if err != nil {
		t.Fatalf("Input(半角) 失败: %v", err)
	}
	if comp.Committed != ";" {
		t.Fatalf("半角模式 ';' 上屏 = %q, 期望 \";\"", comp.Committed)
	}

	// 全角：应上屏中文「；」——宽度只对本次请求生效，不残留上一次的半角
	comp, err = client.Input(";", false)
	if err != nil {
		t.Fatalf("Input(全角) 失败: %v", err)
	}
	if comp.Committed != "；" {
		t.Fatalf("全角模式 ';' 上屏 = %q, 期望「；」", comp.Committed)
	}

	comp, err = client.Input(";", false)
	if err != nil {
		t.Fatalf("旧客户端 Input(\";\") 失败: %v", err)
	}
	if comp.Committed != "；" {
		t.Fatalf("旧客户端应保持会话内全角状态，实际 %q", comp.Committed)
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
				c.Input("nihao", false)
				c.Select(0)
				c.Input("shi", false)
				c.Punct(",", false)
				c.Input("zhong", false)
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

	if _, err := client.Input("nizaiganma", false); err != nil {
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

	if _, err := client.Input("nihaoma", false); err != nil {
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

	// 打长串 jin'tian'wo'chi'le'yi'da'wan，逐段渐进上屏（引擎不做 DP 拼合
	// 造词，候选只能来自词典已有词；这里逐段选 今天/我/吃/了/一大碗）。
	comp, err := client.Input("jintianwochi leyidawan", false)
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "今天" {
		t.Fatalf("前置失败：候选首选应为最长已有词前缀「今天」，实际 %+v", comp.Candidates)
	}

	wantSegs := []string{"今天", "我", "吃", "了", "一大碗"}
	wantPre := []string{"wo'chi'le'yi'da'wan", "chi'le'yi'da'wan", "le'yi'da'wan", "yi'da'wan", ""}
	for i, want := range wantSegs {
		comp, _, err = client.SelectPrefix(0)
		if err != nil {
			t.Fatalf("第 %d 段 SelectPrefix 失败: %v", i+1, err)
		}
		if comp.Committed != want {
			t.Fatalf("第 %d 段 committed 应为「%s」，实际 %q（候选: %+v）", i+1, want, comp.Committed, comp.Candidates)
		}
		if comp.Preedit != wantPre[i] {
			t.Fatalf("第 %d 段剩余拼音应为 %q，实际 %q", i+1, wantPre[i], comp.Preedit)
		}
		if comp.Preedit == "" {
			if comp.Input != "" || len(comp.Candidates) != 0 {
				t.Fatalf("剩余耗尽后应无剩余拼音：input=%q candidates=%d", comp.Input, len(comp.Candidates))
			}
			continue
		}
		if len(comp.Candidates) == 0 {
			t.Fatalf("第 %d 段后剩余拼音应有继续组词的候选", i+1)
		}
	}
}

// resetComposition 与 reset 的语义差异必须跨 IPC 保持：
// 前者只丢组词、保留学习上下文（prev/history），后者连上下文一起清。
// 壳在切中英/透传等「继续打字」场景走前者，靠这条链路生效。
func TestServerResetCompositionVsReset(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "resetcomp.sock")

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
	defer srv.Close()

	newClient := func() *Client {
		c, err := waitForDial(sock)
		if err != nil {
			t.Fatalf("连接 daemon 失败: %v", err)
		}
		return c
	}

	// A: resetComposition 后 input 清空，但学习上下文保留（下次组词带 prev 加成）
	c1 := newClient()
	defer c1.Close()
	if _, err := c1.Input("nihao", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := c1.Select(0); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	comp, err := c1.ResetComposition()
	if err != nil {
		t.Fatalf("ResetComposition 失败: %v", err)
	}
	if comp.Input != "" {
		t.Fatalf("ResetComposition 后 input 应清空, 得到 %q", comp.Input)
	}

	// B: reset 仍然可用且清空组词（Esc/失焦语义未变）
	c2 := newClient()
	defer c2.Close()
	if _, err := c2.Input("nihao", false); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	comp2, err := c2.Reset()
	if err != nil {
		t.Fatalf("Reset 失败: %v", err)
	}
	if comp2.Input != "" {
		t.Fatalf("Reset 后 input 应清空, 得到 %q", comp2.Input)
	}
}
