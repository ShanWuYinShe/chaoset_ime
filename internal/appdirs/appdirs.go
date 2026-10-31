// Package appdirs 集中各平台用户数据/运行时目录约定，避免 daemon、工具、
// 平台壳各自拼一套路径（同一份数据被不同进程写到不同位置）。
//
// 这里只做路径计算，不创建目录、不读写文件；平台差异留在本包内，
// engine 仍保持平台中立（engine 不 import 本包）。
package appdirs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const appName = "chaoset"

// DataDir 返回用户数据目录（macOS Application Support / Linux XDG_DATA_HOME /
// Windows LOCALAPPDATA）。调用方负责创建目录。
func DataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("定位用户主目录失败: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", appName), nil
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("定位 LOCALAPPDATA 失败: %w", err)
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "Chaoset"), nil
	default:
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("定位用户主目录失败: %w", err)
			}
			base = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(base, appName), nil
	}
}

// LinuxDataDir 固定按 Linux XDG 约定返回数据目录。
// 供只在 Linux 运行的壳/工具使用：这些代码即使在 macOS 上跑单测，也必须
// 按 Linux 路径断言，不能因为测试机是 macOS 就变成 Application Support。
func LinuxDataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("定位用户主目录失败: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, appName), nil
}

// LinuxUserDictPath / LinuxImportedDictPath 是 Linux ibus 引擎/设置窗专用路径，
// 与 dictimport_ctl.py / userdict_ctl.py 的 XDG 口径一致。
func LinuxUserDictPath() (string, error) {
	dir, err := LinuxDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "userdict.json"), nil
}

func LinuxImportedDictPath() (string, error) {
	dir, err := LinuxDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dict.tsv"), nil
}

// SocketPath 返回 Unix socket 路径。Linux 优先 XDG_RUNTIME_DIR（生命周期随
// 登录会话，重启后自动清理）；macOS 放 Application Support；Windows 用命名
// 管道，不返回 socket 路径。
func SocketPath() (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("Windows 使用命名管道（ipc.DefaultPipeName），没有 Unix socket 路径")
	}
	if runtime.GOOS == "linux" {
		if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
			return filepath.Join(runtimeDir, appName, "chaoset.sock"), nil
		}
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chaoset.sock"), nil
}

// UserDictPath 返回 daemon 用户词典默认路径。
func UserDictPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "userdict.json"), nil
}

// ImportedDictPath 返回 Linux 设置窗导入的用户词库路径（与 ibus 引擎、
// dictimport_ctl.py 同口径）。
func ImportedDictPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dict.tsv"), nil
}
