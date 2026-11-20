package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUsageAndHelp(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"help"},
		{"-h"},
		{"--help"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr, "chaosetctl")
		if code != 0 {
			t.Errorf("Run(%v) 退出码 = %d，期望 0", args, code)
		}
		if !strings.Contains(stdout.String(), "Chaoset 拼音输入法全能命令行控制工具") {
			t.Errorf("Run(%v) stdout 未输出用法: %q", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("Run(%v) stderr 非空: %q", args, stderr.String())
		}
	}
}

func TestRunVersion(t *testing.T) {
	for _, flag := range []string{"version", "-version", "--version", "-v"} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{flag}, &stdout, &stderr, "chaosetctl")
		if code != 0 {
			t.Errorf("Run(%q) 退出码 = %d，期望 0", flag, code)
		}
		if !strings.Contains(stdout.String(), "chaoset") {
			t.Errorf("Run(%q) stdout 未包含版本信息: %q", flag, stdout.String())
		}
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"unknown_cmd"}, &stdout, &stderr, "chaosetctl")
	if code != 2 {
		t.Errorf("未知子命令退出码 = %d，期望 2", code)
	}
	if !strings.Contains(stderr.String(), "未知子命令") {
		t.Errorf("stderr 缺少未知子命令提示: %q", stderr.String())
	}
}

func TestRunSubcommands(t *testing.T) {
	// 1. settingsctl / config
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{"config", "help"}, &stdout, &stderr, "chaosetctl")
		if code != 0 {
			t.Errorf("config help 退出码 = %d，期望 0", code)
		}
		if !strings.Contains(stdout.String(), "chaoset-settingsctl get") {
			t.Errorf("config help 输出不符: %q", stdout.String())
		}
	}
	// 2. import usage
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{"import"}, &stdout, &stderr, "chaosetctl")
		if code != 2 {
			t.Errorf("import 缺少参数应退出 2，实际 %d", code)
		}
	}
	// 3. userdict usage
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{"userdict", "delete"}, &stdout, &stderr, "chaosetctl")
		if code != 2 {
			t.Errorf("userdict 缺少 delete 参数应退出 2，实际 %d", code)
		}
	}
}

func TestMulticall(t *testing.T) {
	// chaoset-settingsctl multicall
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{"help"}, &stdout, &stderr, "chaoset-settingsctl")
		if code != 0 {
			t.Errorf("multicall settingsctl 退出码 = %d", code)
		}
		if !strings.Contains(stdout.String(), "chaoset-settingsctl get") {
			t.Errorf("multicall settingsctl stdout 不符: %q", stdout.String())
		}
	}
	// chaoset-import.exe multicall
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{}, &stdout, &stderr, "chaoset-import.exe")
		if code != 2 {
			t.Errorf("multicall import.exe 缺参数应退出 2，实际 %d", code)
		}
	}
	// chaoset-userdict multicall
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{"-version"}, &stdout, &stderr, "chaoset-userdict")
		if code != 0 {
			t.Errorf("multicall userdict -version 应退出 0，实际 %d", code)
		}
		if !strings.Contains(stdout.String(), "chaoset") {
			t.Errorf("multicall userdict -version 输出不符: %q", stdout.String())
		}
	}
	// chaoset-settings multicall with CLI subargs
	{
		var stdout, stderr bytes.Buffer
		code := Run([]string{"help"}, &stdout, &stderr, "chaoset-settings")
		if code != 0 {
			t.Errorf("multicall chaoset-settings help 应退出 0，实际 %d", code)
		}
		if !strings.Contains(stdout.String(), "chaoset-settingsctl get") {
			t.Errorf("multicall chaoset-settings help stdout 不符: %q", stdout.String())
		}
	}
}
