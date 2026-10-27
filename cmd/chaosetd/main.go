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
	"runtime/debug"
	"strings"
	"syscall"
	"time"

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
	tcpAddr := flag.String("tcp", "", "监听 TCP 地址（如 127.0.0.1:7949；设置后忽略 -socket，供 Windows 壳使用）")
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径（可多次传入合并加载；也可用 --import 产物）")
	userPath := flag.String("userdict", "chaoset-userdict.json", "用户词典路径（空串表示不落盘）")
	logPath := flag.String("log", "", "日志输出文件路径（为空则输出到标准日志）")
	fuzzy := flag.Bool("fuzzy", false, "启用模糊音（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）")
	trad := flag.Bool("traditional", false, "候选输出繁体（零错误单字映射，见 engine/s2t.go）")
	shuangpin := flag.String("shuangpin", "", "双拼方案：zrm（自然码）/ mspy（微软）/ flypy（小鹤）；空关闭（键位表见 engine/double_pinyin.go）")
	flag.Parse()

	// 降低 GC 频率，削掉输入路径上的停顿尖峰。
	//
	// 2026-10-26 实测（14 万词库、M1）：daemon 处理单键 input 的耗时
	// p50 仅 1.5ms，但 p99 到 51ms、max 88ms——尖峰来自 GC，而壳侧 IPC
	// 超时只有 100ms。尖峰与超时咬合时客户端拿到 nil，表现为
	// 「切换输入法后首字母丢失」（按键被消费但没上屏）。
	// 同一基准下 GOGC=400 把 p99 从 51ms 压到 18ms、max 压到 20ms。
	// 本进程常驻内存约 100–150MB（词库 14 万条占大头且不会被回收），
	// 放宽 GC 阈值对内存的实际增量很小，换来的是按键延迟的确定性。
	debug.SetGCPercent(400)
	if *logPath != "" {
		if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			defer f.Close()
			log.SetOutput(f)
		}
	}
	if len(dicts) == 0 {
		dicts = repeatFlag{"data/dict_common.tsv"}
	}

	if err := run(*socket, *tcpAddr, dicts, *userPath, *fuzzy, *trad, *shuangpin); err != nil {
		log.Fatal(err)
	}
}

func run(socket, tcpAddr string, dicts []string, userPath string, fuzzy bool, trad bool, shuangpin string) error {
	dict, err := engine.LoadDictFiles(dicts)
	if err != nil {
		return err
	}
	log.Printf("已加载词典 %d 条（%s）", dict.Len(), strings.Join(dicts, ", "))

	user, err := engine.NewUserDict(userPath)
	if err != nil {
		return err
	}
	// daemon 常驻且上屏密集：开启延迟落盘，避免每次上屏都同步重写整份用户
	// 词典（详见 engine/userdict.go 的 deferred 契约）。失败时的落盘错误由
	// 后台 flush 记录日志，不再阻断上屏——学习数据是增量优化，绝不能因为它
	// 丢字符（2026-10-26 修复：此前落盘失败会让 Select 返回错误，用户按数字
	// 键后文字不进文档）。
	user.SetDeferred(true)

	// 单实例保护：必须在 os.Remove(socket) 之前拿到锁。
	// 否则第二个实例会先 unlink 掉正在运行的 daemon 的套接字文件，
	// 再成功监听同名路径——两个进程都不报错（细节见 singleton_unix.go）。
	// TCP 模式（Windows）由端口绑定天然互斥，锁在那边是空操作。
	if tcpAddr == "" {
		lock, lerr := acquireSingleton(socket)
		if lerr != nil {
			return lerr
		}
		defer lock.release()
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
		// 拿到锁之后才清理残留套接字：此时可以确定没有其它实例在用这个路径。
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理旧套接字失败: %w", err)
		}
		ln, err = net.Listen("unix", socket)
		if err != nil {
			return fmt.Errorf("监听 %s 失败: %w", socket, err)
		}
		log.Printf("chaosetd 就绪，套接字: %s", socket)
	}
	var opts []ipc.Option
	if fuzzy {
		opts = append(opts, ipc.WithFuzzy(true))
		log.Printf("模糊音已启用")
	}
	if trad {
		opts = append(opts, ipc.WithTraditional(true))
		log.Printf("繁体输出已启用")
	}
	if shuangpin != "" {
		opts = append(opts, ipc.WithDoublePinyin(shuangpin))
		log.Printf("双拼已启用（%s）", shuangpin)
	}
	srv := ipc.NewServer(ln, dict, user, opts...)

	// SIGTERM 必须一起处理：macOS 的 LaunchAgent（launchctl kickstart -k）与
	// 各平台的 init 系统都用 SIGTERM 停服务。只监听 os.Interrupt 时，SIGTERM
	// 会让进程直接消失——用户词典来不及落盘（最后一次上屏的学习数据丢失），
	// 套接字文件也会残留成无人监听的僵尸路径（客户端连接被拒，表现为
	// 「切换输入法后打不出字」）。2026-10-26 实测确认。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	// 后台定期把脏用户词典落盘（延迟模式的实际执行者）。
	// 间隔取 2s：足够摊薄「每次上屏一次全量写」的开销，又不会在异常退出
	// （被 kill -9、断电）时丢掉太多学习数据。正常退出走 Close() 同步落盘。
	flushCtx, flushStop := context.WithCancel(context.Background())
	defer flushStop()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-flushCtx.Done():
				return
			case <-t.C:
				if wrote, ferr := user.FlushIfDirty(); ferr != nil {
					log.Printf("用户词典落盘失败: %v", ferr)
				} else if wrote {
					// 仅在真的写过盘时记录调试线索，避免刷屏
					_ = wrote
				}
			}
		}
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		log.Printf("收到退出信号，保存用户词典并退出…")
		err := srv.Close()
		// 主动删除套接字文件：正常退出后路径上不该再留下一个无人监听的
		// 僵尸套接字——客户端连它会得到 ECONNREFUSED，表现为「输入法打不出字」。
		// 单实例锁仍持有，所以此时删掉的必然是我们自己的套接字。
		if tcpAddr == "" {
			if rmErr := os.Remove(socket); rmErr != nil && !os.IsNotExist(rmErr) {
				log.Printf("清理套接字失败: %v", rmErr)
			}
		}
		return err
	}
}
