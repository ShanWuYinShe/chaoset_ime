package userdict

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"chaoset.ime/internal/appdirs"
	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/ipc"
)

type CmdArgs struct {
	PhrasesOnly bool
	Out         string
	Word        string
	Sylls       []string
}

func Main(args []string) int {
	return RunCLI(args, os.Stdout, os.Stderr)
}

func RunCLI(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	fs := flag.NewFlagSet("userdict", flag.ContinueOnError)
	fs.SetOutput(stderr)

	socket := fs.String("socket", "", "chaosetd 的 Unix 套接字路径（空=按平台用户数据目录生成）；管理操作经 daemon 生效")
	usePipe := fs.Bool("pipe", false, "经 Windows 命名管道连接 chaosetd（按当前用户 SID 自动命名；仅 Windows）")
	userPath := fs.String("userdict", "", "直接操作该用户词典文件（本地模式；daemon 运行时清空会被其内存态覆盖，慎用）")
	showVersion := fs.Bool("version", false, "打印构建信息（提交号/构建时间）后退出")
	printPaths := fs.Bool("print-paths", false, "打印平台数据路径 JSON 后退出（设置窗/脚本用）")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.Info())
		return 0
	}
	if *printPaths {
		text, err := PathsJSON()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, text)
		return 0
	}

	socketSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "socket" {
			socketSet = true
		}
	})
	if *usePipe && socketSet {
		fmt.Fprintln(stderr, "-socket / -pipe 只能二选一")
		return 2
	}

	cmd := ""
	if rest := fs.Args(); len(rest) > 0 {
		cmd = rest[0]
	}
	cArgs := CmdArgs{}
	switch cmd {
	case "export":
		expFs := flag.NewFlagSet("export", flag.ContinueOnError)
		expFs.SetOutput(stderr)
		expFs.BoolVar(&cArgs.PhrasesOnly, "phrases", false, "只导出用户词组（三列 TSV，可直接作为 chaoset-import 输入）")
		expFs.StringVar(&cArgs.Out, "o", "", "输出文件路径（空则打印到标准输出）")
		if err := expFs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
	case "delete":
		rest := fs.Args()[1:]
		if len(rest) == 0 || rest[0] == "" {
			fmt.Fprintln(stderr, "用法: chaoset userdict ... delete 词 [音节...]")
			return 2
		}
		cArgs.Word = rest[0]
		cArgs.Sylls = SplitSyllables(strings.Join(rest[1:], " "))
	case "stats", "clear":
		// 无专属旗标
	default:
		fmt.Fprintln(stderr, "用法: chaoset userdict [-socket PATH | -pipe | -userdict FILE] stats|clear|delete 词 [音节...]|export [-phrases] [-o FILE]")
		return 2
	}

	var err error
	if *userPath != "" {
		err = RunLocal(*userPath, cmd, cArgs, stdout)
	} else {
		ep := ipc.Endpoint{Network: "unix", Address: *socket}
		if *usePipe {
			name, nameErr := ipc.DefaultPipeName()
			if nameErr != nil {
				fmt.Fprintln(stderr, nameErr)
				return 1
			}
			ep = ipc.Endpoint{Network: "pipe", Address: name}
		} else if ep.Address == "" {
			socketPath, pathErr := appdirs.SocketPath()
			if pathErr != nil {
				fmt.Fprintf(stderr, "%v（Windows 请显式加 -pipe，或加 -userdict FILE 走本地文件模式）\n", pathErr)
				return 1
			}
			ep.Address = socketPath
		}
		err = RunDaemon(ep, cmd, cArgs, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func PathsJSON() (string, error) {
	paths := make(map[string]string)
	add := func(key string, fn func() (string, error)) {
		if value, err := fn(); err == nil {
			paths[key] = value
		}
	}
	add("data", appdirs.DataDir)
	add("userdict", appdirs.UserDictPath)
	add("imported_dict", appdirs.ImportedDictPath)
	add("config", appdirs.ConfigPath)
	add("socket", appdirs.SocketPath)
	data, err := json.Marshal(paths)
	if err != nil {
		return "", fmt.Errorf("序列化路径失败: %w", err)
	}
	return string(data), nil
}

func SplitSyllables(s string) []string {
	return strings.Fields(strings.ReplaceAll(s, "'", " "))
}

func RunDaemon(ep ipc.Endpoint, cmd string, args CmdArgs, stdout io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
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
		printStats(st.Words, st.Phrases, st.Bigrams, stdout)
	case "clear":
		before, err := c.UserStats()
		if err != nil {
			return err
		}
		if err := c.UserClear(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已清空：词 %d 条、词组 %d 条、上下文 %d 条\n", before.Words, before.Phrases, before.Bigrams)
	case "delete":
		deleted, err := c.UserDelete(args.Word, args.Sylls)
		if err != nil {
			return err
		}
		printDeleteResult(args.Word, deleted, stdout)
	case "export":
		tsv, err := c.UserExport(args.PhrasesOnly)
		if err != nil {
			return err
		}
		return writeOut(args.Out, tsv, stdout)
	}
	return nil
}

func RunLocal(path, cmd string, args CmdArgs, stdout io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	u, err := newLocalUserDict(path)
	if err != nil {
		return err
	}
	switch cmd {
	case "stats":
		w, p, b := u.Stats()
		printStats(w, p, b, stdout)
	case "clear":
		w, p, b := u.Stats()
		if err := u.Clear(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已清空：词 %d 条、词组 %d 条、上下文 %d 条\n", w, p, b)
	case "delete":
		deleted, err := u.DeleteWord(args.Word, args.Sylls)
		if err != nil {
			return err
		}
		printDeleteResult(args.Word, deleted, stdout)
	case "export":
		var buf bytes.Buffer
		if err := u.ExportTSV(&buf, args.PhrasesOnly); err != nil {
			return err
		}
		return writeOut(args.Out, buf.String(), stdout)
	}
	return nil
}

func printDeleteResult(word string, deleted bool, stdout io.Writer) {
	if deleted {
		fmt.Fprintf(stdout, "已删除「%s」的学习记录\n", word)
		return
	}
	fmt.Fprintf(stdout, "未找到「%s」的学习记录（可能未学习过或已删除）\n", word)
}

// printStats 打印三类学习记录条目数。参数拆开而不是收一个结构体：
// daemon 路径拿 ipc.UserStats、本地路径拿 engine.UserStats，二者字段相同。
func printStats(words, phrases, bigrams int, stdout io.Writer) {
	fmt.Fprintf(stdout, "词 %d 条 / 词组 %d 条 / 上下文 %d 条\n", words, phrases, bigrams)
}

func WriteOut(out, content string, stdout io.Writer) error {
	return writeOut(out, content, stdout)
}

func writeOut(out, content string, stdout io.Writer) error {
	if out == "" {
		fmt.Fprint(stdout, content)
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
