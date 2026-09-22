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
	"strings"

	"chaoset.ime/engine"
)

// repeatFlag 支持同一参数多次传入（与其他二进制一致）。
type repeatFlag []string

func (f *repeatFlag) String() string { return strings.Join(*f, ",") }
func (f *repeatFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	var dicts repeatFlag
	flag.Var(&dicts, "dict", "主词典 TSV 路径（可多次传入）")
	ibusFlag := flag.Bool("ibus", false, "由 ibus-daemon 启动时传入（兼容约定，无实际作用）")
	userPath := flag.String("userdict", "", "用户词典路径（空串表示不落盘）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag{"data/dict_seed.tsv"}
	}
	_ = *ibusFlag

	dict, err := engine.LoadDictFiles(dicts)
	if err != nil {
		log.Fatal(err)
	}
	user, err := engine.NewUserDict(*userPath)
	if err != nil {
		log.Fatal(err)
	}

	ibus, err := newIBusEngine(dict, user)
	if err != nil {
		log.Fatal(err)
	}
	if err := ibus.run(); err != nil {
		log.Fatal(err)
	}
}
