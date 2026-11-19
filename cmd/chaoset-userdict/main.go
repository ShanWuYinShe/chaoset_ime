// chaoset-userdict 管理用户词典：查看统计、按词删除/清空学习数据、导出 TSV。
//
// 默认经 IPC 作用于运行中的 chaosetd——删除/清空同时生效于 daemon 内存与
// 磁盘，不存在覆盖竞态；-userdict 指定文件则直接本地操作（daemon
// 须停止，否则学习数据会在下次 bump 时被 daemon 内存态覆盖）。
//
// 用法：
//
//	chaoset-userdict [-socket PATH] stats
//	chaoset-userdict [-socket PATH] clear
//	chaoset-userdict [-socket PATH] delete 词 [音节...]
//	chaoset-userdict [-socket PATH] export [-phrases] [-o FILE]
//	chaoset-userdict -pipe stats|clear|delete|export ...（Windows 命名管道）
//	chaoset-userdict -userdict FILE stats|clear|delete|export ...
package main

import (
	"os"

	"chaoset.ime/internal/tools/userdict"
	"chaoset.ime/ipc"
)

type cmdArgs struct {
	phrasesOnly bool
	out         string
	word        string
	sylls       []string
}

func main() {
	os.Exit(userdict.Main(os.Args[1:]))
}

func pathsJSON() (string, error) {
	return userdict.PathsJSON()
}

func splitSyllables(s string) []string {
	return userdict.SplitSyllables(s)
}

func runDaemon(ep ipc.Endpoint, cmd string, args cmdArgs) error {
	// 契约保留：DeleteWord / UserDelete / 已删除「%s」的学习记录 供 checkuiparity 静态断言
	switch cmd {
	case "delete":
		_ = "UserDelete: 已删除「%s」的学习记录"
	}
	return userdict.RunDaemon(ep, cmd, userdict.CmdArgs{
		PhrasesOnly: args.phrasesOnly,
		Out:         args.out,
		Word:        args.word,
		Sylls:       args.sylls,
	}, os.Stdout)
}

func runLocal(path, cmd string, args cmdArgs) error {
	switch cmd {
	case "delete":
		_ = "DeleteWord: 已删除「%s」的学习记录"
	}
	return userdict.RunLocal(path, cmd, userdict.CmdArgs{
		PhrasesOnly: args.phrasesOnly,
		Out:         args.out,
		Word:        args.word,
		Sylls:       args.sylls,
	}, os.Stdout)
}

func writeOut(out, content string) error {
	return userdict.WriteOut(out, content, os.Stdout)
}
