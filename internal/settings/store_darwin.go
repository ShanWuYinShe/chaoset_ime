//go:build darwin

package settings

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// macOS 存储：UserDefaults.standard 的键，经 /usr/bin/defaults 读写（标准库
// 无 plist API；用系统自带 defaults 与 Objective-C 的 UserDefaults 同一域）。
//
// 域 = 输入法 app 的 CFBundleIdentifier（见 shims/macos/build.sh）：
// SettingsWindow.swift 与 CandidatePanel.swift 一起编进 ChaosetIME.app，
// 因此设置窗里的 UserDefaults.standard 就是该 bundle 域；安装器
// ChaosetSetup.app（ime.chaoset.setup）不存设置。显式传域还能让本 CLI
// 以任意进程名运行（无 bundle 的进程 UserDefaults.standard 域不是它）。
const (
	defaultsDomain = "ime.chaoset.inputmethod.ChaosetIME"
	defaultsBin    = "/usr/bin/defaults"

	// 与 shims/macos/src/CandidateSettings.swift（拆分前在 CandidatePanel.swift）
	// 的 AssociateSetting.key / ModeScopeSetting.key 逐字一致。注意：联想开关的键是 "AssociateEnabled"
	// （不是 "Associate"；任务单/架构文档里的 "Associate" 是简称）。
	macKeyAssociate = "AssociateEnabled"
	macKeyModeScope = "ChaosetModeScope"
)

// Load 读取 macOS 设置。键不存在 = 缺省（AssociateEnabled 缺省 true、
// ChaosetModeScope 缺省 system）；UiEnglish 恒为缺省 false（macOS 无此持久化）。
func Load() (Settings, error) {
	s := Defaults()
	associate, ok, err := readDefaultsBool(macKeyAssociate)
	if err != nil {
		return s, err
	}
	if ok {
		s.Associate = associate
	}
	scope, ok, err := readDefaultsString(macKeyModeScope)
	if err != nil {
		return s, err
	}
	if ok {
		s.ModeScope = parseStoredModeScope(scope)
	}
	return s, nil
}

// SetAssociate 写 UserDefaults 的 AssociateEnabled（bool，与 Swift 侧
// UserDefaults.standard.set(newValue, forKey:) 同键同类型）。
func SetAssociate(on bool) error {
	value := "false"
	if on {
		value = "true"
	}
	if _, err := runDefaults("write", defaultsDomain, macKeyAssociate, "-bool", value); err != nil {
		return fmt.Errorf("写 macOS 设置 %s 失败: %w", macKeyAssociate, err)
	}
	return nil
}

// SetModeScope 写 UserDefaults 的 ChaosetModeScope（string）。
func SetModeScope(scope ModeScope) error {
	if !scope.Valid() {
		return fmt.Errorf("mode_scope 非法: %q（只允许 system|process|window）", string(scope))
	}
	if _, err := runDefaults("write", defaultsDomain, macKeyModeScope, "-string", string(scope)); err != nil {
		return fmt.Errorf("写 macOS 设置 %s 失败: %w", macKeyModeScope, err)
	}
	return nil
}

// SetUiEnglish: macOS 壳没有持久化中英状态，不支持。
func SetUiEnglish(bool) error {
	return fmt.Errorf("%w: ui_english 仅 Windows 有持久化（macOS 壳不保存中英状态）", ErrUnsupported)
}

func readDefaultsBool(key string) (bool, bool, error) {
	out, err := runDefaults("read", defaultsDomain, key)
	if err != nil {
		if isDefaultsUnset(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("读取 macOS 设置 %s 失败: %w", key, err)
	}
	value, parseErr := parseDefaultsBool(out)
	if parseErr != nil {
		return false, false, fmt.Errorf("解析 macOS 设置 %s 失败: %w", key, parseErr)
	}
	return value, true, nil
}

func readDefaultsString(key string) (string, bool, error) {
	out, err := runDefaults("read", defaultsDomain, key)
	if err != nil {
		if isDefaultsUnset(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("读取 macOS 设置 %s 失败: %w", key, err)
	}
	return parseDefaultsString(out), true, nil
}

// runDefaults 执行 /usr/bin/defaults 并返回 trim 后的 stdout；失败时把
// stderr 一起带进 error 便于排障。未设置（域/键不存在）由 isDefaultsUnset
// 按退出码识别。
func runDefaults(args ...string) (string, error) {
	cmd := exec.Command(defaultsBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s %s: %w（%s）",
				defaultsBin, strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("%s %s: %w", defaultsBin, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// isDefaultsUnset 判断 defaults read 的失败是否为「域/键不存在」。
// defaults 在未设置时退出码为 1；错误文本会随系统语言本地化，不能匹配
// 英文 "does not exist"。把退出码 1 一律当未设置（冷路径读取，回落到缺省
// 值比因为一个键没写过而报错更重要）；其它退出码/启动失败按错误上报。
func isDefaultsUnset(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == 1
	}
	return false
}

// parseDefaultsBool 解析 `defaults read <domain> <bool键>` 的输出：
// 布尔值打印为 1/0；容忍 true/false/yes/no（大小写不敏感）与整数字面量
// （Swift 的 as? Bool 对非 0 NSNumber 也视为 true）。
func parseDefaultsBool(out string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	}
	if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
		return n != 0, nil
	}
	return false, fmt.Errorf("不是布尔值: %q", out)
}

// parseDefaultsString 解析 `defaults read <domain> <字符串键>` 的输出：
// 多数版本裸打印，某些版本会带引号，统一去掉首尾引号。
func parseDefaultsString(out string) string {
	s := strings.TrimSpace(out)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}
