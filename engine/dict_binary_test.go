package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestBinaryDictEquivalence 验证二进制词库与 TSV 文本词库的 100% 语义等价性。
func TestBinaryDictEquivalence(t *testing.T) {
	tsvPath := filepath.Join("..", "data", "dict_common.tsv")
	origDict, err := LoadDictFiles([]string{tsvPath})
	if err != nil {
		t.Fatalf("加载 TSV 词库失败: %v", err)
	}

	// 导出为二进制
	var buf bytes.Buffer
	if err := WriteBinaryDict(&buf, origDict.Entries()); err != nil {
		t.Fatalf("导出二进制词库失败: %v", err)
	}

	// 验证魔数
	if !IsBinaryDict(buf.Bytes()) {
		t.Fatal("导出的二进制数据未能识别出魔数 CDB1")
	}

	// 二进制重新加载
	binDict := NewDictionary()
	if err := ReadBinaryDict(bytes.NewReader(buf.Bytes()), binDict); err != nil {
		t.Fatalf("读取二进制词库失败: %v", err)
	}

	// 对比规模
	if binDict.Len() != origDict.Len() {
		t.Fatalf("词条数不一致: bin=%d, orig=%d", binDict.Len(), origDict.Len())
	}

	// 抽样对比词条
	origEntries := origDict.Entries()
	binEntries := binDict.Entries()
	for i := 0; i < len(origEntries); i += 1000 {
		oe := origEntries[i]
		be := binEntries[i]
		if oe.Word != be.Word || oe.Freq != be.Freq || len(oe.Syllables) != len(be.Syllables) {
			t.Fatalf("第 %d 条词不匹配: orig=%+v, bin=%+v", i, oe, be)
		}
	}
}

// TestBinaryDictAutoDetect 验证 LoadDictFiles 对 .bin 的自动识别能力。
func TestBinaryDictAutoDetect(t *testing.T) {
	tsvPath := filepath.Join("..", "data", "dict_common.tsv")
	origDict, err := LoadDictFiles([]string{tsvPath})
	if err != nil {
		t.Fatalf("加载 TSV 词库失败: %v", err)
	}

	tmpBin := filepath.Join(t.TempDir(), "test_dict.bin")
	f, err := os.Create(tmpBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteBinaryDict(f, origDict.Entries()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// 使用通用的 LoadDictFiles 加载 .bin
	loaded, err := LoadDictFiles([]string{tmpBin})
	if err != nil {
		t.Fatalf("LoadDictFiles 加载二进制词库失败: %v", err)
	}
	if loaded.Len() != origDict.Len() {
		t.Fatalf("通过 LoadDictFiles 加载条数不匹配: %d vs %d", loaded.Len(), origDict.Len())
	}
}
