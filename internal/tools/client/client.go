package client

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"chaoset.ime/engine"
	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/internal/cliutil"
	"chaoset.ime/ipc"
)

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

	socket := fs.String("socket", "", "连接该 Unix 套接字上的 chaosetd；与 -pipe 二选一，都为空则使用本地内嵌引擎")
	usePipe := fs.Bool("pipe", false, "经 Windows 命名管道连接 chaosetd（按当前用户 SID 自动命名；仅 Windows）")
	var dicts cliutil.RepeatFlag
	fs.Var(&dicts, "dict", "主词典 TSV 路径，可多次传入（仅本地模式）")
	userPath := fs.String("userdict", "chaoset-userdict.json", "用户词典路径（仅本地模式，空串表示不落盘）")
	fuzzy := fs.Bool("fuzzy", false, "启用模糊音（仅本地模式；daemon 模式由 chaosetd 的 -fuzzy 决定）")
	trad := fs.Bool("traditional", false, "候选输出繁体（仅本地模式；daemon 模式由 chaosetd 的 -traditional 决定）")
	sp := fs.String("shuangpin", "", "双拼方案：zrm（自然码）/ mspy（微软）/ flypy（小鹤）；空关闭（仅本地模式；daemon 模式由 chaosetd 的 -shuangpin 决定）")
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
	if len(dicts) == 0 {
		dicts = cliutil.RepeatFlag{"data/dict_common.tsv"}
	}

	ctrl, closeFn, err := BuildController(*socket, *usePipe, dicts, *userPath, *fuzzy, *trad, *sp)
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
		case len(line) == 1 && engine.IsPunctKey(rune(line[0])):
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

func BuildController(socket string, usePipe bool, dicts []string, userPath string, fuzzy bool, trad bool, sp string) (engine.Controller, func(), error) {
	if socket != "" || usePipe {
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
	dict, dictWarnings, err := engine.LoadDictFilesLenient(dicts)
	if err != nil {
		return nil, nil, err
	}
	for _, w := range dictWarnings {
		fmt.Fprintf(os.Stderr, "警告: %s\n", w)
	}
	if dict.Len() == 0 {
		return nil, nil, fmt.Errorf("没有加载到任何词条，请检查 -dict 指定的词库是否为空或格式不对")
	}
	user, err := engine.NewUserDict(userPath)
	if err != nil {
		return nil, nil, err
	}
	sess := engine.NewSession(dict, user)
	sess.SetFuzzy(fuzzy)
	sess.SetTraditional(trad)
	sess.SetDoublePinyin(sp)
	return sess, func() {
		_ = user.Save()
	}, nil
}

func PrintComposition(comp engine.Composition, stdout io.Writer) {
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
