//go:build linux

package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"chaoset.ime/internal/appdirs"
)

// Linux 存储：$XDG_CONFIG_HOME/chaoset/config.json，与 shims/linux/setup/
// setup_ctl.c 逐字兼容（键名 "associate"/"mode_scope"、缺省 true/system）。
// 路径解析复用 internal/appdirs.LinuxConfigPath，与 chaoset-userdict
// -print-paths 给壳的 config 路径同源。
const (
	linuxKeyAssociate = "associate"
	linuxKeyModeScope = "mode_scope"
)

// Load 读取 Linux 设置。文件不存在/为空时返回 Defaults()（与 C 设置窗
// 缺省行为一致）；JSON 损坏或字段类型非法时报错（不静默当默认，避免
// 后续写入把用户配置覆盖掉）。
func Load() (Settings, error) {
	s := Defaults()
	path, err := linuxConfigPath()
	if err != nil {
		return s, err
	}
	m, err := readConfigMap(path)
	if err != nil {
		return s, err
	}
	if m == nil {
		return s, nil
	}
	if raw, ok := m[linuxKeyAssociate]; ok {
		var on bool
		if err := json.Unmarshal(raw, &on); err != nil {
			return s, fmt.Errorf("设置文件 %s 的 \"associate\" 不是 bool: %w", path, err)
		}
		s.Associate = on
	}
	if raw, ok := m[linuxKeyModeScope]; ok {
		var scope string
		if err := json.Unmarshal(raw, &scope); err != nil {
			return s, fmt.Errorf("设置文件 %s 的 \"mode_scope\" 不是字符串: %w", path, err)
		}
		s.ModeScope = parseStoredModeScope(scope)
	}
	return s, nil
}

// SetAssociate 只改 "associate"，保留文件里的其它键（fuzzy/traditional/
// shuangpin 等），与 C 的 setup_config_save（原地替换）语义一致。
func SetAssociate(on bool) error {
	path, err := linuxConfigPath()
	if err != nil {
		return err
	}
	return updateConfig(path, func(m map[string]json.RawMessage) error {
		raw, err := json.Marshal(on)
		if err != nil {
			return err
		}
		m[linuxKeyAssociate] = raw
		return nil
	})
}

// SetModeScope 只改 "mode_scope"，保留文件里已有的其它键。
//
// 注意：C 的 setup_config_save_mode_scope 会「重建最小 JSON」从而丢掉
// fuzzy/traditional/shuangpin；Go 版选择保留未知键（更安全，对 C 的 strstr
// 读取无影响）。这是本包与现有 C 写入实现的唯一有意差异，已写进 A1 报告。
func SetModeScope(scope ModeScope) error {
	if !scope.Valid() {
		return fmt.Errorf("mode_scope 非法: %q（只允许 system|process|window）", string(scope))
	}
	path, err := linuxConfigPath()
	if err != nil {
		return err
	}
	return updateConfig(path, func(m map[string]json.RawMessage) error {
		raw, err := json.Marshal(string(scope))
		if err != nil {
			return err
		}
		m[linuxKeyModeScope] = raw
		return nil
	})
}

// SetUiEnglish: Linux 壳没有持久化中英状态，不支持（方案 A 的 schema 仍需
// 暴露该字段供 get 的 JSON 契约统一）。
func SetUiEnglish(bool) error {
	return fmt.Errorf("%w: ui_english 仅 Windows 有持久化（Linux 壳不保存中英状态）", ErrUnsupported)
}

func linuxConfigPath() (string, error) {
	path, err := appdirs.LinuxConfigPath()
	if err != nil {
		return "", fmt.Errorf("定位 Linux 设置文件失败: %w", err)
	}
	return path, nil
}

// readConfigMap 读取顶层 JSON 对象：文件不存在返回 (nil, nil)（= 全缺省）；
// 空文件按缺省处理；损坏的 JSON 报错。用 RawMessage 保留未知键的原始写法。
func readConfigMap(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取设置文件 %s 失败: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析设置文件 %s 失败（不是合法 JSON 对象）: %w", path, err)
	}
	return m, nil
}

// updateConfig 读-改-写：读现有对象（文件不存在时为空对象），交给 mutate
// 修改，再原子写回（temp + rename，与 glib g_file_set_contents 同语义，
// 避免引擎/设置窗读到写了一半的 JSON）。
func updateConfig(path string, mutate func(map[string]json.RawMessage) error) error {
	m, err := readConfigMap(path)
	if err != nil {
		return err
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	if err := mutate(m); err != nil {
		return err
	}
	return writeConfigMap(path, m)
}

// writeConfigMap 以 2 空格缩进输出（与 C 设置窗的格式逐字节一致：json 的
// map 键按字典序输出，associate < mode_scope），末尾补换行。
func writeConfigMap(path string, m map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("生成设置 JSON 失败: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建设置目录 %s 失败: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config.json.tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时设置文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写临时设置文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时设置文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("设置文件权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换设置文件 %s 失败: %w", path, err)
	}
	tmpName = ""
	return nil
}
