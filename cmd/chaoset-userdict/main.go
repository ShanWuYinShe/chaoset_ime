// chaoset-userdict 管理用户词典：查看统计、清空学习数据、导出 TSV。
//
// 默认经 IPC 作用于运行中的 chaosetd——清空同时生效于 daemon 内存与
// 磁盘，不存在覆盖竞态；-userdict 指定文件则直接本地操作（daemon
// 须停止，否则学习数据会在下次 bump 时被 daemon 内存态覆盖）。
//
// 用法：
//
//	chaoset-userdict [-socket PATH] stats
//	chaoset-userdict [-socket PATH] clear
//	chaoset-userdict [-socket PATH] export [-phrases] [-o FILE]
//	chaoset-userdict [-tcp ADDR] stats|clear|export ...（Windows 壳的 TCP 形态）
//	chaoset-userdict -userdict FILE stats|clear|export ...
//
// export 的 -phrases 只导出用户词组（词\t拼音\t次数 三列），
// 可直接作为 chaoset-import 的输入词库回流。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"chaoset.ime/engine"
	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/ipc"
)

func main() {
	socket := flag.String("socket", "chaoset.sock", "chaosetd 的 Unix 套接字路径；管理操作经 daemon 生效")
	tcpAddr := flag.String("tcp", "", "经该 TCP 地址连接 chaosetd（如 127.0.0.1:7949，旧壳兼容）；与 -socket/-pipe 二选一")
	usePipe := flag.Bool("pipe", false, "经 Windows 命名管道连接 chaosetd（按当前用户 SID 自动命名；仅 Windows）")
	userPath := flag.String("userdict", "", "直接操作该用户词典文件（本地模式；daemon 运行时清空会被其内存态覆盖，慎用）")
	showVersion := flag.Bool("version", false, "打印构建信息（提交号/构建时间）后退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Info())
		return
	}
	// 显式传入 -socket 又传 -tcp 视为用法错误；只看默认值无法区分「没传」
	// 与「显式传了默认值」，所以用 flag.Visit 检测是否出现过。
	socketSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "socket" {
			socketSet = true
		}
	})
	if (socketSet && *tcpAddr != "") || (*usePipe && (socketSet || *tcpAddr != "")) {
		fmt.Fprintln(os.Stderr, "-socket / -tcp / -pipe 只能三选一")
		os.Exit(2)
	}

	// Go 的 flag 包遇到子命令即停止，export 专属旗标用 FlagSet 二次解析
	cmd := ""
	if args := flag.Args(); len(args) > 0 {
		cmd = args[0]
	}
	phrasesOnly := false
	out := ""
	switch cmd {
	case "export":
		fs := flag.NewFlagSet("export", flag.ExitOnError)
		fs.BoolVar(&phrasesOnly, "phrases", false, "只导出用户词组（三列 TSV，可直接作为 chaoset-import 输入）")
		fs.StringVar(&out, "o", "", "输出文件路径（空则打印到标准输出）")
		if err := fs.Parse(flag.Args()[1:]); err != nil {
			os.Exit(2)
		}
	case "stats", "clear":
		// 无专属旗标
	default:
		fmt.Fprintln(os.Stderr, "用法: chaoset-userdict [-socket PATH | -tcp ADDR | -pipe | -userdict FILE] stats|clear|export [-phrases] [-o FILE]")
		os.Exit(2)
	}

	var err error
	if *userPath != "" {
		err = runLocal(*userPath, cmd, phrasesOnly, out)
	} else {
		ep := ipc.Endpoint{Network: "unix", Address: *socket}
		if *usePipe {
			name, nameErr := ipc.DefaultPipeName()
			if nameErr != nil {
				log.Fatal(nameErr)
			}
			ep = ipc.Endpoint{Network: "pipe", Address: name}
		} else if *tcpAddr != "" {
			ep = ipc.Endpoint{Network: "tcp", Address: *tcpAddr}
		}
		err = runDaemon(ep, cmd, phrasesOnly, out)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// runDaemon 经 IPC 作用于运行中的 chaosetd（推荐；清空无覆盖竞态）。
// ep 为 unix/tcp/pipe 任一传输端点。
func runDaemon(ep ipc.Endpoint, cmd string, phrasesOnly bool, out string) error {
	c, err := ipc.DialEndpoint(ep)
	if err != nil {
		return fmt.Errorf("连接 daemon 失败（daemon 未运行可改用 -userdict 本地模式）: %w", err)
	}
	defer c.Close()

	switch cmd {
	case "stats":
		st, err := c.UserStats()
		if err != nil {
			return err
		}
		printStats(st)
	case "clear":
		before, err := c.UserStats()
		if err != nil {
			return err
		}
		if err := c.UserClear(); err != nil {
			return err
		}
		fmt.Printf("已清空：词 %d 条、词组 %d 条、上下文 %d 条\n", before.Words, before.Phrases, before.Bigrams)
	case "export":
		tsv, err := c.UserExport(phrasesOnly)
		if err != nil {
			return err
		}
		return writeOut(out, tsv)
	}
	return nil
}

// runLocal 直接操作用户词典文件（daemon 须停止）。
func runLocal(path, cmd string, phrasesOnly bool, out string) error {
	u, err := engine.NewUserDict(path)
	if err != nil {
		return err
	}
	switch cmd {
	case "stats":
		printStats(u.Stats())
	case "clear":
		before := u.Stats()
		if err := u.Clear(); err != nil {
			return err
		}
		fmt.Printf("已清空：词 %d 条、词组 %d 条、上下文 %d 条\n", before.Words, before.Phrases, before.Bigrams)
	case "export":
		var buf bytes.Buffer
		if err := u.ExportTSV(&buf, phrasesOnly); err != nil {
			return err
		}
		return writeOut(out, buf.String())
	}
	return nil
}

func printStats(st engine.UserStats) {
	fmt.Printf("词 %d 条 / 词组 %d 条 / 上下文 %d 条\n", st.Words, st.Phrases, st.Bigrams)
}

// writeOut 把导出内容写到 out（空则打印到标准输出）。文件写入走同目录
// 临时文件 + rename：导出失败/进程被杀不会把用户已有的导出文件截成半截
// （与 chaoset-import 的原子写语义一致）。
func writeOut(out, content string) error {
	if out == "" {
		fmt.Print(content)
		return nil
	}
	dir := filepath.Dir(out)
	tmp, err := os.CreateTemp(dir, ".chaoset-userdict-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	removeTmp := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("写出导出文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeTmp()
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		removeTmp()
		return fmt.Errorf("设置导出文件权限失败: %w", err)
	}
	if err := os.Rename(tmpName, out); err != nil {
		removeTmp()
		return fmt.Errorf("替换导出文件失败（旧文件保持不变）: %w", err)
	}
	return nil
}
