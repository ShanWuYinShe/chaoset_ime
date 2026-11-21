package client

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"chaoset.ime/internal/appdirs"
	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/ipc"
)

// 编译期断言：*ipc.Client 满足 Controller（daemon-only 后没有第二个实现）。
var _ ipc.Controller = (*ipc.Client)(nil)

func Main(args []string) int {
	return RunCLI(args, os.Stdout, os.Stderr, os.Stdin)
}

func RunCLI(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if stdin == nil {
		stdin = os.Stdin
	}

	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(stderr)

	socket := fs.String("socket", "", "连接该 Unix 套接字上的 chaosetd；与 -pipe 二选一，缺省用平台默认 daemon socket")
	usePipe := fs.Bool("pipe", false, "经 Windows 命名管道连接 chaosetd（按当前用户 SID 自动命名；仅 Windows）")
	showVersion := fs.Bool("version", false, "打印构建信息（提交号/构建时间）后退出")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.Info())
		return 0
	}
	if *usePipe && *socket != "" {
		fmt.Fprintln(stderr, "-socket / -pipe 只能二选一")
		return 2
	}
	if *socket == "" && !*usePipe {
		p, err := appdirs.SocketPath()
		if err != nil {
			fmt.Fprintln(stderr, "无法确定默认 daemon socket：", err)
			return 1
		}
		*socket = p
	}
	ctrl, closeFn, err := BuildController(*socket, *usePipe)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeFn()

	fmt.Fprintln(stdout, "chaoset 输入法测试台 —— 输入拼音回车看候选；单独数字 1-9 上屏对应候选；= 下一页 / - 上一页；标点键 , . ; : ? ! 等输出中文标点；:b 退格；:r 重置；:q 退出")
	sc := bufio.NewScanner(stdin)
	for {
		fmt.Fprint(stdout, "> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == ":q":
			return 0
		case line == "":
			continue
		case line == ":b":
			comp, err := ctrl.Backspace()
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			PrintComposition(comp, stdout)
		case line == ":bw":
			comp, err := ctrl.BackspaceWord()
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			PrintComposition(comp, stdout)
		case line == ":r" || line == ":c":
			comp, err := ctrl.Reset()
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			PrintComposition(comp, stdout)
		case line == ":enter":
			text, err := ctrl.CommitRaw()
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			fmt.Fprintf(stdout, "上屏: %s\n", text)
		case line == "=" || line == "]":
			comp, err := ctrl.NextPage()
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			PrintComposition(comp, stdout)
		case line == "-" || line == "[":
			comp, err := ctrl.PrevPage()
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			PrintComposition(comp, stdout)
		case len(line) == 1 && line[0] >= '1' && line[0] <= '9':
			comp, committed, err := ctrl.SelectPrefix(int(line[0] - '1'))
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			fmt.Fprintf(stdout, "上屏: %s\n", committed)
			if comp.Input != "" {
				PrintComposition(comp, stdout)
			}
		case len(line) == 1 && isPunctKey(rune(line[0])):
			text, err := ctrl.Punct(line, false)
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			fmt.Fprintf(stdout, "上屏: %s\n", text)
		default:
			if cur, err := ctrl.Input("", false); err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			} else if cur.Input != "" {
				if _, err := ctrl.Reset(); err != nil {
					fmt.Fprintln(stdout, "错误:", err)
					continue
				}
			}
			comp, err := ctrl.Input(line, false)
			if err != nil {
				fmt.Fprintln(stdout, "错误:", err)
				continue
			}
			PrintComposition(comp, stdout)
		}
	}
	return 0
}

// BuildController 连接 daemon（-socket / -pipe 二选一）。
//
// 本地内嵌引擎已随 Go 引擎退役删除：CLI 只作为 daemon 的终端测试台，
// 不复制任何组词逻辑。未指定传输时直接报错并提示先起 daemon，
// 不静默回退（回退会掩盖「以为测的是 daemon、其实测的是本地引擎」）。
func BuildController(socket string, usePipe bool) (ipc.Controller, func(), error) {
	if socket == "" && !usePipe {
		return nil, nil, fmt.Errorf("必须用 -socket 或 -pipe 连接 chaosetd（本地内嵌引擎已移除；先 make run-daemon）")
	}
	ep := ipc.Endpoint{Network: "unix", Address: socket}
	if usePipe {
		name, err := ipc.DefaultPipeName()
		if err != nil {
			return nil, nil, err
		}
		ep = ipc.Endpoint{Network: "pipe", Address: name}
	}
	c, err := ipc.DialEndpoint(ep)
	if err != nil {
		return nil, nil, err
	}
	return c, func() { c.Close() }, nil
}

// isPunctKey 报告该字符是否为由 daemon 接管的标点键（终端测试台用）。
// 键集与 daemon 引擎一致：ASCII 标点 + 引号（引号空组词时才是标点）。
func isPunctKey(r rune) bool {
	return strings.ContainsRune(",.;:?!()[]<>\\~^'\"", r)
}

func PrintComposition(comp ipc.Composition, stdout io.Writer) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if comp.Input == "" {
		fmt.Fprintln(stdout, "(空)")
		return
	}
	pageInfo := ""
	if comp.PageCount > 1 {
		pageInfo = fmt.Sprintf("   [%d/%d 页]", comp.Page+1, comp.PageCount)
	}
	fmt.Fprintf(stdout, "输入: %s   预编辑: %s%s\n", comp.Input, comp.Preedit, pageInfo)
	if len(comp.Candidates) == 0 {
		fmt.Fprintln(stdout, "  (无候选)")
		return
	}
	parts := make([]string, 0, len(comp.Candidates))
	for i, c := range comp.Candidates {
		mark := ""
		if c.FromUser {
			mark = "*"
		}
		parts = append(parts, fmt.Sprintf("%d.%s%s", i+1, c.Text, mark))
	}
	fmt.Fprintln(stdout, "  "+strings.Join(parts, "  "))
}
