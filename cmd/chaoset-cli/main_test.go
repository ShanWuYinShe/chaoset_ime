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
	if _, _, err := buildController("", true, nil, "", false, false, ""); err == nil {
		t.Fatal("Unix 上 -pipe 应报错")
	}
}
