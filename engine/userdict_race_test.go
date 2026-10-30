package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 并发安全：多会话共享同一 UserDict 时，bump/查询/导出/清空并发执行
// 不应产生数据竞争（配合 go test -race 运行）。真实场景即 daemon 的
// 多条 IPC 连接同时打字。
func TestUserDictConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess := NewSession(mustDict(t), u)
			for j := 0; j < 50; j++ {
				sess.Input("nihao")
				sess.Select(0)
				sess.Input("shi")
				sess.Punct(",")
				_ = u.Score("你好", []string{"ni", "hao"}, "")
				_ = u.QueryPhrases([]string{"ni", "hao"}, "")
				_ = u.Stats()
				var b strings.Builder
				_ = u.ExportTSV(&b, j%2 == 0)
			}
		}(i)
	}
	wg.Wait()

	// 收尾：清空与统计亦需并发安全
	if err := u.Clear(); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	if st := u.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("Clear 后 Stats = %+v", st)
	}
}

// 并发写盘串行化回归（配合 go test -race 运行）。
//
// 背景（2026-10-30 审计 P1）：FlushIfDirty 的「锁内快照、锁外写盘」与
// Save/Clear 的 saveLocked 此前共用固定 <path>.tmp 且无串行化——两个写者
// 交错写同一 tmp 再 rename 可能落盘半截 JSON，下次启动 lenient 会把它改名
// 备份并以空词典启动（整份学习数据重置）。这里让 FlushIfDirty 与
// Save/Clear/Bump 高频并发，断言：结束时磁盘文件是完整合法 JSON、
// 学习数据可见、且没有残留的 .tmp-* 垃圾文件。
func TestUserDictConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	u.SetDeferred(true)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				switch (i + j) % 5 {
				case 0:
					_ = u.BumpWord("你好", []string{"ni", "hao"})
					_ = u.BumpBigram("你好", "世界")
				case 1:
					_, _ = u.FlushIfDirty()
				case 2:
					_ = u.Save()
				case 3:
					_ = u.Clear()
				case 4:
					_ = u.BumpPhrase("键盘鼠标", []string{"jian", "pan", "shu", "biao"})
				}
			}
		}(i)
	}
	wg.Wait()

	// 收尾同步落盘：写盘完成后文件必须能被严格解析（半截 JSON 会让 daemon
	// 下次启动走损坏恢复路径，学习数据全部丢失）。
	if err := u.Save(); err != nil {
		t.Fatalf("收尾 Save 失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取用户词典失败: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("并发写盘后文件不是合法 JSON: %v\n%s", err, data)
	}
	for _, key := range []string{"words", "phrases", "bigrams"} {
		if _, ok := probe[key]; !ok {
			t.Fatalf("落盘文件缺少 %q 段（新格式三张表必须齐全）", key)
		}
	}

	// tmp 文件必须清理干净：残留说明某个写者失败路径漏了清理。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("读取用户词典目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("落盘失败路径残留临时文件: %s", e.Name())
		}
	}
}

// Clear 语义不被旧快照覆盖（确定性回归）。
//
// FlushIfDirty 在锁外写盘：它可能在 Clear 之前取好快照、在 Clear 落盘之后
// 才拿到写锁。若写盘不做代数守卫，这份旧快照会把清空后的空词典覆盖回
// 清空前的内容（用户点「清空学习数据」却清不掉）。这里用同一份快照精确
// 复现该时序。
func TestUserDictClearNotOverwrittenByStaleSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	u.SetDeferred(true)
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}

	// 模拟 FlushIfDirty 的「锁内快照」：拿到清空前的旧快照后释放锁。
	u.mu.Lock()
	stale := u.snapshotLocked()
	u.mu.Unlock()

	// Clear 先落盘（空词典）。
	if err := u.Clear(); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	// 旧快照随后才写盘：必须被代数守卫丢弃，不能覆盖清空结果。
	if err := u.writeSnapshot(stale); err != nil {
		t.Fatalf("写旧快照失败: %v", err)
	}

	reloaded, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重载用户词典失败（落盘文件应仍为合法 JSON）: %v", err)
	}
	if st := reloaded.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("Clear 被旧快照覆盖，重载 Stats = %+v（期望全 0）", st)
	}
}
