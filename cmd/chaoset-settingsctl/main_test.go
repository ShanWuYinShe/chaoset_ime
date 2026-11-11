package main

import (
	"bytes"
	"strings"
	"testing"
)

// 参数非法的退出码契约：2，且 stdout 不得有输出（stdout 只放 get 的 JSON）。
// 这些用例都在访问存储之前失败，因此在任何平台/CI 上都不会读写真实配置。
func TestRunUsageErrors(t *testing.T) {
	cases := [][]string{
		{},                                  // 无子命令
		{"bogus"},                           // 未知子命令
		{"help", "extra"},                   // help 不接受参数
		{"get", "extra"},                    // get 不接受参数
		{"set"},                             // set 缺设置项
		{"set", "bogus", "on"},              // 未知设置项
		{"set", "associate"},                // 缺取值
		{"set", "associate", "maybe"},       // 非法开关
		{"set", "associate", "on", "off"},   // 多余参数
		{"set", "mode-scope"},               // 缺取值
		{"set", "mode-scope", "auto"},       // 非法枚举
		{"set", "mode-scope", "bogus"},      // 非法枚举
		{"set", "mode-scope", "system", ""}, // 多余参数
		{"set", "ui-english", "maybe"},      // 非法开关
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("run(%q) 退出码 = %d，期望 2", args, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("run(%q) stdout 非空（应只放 get 的 JSON）: %q", args, stdout.String())
		}
		if stderr.Len() == 0 {
			t.Errorf("run(%q) stderr 为空（错误必须写 stderr）", args)
		}
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Errorf("run(%q) 退出码 = %d，期望 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "chaoset-settingsctl get") {
			t.Errorf("run(%q) 用法未输出到 stdout: %q", arg, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("run(%q) stderr 非空: %q", arg, stderr.String())
		}
	}
}
