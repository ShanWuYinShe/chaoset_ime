// chaoset-cli 是阶段 0 的终端测试台：在终端里模拟击键流程，
// 验证引擎与 IPC 链路，不涉及任何图形界面。
// 默认本地内嵌引擎；--socket 指定后经 IPC 连接 chaosetd。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"chaoset.ime/engine"
	"chaoset.ime/ipc"
)

func main() {
	socket := flag.String("socket", "", "连接该套接字上的 chaosetd；为空则使用本地内嵌引擎")
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径，可多次传入（仅本地模式）")
	userPath := flag.String("userdict", "chaoset-userdict.json", "用户词典路径（仅本地模式，空串表示不落盘）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag{"data/dict_seed.tsv"}
	}

	ctrl, closeFn, err := buildController(*socket, dicts, *userPath)
	if err != nil {
		log.Fatal(err)
	}
	defer closeFn()

	fmt.Println("chaoset 输入法测试台 —— 输入拼音回车看候选；单独数字 1-9 上屏对应候选；= 下一页 / - 上一页；标点键 , . ; : ? ! 等输出中文标点；:b 退格；:r 重置；:q 退出")
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == ":q":
			return
		case line == "":
			continue
		case line == ":b":
			comp, err := ctrl.Backspace()
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			printComposition(comp)
		case line == ":r":
			comp, err := ctrl.Reset()
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			printComposition(comp)
		case line == "=":
			comp, err := ctrl.NextPage()
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			printComposition(comp)
		case line == "-":
			comp, err := ctrl.PrevPage()
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			printComposition(comp)
		case len(line) == 1 && line[0] >= '1' && line[0] <= '9':
			text, err := ctrl.Select(int(line[0] - '1'))
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			fmt.Printf("上屏: %s\n", text)
		case len(line) == 1 && engine.IsPunctKey(rune(line[0])):
			// 标点键：组词中先上屏首选候选再出中文标点，空组词直接出标点
			text, err := ctrl.Punct(line)
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			fmt.Printf("上屏: %s\n", text)
		default:
			// 仅当上一行还挂着未上屏的输入时才清空，
			// 保住跨行的学习上下文（词组进化依赖相邻上屏序列）
			if cur, err := ctrl.Input(""); err != nil {
				fmt.Println("错误:", err)
				continue
			} else if cur.Input != "" {
				if _, err := ctrl.Reset(); err != nil {
					fmt.Println("错误:", err)
					continue
				}
			}
			comp, err := ctrl.Input(line)
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			printComposition(comp)
		}
	}
}

// repeatFlag 支持同一参数多次传入，与 chaosetd 保持一致。
type repeatFlag []string

func (f *repeatFlag) String() string { return strings.Join(*f, ",") }
func (f *repeatFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func buildController(socket string, dicts []string, userPath string) (engine.Controller, func(), error) {
	if socket != "" {
		c, err := ipc.Dial("unix", socket)
		if err != nil {
			return nil, nil, err
		}
		return c, func() { c.Close() }, nil
	}
	dict, err := engine.LoadDictFiles(dicts)
	if err != nil {
		return nil, nil, err
	}
	user, err := engine.NewUserDict(userPath)
	if err != nil {
		return nil, nil, err
	}
	return engine.NewSession(dict, user), func() {
		// 退出时兜底保存（bump 本身已即时落盘，这里只覆盖异常路径）
		if err := user.Save(); err != nil {
			log.Printf("保存用户词典失败: %v", err)
		}
	}, nil
}

func printComposition(comp engine.Composition) {
	if comp.Input == "" {
		fmt.Println("(空)")
		return
	}
	pageInfo := ""
	if comp.PageCount > 1 {
		pageInfo = fmt.Sprintf("   [%d/%d 页]", comp.Page+1, comp.PageCount)
	}
	fmt.Printf("输入: %s   预编辑: %s%s\n", comp.Input, comp.Preedit, pageInfo)
	if len(comp.Candidates) == 0 {
		fmt.Println("  (无候选)")
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
	fmt.Println("  " + strings.Join(parts, "  "))
}
