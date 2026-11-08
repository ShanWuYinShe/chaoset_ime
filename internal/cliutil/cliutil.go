// Package cliutil 是 cmd/ 下多个命令行工具共用的 flag 辅助类型。
//
// 只放「与具体业务无关、多处逐字重复」的小工具；不要把业务逻辑搬进来。
package cliutil

import "strings"

// RepeatFlag 支持同一参数多次传入（如多个 -dict 叠加加载）。
//
// 由 chaosetd / chaoset-cli 共用：此前两处各有一份逐字相同的实现
// （2026-11-08 审计 GO-07），补参数时容易只改一边。
type RepeatFlag []string

func (f *RepeatFlag) String() string { return strings.Join(*f, ",") }

func (f *RepeatFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}
