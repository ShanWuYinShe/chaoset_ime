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
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"chaoset.ime/engine"
	"chaoset.ime/internal/buildinfo"
)

type acc struct {
	word  string
	sylls []string
	freq  int
}

func main() {
	out := flag.String("o", "", "输出 TSV 路径（必填）")
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
	if err := run(*out, *replace, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "chaoset-import:", err)
		os.Exit(1)
	}
}

func run(outPath string, replace bool, inputs []string) error {
	merged := make(map[string]*acc)

	// 已有输出文件作为底表先吸收，导入内容叠加其上
	if data, err := os.ReadFile(outPath); err == nil {
		d, skipped, perr := engine.ParseDictAuto(bytes.NewReader(data))
		if perr != nil {
			return fmt.Errorf("解析已有词库 %s 失败: %w", outPath, perr)
		}
		absorb(merged, d.Entries(), false)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "%s: 跳过 %d 行无效词条\n", outPath, skipped)
		}
	}

	for _, p := range inputs {
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", p, err)
		}
		d, skipped, perr := engine.ParseDictAuto(bytes.NewReader(data))
		if perr != nil {
			return fmt.Errorf("解析 %s 失败: %w", p, perr)
		}
		absorb(merged, d.Entries(), replace)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "%s: 跳过 %d 行无效词条\n", p, skipped)
		}
	}

	entries := make([]*engine.Entry, 0, len(merged))
	for _, a := range merged {
		entries = append(entries, &engine.Entry{Word: a.word, Syllables: a.sylls, Freq: a.freq})
	}
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	// 原子写：先写同目录临时文件再 rename，写失败/进程被杀时旧词库保持原样。
	// 壳侧导入流程也依赖该语义（它们传 -o 到临时路径后再自行原子替换；
	// 直接调用 CLI 的用户同样不能被半截文件破坏词库）。
	tmp, err := os.CreateTemp(dir, ".chaoset-import-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	removeTmp := func() { _ = os.Remove(tmpName) }
	if err := engine.WriteTSV(tmp, entries); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("写出词库失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeTmp()
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		removeTmp()
		return fmt.Errorf("设置词库权限失败: %w", err)
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		removeTmp()
		return fmt.Errorf("替换词库失败（旧词库保持不变）: %w", err)
	}
	fmt.Printf("已写入 %s（共 %d 条）\n", outPath, len(entries))
	return nil
}

// absorb 把词条并入累计表。replace=false 时重复词条取较大词频。
func absorb(merged map[string]*acc, entries []*engine.Entry, replace bool) {
	for _, e := range entries {
		key := e.Word + "\x00" + joinSylls(e.Syllables)
		if old, ok := merged[key]; ok {
			if replace || e.Freq > old.freq {
				old.freq = e.Freq
			}
			continue
		}
		merged[key] = &acc{word: e.Word, sylls: e.Syllables, freq: e.Freq}
	}
}

func joinSylls(sylls []string) string {
	out := ""
	for i, s := range sylls {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
