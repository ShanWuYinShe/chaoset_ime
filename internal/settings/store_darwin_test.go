//go:build darwin

package settings

// store_darwin_test.go —— macOS 设置存储的运行时测试（2026-11-08 审计 GO-04）。
//
// 此前 store_darwin.go 的 defaults 解析/写入路径从未被执行过（只有 Linux store
// 有测试；CI 的 macos job 也不跑 go test）。本测试通过 defaultsRunner 注入点
// 替换 /usr/bin/defaults，验证输出解析、缺省回退、损坏值报错与写入参数——
// 不读写真实用户偏好。

import (
	"os/exec"
	"reflect"
	"strconv"
	"testing"
)

// withDefaultsRunner 在测试期间替换 defaults 执行器，结束后恢复。
func withDefaultsRunner(t *testing.T, fn func(args ...string) (string, error)) {
	t.Helper()
	old := defaultsRunner
	defaultsRunner = fn
	t.Cleanup(func() { defaultsRunner = old })
}

// exitStatusError 构造带指定退出码的 *exec.ExitError（模拟 defaults read 未设置 = 退出码 1）。
func exitStatusError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("构造退出码 %d 的 ExitError 失败", code)
	}
	return err
}

func TestStoreDarwinLoad(t *testing.T) {
	var calls [][]string
	withDefaultsRunner(t, func(args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) != 3 || args[0] != "read" {
			t.Fatalf("意外的 defaults 参数: %v", args)
		}
		switch args[2] {
		case macKeyAssociate:
			return "1", nil
		case macKeyModeScope:
			return "\"process\"", nil // 某些系统版本带引号
		}
		t.Fatalf("意外的 defaults 读取键: %v", args)
		return "", nil
	})
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	want := Settings{Associate: true, ModeScope: ModeScopeProcess, UiEnglish: false}
	if got != want {
		t.Fatalf("Load() = %+v, 期望 %+v", got, want)
	}
	if len(calls) != 2 {
		t.Fatalf("Load() 应只读两个键，实际调用 %d 次", len(calls))
	}
	// 域与键名必须与 macOS 壳逐字一致（改一个字符 Swift 侧就读不到）。
	for _, c := range calls {
		if c[1] != defaultsDomain {
			t.Fatalf("defaults 域 = %q，期望 %q", c[1], defaultsDomain)
		}
	}
}

func TestStoreDarwinDefaultsWhenUnset(t *testing.T) {
	unsetErr := exitStatusError(t, 1)
	withDefaultsRunner(t, func(args ...string) (string, error) { return "", unsetErr })
	got, err := Load()
	if err != nil {
		t.Fatalf("键未设置时 Load() 报错: %v", err)
	}
	if got != Defaults() {
		t.Fatalf("键未设置应回退缺省值，实际 %+v", got)
	}
}

func TestStoreDarwinCorruptBool(t *testing.T) {
	withDefaultsRunner(t, func(args ...string) (string, error) {
		if args[2] == macKeyAssociate {
			return "banana", nil
		}
		return "system", nil
	})
	if _, err := Load(); err == nil {
		t.Fatal("损坏的 AssociateEnabled 值应报错，而不是静默用缺省值")
	}
}

func TestStoreDarwinReadFailureNotTreatedAsUnset(t *testing.T) {
	// 退出码 1 = 未设置；其它退出码（如 255 权限/域错误）必须上报，不能静默回缺省。
	withDefaultsRunner(t, func(args ...string) (string, error) {
		return "", exitStatusError(t, 255)
	})
	if _, err := Load(); err == nil {
		t.Fatal("非「未设置」的 defaults 失败应上报错误")
	}
}

func TestStoreDarwinWrites(t *testing.T) {
	var calls [][]string
	withDefaultsRunner(t, func(args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		return "", nil
	})
	if err := SetAssociate(false); err != nil {
		t.Fatalf("SetAssociate(false) 报错: %v", err)
	}
	if err := SetModeScope(ModeScopeWindow); err != nil {
		t.Fatalf("SetModeScope(window) 报错: %v", err)
	}
	if err := SetUiEnglish(true); err == nil {
		t.Fatal("macOS 没有 UiEnglish 持久化，SetUiEnglish 应返回错误")
	}
	want := [][]string{
		{"write", defaultsDomain, macKeyAssociate, "-bool", "false"},
		{"write", defaultsDomain, macKeyModeScope, "-string", "window"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("defaults 写入调用 = %v，期望 %v", calls, want)
	}

	// 非法作用域必须在调用 defaults 之前就拒绝。
	calls = nil
	if err := SetModeScope(ModeScope("bogus")); err == nil {
		t.Fatal("非法 ModeScope 应报错")
	}
	if len(calls) != 0 {
		t.Fatalf("非法 ModeScope 不应调用 defaults，实际: %v", calls)
	}
}

// TestStoreDarwinParseDefaultsOutput 锁住 defaults 输出解析口径（不同 macOS 版本
// 的输出格式差异是这一层最容易漂移的地方）。
func TestStoreDarwinParseDefaultsOutput(t *testing.T) {
	boolCases := []struct {
		in   string
		want bool
		ok   bool
	}{
		{"1", true, true},
		{"0", false, true},
		{"true", true, true},
		{"FALSE", false, true},
		{"yes", true, true},
		{"2", true, true},
		{"-3", true, true},
		{"", false, false},
		{"banana", false, false},
	}
	for _, c := range boolCases {
		got, err := parseDefaultsBool(c.in)
		if c.ok != (err == nil) {
			t.Errorf("parseDefaultsBool(%q) err = %v, 期望 ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parseDefaultsBool(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
	strCases := []struct{ in, want string }{
		{"system", "system"},
		{"  process  ", "process"},
		{"\"window\"", "window"},
		{"\"\"", ""},
	}
	for _, c := range strCases {
		if got := parseDefaultsString(c.in); got != c.want {
			t.Errorf("parseDefaultsString(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
