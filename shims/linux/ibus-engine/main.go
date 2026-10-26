// chaoset ibus 引擎（Linux）：ibus-daemon 转发的按键事件直接接入内嵌的
// chaoset 引擎（engine 包）。Linux 上 ibus engine 本就是独立进程，
// 与项目「daemon 形态」天然一致，无需额外 IPC。
//
// 验证策略：核心逻辑由 engine 包测试覆盖；DBus 线缆行为在容器内用
// dbus-send/dbus-monitor 端到端验证（见 shims/linux/test.sh）。
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	"chaoset.ime/engine"
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
	// 都找不到：返回系统安装路径报错——它最能提示用户「词库没装」。
	if _, err := os.Stat(installed); err != nil {
		return []string{installed}
	}
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

// defaultUserDictPath 返回 XDG 规范下的用户词典路径。
// 优先 $XDG_DATA_HOME，否则 ~/.local/share；目录不可写时返回空串（退化为
// 仅内存），避免因为一个可选功能让整个引擎起不来。
func defaultUserDictPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "chaoset", "userdict.json")
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
	cfgPath := flag.String("config", defaultConfigPath(), "配置文件路径（默认 XDG 配置目录）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag(resolveDefaultDicts())
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
	if *fuzzy || *trad || *sp != "" {
		log.Printf("配置生效：模糊音=%v 繁体=%v 双拼=%q", *fuzzy, *trad, *sp)
	}

	log.Printf("加载词库: %s", strings.Join(dicts, ", "))
	dict, err := engine.LoadDictFiles(dicts)
	if err != nil {
		log.Fatal(err)
	}
	user, err := engine.NewUserDict(*userPath)
	if err != nil {
		log.Fatal(err)
	}

	factory, err := newIBusFactory(dict, user, *fuzzy, *trad, *sp)
	if err != nil {
		log.Fatal(err)
	}
	if err := factory.run(); err != nil {
		log.Fatal(err)
	}
}
