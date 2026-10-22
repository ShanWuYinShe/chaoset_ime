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
	fuzzy := flag.Bool("fuzzy", false, "启用模糊音（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）")
	trad := flag.Bool("traditional", false, "候选输出繁体（零错误单字映射，见 engine/s2t.go）")
	sp := flag.String("shuangpin", "", "双拼方案：zrm（自然码）/ mspy（微软）；空关闭（键位表见 engine/double_pinyin.go）")
	flag.Parse()
	if len(dicts) == 0 {
		dicts = repeatFlag{"data/dict_common.tsv"}
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

	factory, err := newIBusFactory(dict, user, *fuzzy, *trad, *sp)
	if err != nil {
		log.Fatal(err)
	}
	if err := factory.run(); err != nil {
		log.Fatal(err)
	}
}
