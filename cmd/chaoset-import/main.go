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
	"io"
	"os"
	"path/filepath"
	"time"

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
	merged := make(map[string]*acc)

	// 种子词库先并入底表（壳用它保留随包基础词库），再由已有输出文件与导入
	// 文件叠加。顺序固定，避免不同壳各拼一套合并顺序。
	if seed != "" {
		if err := absorbFile(merged, seed, false); err != nil {
			return fmt.Errorf("种子词库: %w", err)
		}
	}
	if _, err := os.Stat(outPath); err == nil {
		if err := absorbFile(merged, outPath, false); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查已有词库 %s 失败: %w", outPath, err)
	}
	for _, p := range inputs {
		if err := absorbFile(merged, p, replace); err != nil {
			return err
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
	// 写目标前按需备份旧词库：备份失败直接中止（宁可不导入，也不能让用户
	// 失去旧词库）。备份与原子写都在本工具内完成，壳不再自己 copy/rename。
	if backup {
		if _, err := os.Stat(outPath); err == nil {
			if err := backupFile(outPath); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查已有词库 %s 失败: %w", outPath, err)
		}
	}
	// 原子写：先写同目录临时文件再 rename，写失败/进程被杀时旧词库保持原样。
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

// absorbFile 解析一个词库文件并并入累计表；skipped>0 时在 stderr 报告。
func absorbFile(merged map[string]*acc, path string, replace bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	d, skipped, perr := engine.ParseDictAuto(bytes.NewReader(data))
	if perr != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, perr)
	}
	absorb(merged, d.Entries(), replace)
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "%s: 跳过 %d 行无效词条\n", path, skipped)
	}
	return nil
}

// backupFile 把 path 复制为同目录的 <path>.bak.<时间戳>，返回备份路径。
// 时间戳精确到秒；同一秒内重复备份时追加 -1/-2 后缀，绝不覆盖已有备份。
func backupFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("备份前检查旧词库失败: %w", err)
	}
	stamp := time.Now().Format("20060102-150405")
	bak := path + ".bak." + stamp
	for i := 1; ; i++ {
		if _, statErr := os.Stat(bak); os.IsNotExist(statErr) {
			break
		}
		bak = fmt.Sprintf("%s.bak.%s-%d", path, stamp, i)
	}
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开旧词库备份失败: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(bak, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("创建旧词库备份失败: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(bak)
		return fmt.Errorf("写入旧词库备份失败: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(bak)
		return fmt.Errorf("关闭旧词库备份失败: %w", err)
	}
	fmt.Fprintf(os.Stderr, "旧词库已备份：%s\n", bak)
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
