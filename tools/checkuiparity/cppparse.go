// cppparse.go：C/C++ 源码解析原语（函数体抽取、注释剥离、空白归一化）。
// 目的是让「按函数体断言」不被注释/字符串字面量里的解释性文字误伤。

package main

import (
	"strings"
)

// cppFunctionBody 从 C++ 源码中抽取函数/方法体（signature 之后第一个 '{' 到
// 匹配的 '}'），返回函数体与 '{' 所在行号。
//
// 只统计代码中的花括号：跳过 // 与 /* */ 注释、字符/字符串字面量（注释里解释性
// 提到某个 API 不应算进函数体）。若 signature 之后先遇到 ';'（纯虚声明/前向
// 声明），跳过该次匹配继续找下一处——避免把 struct 定义体当成函数体。
func cppFunctionBody(src, signature string) (body string, line int, ok bool) {
	searchFrom := 0
	for {
		rel := strings.Index(src[searchFrom:], signature)
		if rel < 0 {
			return "", 0, false
		}
		start := searchFrom + rel
		brace := -1
		i := start + len(signature)
		for i < len(src) {
			c := src[i]
			if c == '/' && i+1 < len(src) && src[i+1] == '/' {
				j := strings.IndexByte(src[i:], '\n')
				if j < 0 {
					// 注释一直延伸到文件末尾：没有可匹配的右花括号，交给外层报 ok=false。
					break
				}
				i += j
				continue
			}
			if c == '/' && i+1 < len(src) && src[i+1] == '*' {
				j := strings.Index(src[i+2:], "*/")
				if j < 0 {
					// 块注释未闭合：同样无需再前进，交给外层报 ok=false。
					break
				}
				i += j + 4
				continue
			}
			if c == '"' || c == '\'' {
				quote := c
				i++
				for i < len(src) && src[i] != quote {
					if src[i] == '\\' {
						i++
					}
					i++
				}
				i++
				continue
			}
			if c == ';' {
				break
			}
			if c == '{' {
				brace = i
				break
			}
			i++
		}
		if brace < 0 {
			searchFrom = start + len(signature)
			continue
		}
		depth := 0
		for j := brace; j < len(src); j++ {
			c := src[j]
			if c == '/' && j+1 < len(src) && src[j+1] == '/' {
				k := strings.IndexByte(src[j:], '\n')
				if k < 0 {
					return "", 0, false
				}
				j += k
				continue
			}
			if c == '/' && j+1 < len(src) && src[j+1] == '*' {
				k := strings.Index(src[j+2:], "*/")
				if k < 0 {
					return "", 0, false
				}
				j += k + 4
				continue
			}
			if c == '"' || c == '\'' {
				quote := c
				j++
				for j < len(src) && src[j] != quote {
					if src[j] == '\\' {
						j++
					}
					j++
				}
				continue
			}
			if c == '{' {
				depth++
			}
			if c == '}' {
				depth--
				if depth == 0 {
					return src[brace+1 : j], strings.Count(src[:brace], "\n") + 1, true
				}
			}
		}
		return "", 0, false
	}
}

// stripCppComments 删除 // 与 /* */ 注释（保留字符串字面量内容），用于禁用语
// 扫描时避免把注释里的「不阻塞 / 旧实现用 CreateProcessW」解释误判为代码。
func stripCppComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		c := src[i]
		if c == '/' && i+1 < len(src) && src[i+1] == '/' {
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				break
			}
			i += j
			continue
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '*' {
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				break
			}
			i += j + 4
			continue
		}
		if c == '"' || c == '\'' {
			quote := c
			b.WriteByte(c)
			i++
			for i < len(src) && src[i] != quote {
				b.WriteByte(src[i])
				if src[i] == '\\' && i+1 < len(src) {
					i++
					b.WriteByte(src[i])
				}
				i++
			}
			if i < len(src) {
				b.WriteByte(src[i])
				i++
			}
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// noSpace 去掉全部空白，便于比对源码里被换行/缩进拆开的表达式。
func noSpace(text string) string { return strings.Join(strings.Fields(text), "") }
