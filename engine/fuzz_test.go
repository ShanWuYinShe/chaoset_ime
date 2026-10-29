package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// fuzzDict 加载内置种子词库：比内联小词库覆盖更多路径（词组、单字竞争），
// 加载一次约 1ms。种子词库缺失时退回内联测试词库——fuzz 不该因为数据文件
// 不在（例如从 tarball 里只取了源码跑测试）而失败。
func fuzzDict(tb testing.TB) *Dictionary {
	tb.Helper()
	if d, err := LoadDictFiles([]string{filepath.Join("..", "data", "dict_seed.tsv")}); err == nil {
		return d
	}
	d, err := ParseDict(strings.NewReader(testDict))
	if err != nil {
		tb.Fatalf("解析内联测试词库失败: %v", err)
	}
	return d
}

// FuzzSessionInputPath 对「输入串 → 候选」整条路径做模糊测试：
// 任意字节序列（含非法 UTF-8、超长串、混合符号）都不得 panic 或死循环。
//
// 背景（2026-10-30 深度审计）：输入路径是全项目最热的代码，任何越界都会
// 让输入法进程崩溃（壳进程内嵌引擎时等于带走宿主程序）。审计期一次性探针
// 跑过 9.4 万次执行无 panic，转为常驻回归测试：
//   - 日常 go test ./... 只跑种子语料（毫秒级）；
//   - 需要更深挖掘时手动跑 -fuzz FuzzSessionInputPath -fuzztime 60s。
func FuzzSessionInputPath(f *testing.F) {
	dict := fuzzDict(f)
	for _, seed := range []string{
		"", "n", "ni", "nihao", "xian", "zhongguo", "a", "1", " ", "'",
		"ni'hao", "ni hao", "NIHAO", "nihao123", "。", "，", "z", "zh", "ng",
		"vvvv", "nininini", "hao", "de", "le", strings.Repeat("a", 200),
		"xi'an", "xi an", "lü", "lv", "nve", "nue", "  ni  hao  ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		s := NewSession(dict, nil)
		if _, err := s.Input(text); err != nil {
			return // 输入被拒（如含非法字符）是合法行为，不继续
		}
		_ = s.Compose()
		for i := 0; i < 3; i++ {
			_, _ = s.NextPage()
			_, _ = s.PrevPage()
		}
		_, _ = s.Backspace()
		_, _ = s.BackspaceWord()
		_, _ = s.CommitRaw()
		_, _ = s.Select(0)
		_, _ = s.Select(-1)
		_, _ = s.Select(999)
		_, _, _ = s.SelectPrefix(0)
		_ = s.Associate([]string{"ni", "hao"})
		_, _ = s.Punct(",")
		_, _ = s.Reset()
	})
}

// FuzzSessionModes 覆盖双拼 / 模糊音 / 繁体 / 半角开关组合下的同一路径：
// 每个开关都会改变切分或映射逻辑，是最容易出现越界的分支。
func FuzzSessionModes(f *testing.F) {
	dict := fuzzDict(f)
	f.Add("nihao", uint8(1), true, true)
	f.Add("xian", uint8(2), true, false)
	f.Add("zhong", uint8(3), false, true)
	f.Add("a", uint8(0), false, false)
	f.Fuzz(func(t *testing.T, text string, mode uint8, fuzzy, trad bool) {
		s := NewSession(dict, nil)
		s.SetFuzzy(fuzzy)
		s.SetTraditional(trad)
		switch mode % 4 {
		case 1:
			s.SetDoublePinyin("zrm")
		case 2:
			s.SetDoublePinyin("mspy")
		case 3:
			s.SetDoublePinyin("flypy")
		}
		if _, err := s.Input(text); err != nil {
			return
		}
		_ = s.Compose()
		_, _ = s.Backspace()
		_, _ = s.Select(0)
		_ = s.ToggleHalfWidth()
		_, _ = s.Punct(";")
	})
}
