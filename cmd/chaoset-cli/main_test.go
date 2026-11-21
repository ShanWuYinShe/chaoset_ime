package main

import (
	"runtime"
	"testing"
)

// TestBuildControllerPipeUnsupportedOnUnix：-pipe 仅 Windows 支持；Unix 上必须
// 明确报错，而不是静默回退到本地引擎。
func TestBuildControllerPipeUnsupportedOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 -pipe 走真实命名管道，需 daemon；本用例只测 Unix 报错")
	}
	if _, _, err := buildController("", true); err == nil {
		t.Fatal("Unix 上 -pipe 应报错")
	}
}

// TestBuildControllerRequiresTransport：本地内嵌引擎已删除，既没 -socket 也没
// -pipe 时必须给出明确错误（防止用户以为测的是 daemon，实际什么都没连）。
func TestBuildControllerRequiresTransport(t *testing.T) {
	if _, _, err := buildController("", false); err == nil {
		t.Fatal("未指定传输时应报错并提示先起 daemon")
	}
}
