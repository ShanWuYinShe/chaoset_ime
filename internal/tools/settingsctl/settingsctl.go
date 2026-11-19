package settingsctl

import (
	"fmt"
	"io"
	"os"

	"chaoset.ime/internal/settings"
)

const UsageText = `用法：
  chaoset-settingsctl get
  chaoset-settingsctl set associate on|off
  chaoset-settingsctl set mode-scope system|process
  chaoset-settingsctl set theme system|light|dark
  chaoset-settingsctl set ui-english on|off

get 输出单行 JSON：{"associate":true,"mode_scope":"system","ui_english":false,"theme":"system"}
mode-scope 同时接受旧三档值 window 并归一化（window→process）。
theme 取值：system 跟随系统 / light 浅色 / dark 深色。
退出码：0 成功；1 运行错误；2 参数非法。错误写 stderr。`

// Run 是可单测的入口：返回退出码，不直接 os.Exit。
func Run(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if len(args) == 0 {
		return usageError(stderr, "缺少子命令")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError(stderr, "%s 不接受额外参数", args[0])
		}
		fmt.Fprintln(stdout, UsageText)
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
			return usageError(stderr, "set mode-scope 需要 system|process")
		}
		scope, err := settings.ParseModeScope(value)
		if err != nil {
			return fail(stderr, 2, "set mode-scope: %v", err)
		}
		if err := settings.SetModeScope(scope); err != nil {
			return fail(stderr, 1, "写入 mode-scope 失败: %v", err)
		}
	case "theme":
		if len(args) != 2 {
			return usageError(stderr, "set theme 需要 system|light|dark")
		}
		theme, err := settings.ParseTheme(value)
		if err != nil {
			return fail(stderr, 2, "set theme: %v", err)
		}
		if err := settings.SetTheme(theme); err != nil {
			return fail(stderr, 1, "写入 theme 失败: %v", err)
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

func fail(stderr io.Writer, code int, format string, args ...any) int {
	fmt.Fprintf(stderr, "chaoset-settingsctl: "+format+"\n", args...)
	return code
}

func usageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "chaoset-settingsctl: "+format+"\n", args...)
	fmt.Fprintln(stderr, UsageText)
	return 2
}
