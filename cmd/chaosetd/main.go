// chaosetd 是输入法守护进程：加载词典，通过 IPC 向各平台壳提供组词会话。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"

	"chaoset.ime/engine"
	"chaoset.ime/ipc"
	"chaoset.ime/renderer"
)

// repeatFlag 支持同一参数多次传入（如多个 -dict 叠加加载）。
type repeatFlag []string

func (f *repeatFlag) String() string { return strings.Join(*f, ",") }
func (f *repeatFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	socket := flag.String("socket", "chaoset.sock", "Unix 套接字路径")
	tcpAddr := flag.String("tcp", "", "监听 TCP 地址（如 127.0.0.1:7949；设置后忽略 -socket，供 Windows 壳使用）")
	fontPath := flag.String("font", "", "候选窗渲染 CJK 字体路径（空则按平台探测；找不到则禁用渲染）")
	fontSize := flag.Float64("fontsize", 16, "候选窗字号")
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径（可多次传入合并加载；也可用 --import 产物）")
	userPath := flag.String("userdict", "chaoset-userdict.json", "用户词典路径（空串表示不落盘）")
	fuzzy := flag.Bool("fuzzy", false, "启用模糊音（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）")
	trad := flag.Bool("traditional", false, "候选输出繁体（零错误单字映射，见 engine/s2t.go）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag{"data/dict_seed.tsv"}
	}

	if err := run(*socket, *tcpAddr, *fontPath, *fontSize, dicts, *userPath, *fuzzy, *trad); err != nil {
		log.Fatal(err)
	}
}

// serverOptions 组装 Server 可选能力：字体可解析时启用 candidateImage 渲染。
func serverOptions(fontPath string, fontSize float64) []ipc.Option {
	skin := renderer.DefaultSkin()
	if fontPath != "" {
		skin.FontPath = fontPath
	}
	if fontSize > 0 {
		skin.FontSize = fontSize
	}
	if skin.FontPath == "" {
		log.Printf("未找到 CJK 字体，candidateImage 渲染未启用")
		return nil
	}
	if _, err := os.Stat(skin.FontPath); err != nil {
		log.Printf("字体 %s 不可用，candidateImage 渲染未启用", skin.FontPath)
		return nil
	}
	log.Printf("candidateImage 渲染已启用（字体 %s，字号 %.0f）", skin.FontPath, skin.FontSize)
	return []ipc.Option{ipc.WithSkin(skin)}
}

func run(socket, tcpAddr, fontPath string, fontSize float64, dicts []string, userPath string, fuzzy bool, trad bool) error {
	dict, err := engine.LoadDictFiles(dicts)
	if err != nil {
		return err
	}
	log.Printf("已加载词典 %d 条（%s）", dict.Len(), strings.Join(dicts, ", "))

	user, err := engine.NewUserDict(userPath)
	if err != nil {
		return err
	}

	var ln net.Listener
	if tcpAddr != "" {
		// TCP 回环：Windows 壳的传输形态（Windows 服务无 Unix socket 语义）
		ln, err = net.Listen("tcp", tcpAddr)
		if err != nil {
			return fmt.Errorf("监听 %s 失败: %w", tcpAddr, err)
		}
		log.Printf("chaosetd 就绪，TCP: %s", tcpAddr)
	} else {
		// 清理上次异常退出残留的套接字文件
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理旧套接字失败: %w", err)
		}
		ln, err = net.Listen("unix", socket)
		if err != nil {
			return fmt.Errorf("监听 %s 失败: %w", socket, err)
		}
		log.Printf("chaosetd 就绪，套接字: %s", socket)
	}
	opts := serverOptions(fontPath, fontSize)
	if fuzzy {
		opts = append(opts, ipc.WithFuzzy(true))
		log.Printf("模糊音已启用")
	}
	if trad {
		opts = append(opts, ipc.WithTraditional(true))
		log.Printf("繁体输出已启用")
	}
	srv := ipc.NewServer(ln, dict, user, opts...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		log.Printf("收到退出信号，保存用户词典并退出…")
		return srv.Close()
	}
}
