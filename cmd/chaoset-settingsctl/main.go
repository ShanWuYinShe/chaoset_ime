// chaoset-settingsctl 是三端设置项的 Go 单一真源命令行接口（方案 A）。
//
// # 用途
//
// 三端平台壳（Windows chaoset-ui.exe 设置窗、macOS SettingsWindow、Linux
// GTK3 设置窗）只做渲染；设置项的 schema/默认值/校验/持久化唯一真源在
// Go internal/settings，本命令是它给 shim 的**冷路径**入口（设置窗读取/
// 写入、诊断脚本）。输入热路径（Windows TSF 回调、macOS IMK、Linux ibus）
// 禁止调用本命令，仍读各自本机缓存/原生存储，保证按键延迟不受影响。
//
// # 命令与输出契约
//
//	chaoset-settingsctl get
//	    单行紧凑 JSON（字段顺序固定），写 stdout，末尾换行：
//	    {"associate":true,"mode_scope":"system","ui_english":false}
//
//	chaoset-settingsctl set associate on|off
//	    写联想开关；成功无输出。
//
//	chaoset-settingsctl set mode-scope system|process|window
//	    写中英状态作用域；成功无输出。
//
//	chaoset-settingsctl set ui-english on|off
//	    写 Windows UI 宿主持久化的中英状态镜像（内部用）。
//	    macOS/Linux 壳没有对应存储：报错退出 1（不支持，不是参数错误）。
//
// 取值大小写不敏感（on/ON、SYSTEM 均可），前后空白忽略。
//
// # 退出码
//
//	0  成功
//	1  运行错误（存储不可用、读取失败、当前平台不支持）
//	2  参数非法（未知子命令、缺少参数、枚举值非法）
//
// 约定：错误只写 stderr；stdout 只放 get 的 JSON。help/-h/--help 输出
// 用法到 stdout 并退出 0；无子命令按参数非法处理（退出 2）。
//
// # 存储映射（与现有三端壳逐字兼容；细节见 internal/settings 包注释）
//
//	Windows  HKCU\Software\Chaoset
//	         Associate REG_DWORD 缺省 1
//	         ModeScope REG_SZ    缺省 "system"（system|process|window）
//	         UiEnglish REG_DWORD 缺省 0
//	macOS    UserDefaults 域 ime.chaoset.inputmethod.ChaosetIME（输入法
//	         bundle id；SettingsWindow.swift 编进 ChaosetIME.app）
//	         AssociateEnabled Bool   缺省 true
//	         ChaosetModeScope String 缺省 "system"
//	         ui_english 不支持（macOS 壳不持久化中英状态）
//	Linux    $XDG_CONFIG_HOME/chaoset/config.json（回退
//	         $HOME/.config/chaoset/config.json）
//	         "associate"  bool   缺省 true
//	         "mode_scope" string 缺省 "system"
//	         其它键（fuzzy/traditional/shuangpin 等）原样保留
//	         ui_english 不支持（Linux 壳不持久化中英状态）
package main

import (
	"fmt"
	"io"
	"os"

	"chaoset.ime/internal/settings"
)

const usageText = `用法：
  chaoset-settingsctl get
  chaoset-settingsctl set associate on|off
  chaoset-settingsctl set mode-scope system|process|window
  chaoset-settingsctl set ui-english on|off

get 输出单行 JSON：{"associate":true,"mode_scope":"system","ui_english":false}
退出码：0 成功；1 运行错误；2 参数非法。错误写 stderr。`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 是可单测的入口：返回退出码，不直接 os.Exit（方便断言退出码契约）。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "缺少子命令")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError(stderr, "%s 不接受额外参数", args[0])
		}
		fmt.Fprintln(stdout, usageText)
		return 0
	case "get":
		if len(args) != 1 {
			return usageError(stderr, "get 不接受参数")
		}
		s, err := settings.Load()
		if err != nil {
			return fail(stderr, 1, "读取设置失败: %v", err)
		}
		line, err := s.JSON()
		if err != nil {
			return fail(stderr, 1, "生成设置 JSON 失败: %v", err)
		}
		fmt.Fprintln(stdout, line)
		return 0
	case "set":
		return runSet(args[1:], stderr)
	default:
		return usageError(stderr, "未知子命令 %q", args[0])
	}
}

func runSet(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "set 缺少设置项")
	}
	name := args[0]
	if len(args) > 2 {
		return usageError(stderr, "set %s 只接受一个取值", name)
	}
	value := ""
	if len(args) == 2 {
		value = args[1]
	}
	switch name {
	case "associate":
		if len(args) != 2 {
			return usageError(stderr, "set associate 需要 on|off")
		}
		on, err := settings.ParseOnOff(value)
		if err != nil {
			return fail(stderr, 2, "set associate: %v", err)
		}
		if err := settings.SetAssociate(on); err != nil {
			return fail(stderr, 1, "写入 associate 失败: %v", err)
		}
	case "mode-scope":
		if len(args) != 2 {
			return usageError(stderr, "set mode-scope 需要 system|process|window")
		}
		scope, err := settings.ParseModeScope(value)
		if err != nil {
			return fail(stderr, 2, "set mode-scope: %v", err)
		}
		if err := settings.SetModeScope(scope); err != nil {
			return fail(stderr, 1, "写入 mode-scope 失败: %v", err)
		}
	case "ui-english":
		if len(args) != 2 {
			return usageError(stderr, "set ui-english 需要 on|off")
		}
		on, err := settings.ParseOnOff(value)
		if err != nil {
			return fail(stderr, 2, "set ui-english: %v", err)
		}
		if err := settings.SetUiEnglish(on); err != nil {
			return fail(stderr, 1, "写入 ui-english 失败: %v", err)
		}
	default:
		return usageError(stderr, "未知设置项 %q", name)
	}
	return 0
}

// fail 打印错误到 stderr 并返回退出码（stdout 永远只放 get 的 JSON）。
func fail(stderr io.Writer, code int, format string, args ...any) int {
	fmt.Fprintf(stderr, "chaoset-settingsctl: "+format+"\n", args...)
	return code
}

// usageError 处理参数非法：错误行 + 完整用法，退出码 2。
func usageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "chaoset-settingsctl: "+format+"\n", args...)
	fmt.Fprintln(stderr, usageText)
	return 2
}
