package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 辅助：写临时源码目录并跑 run()，返回 (退出码, 合并输出)。
func runOn(t *testing.T, files map[string]string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 捕获 stdout/stderr：临时改写 os.Stdout 太重，这里直接调内部逻辑不行
	//（逻辑在 run 里耦合了输出），因此用 os.Pipe 捕获。
	oldOut, oldErr := os.Stdout, os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout, os.Stderr = w, w
	code := run([]string{dir})
	w.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return code, sb.String()
}

// 合法调用：5 参数，plbrush 指向 &lb。
func TestValidCallPasses(t *testing.T) {
	code, out := runOn(t, map[string]string{
		"a.cpp": "void f() {\n  auto pen = ExtCreatePen(PS_GEOMETRIC, 2, &lb, 0, nullptr);\n}\n",
	})
	if code != 0 {
		t.Fatalf("合法调用应通过，退出码 %d，输出 %s", code, out)
	}
	if !strings.Contains(out, "1 处 ExtCreatePen 调用") {
		t.Fatalf("应统计到 1 处调用，输出 %s", out)
	}
}

// 注释里的 ExtCreatePen 文字不算调用（2026-10-30 Go 版初版曾误报）。
func TestCommentTextNotACall(t *testing.T) {
	code, out := runOn(t, map[string]string{
		"a.cpp": "// ExtCreatePen 的 plbrush 是必填的\n// （ExtCreatePen PS_GEOMETRIC）才有圆头\nvoid f() {}\n",
	})
	if code != 0 {
		t.Fatalf("注释文字不应算调用，退出码 %d，输出 %s", code, out)
	}
	if strings.Contains(out, "0 处") && strings.Contains(out, "失败") {
		t.Fatalf("不应有失败: %s", out)
	}
}

// plbrush 传 nullptr 必须报错（2026-10-29 崩溃事故的根因）。
func TestNullptrPlbrushFails(t *testing.T) {
	code, out := runOn(t, map[string]string{
		"a.cpp": "auto pen = ExtCreatePen(PS_GEOMETRIC | PS_ENDCAP_ROUND, width, nullptr, 0, nullptr);\n",
	})
	if code != 1 {
		t.Fatalf("nullptr 应报错，退出码 %d，输出 %s", code, out)
	}
	if !strings.Contains(out, "plbrush 传了 nullptr") {
		t.Fatalf("应指明 plbrush=nullptr: %s", out)
	}
}

// 参数个数不对必须报错。
func TestWrongArgCountFails(t *testing.T) {
	code, out := runOn(t, map[string]string{
		"a.cpp": "auto pen = ExtCreatePen(PS_GEOMETRIC, 2, &lb);\n",
	})
	if code != 1 {
		t.Fatalf("3 参数应报错，退出码 %d，输出 %s", code, out)
	}
	if !strings.Contains(out, "实际 3 个") {
		t.Fatalf("应报实际参数个数: %s", out)
	}
}

// 更长标识符（ExtCreatePenFoo）不算调用。
func TestLongerIdentifierSkipped(t *testing.T) {
	code, _ := runOn(t, map[string]string{
		"a.cpp": "auto x = ExtCreatePenWrapper(1, 2, 3, 4, 5);\n",
	})
	if code != 0 {
		t.Fatal("ExtCreatePenWrapper 不应被当成 ExtCreatePen 调用")
	}
}

// TestOversizedFileSkipped：超过大小上限的文件必须报错跳过而不是处理。
//
// 2026-10-30 防失控护栏（用户机器上 checkgdi 进程吃到 15GB 被手动 kill，
// 根因未定位）。护栏保证最坏情况也是「报错退出」而不是「吃光内存」。
func TestOversizedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	// 造一个超过 maxSourceFileSize 的文件（10MiB+1）
	big := strings.Repeat("x", maxSourceFileSize+1)
	p := filepath.Join(dir, "big.cpp")
	if err := os.WriteFile(p, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runOn(t, map[string]string{"big.cpp": big})
	// maxSourceFileSize+1 字节会导致文件系统写入正常，但检查器必须跳过
	_ = p
	if code != 1 {
		t.Fatalf("超限文件应导致整体失败（有报错信息），退出码 %d", code)
	}
	if !strings.Contains(out, "超过上限") {
		t.Fatalf("应提示超过上限: %s", out)
	}
}

// TestGuardOnHugeSingleLine：单行超长内容（模拟畸形输入）也不得失控——
// 主循环与 extractArgs 的迭代上限必须在有限时间内返回。
func TestGuardOnHugeSingleLine(t *testing.T) {
	// 12MiB 的单行，内嵌大量 ExtCreatePen 字样但永远不闭合括号：
	// 若无护栏，extractArgs 会逐字符扫完整个 text（理论可终止），
	// 主循环也会推进——此测试验证的是「快速终止 + 正确报错」。
	huge := "ExtCreatePen(" + strings.Repeat("a", 4<<20) + "\n" +
		strings.Repeat("/* ExtCreatePen 注释 */\n", 800)
	code, out := runOn(t, map[string]string{"a.cpp": huge})
	if code != 1 {
		t.Fatalf("畸形输入应报错退出，退出码 %d", code)
	}
	if !strings.Contains(out, "ExtCreatePen") {
		t.Fatalf("输出应提及 ExtCreatePen 相关错误: %s", out)
	}
}
