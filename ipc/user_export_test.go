package ipc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
)

// exportTestUserDict 构造一个带确定性学习记录的用户词典：
// words 个词条、phrases 个词组、bigrams 个上下文共现。延迟落盘避免每条
// bump 都全量写盘（测试结束由 Server.Close 统一落盘）。
func exportTestUserDict(t *testing.T, words, phrases, bigrams int) *engine.UserDict {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	u.SetDeferred(true)
	for i := 0; i < words; i++ {
		if err := u.BumpWord(fmt.Sprintf("词%04d", i), []string{"ci"}); err != nil {
			t.Fatalf("BumpWord 失败: %v", err)
		}
	}
	for i := 0; i < phrases; i++ {
		if err := u.BumpPhrase(fmt.Sprintf("词组%04d", i), []string{"ci", "zu"}); err != nil {
			t.Fatalf("BumpPhrase 失败: %v", err)
		}
	}
	for i := 0; i < bigrams; i++ {
		if err := u.BumpBigram(fmt.Sprintf("词%04d", i), fmt.Sprintf("词%04d", i+1)); err != nil {
			t.Fatalf("BumpBigram 失败: %v", err)
		}
	}
	return u
}

// exportTestServer 起一个真实 Server 并返回已连接的客户端与 server。
//
// 套接字目录用 os.MkdirTemp 而不是 t.TempDir：Unix socket 路径上限约
// 104 字节，t.TempDir 会带上很长的测试名，长测试名 + 长文件名会 bind 失败
// （macOS 上报 "invalid argument"，极易误判成协议问题）。
func exportTestServer(t *testing.T, user *engine.UserDict) (*Client, *Server) {
	t.Helper()
	dir, err := os.MkdirTemp("", "csipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "export.sock")
	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, user)
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })
	c, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, srv
}

// exportFull 用旧协议（不带分页参数）请求整份导出，作为分页拼接的基准。
func exportFull(t *testing.T, c *Client, phrasesOnly bool) string {
	t.Helper()
	var out struct {
		TSV  string `json:"tsv"`
		Next int    `json:"next"`
	}
	params := map[string]any{"phrases_only": phrasesOnly}
	if err := c.call("userExport", params, &out); err != nil {
		t.Fatalf("全量导出失败: %v", err)
	}
	if out.Next != -1 {
		t.Fatalf("limit<=0 的旧协议行为应返回 next=-1，实际 %d", out.Next)
	}
	return out.TSV
}

// userExport 分页协议：limit=7 逐页拉取，next 链正确、每页小于协议上限、
// 拼接结果与全量导出逐字节一致；client.UserExport 对外仍是完整 TSV。
func TestUserExportPagination(t *testing.T) {
	user := exportTestUserDict(t, 50, 5, 5)
	c, _ := exportTestServer(t, user)

	for _, phrasesOnly := range []bool{false, true} {
		full := exportFull(t, c, phrasesOnly)
		if full == "" {
			t.Fatalf("phrasesOnly=%v 全量导出为空", phrasesOnly)
		}
		totalLines := strings.Count(full, "\n")

		// 对外签名不变：内部循环分页，返回完整 TSV。
		got, err := c.UserExport(phrasesOnly)
		if err != nil {
			t.Fatalf("UserExport(phrasesOnly=%v) 失败: %v", phrasesOnly, err)
		}
		if got != full {
			t.Fatalf("UserExport 与全量导出不一致（phrasesOnly=%v）:\n--- 全量 ---\n%s\n--- 分页拼接 ---\n%s",
				phrasesOnly, full, got)
		}

		const limit = 7
		var joined strings.Builder
		offset := 0
		pages := 0
		for {
			var out struct {
				TSV  string `json:"tsv"`
				Next int    `json:"next"`
			}
			params := map[string]any{
				"phrases_only": phrasesOnly,
				"offset":       offset,
				"limit":        limit,
			}
			if err := c.call("userExport", params, &out); err != nil {
				t.Fatalf("分页导出失败（offset=%d）: %v", offset, err)
			}
			if len(out.TSV) > maxMessageSize {
				t.Fatalf("分页 %d 大小 %d 超过协议上限 %d", offset, len(out.TSV), maxMessageSize)
			}
			joined.WriteString(out.TSV)
			pages++
			if out.Next < 0 {
				break
			}
			// 中间页的 next 必须恰好前进 limit 行（页边界稳定）。
			if out.Next != offset+limit {
				t.Fatalf("next 链错误：offset=%d 时 next=%d，期望 %d", offset, out.Next, offset+limit)
			}
			if out.Next <= offset {
				t.Fatalf("next 未前进：offset=%d next=%d", offset, out.Next)
			}
			offset = out.Next
		}
		if joined.String() != full {
			t.Fatalf("分页拼接与全量导出不一致（phrasesOnly=%v）:\n--- 全量 ---\n%s\n--- 分页拼接 ---\n%s",
				phrasesOnly, full, joined.String())
		}
		if want := (totalLines + limit - 1) / limit; pages != want {
			t.Fatalf("分页数 = %d，期望 %d（共 %d 行，每页 %d 行）", pages, want, totalLines, limit)
		}
		if totalLines < 50 && !phrasesOnly {
			t.Fatalf("测试数据不足：全量导出只有 %d 行（期望 50+ 条记录）", totalLines)
		}
	}

	// 越过末尾：返回空页且 next=-1（不是错误）。
	var out struct {
		TSV  string `json:"tsv"`
		Next int    `json:"next"`
	}
	if err := c.call("userExport", map[string]any{"offset": 1 << 20, "limit": 7}, &out); err != nil {
		t.Fatalf("越界分页应返回空页而非错误: %v", err)
	}
	if out.TSV != "" || out.Next != -1 {
		t.Fatalf("越界分页 = (%q, %d)，期望 (\"\", -1)", out.TSV, out.Next)
	}

	// 旧协议兼容：不带参数（limit 缺省 0）仍返回全量、next=-1。
	if full := exportFull(t, c, false); !strings.Contains(full, "# 词") {
		t.Fatalf("旧协议全量导出缺少词段头：\n%s", full)
	}
}

// 单页响应超过 1 MiB 时，客户端必须报错并关闭连接（避免残留未读 body
// 让后续请求串包）。用假 server 直接回一个超限帧，覆盖客户端防御路径。
func TestUserExportOversizedPageClosesConn(t *testing.T) {
	dir, err := os.MkdirTemp("", "csipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "oversize.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		// 读掉请求帧（内容无关紧要）。
		var header [4]byte
		if _, rerr := io.ReadFull(conn, header[:]); rerr != nil {
			return
		}
		if _, rerr := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(header[:]))); rerr != nil {
			return
		}
		// 回一个刚好超过协议上限的响应帧：客户端读侧应拒绝并关闭连接。
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], maxMessageSize+1)
		_, _ = conn.Write(h[:])
		_, _ = conn.Write(make([]byte, maxMessageSize+1))
	}()

	c, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer c.Close()
	if _, err := c.UserExport(false); err == nil {
		t.Fatal("单页超过 1 MiB 应返回错误")
	} else if !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("期望读帧超限错误，实际: %v", err)
	}
	// 连接必须已关闭：继续用同一连接会请求/响应错位（串包）。
	if _, err := c.UserStats(); err == nil {
		t.Fatal("超限分页后连接应已关闭，后续调用必须失败")
	}
}

// 大词典分页：全量 TSV 超过 1 MiB 时，单帧旧路径必然失败，而分页路径
// 必须能完整导出（这是审计 P1 的核心回归）。
func TestUserExportLargeDictPagination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	user, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	user.SetDeferred(true)
	// 每行约 500 字节 × 3000 行 ≈ 1.5 MB > maxMessageSize（1 MiB）。
	longTail := strings.Repeat("测", 150)
	for i := 0; i < 3000; i++ {
		if err := user.BumpWord(fmt.Sprintf("测试词%05d%s", i, longTail), []string{"ce"}); err != nil {
			t.Fatalf("BumpWord 失败: %v", err)
		}
	}

	var expected strings.Builder
	if err := user.ExportTSV(&expected, false); err != nil {
		t.Fatalf("引擎侧导出失败: %v", err)
	}
	if expected.Len() <= maxMessageSize {
		t.Fatalf("测试数据不足：全量 TSV 只有 %d 字节，未超过协议上限 %d", expected.Len(), maxMessageSize)
	}

	c, _ := exportTestServer(t, user)
	got, err := c.UserExport(false)
	if err != nil {
		t.Fatalf("大词典分页导出失败: %v", err)
	}
	if got != expected.String() {
		t.Fatalf("大词典分页导出与引擎全量导出不一致（长度 %d vs %d）", len(got), expected.Len())
	}

	// 旧单帧路径（limit<=0）在这个规模下必然失败——证明分页是必需的。
	// 用独立连接：超限响应会在流里残留未读 body，不能污染后续断言。
	dir, err := os.MkdirTemp("", "csipc")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "legacy.sock")
	dict, err := engine.ParseDict(strings.NewReader(ipcTestDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := NewServer(ln, dict, user)
	go srv.Serve()
	defer srv.Close()
	legacy, err := waitForDial(sock)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer legacy.Close()
	var out struct {
		TSV  string `json:"tsv"`
		Next int    `json:"next"`
	}
	err = legacy.call("userExport", map[string]any{"phrases_only": false}, &out)
	if err == nil {
		t.Fatalf("超过 1 MiB 的单帧响应本应被读侧拒绝，却读取成功（%d 字节）", len(out.TSV))
	}
	if !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("期望读帧超限错误，实际: %v", err)
	}
}
