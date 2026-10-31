package appdirs

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDataDirPlatform 覆盖 macOS/Linux 的目录约定；Windows 由交叉编译
// 保证可编译，路径规则在 Windows 真机/CI 上另行验证。
func TestDataDirPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		home := t.TempDir()
		t.Setenv("HOME", home)
		got, err := DataDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "Library", "Application Support", "chaoset")
		if got != want {
			t.Fatalf("DataDir() = %q，期望 %q", got, want)
		}
	case "linux":
		xdg := t.TempDir()
		t.Setenv("XDG_DATA_HOME", xdg)
		got, err := DataDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(xdg, "chaoset"); got != want {
			t.Fatalf("DataDir() = %q，期望 %q", got, want)
		}
	default:
		t.Skip("当前平台不是 macOS/Linux")
	}
}

// TestSocketPathPlatform：Linux 优先 XDG_RUNTIME_DIR，macOS 用 DataDir。
func TestSocketPathPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "linux":
		runtimeDir := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
		got, err := SocketPath()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(runtimeDir, "chaoset", "chaoset.sock")
		if got != want {
			t.Fatalf("SocketPath() = %q，期望 %q", got, want)
		}
	case "darwin":
		home := t.TempDir()
		t.Setenv("HOME", home)
		got, err := SocketPath()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(got, filepath.Join("chaoset", "chaoset.sock")) {
			t.Fatalf("SocketPath() = %q，应以 chaoset/chaoset.sock 结尾", got)
		}
	default:
		t.Skip("当前平台不是 macOS/Linux")
	}
}

// TestLinuxPaths 固定按 XDG 断言 Linux 专用路径（不随测试机 OS 变化）。
func TestLinuxPaths(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	dir, err := LinuxDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "chaoset"); dir != want {
		t.Fatalf("LinuxDataDir() = %q，期望 %q", dir, want)
	}
	if got, err := LinuxUserDictPath(); err != nil || got != filepath.Join(dir, "userdict.json") {
		t.Fatalf("LinuxUserDictPath() = %q err=%v", got, err)
	}
	if got, err := LinuxImportedDictPath(); err != nil || got != filepath.Join(dir, "dict.tsv") {
		t.Fatalf("LinuxImportedDictPath() = %q err=%v", got, err)
	}
}

// TestAuxPaths 确认 userdict/imported dict 与 DataDir 同根。
func TestAuxPaths(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := UserDictPath(); err != nil || got != filepath.Join(dir, "userdict.json") {
		t.Fatalf("UserDictPath() = %q err=%v", got, err)
	}
	if got, err := ImportedDictPath(); err != nil || got != filepath.Join(dir, "dict.tsv") {
		t.Fatalf("ImportedDictPath() = %q err=%v", got, err)
	}
}
