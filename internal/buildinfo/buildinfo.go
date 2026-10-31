// Package buildinfo 提供运行中二进制的构建信息，供各命令行工具的 -version 使用。
//
// 数据来自 runtime/debug.ReadBuildInfo 的 VCS 设置，**不需要打包脚本注入
// ldflags**：Go 1.18+ 在版本控制目录内构建时自动写入 vcs.revision / vcs.time /
// vcs.modified，Windows 的 go build（含 -ldflags "-s -w"）同样保留该段。
//
// 为什么需要它（2026-10-31）：Windows 真机排障的最大坑之一是「测的其实是旧
// 构建」——用户 LastWriteTime 停在旧时间戳，几轮修复从未被真正验证。真机
// 第一步应跑 `chaosetd.exe -version` / `chaoset-cli.exe -version` 确认提交号。
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Info 返回单行构建信息，例如：
//
//	chaoset go1.27.3 darwin/arm64 rev=3f2a1c9b7d4e built=2026-10-31T01:23:45Z
//
// 不在版本控制下构建（源码 tarball、-buildvcs=false）时 rev/built 缺失，
// 末尾标 no-vcs，仍能区分「是不是本次构建」。
func Info() string {
	bi, ok := debug.ReadBuildInfo()
	parts := []string{"chaoset", runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH}
	if ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		parts = append(parts, "version="+bi.Main.Version)
	}
	rev, built, modified := "", "", false
	if ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				built = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev != "" {
		parts = append(parts, "rev="+rev)
	}
	if built != "" {
		parts = append(parts, "built="+built)
	}
	if modified {
		parts = append(parts, "dirty")
	}
	if rev == "" && built == "" {
		parts = append(parts, "no-vcs")
	}
	return strings.Join(parts, " ")
}
