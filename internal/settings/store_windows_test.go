//go:build windows

package settings

// store_windows_test.go —— Windows 设置存储的运行时测试（2026-11-08 审计 GO-04）。
//
// 此前 store_windows.go 的注册表读写路径从未被任何测试/CI 执行过（只有 Linux
// store 有测试，CI 的 Windows job 只做交叉编译）。本测试在真实 HKCU 上验证：
// 键/值往返（DWORD/REG_SZ）、未知 ModeScope 回退 system、值类型被手工改坏时
// 回退缺省。测试结束时恢复原值（CI runner 为一次性环境，仍保持可重复运行）。

import (
	"syscall"
	"testing"
	"unsafe"
)

// Windows syscall 包未导出 RegDeleteKey/RegDeleteValue：与 store_windows.go
// 的写值一致，直接用 advapi32 导出（仍属标准库范围）。
var (
	regDeleteKeyW   = winAdvapi32.NewProc("RegDeleteKeyW")
	regDeleteValueW = winAdvapi32.NewProc("RegDeleteValueW")
)

func regDeleteKey(t *testing.T, subKey *uint16) error {
	t.Helper()
	ret, _, _ := regDeleteKeyW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(subKey)))
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

func regDeleteValue(h syscall.Handle, name *uint16) error {
	ret, _, _ := regDeleteValueW.Call(uintptr(h), uintptr(unsafe.Pointer(name)))
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

func TestStoreWindowsLoadDefaultsWhenKeyMissing(t *testing.T) {
	// 删除 HKCUSoftwareChaoset：从未保存过设置时 Load 必须返回三端缺省值。
	subKey, err := syscall.UTF16PtrFromString(windowsRegistrySubKey)
	if err != nil {
		t.Fatalf("UTF16PtrFromString 失败: %v", err)
	}
	if err := regDeleteKey(t, subKey); err != nil &&
		err != syscall.ERROR_FILE_NOT_FOUND && err != syscall.ERROR_PATH_NOT_FOUND {
		t.Fatalf("删除测试注册表键失败: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("键不存在时 Load() 报错: %v", err)
	}
	if got != Defaults() {
		t.Fatalf("键不存在应回退缺省值，实际 %+v", got)
	}
}

// winTestKey 打开/创建测试用注册表键，并在测试结束后恢复三类值。
func winTestKey(t *testing.T) syscall.Handle {
	t.Helper()
	h, err := winCreateKey()
	if err != nil {
		t.Fatalf("创建注册表键失败: %v", err)
	}
	return h
}

func snapshotDWORD(t *testing.T, h syscall.Handle, name string) (uint32, bool) {
	t.Helper()
	v, ok, err := winQueryDWORD(h, name)
	if err != nil {
		t.Fatalf("读取注册表值 %s 失败: %v", name, err)
	}
	return v, ok
}

func restoreDWORD(t *testing.T, h syscall.Handle, name string, v uint32, ok bool) {
	t.Helper()
	if ok {
		if err := winSetDWORD(h, name, v); err != nil {
			t.Fatalf("恢复注册表值 %s 失败: %v", name, err)
		}
		return
	}
	deleteRegistryValue(t, h, name)
}

func snapshotString(t *testing.T, h syscall.Handle, name string) (string, bool) {
	t.Helper()
	v, ok, err := winQueryString(h, name)
	if err != nil {
		t.Fatalf("读取注册表值 %s 失败: %v", name, err)
	}
	return v, ok
}

func restoreString(t *testing.T, h syscall.Handle, name, v string, ok bool) {
	t.Helper()
	if ok {
		if err := winSetString(h, name, v); err != nil {
			t.Fatalf("恢复注册表值 %s 失败: %v", name, err)
		}
		return
	}
	deleteRegistryValue(t, h, name)
}

func deleteRegistryValue(t *testing.T, h syscall.Handle, name string) {
	t.Helper()
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q) 失败: %v", name, err)
	}
	if err := regDeleteValue(h, ptr); err != nil && err != syscall.ERROR_FILE_NOT_FOUND {
		t.Fatalf("删除注册表值 %s 失败: %v", name, err)
	}
}

// withRegistrySnapshot 在测试开始时快照三个设置值，结束后恢复。
func withRegistrySnapshot(t *testing.T) syscall.Handle {
	t.Helper()
	h := winTestKey(t)
	t.Cleanup(func() { syscall.RegCloseKey(h) })

	assoc, assocOK := snapshotDWORD(t, h, winValueAssociate)
	scope, scopeOK := snapshotString(t, h, winValueModeScope)
	english, englishOK := snapshotDWORD(t, h, winValueUiEnglish)
	t.Cleanup(func() {
		restoreDWORD(t, h, winValueAssociate, assoc, assocOK)
		restoreString(t, h, winValueModeScope, scope, scopeOK)
		restoreDWORD(t, h, winValueUiEnglish, english, englishOK)
	})
	return h
}

func TestStoreWindowsRoundTrip(t *testing.T) {
	withRegistrySnapshot(t)

	if err := SetAssociate(false); err != nil {
		t.Fatalf("SetAssociate(false) 报错: %v", err)
	}
	if got, err := Load(); err != nil {
		t.Fatalf("Load() 报错: %v", err)
	} else if got.Associate {
		t.Fatal("SetAssociate(false) 后 Load().Associate 应为 false")
	}
	if err := SetAssociate(true); err != nil {
		t.Fatalf("SetAssociate(true) 报错: %v", err)
	}
	if got, _ := Load(); !got.Associate {
		t.Fatal("SetAssociate(true) 后 Load().Associate 应为 true")
	}

	if err := SetModeScope(ModeScopeProcess); err != nil {
		t.Fatalf("SetModeScope(process) 报错: %v", err)
	}
	if got, _ := Load(); got.ModeScope != ModeScopeProcess {
		t.Fatalf("ModeScope = %q，期望 process", got.ModeScope)
	}
	if err := SetModeScope(ModeScope("bogus")); err == nil {
		t.Fatal("非法 ModeScope 应报错")
	}

	if err := SetUiEnglish(true); err != nil {
		t.Fatalf("SetUiEnglish(true) 报错: %v", err)
	}
	if got, _ := Load(); !got.UiEnglish {
		t.Fatal("SetUiEnglish(true) 后 Load().UiEnglish 应为 true")
	}
}

func TestStoreWindowsModeScopeUnknownFallsBackToSystem(t *testing.T) {
	h := withRegistrySnapshot(t)
	if err := winSetString(h, winValueModeScope, "Bogus"); err != nil {
		t.Fatalf("写入未知 ModeScope 失败: %v", err)
	}
	if got, err := Load(); err != nil {
		t.Fatalf("Load() 报错: %v", err)
	} else if got.ModeScope != ModeScopeSystem {
		t.Fatalf("未知 ModeScope 应回退 system，实际 %q", got.ModeScope)
	}
}

func TestStoreWindowsCorruptTypeFallsBackToDefaults(t *testing.T) {
	h := withRegistrySnapshot(t)
	// 值类型被改坏（DWORD 键写成 REG_SZ）：Load 必须回退缺省，不能返回垃圾。
	if err := winSetString(h, winValueAssociate, "1"); err != nil {
		t.Fatalf("写入错误类型的 Associate 失败: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	if got.Associate != Defaults().Associate {
		t.Fatalf("类型被改坏的 Associate 应回退缺省 %v，实际 %v", Defaults().Associate, got.Associate)
	}
}
