package engine

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Entry 是词典中的一条记录：一个词、其标准拼音切分与基础词频。
type Entry struct {
	Word      string
	Syllables []string
	Freq      int
}

type dictNode struct {
	children map[string]*dictNode
	ids      []int
}

// Dictionary 以音节为边的 Trie：每个节点代表一段音节序列，
// 节点上挂着以该序列开头的词条。查询支持「完整音节序列」与
// 「音节序列 + 末尾残缺音节前缀」两类（对应击键打到一半的场景）。
// 种子规模下用 map Trie；词条量到十万级再换紧凑结构（双数组 Trie）。
type Dictionary struct {
	root    *dictNode
	entries []*Entry
}

func NewDictionary() *Dictionary {
	return &Dictionary{root: &dictNode{}}
}

func (d *Dictionary) add(word string, syllables []string, freq int) {
	id := len(d.entries)
	d.entries = append(d.entries, &Entry{
		Word:      word,
		Syllables: append([]string(nil), syllables...),
		Freq:      freq,
	})
	node := d.root
	for _, s := range syllables {
		if node.children == nil {
			node.children = map[string]*dictNode{}
		}
		next := node.children[s]
		if next == nil {
			next = &dictNode{}
			node.children[s] = next
		}
		node = next
	}
	node.ids = append(node.ids, id)
}

// Query 按「完整音节序列」查询，返回该节点及其子树上的所有词条
// （即以该序列开头的词，长短皆含）。
func (d *Dictionary) Query(syllables []string) []*Entry {
	node := d.root
	for _, s := range syllables {
		if node.children == nil {
			return nil
		}
		node = node.children[s]
		if node == nil {
			return nil
		}
	}
	return d.collect(node)
}

// QueryPrefix 在走完 syllables 之后，对所有以 frag 为前缀的下一音节做子树收集。
func (d *Dictionary) QueryPrefix(syllables []string, frag string) []*Entry {
	node := d.root
	for _, s := range syllables {
		if node.children == nil {
			return nil
		}
		node = node.children[s]
		if node == nil {
			return nil
		}
	}
	var out []*Entry
	for s, child := range node.children {
		if strings.HasPrefix(s, frag) {
			out = append(out, d.collect(child)...)
		}
	}
	return out
}

// collect 深度优先收集节点自身及子树上的词条。遍历顺序不确定，
// 调用方（Session）最终会按确定性规则排序。
func (d *Dictionary) collect(node *dictNode) []*Entry {
	var out []*Entry
	for _, id := range node.ids {
		out = append(out, d.entries[id])
	}
	for _, child := range node.children {
		out = append(out, d.collect(child)...)
	}
	return out
}

// ParseDict 解析 TSV 词库：每行「词 \t 拼音(空格分隔) \t 词频」，
// # 开头的行为注释。重复词条保留较大词频；非法音节直接报错，
// 防止脏数据静默进入词库。
func ParseDict(r io.Reader) (*Dictionary, error) {
	d := NewDictionary()
	seen := make(map[string]int) // word \x00 syllables -> entry 下标
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("词库第 %d 行格式错误：应为「词\\t拼音\\t词频」", lineNo)
		}
		word := strings.TrimSpace(parts[0])
		sylls := strings.Fields(parts[1])
		freq, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil {
			return nil, fmt.Errorf("词库第 %d 行词频不是整数: %w", lineNo, err)
		}
		if word == "" || len(sylls) == 0 {
			return nil, fmt.Errorf("词库第 %d 行词或拼音为空", lineNo)
		}
		for _, s := range sylls {
			if !IsSyllable(s) {
				return nil, fmt.Errorf("词库第 %d 行拼音 %q 不是合法音节", lineNo, s)
			}
		}
		key := word + "\x00" + strings.Join(sylls, " ")
		if prev, ok := seen[key]; ok {
			if freq > d.entries[prev].Freq {
				d.entries[prev].Freq = freq
			}
			continue
		}
		seen[key] = len(d.entries)
		d.add(word, sylls, freq)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取词库失败: %w", err)
	}
	return d, nil
}
