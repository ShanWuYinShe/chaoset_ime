// chaoset 是 Chaoset 拼音输入法的多功能命令行工具。
//
// 集成了词库导入合并（import）、用户词典管理（userdict）、
// 配置读写（config / settingsctl）以及终端测试（client / cli）。
//
// 支持以特定名称（如 chaoset-import、chaoset-userdict 等符号链接或硬链接）
// 直接调用（Multicall），无缝兼容原有工具接口。
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/internal/tools/client"
	"chaoset.ime/internal/tools/dictimport"
	"chaoset.ime/internal/tools/settingsctl"
	"chaoset.ime/internal/tools/userdict"
)

const usageText = `Chaoset 拼音输入法全能命令行控制工具 (chaosetctl)

用法:
  chaosetctl <command> [flags...] [args...]

可用命令:
  config | settings    读取或写入输入法设置项（开关/模式/主题）
  import               导入与合并词库文件（TSV 或 Rime dict.yaml）
  userdict             管理用户自学习词典（查看统计/导出/清空/按词删除）
  client | cli         终端交互测试台与 IPC 链路测试
  version              显示版本信息
  help                 显示帮助说明

多调用模式 (Multicall):
  当程序名（argv[0]）为 chaoset-import、chaoset-userdict、chaoset-settingsctl、
  chaoset-cli 时，将自动分发至对应的子命令执行。

输入 'chaosetctl <command> -h' 查看各子命令的详细用法。`

func Run(args []string, stdout, stderr io.Writer, progName string) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	base := filepath.Base(progName)
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")

	// 1. Multicall dispatch by argv[0]
	switch base {
	case "chaoset-import":
		return dictimport.RunCLI(args, stdout, stderr)
	case "chaoset-userdict":
		return userdict.RunCLI(args, stdout, stderr)
	case "chaoset-settingsctl":
		return settingsctl.Run(args, stdout, stderr)
	case "chaoset-settings":
		if len(args) > 0 && args[0] != "gui" && args[0] != "--gui" && !strings.HasPrefix(args[0], "-") {
			return settingsctl.Run(args, stdout, stderr)
		}
		return launchSettingsGUI(args, stdout, stderr)
	case "chaoset-cli":
		return client.RunCLI(args, stdout, stderr, os.Stdin)
	}

	// 2. Dispatch by subcommand
	if len(args) == 0 {
		fmt.Fprintln(stdout, usageText)
		return 0
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usageText)
		return 0
	case "version", "-version", "--version", "-v":
		fmt.Fprintln(stdout, buildinfo.Info())
		return 0
	case "import", "dict-import":
		return dictimport.RunCLI(subArgs, stdout, stderr)
	case "userdict":
		return userdict.RunCLI(subArgs, stdout, stderr)
	case "settingsctl", "settings", "config":
		if len(subArgs) > 0 && (subArgs[0] == "gui" || subArgs[0] == "--gui") {
			return launchSettingsGUI(subArgs[1:], stdout, stderr)
		}
		return settingsctl.Run(subArgs, stdout, stderr)
	case "get", "set":
		return settingsctl.Run(args, stdout, stderr)
	case "cli", "client", "test":
		return client.RunCLI(subArgs, stdout, stderr, os.Stdin)
	default:
		fmt.Fprintf(stderr, "未知子命令 %q\n\n", subcmd)
		fmt.Fprintln(stderr, usageText)
		return 2
	}
}

func launchSettingsGUI(args []string, stdout, stderr io.Writer) int {
	exeName := "chaoset-settings"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	self, err := os.Executable()
	var bin string
	if err == nil {
		dir := filepath.Dir(self)
		candidate := filepath.Join(dir, exeName)
		if _, err := os.Stat(candidate); err == nil {
			bin = candidate
		}
	}
	if bin == "" {
		if p, err := exec.LookPath(exeName); err == nil {
			bin = p
		}
	}
	if bin == "" {
		fmt.Fprintf(stderr, "未找到 %s 可执行文件\n", exeName)
		return 1
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "启动设置窗口失败: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr, os.Args[0]))
}
