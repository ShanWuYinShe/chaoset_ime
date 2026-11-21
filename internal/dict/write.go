package dict

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteTSV 把词条导出为 TSV（词频降序、同频按词、再按音节排序），
// 供 chaoset-import 生成合并后的词库文件。
func WriteTSV(w io.Writer, entries []*Entry) error {
	sorted := append([]*Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Freq != b.Freq {
			return a.Freq > b.Freq
		}
		if a.Word != b.Word {
			return a.Word < b.Word
		}
		// 同词多拼音（多音字）必须再按音节排定，否则比较器不是全序：
		// 「着 zhao」与「着 zhe」同频同词，compare 恒为 false，两条记录的
		// 相对顺序取决于 sort 内部实现与输入顺序——同样的输入从不同路径
		// 进来（CLI 脚本 vs GUI --selftest）会产出不同的 dict.tsv，
		// 等价性测试 B 段因此失败（2026-10-29 多音字全读音引入后暴露）。
		// dictgen 侧的排序早已含这一级（见 cmd/chaoset-dictgen main.go），
		// 这里是同一规则的补齐。
		return strings.Join(a.Syllables, " ") < strings.Join(b.Syllables, " ")
	})
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintln(bw, "# chaoset 词库（由 chaoset-import 生成，词\\t拼音\\t词频）"); err != nil {
		return err
	}
	for _, e := range sorted {
		if _, err := fmt.Fprintf(bw, "%s\t%s\t%d\n", e.Word, strings.Join(e.Syllables, " "), e.Freq); err != nil {
			return err
		}
	}
	return bw.Flush()
}
