// chaoset ibus 引擎（Linux）：ibus-daemon 转发的按键事件直接接入内嵌的
// chaoset 引擎（engine 包）。Linux 上 ibus engine 本就是独立进程，
// 与项目「daemon 形态」天然一致，无需额外 IPC。
//
// 验证策略：核心逻辑由 engine 包测试覆盖；DBus 线缆行为在容器内用
// dbus-send/dbus-monitor 端到端验证（见 shims/linux/test.sh）。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"chaoset.ime/engine"
	"chaoset.ime/internal/appdirs"
)

// installedDictDir 是安装包把词库放进系统目录的位置（见 Makefile 的
// package-linux 与 shims/linux/README 的安装步骤）。
const installedDictDir = "/usr/share/chaoset"

// installedDictFile 是安装后的主词库文件名（由打包时 dict_common.tsv 改名而来，
// 与 test.sh / setup-container.sh 使用的 /usr/local/share/chaoset/dict.tsv 同名）。
const installedDictFile = "dict.tsv"

// resolveDefaultDicts 在用户没传 -dict 时决定默认词库路径。
//
// 背景（2026-10-25 实测的发行缺陷）：此前默认值写死成相对路径
// "data/dict_common.tsv"，而 ibus-daemon 按 component XML 的 <exec> 拉起引擎时
// **不保证 cwd**，安装包里也只有 data/dict_seed.tsv。结果是装完怎么启动都报
// "打开词库 data/dict_common.tsv 失败" 并立即退出——ibus 里选了这个输入法
// 一个字都打不出来。
//
// 现在的查找顺序（命中即用，全部找不到才用最后一个候选报错，
// 让错误信息里出现最有指导性的路径）：
//  1. 编译期写入的绝对路径（打包时 -ldflags -X 注入，最可靠）；
//  2. 可执行文件同级/上级目录里的 data/*.tsv（解包即用的绿色形态）；
//  3. 系统安装目录 /usr/share/chaoset/dict.tsv（安装包部署位置）；
//  4. 开发仓库的相对路径（go run / 仓库内直接跑时）。
func resolveDefaultDicts() []string {
	if buildTimeDict != "" {
		if _, err := os.Stat(buildTimeDict); err == nil {
			return []string{buildTimeDict}
		}
	}
	if exe, err := os.Executable(); err == nil {
		if real, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		// 解包形态：<root>/bin/chaoset-engine + <root>/data/*.tsv
		for _, rel := range []string{
			filepath.Join(dir, "..", "data", "dict.tsv"),
			filepath.Join(dir, "..", "data", "dict_common.tsv"),
			filepath.Join(dir, "..", "data", "dict_seed.tsv"),
			filepath.Join(dir, "data", "dict.tsv"),
		} {
			if _, err := os.Stat(rel); err == nil {
				return []string{rel}
			}
		}
	}
	installed := filepath.Join(installedDictDir, installedDictFile)
	if _, err := os.Stat(installed); err == nil {
		return []string{installed}
	}
	// 第 4 级：仓库相对路径（go run / 仓库内直接跑）。
	//
	// 2026-10-26 审查发现此处是死代码：中间曾有一个「installed 不存在就
	// 返回 installed」的分支，让第 4 级永远不可达——而函数头注释与 README
	// 都宣称这一级存在（仓库内 go run 必须能找到词库）。删掉死分支，
	// 让回退链与文档一致；装好的发行包永远在前几级命中，不受影响。
	return []string{"data/dict_common.tsv"}
}

// buildTimeDict 由打包流程通过
// -ldflags "-X main.buildTimeDict=/usr/share/chaoset/dict.tsv" 注入；
// 为空表示未注入（开发构建），走运行时探测。
var buildTimeDict string

// repeatFlag 支持同一参数多次传入（与其他二进制一致）。
type repeatFlag []string

func (f *repeatFlag) String() string { return strings.Join(*f, ",") }
func (f *repeatFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// flushUserDictOnExit 退出路径的兜底落盘：先把延迟模式下的脏数据刷下去，
// 再 Save 一次保证「内存态 = 磁盘态」。错误只记日志——退出路径上落盘失败
// 不能反过来阻塞/改变退出码，学习数据是增量优化（与 chaosetd 同一原则）。
func flushUserDictOnExit(u *engine.UserDict) {
	if _, err := u.FlushIfDirty(); err != nil {
		log.Printf("退出前用户词典落盘失败: %v", err)
	}
	if err := u.Save(); err != nil {
		log.Printf("退出前保存用户词典失败: %v", err)
	}
}

// startUserDictFlusher 启动延迟落盘的后台执行者（与 chaosetd 的 2s ticker
// 同一策略，2026-10-30 审计 P1）。
//
// 为什么：Linux 此前只 NewUserDictLenient 而从未 SetDeferred(true)，每次
// 选词都同步做 2–3 次「全量 MarshalIndent + 写临时文件 + rename」，学习
// 数据越大按键越卡（daemon 侧早已修过同一问题，见 cmd/chaosetd）。
// 错误只记一次，避免每 2s 刷屏；后续轮次仍会重试。
func startUserDictFlusher(u *engine.UserDict) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		logged := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := u.FlushIfDirty(); err != nil && !logged {
					logged = true
					log.Printf("用户词典落盘失败: %v", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// defaultUserDictPath 返回 XDG 规范下的用户词典路径。
// 优先 $XDG_DATA_HOME，否则 ~/.local/share；目录不可写时返回空串（退化为
// 仅内存），避免因为一个可选功能让整个引擎起不来。
func defaultUserDictPath() string {
	path, err := appdirs.LinuxUserDictPath()
	if err != nil {
		return ""
	}
	return path
}

// defaultImportedDictPath 返回 Linux 设置窗「导入词库」写入的用户词库路径。
// 与 userdict_ctl.py / dictimport_ctl.py 的 imported_dict_path() 同口径：
// $XDG_DATA_HOME/chaoset/dict.tsv（缺省 ~/.local/share/chaoset/dict.tsv）。
func defaultImportedDictPath() string {
	path, err := appdirs.LinuxImportedDictPath()
	if err != nil {
		return ""
	}
	return path
}

// withImportedDict 在文件可读时把用户导入词库追加到主词库列表。
//
// 这是可选文件：不存在（还没导入过）静默跳过；存在但不可读/不是普通文件时
// 记录警告并跳过——否则 LoadDictFilesLenient 会因打开失败让整个引擎退出，
// 一个损坏的导入文件就足以让输入法完全打不出字。
func withImportedDict(dicts []string, path string) []string {
	if path == "" {
		return dicts
	}
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("警告: 用户导入词库不可读，已跳过: %v", err)
		}
		return dicts
	}
	st, statErr := f.Stat()
	f.Close()
	if statErr != nil || st.IsDir() {
		log.Printf("警告: 用户导入词库不是普通文件，已跳过: %s", path)
		return dicts
	}
	return append(dicts, path)
}

func main() {
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径（可多次传入）")
	ibusFlag := flag.Bool("ibus", false, "由 ibus-daemon 启动时传入（兼容约定，无实际作用）")
	// 空串 = 用 XDG 默认路径（**不是**「不落盘」）。
	//
	// 2026-10-26 修复：此前默认空串，而 engine/userdict.go 的 saveLocked 在
	// path=="" 时直接 return nil——而 ibus 按 component XML 拉起引擎时只传
	// --ibus，从来不传 -userdict。结果是 Linux 上所有调频/bigram/词组进化
	// 只存在于进程内存，退出即丢，「自学习」在 Linux 发行包里根本没生效。
	// 其他端都是可落盘的（macOS Setup 传 support/user.json、Windows
	// install.bat 传 %LOCALAPPDATA%、daemon 默认 chaoset-userdict.json）。
	// 显式传 -userdict= 空值（带等号）才表示真正关闭落盘。
	userPath := flag.String("userdict", defaultUserDictPath(), "用户词典路径（默认 XDG 数据目录；显式传空串则不落盘）")
	// 这三项在发行包里靠配置文件生效（见 config.go）：ibus 按 component XML
	// 拉起引擎时只传 --ibus，flag 永远不会被传，此前等于功能不可达。
	fuzzy := flag.Bool("fuzzy", false, "启用模糊音（也可在 ~/.config/chaoset/config.json 配 fuzzy）")
	trad := flag.Bool("traditional", false, "候选输出繁体（也可在 config.json 配 traditional）")
	sp := flag.String("shuangpin", "", "双拼方案：zrm/mspy/flypy（也可在 config.json 配 shuangpin）")
	assoc := flag.Bool("associate", true, "启用词组联想（也可在 config.json 配 associate=false 关闭）")
	cfgPath := flag.String("config", defaultConfigPath(), "配置文件路径（默认 XDG 配置目录）")
	// Linux 设置窗「导入词库」写入的用户词库（可选）：引擎启动时若文件存在
	// 就与系统词库一起宽松加载，导入的词条立即参与匹配。
	importedDict := flag.String("imported-dict", defaultImportedDictPath(), "用户导入词库 TSV 路径（默认 XDG 数据目录；文件不存在则忽略）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag(resolveDefaultDicts())
	}
	if imported := withImportedDict(nil, *importedDict); len(imported) > 0 {
		dicts = repeatFlag(append([]string(dicts), imported...))
		log.Printf("用户导入词库: %s", imported[0])
	}
	_ = *ibusFlag

	// 合并用户配置：flag 显式传值时优先，否则用配置文件。
	// flag.Visit 只遍历「命令行实际出现过」的 flag，正是我们要的语义。
	cfg, cfgErr := loadUserConfig(*cfgPath)
	if cfgErr != nil {
		// 配置坏了不能静默——用户会以为设置生效了。但也不该让引擎起不来
		// （那样 ibus 里就完全打不出字），所以记录并继续用默认值。
		log.Printf("⚠ %v（已忽略，使用默认配置）", cfgErr)
	}
	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	if !setFlags["fuzzy"] {
		*fuzzy = cfg.Fuzzy
	}
	if !setFlags["traditional"] {
		*trad = cfg.Traditional
	}
	if !setFlags["shuangpin"] {
		*sp = cfg.Shuangpin
	}
	if !setFlags["associate"] && cfg.Associate != nil {
		*assoc = *cfg.Associate
	}
	log.Printf("配置生效：模糊音=%v 繁体=%v 双拼=%q 联想=%v", *fuzzy, *trad, *sp, *assoc)

	log.Printf("加载词库: %s", strings.Join(dicts, ", "))
	// 宽松加载：用户自备词库里的坏行跳过并汇总报告，不让一处脏数据毁掉
	// 整个输入法。内置词库的正确性由 CI 保证（见 engine/data_contract_test.go）。
	dict, dictWarnings, err := engine.LoadDictFilesLenient(dicts)
	if err != nil {
		log.Fatal(err)
	}
	for _, w := range dictWarnings {
		log.Printf("警告: %s", w)
	}
	if dict.Len() == 0 {
		log.Fatalf("没有加载到任何词条，请检查词库路径是否为空或格式不对（%s）", strings.Join(dicts, ", "))
	}
	// 宽松加载：用户词典损坏不能让 ibus 引擎退出（否则整个输入法不可用）。
	// 损坏文件改名备份为 *.corrupt-<时间戳>，与 daemon 侧同一策略。
	user, userWarn, err := engine.NewUserDictLenient(*userPath)
	if err != nil {
		log.Fatal(err)
	}
	if userWarn != "" {
		log.Printf("警告: %s", userWarn)
	}
	// 延迟落盘 + 2s 后台 ticker：与 daemon 对齐，避免每次选词同步全量重写
	// 用户词典（2026-10-30 审计 P1：Linux 是唯一没启用延迟落盘的一端）。
	user.SetDeferred(true)
	stopFlusher := startUserDictFlusher(user)
	defer stopFlusher()

	// 联想开关传 configPath 与「flag 是否显式出现」：引擎在每次进入联想前
	// 重读 config.json，设置窗口保存后对已打开的输入上下文立即生效
	// （与 macOS/Windows 一致；命令行 -associate 显式覆盖时不再重读）。
	factory, err := newIBusFactory(dict, user, *fuzzy, *trad, *sp, *assoc, *cfgPath, setFlags["associate"])
	if err != nil {
		// log.Fatal 走 os.Exit，defer 不会执行：先兜底落盘再退出。
		flushUserDictOnExit(user)
		log.Fatal(err)
	}

	// 进程退出兜底：factory.run() 收到 SIGINT/SIGTERM 时在内部直接
	// os.Exit(0)（见 ibus.go），main 的 defer 不会执行。这里再注册一个信号
	// 通道（signal.Notify 会广播给所有注册者），收到信号先同步落盘。
	//
	// 与 run() 内的 os.Exit 存在竞争：最坏情况仍只丢 2s（与 ticker 同量级）。
	// 要做到零丢失需要 run() 的信号分支在 os.Exit 前同步落盘——那是
	// ibus.go 的改动（本轮由另一 workstream 负责），已在报告中提交给 Lead。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		flushUserDictOnExit(user)
	}()

	if err := factory.run(); err != nil {
		flushUserDictOnExit(user)
		log.Fatal(err)
	}
	flushUserDictOnExit(user)
}
