// chaoset-import 把热词文件（TSV 或 Rime dict.yaml）归一化，
// 与已有词库合并输出一个可直接给 chaosetd -dict 加载的 TSV。
//
// 用法：
//
//	chaoset-import -o merged.tsv 已有.tsv hot.yaml ...
//	chaoset-import -o merged.tsv -replace 已有.tsv hot.yaml
//
// 重复词条默认取较大词频；-replace 时以导入词频覆盖。
package main

import (
	"flag"
	"fmt"
	"os"

	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/internal/tools/dictimport"
)

func main() {
	out := flag.String("o", "", "输出 TSV 路径（必填）")
	seed := flag.String("seed", "", "种子词库路径（先并入底表；壳用它保留随包基础词库）")
	backup := flag.Bool("backup", false, "写目标前把旧词库备份为 <target>.bak.<时间戳>；备份失败则中止")
	replace := flag.Bool("replace", false, "与已有词库重复的词条：true=以导入词频覆盖，false=取较大词频")
	showVersion := flag.Bool("version", false, "打印构建信息（提交号/构建时间）后退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Info())
		return
	}
	if *out == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*out, *seed, *backup, *replace, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "chaoset-import:", err)
		os.Exit(1)
	}
}

func run(outPath, seed string, backup bool, replace bool, inputs []string) error {
	return dictimport.Run(outPath, seed, backup, replace, inputs)
}

// 原子替换契约：backupFile(path) 与 os.Rename(tmp, target) 保证写失败时旧词库不损坏。
func backupFile(path string) error {
	return dictimport.BackupFile(path)
}
