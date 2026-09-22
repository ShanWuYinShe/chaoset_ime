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
	dictPath := flag.String("dict", "data/dict_seed.tsv", "主词典 TSV 路径（仅本地模式）")
	userPath := flag.String("userdict", "chaoset-userdict.json", "用户词典路径（仅本地模式，空串表示不落盘）")
	flag.Parse()

	ctrl, closeFn, err := buildController(*socket, *dictPath, *userPath)
	if err != nil {
		log.Fatal(err)
	}
	defer closeFn()

	fmt.Println("chaoset 输入法测试台 —— 输入拼音回车看候选；单独数字 1-9 上屏对应候选；:b 退格；:r 重置；:q 退出")
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
		case len(line) == 1 && line[0] >= '1' && line[0] <= '9':
			text, err := ctrl.Select(int(line[0] - '1'))
			if err != nil {
				fmt.Println("错误:", err)
				continue
			}
			fmt.Printf("上屏: %s\n", text)
		default:
			// 每行视为一次全新的击键序列：先清空上一行的组词再输入
			if _, err := ctrl.Reset(); err != nil {
				fmt.Println("错误:", err)
				continue
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

func buildController(socket, dictPath, userPath string) (engine.Controller, func(), error) {
	if socket != "" {
		c, err := ipc.Dial("unix", socket)
		if err != nil {
			return nil, nil, err
		}
		return c, func() { c.Close() }, nil
	}
	f, err := os.Open(dictPath)
	if err != nil {
		return nil, nil, fmt.Errorf("打开词典失败: %w", err)
	}
	defer f.Close()
	dict, err := engine.ParseDict(f)
	if err != nil {
		return nil, nil, err
	}
	user, err := engine.NewUserDict(userPath)
	if err != nil {
		return nil, nil, err
	}
	return engine.NewSession(dict, user), func() {
		// 退出时兜底保存（Bump 本身已即时落盘，这里只覆盖异常路径）
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
	fmt.Printf("输入: %s   预编辑: %s\n", comp.Input, comp.Preedit)
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
