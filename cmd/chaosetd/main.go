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

	"chaoset.ime/engine"
	"chaoset.ime/ipc"
)

func main() {
	socket := flag.String("socket", "chaoset.sock", "Unix 套接字路径")
	dictPath := flag.String("dict", "data/dict_seed.tsv", "主词典 TSV 路径")
	userPath := flag.String("userdict", "chaoset-userdict.json", "用户词典路径（空串表示不落盘）")
	flag.Parse()

	if err := run(*socket, *dictPath, *userPath); err != nil {
		log.Fatal(err)
	}
}

func run(socket, dictPath, userPath string) error {
	dictFile, err := os.Open(dictPath)
	if err != nil {
		return fmt.Errorf("打开词典失败: %w", err)
	}
	defer dictFile.Close()
	dict, err := engine.ParseDict(dictFile)
	if err != nil {
		return err
	}
	log.Printf("已加载词典 %s", dictPath)

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
