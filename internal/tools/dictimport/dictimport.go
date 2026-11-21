package dictimport

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"chaoset.ime/internal/buildinfo"
	"chaoset.ime/internal/dict"
)

type acc struct {
	word  string
	sylls []string
	freq  int
}

func Main(args []string) int {
	return RunCLI(args, os.Stdout, os.Stderr)
}

func RunCLI(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "输出 TSV 路径（必填）")
	seed := fs.String("seed", "", "种子词库路径（先并入底表；壳用它保留随包基础词库）")
	backup := fs.Bool("backup", false, "写目标前把旧词库备份为 <target>.bak.<时间戳>；备份失败则中止")
	replace := fs.Bool("replace", false, "与已有词库重复的词条：true=以导入词频覆盖，false=取较大词频")
	showVersion := fs.Bool("version", false, "打印构建信息（提交号/构建时间）后退出")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.Info())
		return 0
	}
	if *out == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	if err := Run(*out, *seed, *backup, *replace, fs.Args()); err != nil {
		fmt.Fprintln(stderr, "chaoset-import:", err)
		return 1
	}
	return 0
}

func Run(outPath, seed string, backup bool, replace bool, inputs []string) error {
	merged := make(map[string]*acc)

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

	entries := make([]*dict.Entry, 0, len(merged))
	for _, a := range merged {
		entries = append(entries, &dict.Entry{Word: a.word, Syllables: a.sylls, Freq: a.freq})
	}
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if backup {
		if _, err := os.Stat(outPath); err == nil {
			if err := BackupFile(outPath); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查已有词库 %s 失败: %w", outPath, err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".chaoset-import-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	removeTmp := func() { _ = os.Remove(tmpName) }
	if err := dict.WriteTSV(tmp, entries); err != nil {
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

func absorbFile(merged map[string]*acc, path string, replace bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	entries, skipped, perr := dict.ParseAuto(bytes.NewReader(data))
	if perr != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, perr)
	}
	absorb(merged, entries, replace)
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "%s: 跳过 %d 行无效词条\n", path, skipped)
	}
	return nil
}

func BackupFile(path string) error {
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

func absorb(merged map[string]*acc, entries []*dict.Entry, replace bool) {
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
