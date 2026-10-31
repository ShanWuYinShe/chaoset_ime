package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
	"chaoset.ime/ipc"
)

// TestPathsJSON：-print-paths 输出当前平台的 userdict/imported_dict 路径。
func TestPathsJSON(t *testing.T) {
	text, err := pathsJSON()
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal([]byte(text), &paths); err != nil {
		t.Fatalf("pathsJSON 不是合法 JSON: %v (%s)", err, text)
	}
	if !strings.HasSuffix(paths["userdict"], "userdict.json") {
		t.Fatalf("userdict 路径错误: %q", paths["userdict"])
	}
	if !strings.HasSuffix(paths["imported_dict"], "dict.tsv") {
		t.Fatalf("imported_dict 路径错误: %q", paths["imported_dict"])
	}
}

// TestRunDaemonTCPStats：daemon 模式的 endpoint 连接（TCP）能完成 stats 请求，
// 覆盖 runDaemon 从 network/address 改为 ipc.Endpoint 后的路径。
func TestRunDaemonTCPStats(t *testing.T) {
	dict, _, err := engine.LoadDictFilesLenient([]string{"../../data/dict_common.tsv"})
	if err != nil {
		t.Fatalf("加载词库失败: %v", err)
	}
	user, _, err := engine.NewUserDictLenient("")
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听 TCP 失败: %v", err)
	}
	srv := ipc.NewServer(ln, dict, user)
	go func() { _ = srv.Serve() }()
	defer srv.Close()
	if err := runDaemon(ipc.Endpoint{Network: "tcp", Address: ln.Addr().String()}, "stats", false, ""); err != nil {
		t.Fatalf("runDaemon(TCP stats) 失败: %v", err)
	}
}

// TestRunLocalStatsClearExport：本地模式的 stats/clear/export 全链路。
func TestRunLocalStatsClearExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.json")
	u, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("加载用户词典失败: %v", err)
	}
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	if err := runLocal(path, "stats", false, ""); err != nil {
		t.Fatalf("stats 失败: %v", err)
	}
	out := filepath.Join(dir, "export.tsv")
	if err := runLocal(path, "export", false, out); err != nil {
		t.Fatalf("export 失败: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读导出文件失败: %v", err)
	}
	if !strings.Contains(string(data), "你好") {
		t.Fatalf("导出内容应含「你好」，实际 %q", data)
	}

	if err := runLocal(path, "clear", false, ""); err != nil {
		t.Fatalf("clear 失败: %v", err)
	}
	u2, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatalf("重载用户词典失败: %v", err)
	}
	if st := u2.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("clear 后统计应全 0，实际 %+v", st)
	}
}

// TestWriteOutAtomicReplaces：覆盖已有导出文件时整体替换，不留临时文件。
func TestWriteOutAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "export.tsv")
	if err := os.WriteFile(out, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOut(out, "新内容"); err != nil {
		t.Fatalf("writeOut 失败: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "新内容" {
		t.Fatalf("导出文件内容 = %q，期望 新内容", data)
	}
	assertNoTempFiles(t, dir)
}

// TestWriteOutRenameFailureCleansTemp：目标不可替换时报错，旧目标不变且
// 临时文件必须清理（不留下 .chaoset-userdict-*.tmp 垃圾）。
func TestWriteOutRenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "outdir")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeOut(out, "内容"); err == nil {
		t.Fatal("目标是目录时 writeOut 应报错")
	}
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".chaoset-userdict-") {
			t.Fatalf("不应残留临时文件: %s", entry.Name())
		}
	}
}
