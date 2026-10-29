// checkgdi 校验 Windows 壳的 Win32 GDI 调用契约（2026-10-29 崩溃事故）。
//
// 背景：v0.2.14 候选窗齿轮为了圆头线帽调用
//
//	ExtCreatePen(PS_GEOMETRIC | PS_ENDCAP_ROUND, width, nullptr, 0, nullptr)
//
// ExtCreatePen 的第三个参数 plbrush 是必填的 LOGBRUSH 指针，gdi32 在用户态
// 直接读 lbStyle/lbColor（ReactOS win32ss/gdi/gdi32/objects/brush.c 同构），
// 传 nullptr 就是空指针解引用。TSF DLL 跑在宿主进程里，用户每敲一个字母，
// 候选窗首帧绘制就会把宿主程序带走（现象：只出现一个空候选框，随后进程退出）。
//
// 本工具检查 shims/windows/src 下所有 ExtCreatePen 调用：
//  1. 必须正好 5 个参数；
//  2. 第三个参数（plbrush）不得是 nullptr / NULL / 0。
//
// 用法：go run ./tools/checkgdi shims/windows/src
// 退出码：0 = 通过，1 = 发现违规，2 = 用法错误。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const callName = "ExtCreatePen"

// 防失控护栏（2026-10-30 实测事故：用户机器上 checkgdi 进程吃到 15GB 内存
// 被手动 kill；本地多场景复现均只有 4-100MB，根因未定位）。工具被 make vet
// 与 CI 调用，绝不允许任何输入下失控——以下三道硬上限保证最坏情况也是
// 「报错退出」而不是「吃光内存」：
//  1. maxSourceFileSize：单文件超过它直接报错跳过（C++ 源文件不可能更大，
//     超了说明传错目录或文件损坏）；
//  2. 主循环迭代上限 = len(text)/len(callName)+2（每个匹配至少推进
//     len(callName) 字节，这是数学上的理论上限）；
//  3. extractArgs 迭代上限 = len(text)（逐字符扫描的理论上限）。
const maxSourceFileSize = 10 << 20 // 10 MiB

// extractArgs 从 openParen 指向的 ( 开始，返回顶层逗号分隔的参数列表；
// 括号不闭合返回 nil。
func extractArgs(text string, openParen int) []string {
	var args []string
	depth := 0
	start := openParen + 1
	i := openParen
	inStr := byte(0)
	dq := byte(34)
	sq := byte(39)
	// 护栏 3：迭代上限（逐字符扫描的理论上限），超出返回 nil（调用方按
	// 「括号不闭合」处理报错）。防御性保护：任何情况下不允许失控。
	maxIters := len(text) - openParen + 2
	iters := 0
	for i < len(text) {
		iters++
		if iters > maxIters {
			return nil
		}
		ch := text[i]
		if inStr != 0 {
			if ch == 92 { // 反斜杠转义：跳过下一个字符
				i += 2
				continue
			}
			if ch == inStr {
				inStr = 0
			}
		} else if ch == dq || ch == sq {
			inStr = ch
		} else if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
			if depth == 0 {
				args = append(args, text[start:i])
				return args
			}
		} else if ch == ',' && depth == 1 {
			args = append(args, text[start:i])
			start = i + 1
		}
		i++
	}
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) != 1 {
		fmt.Fprintln(os.Stderr, "用法: checkgdi <源码目录>")
		return 2
	}
	srcDir := argv[0]
	cpp, _ := filepath.Glob(filepath.Join(srcDir, "*.cpp"))
	hpp, _ := filepath.Glob(filepath.Join(srcDir, "*.hpp"))
	files := append(cpp, hpp...)
	sort.Strings(files)
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "未找到任何 C++ 源文件: %s\n", srcDir)
		return 2
	}

	var problems []string
	checked := 0
	for _, path := range files {
		// 护栏 1：单文件大小上限（见 maxSourceFileSize 注释）
		if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > maxSourceFileSize {
			problems = append(problems, fmt.Sprintf("%s: 文件 %d 字节超过上限 %d，跳过（疑似传错目录或文件损坏）",
				path, fi.Size(), maxSourceFileSize))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, path+": 读取失败: "+err.Error())
			continue
		}
		text := string(data)
		searchFrom := 0
		// 护栏 2：主循环迭代上限（每个匹配至少推进 len(callName) 字节，
		// 该值是数学理论上限；超出说明出现未知 bug，立即失败而不是失控）
		maxIters := len(text)/len(callName) + 2
		iters := 0
		for {
			iters++
			if iters > maxIters {
				problems = append(problems, path+": 扫描迭代次数超过理论上限，中止（防失控护栏触发，请报告此错误）")
				break
			}
			rel := strings.Index(text[searchFrom:], callName)
			if rel < 0 {
				break
			}
			idx := searchFrom + rel
			searchFrom = idx + len(callName)

			// 词边界：前一个字符不得是标识符字符（排除 ExtCreatePenFoo 之类）
			if idx > 0 {
				prev := text[idx-1]
				if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || prev == '_' {
					continue
				}
			}

			// 名字之后必须（可跳过空白）紧跟左括号——注释里的文字不算调用
			//（与 Python 版正则一致；Python 版靠正则天然具备此语义）
			p := idx + len(callName)
			for p < len(text) && (text[p] == ' ' || text[p] == 9 || text[p] == 10 || text[p] == 13) {
				p++
			}
			if p >= len(text) || text[p] != '(' {
				continue
			}

			openParen := p
			line := strings.Count(text[:idx], "\n") + 1
			where := fmt.Sprintf("%s:%d", path, line)
			checked++
			args := extractArgs(text, openParen)
			if args == nil {
				problems = append(problems, where+": ExtCreatePen 参数括号不闭合，无法校验")
				continue
			}
			if len(args) != 5 {
				problems = append(problems, fmt.Sprintf("%s: ExtCreatePen 需要 5 个参数，实际 %d 个", where, len(args)))
				continue
			}
			plbrush := strings.TrimSpace(args[2])
			if plbrush == "nullptr" || plbrush == "NULL" || plbrush == "0" {
				problems = append(problems, where+": ExtCreatePen 的 plbrush 传了 "+plbrush+
					"；必须指向有效 LOGBRUSH（gdi32 会直接解引用）")
			}
		}
	}

	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "Windows GDI 契约检查失败：")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		return 1
	}
	fmt.Printf("Windows GDI 契约检查通过（%d 处 ExtCreatePen 调用）\n", checked)
	return 0
}
