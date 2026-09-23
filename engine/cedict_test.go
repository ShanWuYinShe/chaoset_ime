package engine

import (
	"strings"
	"testing"
)

const cedictSample = `# CC-CEDICT 示例（许可证头注释会被跳过）
# License: CC-BY-SA 4.0
中國	中国	[Zhong1 guo2] /China/Middle Kingdom/
時間	时间	[shi2 jian1] /time/
女	女	[nv3] /female/
略	略	[lve4] /strategy/
 Liverpool	Liverpool	[Li1 wu2 pu3] /Liverpool/
OK	OK	[O K] /OK/
`

// CC-CEDICT 解析：简体列、声调数字剥离、u: 记法归一、非汉字词条跳过。
func TestParseCCCEDICT(t *testing.T) {
	dict, skipped, err := ParseCCCEDICT(strings.NewReader(cedictSample))
	if err != nil {
		t.Fatalf("ParseCCCEDICT 失败: %v", err)
	}
	if dict.Len() != 4 {
		t.Fatalf("词条数 = %d, 期望 4", dict.Len())
	}
	if skipped != 2 {
		t.Fatalf("跳过数 = %d, 期望 2（OK 行与 Liverpool 行）", skipped)
	}

	entries := map[string]*Entry{}
	for _, e := range dict.Entries() {
		entries[e.Word] = e
	}
	zg := entries["中国"]
	if zg == nil || strings.Join(zg.Syllables, " ") != "zhong guo" || zg.Freq != cedictDefaultFreq {
		t.Fatalf("中国 词条异常: %+v", zg)
	}
	if e := entries["时间"]; e == nil || strings.Join(e.Syllables, " ") != "shi jian" {
		t.Fatalf("时间 词条异常: %+v", e)
	}
	if e := entries["女"]; e == nil || strings.Join(e.Syllables, " ") != "nv" {
		t.Fatalf("女 u: 记法应归一为 nv: %+v", e)
	}
	if e := entries["略"]; e == nil || strings.Join(e.Syllables, " ") != "lve" {
		t.Fatalf("略 应归一为 lve: %+v", e)
	}
}

// ParseDictAuto 应把 CC-CEDICT 内容路由到 cedict 解析器。
func TestParseDictAutoDetectsCedict(t *testing.T) {
	dict, skipped, err := ParseDictAuto(strings.NewReader(cedictSample))
	if err != nil {
		t.Fatalf("ParseDictAuto 失败: %v", err)
	}
	if dict.Len() != 4 || skipped != 2 {
		t.Fatalf("dict=%d skipped=%d, 期望 4/2", dict.Len(), skipped)
	}
}

// TSV 内容不应被误判为 CC-CEDICT（回归保护）。
func TestParseDictAutoTSVUnaffected(t *testing.T) {
	dict, skipped, err := ParseDictAuto(strings.NewReader("你好\tni hao\t99\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if dict.Len() != 1 || skipped != 0 {
		t.Fatalf("TSV 解析异常: dict=%d skipped=%d", dict.Len(), skipped)
	}
}
