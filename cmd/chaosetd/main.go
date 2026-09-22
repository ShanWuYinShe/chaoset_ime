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
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径（可多次传入合并加载；也可用 --import 产物）")
	userPath := flag.String("userdict", "chaoset-userdict.json", "用户词典路径（空串表示不落盘）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag{"data/dict_seed.tsv"}
	}

	if err := run(*socket, dicts, *userPath); err != nil {
		log.Fatal(err)
	}
}

func run(socket string, dicts []string, userPath string) error {
	dict, err := engine.LoadDictFiles(dicts)
	if err != nil {
		return err
	}
	log.Printf("已加载词典 %d 条（%s）", dict.Len(), strings.Join(dicts, ", "))

	user, err := engine.NewUserDict(userPath)
	if err != nil {
		return err
	}

	// 清理上次异常退出残留的套接字文件
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清理旧套接字失败: %w", err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", socket, err)
	}
	srv := ipc.NewServer(ln, dict, user)
	log.Printf("chaosetd 就绪，套接字: %s", socket)

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
