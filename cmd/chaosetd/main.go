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
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"chaoset.ime/engine"
	"chaoset.ime/internal/appdirs"
	"chaoset.ime/internal/buildinfo"
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
	socket := flag.String("socket", "", "Unix 套接字路径（空=按平台用户数据目录生成）")
	tcpAddr := flag.String("tcp", "", "监听 TCP 地址（如 127.0.0.1:7949；仅旧壳兼容，建议用 -pipe/-socket）")
	usePipe := flag.Bool("pipe", false, "使用 Windows 命名管道（按当前用户 SID 自动生成名字；仅 Windows）")
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径（可多次传入合并加载；也可用 --import 产物）")
	userPath := flag.String("userdict", "", "用户词典路径（空=按平台用户数据目录生成；显式传空串表示不落盘）")
	logPath := flag.String("log", "", "日志输出文件路径（为空则输出到标准日志）")
	fuzzy := flag.Bool("fuzzy", false, "启用模糊音（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）")
	trad := flag.Bool("traditional", false, "候选输出繁体（零错误单字映射，见 engine/s2t.go）")
	shuangpin := flag.String("shuangpin", "", "双拼方案：zrm（自然码）/ mspy（微软）/ flypy（小鹤）；空关闭（键位表见 engine/double_pinyin.go）")
	showVersion := flag.Bool("version", false, "打印构建信息（提交号/构建时间）后退出，真机排障先确认装的是不是新构建")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Info())
		return
	}

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
	userdictSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "userdict" {
			userdictSet = true
		}
	})
	resolvedUserPath, err := resolveUserDictPath(*userPath, userdictSet)
	if err != nil {
		log.Fatal(err)
	}

	socketPath := *socket
	if socketPath == "" {
		p, err := appdirs.SocketPath()
		if err != nil {
			log.Fatal(err)
		}
		socketPath = p
	}
	ep, err := resolveEndpoint(socketPath, *tcpAddr, *usePipe)
	if err != nil {
		log.Fatal(err)
	}
	if err := run(ep, dicts, resolvedUserPath, *fuzzy, *trad, *shuangpin); err != nil {
		log.Fatal(err)
	}
}

// resolveUserDictPath 解析 -userdict：显式传入（含显式空串=不落盘）优先，
// 未传入时用平台用户数据目录，避免 daemon 在 cwd 里写学习数据。
func resolveUserDictPath(flagValue string, flagSet bool) (string, error) {
	if flagSet {
		return flagValue, nil
	}
	return appdirs.UserDictPath()
}

// resolveEndpoint 把命令行旗标翻译成 IPC 端点。默认 Unix socket；-tcp 保留
// 一版兼容旧 Windows 壳；-pipe 使用当前用户 SID 自动生成的命名管道名。
func resolveEndpoint(socket, tcpAddr string, usePipe bool) (ipc.Endpoint, error) {
	if usePipe && tcpAddr != "" {
		return ipc.Endpoint{}, fmt.Errorf("-pipe 与 -tcp 只能二选一")
	}
	if tcpAddr != "" {
		return ipc.Endpoint{Network: "tcp", Address: tcpAddr}, nil
	}
	if usePipe {
		name, err := ipc.DefaultPipeName()
		if err != nil {
			return ipc.Endpoint{}, fmt.Errorf("生成命名管道名失败: %w", err)
		}
		return ipc.Endpoint{Network: "pipe", Address: name}, nil
	}
	return ipc.Endpoint{Network: "unix", Address: socket}, nil
}

// run 是 main 的入口：把 SIGINT/SIGTERM 转成 context 后交给 runWithContext。
// 信号处理与生命周期分离后，测试可以用可取消 context 驱动完整启停，而不必
// 向测试进程自己发信号。
//
// SIGTERM 必须一起处理：macOS 的 LaunchAgent（launchctl kickstart -k）与
// 各平台的 init 系统都用 SIGTERM 停服务。只监听 os.Interrupt 时，SIGTERM
// 会让进程直接消失——用户词典来不及落盘（最后一次上屏的学习数据丢失），
// 套接字文件也会残留成无人监听的僵尸路径（客户端连接被拒，表现为
// 「切换输入法后打不出字」）。2026-10-26 实测确认。
func run(ep ipc.Endpoint, dicts []string, userPath string, fuzzy bool, trad bool, shuangpin string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithContext(ctx, ep, dicts, userPath, fuzzy, trad, shuangpin)
}

// runWithContext 是 daemon 的完整生命周期：加载词库/用户词典、单实例锁、
// 监听、serve、按 ctx 取消优雅退出（停 flush ticker → Save → 清理套接字）。
func runWithContext(ctx context.Context, ep ipc.Endpoint, dicts []string, userPath string, fuzzy bool, trad bool, shuangpin string) error {
	// 宽松加载词库：-dict 可能指向用户自备的第三方词库，一行坏数据不能让人家
	// 整个输入法起不来。内置词库的正确性由 CI 保证（生成阶段严格校验 +
	// engine/data_contract_test.go 严格解析断言），运行时只需跳过坏行并报告。
	dict, dictWarnings, err := engine.LoadDictFilesLenient(dicts)
	if err != nil {
		return err
	}
	for _, w := range dictWarnings {
		log.Printf("警告: %s", w)
	}
	// 词条数为 0 说明词库文件全坏/全空——此时静默启动等于给用户一个打不出字
	// 的输入法，必须报错退出并说清原因（与「跳过坏行」不矛盾：那是局部脏数据，
	// 这是整体不可用）。
	if dict.Len() == 0 {
		return fmt.Errorf("没有加载到任何词条，请检查 -dict 指定的词库是否为空或格式不对（%s）",
			strings.Join(dicts, ", "))
	}
	log.Printf("已加载词典 %d 条（%s）", dict.Len(), strings.Join(dicts, ", "))

	// 宽松加载：userdict.json 损坏（断电/磁盘满/手工编辑）不能让 daemon
	// 起不来——学习数据是增量优化，损失它远好过输入法完全不可用。
	// 损坏文件会被改名备份为 *.corrupt-<时间戳>，警告写日志便于排查。
	user, userWarn, err := engine.NewUserDictLenient(userPath)
	if err != nil {
		return err
	}
	if userWarn != "" {
		log.Printf("警告: %s", userWarn)
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
	// TCP/命名管道由系统命名空间天然互斥，不需要锁。
	if ep.Network == "unix" {
		lock, lerr := acquireSingleton(ep.Address)
		if lerr != nil {
			return lerr
		}
		defer lock.release()
	}

	var ln net.Listener
	if ep.Network == "unix" {
		// 默认路径可能位于尚未创建的用户目录（XDG_RUNTIME_DIR/Application Support）。
		if dir := filepath.Dir(ep.Address); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("创建套接字目录 %s 失败: %w", dir, err)
			}
		}
		// 拿到锁之后才清理残留套接字：此时可以确定没有其它实例在用这个路径。
		if err := os.Remove(ep.Address); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理旧套接字失败: %w", err)
		}
	}
	ln, err = ipc.Listen(ep)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", ep, err)
	}
	log.Printf("chaosetd 就绪，%s: %s", ep.Network, ep.Address)
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

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	// 后台定期把脏用户词典落盘（延迟模式的实际执行者）。
	// 间隔取 2s：足够摊薄「每次上屏一次全量写」的开销，又不会在异常退出
	// （被 kill -9、断电）时丢掉太多学习数据。正常退出走 Close() 同步落盘。
	flushCtx, flushStop := context.WithCancel(context.Background())
	defer flushStop()
	// flushDone 在 ticker goroutine 退出后关闭：退出路径必须先停 ticker 并
	// 等它退出，再 srv.Close()（内部 user.Save()）。否则 ticker 可能在 Save
	// 之后才用旧快照写盘，把最后一次落盘覆盖回旧数据（2026-10-30 审计 P1；
	// engine.UserDict 已用 writeMu + 代数守卫兜底，这里再从生命周期上保证
	// 「停 ticker → Save」的顺序，避免依赖底层防御）。
	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-flushCtx.Done():
				return
			case <-t.C:
				if _, ferr := user.FlushIfDirty(); ferr != nil {
					log.Printf("用户词典落盘失败: %v", ferr)
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
		log.Printf("收到退出请求，保存用户词典并退出…")
		// 顺序：先停 flush ticker 并等它退出，再 srv.Close()（内部 Save）。
		// defer flushStop() 只兜底「异常提前返回」的路径，不能代替这里的
		// 同步等待——defer 在进程退出（log.Fatal）时也不会执行。
		flushStop()
		<-flushDone
		err := srv.Close()
		// 主动删除套接字文件：正常退出后路径上不该再留下一个无人监听的
		// 僵尸套接字——客户端连它会得到 ECONNREFUSED，表现为「输入法打不出字」。
		// 单实例锁仍持有，所以此时删掉的必然是我们自己的套接字。
		if ep.Network == "unix" {
			if rmErr := os.Remove(ep.Address); rmErr != nil && !os.IsNotExist(rmErr) {
				log.Printf("清理套接字失败: %v", rmErr)
			}
		}
		return err
	}
}
