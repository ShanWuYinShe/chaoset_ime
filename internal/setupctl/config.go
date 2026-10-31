// Package setupctl 是 Linux 设置窗的纯 Go 业务逻辑（配置读写、学习数据、
// 词库导入编排）。GTK 只负责画界面并调用本包，便于无头 CI 做单测。
//
// 平台路径来自 internal/appdirs；学习数据直接走 engine.UserDict，与
// chaoset-userdict 工具/ibus 引擎同一套语义（engine 包零改动）。
package setupctl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"chaoset.ime/internal/appdirs"
)

// Config 是 Linux 设置窗写入的配置；Associate 为 nil 表示缺省开启（与
// ibus-engine config.go 的 *bool 语义一致）。
type Config struct {
	Associate *bool `json:"associate,omitempty"`
}

// ConfigPath 返回设置窗配置文件路径（$XDG_CONFIG_HOME/chaoset/config.json）。
func ConfigPath() (string, error) {
	return appdirs.LinuxConfigPath()
}

// LoadConfig 读取配置；文件不存在/损坏时返回空配置（缺省开启），与旧
// Python 设置窗语义一致——配置坏了不能让设置窗打不开。
func LoadConfig() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, nil // 损坏配置按缺省处理，避免设置窗无法启动
	}
	return cfg, nil
}

// SaveConfig 原子写入配置（同目录临时文件 + rename），避免引擎读到半截 JSON。
func SaveConfig(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置失败: %w", err)
	}
	tmpName := tmp.Name()
	remove := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		remove()
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		remove()
		return fmt.Errorf("关闭配置失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		remove()
		return fmt.Errorf("设置配置权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		remove()
		return fmt.Errorf("替换配置失败: %w", err)
	}
	return nil
}
