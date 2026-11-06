//go:build windows

package settings

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Windows 存储：HKCU\Software\Chaoset，与 shims/windows/src/win32.cpp
// （按 docs/architecture.md 拆分后归 config.cpp）的
//   - RegGetAssociate / RegSetAssociate（DWORD "Associate"，缺省 1）；
//   - uiLoadModeScope / uiSaveModeScope（REG_SZ "ModeScope"，缺省 "system"；
//     读取 _wcsicmp 大小写不敏感，未知值按 system）；
//   - readHkcuEnglish / uiSaveEnglish（DWORD "UiEnglish"，缺省 0）
//
// 逐字兼容：键名、类型、缺省值、写入格式都不得改。
//
// 只用标准库：syscall 已封装 RegOpenKeyEx/RegQueryValueEx/RegCloseKey；
// 建键与写值的 RegCreateKeyExW/RegSetValueExW 标准库未导出封装，用
// syscall.NewLazyDLL 调 advapi32（与 C 壳调用的是同一组 Win32 API，仍属
// 标准库、无第三方依赖）。
const (
	winValueAssociate = "Associate"
	winValueModeScope = "ModeScope"
	winValueUiEnglish = "UiEnglish"

	winRegOptionNonVolatile = 0 // REG_OPTION_NON_VOLATILE（C 壳同参数）
)

var (
	winAdvapi32        = syscall.NewLazyDLL("advapi32.dll")
	winRegCreateKeyExW = winAdvapi32.NewProc("RegCreateKeyExW")
	winRegSetValueExW  = winAdvapi32.NewProc("RegSetValueExW")
)

// Load 读取 Windows 设置。注册表键/值不存在时取缺省值（与 C 壳一致：
// RegGetAssociate 缺省 1、uiLoadModeScope 缺省 system、UiEnglish 缺省 0）。
func Load() (Settings, error) {
	s := Defaults()
	h, err := winOpenKey(syscall.KEY_READ)
	if err != nil {
		if isWinNotFound(err) {
			return s, nil // 从未保存过设置：全缺省
		}
		return s, fmt.Errorf("打开注册表 HKCU\\%s 失败: %w", windowsRegistrySubKey, err)
	}
	defer syscall.RegCloseKey(h)

	if value, ok, err := winQueryDWORD(h, winValueAssociate); err != nil {
		return s, fmt.Errorf("读取注册表值 %s 失败: %w", winValueAssociate, err)
	} else if ok {
		s.Associate = value != 0
	}
	if value, ok, err := winQueryString(h, winValueModeScope); err != nil {
		return s, fmt.Errorf("读取注册表值 %s 失败: %w", winValueModeScope, err)
	} else if ok {
		s.ModeScope = parseStoredModeScope(value)
	}
	if value, ok, err := winQueryDWORD(h, winValueUiEnglish); err != nil {
		return s, fmt.Errorf("读取注册表值 %s 失败: %w", winValueUiEnglish, err)
	} else if ok {
		s.UiEnglish = value != 0
	}
	return s, nil
}

// SetAssociate 只写 DWORD Associate（与 C 的 RegSetAssociate 一致：建键、
// 写值、不碰其它值）。
func SetAssociate(on bool) error {
	h, err := winCreateKey()
	if err != nil {
		return fmt.Errorf("创建注册表 HKCU\\%s 失败: %w", windowsRegistrySubKey, err)
	}
	defer syscall.RegCloseKey(h)
	value := uint32(0)
	if on {
		value = 1
	}
	if err := winSetDWORD(h, winValueAssociate, value); err != nil {
		return fmt.Errorf("写注册表值 %s 失败: %w", winValueAssociate, err)
	}
	return nil
}

// SetModeScope 只写 REG_SZ ModeScope（小写 system|process|window，C 壳写入
// 同样是小写、带结尾 NUL）。
func SetModeScope(scope ModeScope) error {
	if !scope.Valid() {
		return fmt.Errorf("mode_scope 非法: %q（只允许 system|process|window）", string(scope))
	}
	h, err := winCreateKey()
	if err != nil {
		return fmt.Errorf("创建注册表 HKCU\\%s 失败: %w", windowsRegistrySubKey, err)
	}
	defer syscall.RegCloseKey(h)
	if err := winSetString(h, winValueModeScope, string(scope)); err != nil {
		return fmt.Errorf("写注册表值 %s 失败: %w", winValueModeScope, err)
	}
	return nil
}

// SetUiEnglish 只写 DWORD UiEnglish（UI 宿主上次的中英模式镜像，C 的
// uiSaveEnglish 同键同类型）。
func SetUiEnglish(on bool) error {
	h, err := winCreateKey()
	if err != nil {
		return fmt.Errorf("创建注册表 HKCU\\%s 失败: %w", windowsRegistrySubKey, err)
	}
	defer syscall.RegCloseKey(h)
	value := uint32(0)
	if on {
		value = 1
	}
	if err := winSetDWORD(h, winValueUiEnglish, value); err != nil {
		return fmt.Errorf("写注册表值 %s 失败: %w", winValueUiEnglish, err)
	}
	return nil
}

func winOpenKey(access uint32) (syscall.Handle, error) {
	subKey, err := syscall.UTF16PtrFromString(windowsRegistrySubKey)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, subKey, 0, access, &h); err != nil {
		return 0, err
	}
	return h, nil
}

func winCreateKey() (syscall.Handle, error) {
	subKey, err := syscall.UTF16PtrFromString(windowsRegistrySubKey)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	ret, _, _ := winRegCreateKeyExW.Call(
		uintptr(syscall.HKEY_CURRENT_USER),
		uintptr(unsafe.Pointer(subKey)),
		0, // Reserved
		0, // lpClass
		uintptr(winRegOptionNonVolatile),
		uintptr(syscall.KEY_WRITE),
		0, // lpSecurityAttributes
		uintptr(unsafe.Pointer(&h)),
		0, // lpdwDisposition
	)
	if ret != 0 {
		return 0, syscall.Errno(ret)
	}
	return h, nil
}

func winQueryDWORD(h syscall.Handle, name string) (uint32, bool, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, err
	}
	var value uint32
	var typ uint32
	size := uint32(unsafe.Sizeof(value))
	if err := syscall.RegQueryValueEx(h, namePtr, nil, &typ, (*byte)(unsafe.Pointer(&value)), &size); err != nil {
		if isWinNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	// 类型被手工改坏（如 REG_SZ）时按缺省值处理：C 壳的 RegQueryValueExW
	// 不校验类型，会读到无意义的值；这里宁可回默认也不返回垃圾。
	if typ != syscall.REG_DWORD || size != uint32(unsafe.Sizeof(value)) {
		return 0, false, nil
	}
	return value, true, nil
}

func winQueryString(h syscall.Handle, name string) (string, bool, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false, err
	}
	// ModeScope 只需要 "system"/"process"/"window"；128 个 UTF-16 足够，
	// 更长的值视为异常配置回退缺省（C 壳的 32 字节缓冲同样会回退 system）。
	var buf [128]uint16
	var typ uint32
	size := uint32(len(buf) * 2)
	if err := syscall.RegQueryValueEx(h, namePtr, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		if isWinNotFound(err) || err == syscall.ERROR_MORE_DATA {
			return "", false, nil
		}
		return "", false, err
	}
	// C 壳的 uiLoadModeScope 用 RRF_RT_REG_SZ：类型不是 REG_SZ 时回退 system。
	if typ != syscall.REG_SZ || size < 2 {
		return "", typ == syscall.REG_SZ, nil
	}
	return syscall.UTF16ToString(buf[:size/2]), true, nil
}

func winSetDWORD(h syscall.Handle, name string, value uint32) error {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ret, _, _ := winRegSetValueExW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(namePtr)),
		0,
		uintptr(syscall.REG_DWORD),
		uintptr(unsafe.Pointer(&value)),
		unsafe.Sizeof(value),
	)
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

func winSetString(h syscall.Handle, name, value string) error {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	valueUTF16, err := syscall.UTF16FromString(value) // 含结尾 NUL
	if err != nil {
		return err
	}
	ret, _, _ := winRegSetValueExW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(namePtr)),
		0,
		uintptr(syscall.REG_SZ),
		uintptr(unsafe.Pointer(&valueUTF16[0])),
		uintptr(len(valueUTF16)*2),
	)
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

// isWinNotFound 报告注册表键/值不存在（RegOpenKeyEx/RegQueryValueEx 返回
// ERROR_FILE_NOT_FOUND；子键缺失也可能返回 ERROR_PATH_NOT_FOUND）。
func isWinNotFound(err error) bool {
	return err == syscall.ERROR_FILE_NOT_FOUND || err == syscall.ERROR_PATH_NOT_FOUND
}
