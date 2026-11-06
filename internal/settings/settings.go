// Package settings 是 chaoset_ime 三端设置项的单一真源：schema、默认值、
// 校验与 JSON 契约都在这里；平台持久化由同包的 store_*.go 提供。
//
// 方案 A（2026-11-06 用户要求）：三端设置窗把「设置逻辑/契约」收敛到 Go，
// shim 只做渲染与用户交互。本包与 cmd/chaoset-settingsctl 只服务**冷路径**
// （设置窗读取/写入、诊断）；输入热路径（Windows TSF 回调、macOS IMK、
// Linux ibus）不调用本包，继续读各自的本机缓存/原生存储，避免起进程。
//
// # 三端存储映射（与现有壳逐字兼容，禁止改键名/类型/默认值）
//
// Windows（shims/windows/src/config.cpp，拆分前在 win32.cpp：
// RegGetAssociate/RegSetAssociate、uiLoadModeScope/uiSaveModeScope、
// readHkcuEnglish/uiSaveEnglish）：
//
//	HKCU\Software\Chaoset
//	  Associate  REG_DWORD  缺省 1（未写入 = 开启）
//	  ModeScope  REG_SZ     缺省 "system"；读取大小写不敏感（system|process|window）
//	  UiEnglish  REG_DWORD  缺省 0（UI 宿主上次的中英模式镜像）
//
// macOS（shims/macos/src/CandidateSettings.swift：AssociateSetting、
// ModeScopeSetting，拆分前在 CandidatePanel.swift；SettingsWindow.swift
// 编进 ChaosetIME.app，因此
// UserDefaults.standard 的域 = 输入法 bundle id，见 shims/macos/build.sh 的
// CFBundleIdentifier；不是安装器 Setup.app 的 ime.chaoset.setup）：
//
//	域 ime.chaoset.inputmethod.ChaosetIME
//	  AssociateEnabled    Bool    缺省 true
//	  ChaosetModeScope    String  缺省 "system"（system|process|window）
//	  （UiEnglish：macOS 壳没有持久化中英状态，不支持，见 ErrUnsupported）
//
// Linux（shims/linux/setup/setup_ctl.c：setup_config_load/setup_config_save/
// setup_config_load_mode_scope/setup_config_save_mode_scope；路径口径与
// internal/appdirs LinuxConfigPath 一致）：
//
//	$XDG_CONFIG_HOME/chaoset/config.json（缺省 $HOME/.config/chaoset/config.json）
//	  "associate"   bool    缺省 true
//	  "mode_scope"  string  缺省 "system"
//	  其它键（fuzzy/traditional/shuangpin 等）原样保留。
//	  （UiEnglish：Linux 壳没有持久化中英状态，不支持）
//
// # JSON 契约
//
// 单行紧凑 JSON，字段顺序固定：
//
//	{"associate":true,"mode_scope":"system","ui_english":false}
//
// 供 chaoset-settingsctl get 输出与三端设置窗解析；改字段名即破坏契约。
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// windowsRegistrySubKey 是 Windows 注册表子键（HKCU 下），必须与
// shims/windows/src/win32.cpp（按 docs/architecture.md 拆分后归 config.cpp）的
// L"Software\\Chaoset" 逐字一致。
//
// 故意放在平台无关文件里并由单测锁定：Windows 注册表路径少一个反斜杠会静默写到
// "SoftwareChaoset" 这个扁平键名（与 C 壳读写的 Software\Chaoset 子键完全不
// 兼容），而 Linux 本地/CI 都没有 Windows 运行时，只能靠这里的单测兜底。
const windowsRegistrySubKey = `Software\Chaoset`

// ErrUnsupported 表示当前平台没有该设置项的原生持久化（调用方据此提示/
// 降级，不要把它当参数错误）。
var ErrUnsupported = errors.New("当前平台不支持该设置项")

// ModeScope 是中英状态的共享作用域（与 Windows HKCU ModeScope、macOS
// UserDefaults ChaosetModeScope、Linux config.json mode_scope 同语义）。
//
//   - system：全系统共享（默认）——切一次所有程序都生效；
//   - process：每个程序各自独立；
//   - window：每个窗口/输入上下文独立。
type ModeScope string

const (
	ModeScopeSystem  ModeScope = "system"
	ModeScopeProcess ModeScope = "process"
	ModeScopeWindow  ModeScope = "window"
)

// Valid 报告作用域是否为三种合法取值之一（大小写敏感；存储读取路径用
// parseStoredModeScope 做宽容归一化）。
func (m ModeScope) Valid() bool {
	switch m {
	case ModeScopeSystem, ModeScopeProcess, ModeScopeWindow:
		return true
	}
	return false
}

// Settings 是三端统一设置项。JSON 字段名/顺序即 CLI 契约。
type Settings struct {
	Associate bool      `json:"associate"`
	ModeScope ModeScope `json:"mode_scope"`
	UiEnglish bool      `json:"ui_english"`
}

// Defaults 返回三端一致的缺省值（与三端现有壳的缺省行为一致）。
func Defaults() Settings {
	return Settings{
		Associate: true,
		ModeScope: ModeScopeSystem,
		UiEnglish: false,
	}
}

// Validate 校验设置项取值。bool 项无需校验；mode_scope 必须是合法枚举。
func (s Settings) Validate() error {
	if !s.ModeScope.Valid() {
		return fmt.Errorf("mode_scope 非法: %q（只允许 system|process|window）", string(s.ModeScope))
	}
	return nil
}

// JSON 返回契约规定的单行紧凑 JSON（字段顺序固定）。
func (s Settings) JSON() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ParseModeScope 解析 CLI/调用方传入的作用域：容忍首尾空白与大小写，
// 只接受 system|process|window（供 set mode-scope 用，非法输入报参数错误）。
func ParseModeScope(raw string) (ModeScope, error) {
	scope := ModeScope(strings.ToLower(strings.TrimSpace(raw)))
	if !scope.Valid() {
		return "", fmt.Errorf("mode-scope 只接受 system|process|window，收到 %q", raw)
	}
	return scope, nil
}

// ParseOnOff 解析 CLI/调用方传入的开关值：on/off（大小写不敏感、容忍空白）。
func ParseOnOff(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("只接受 on|off，收到 %q", raw)
}

// parseStoredModeScope 归一化从原生存储读到的 mode_scope。
//
// 三端历史行为：Windows 壳用 _wcsicmp 做大小写不敏感比较，未知值一律按
// system；macOS 只认小写枚举（rawValue 不匹配即 system）；Linux 设置窗把
// 任意字符串原样返回。这里统一成 Windows 语义：大小写不敏感匹配，未知/
// 空值回退 system（不会因为用户手改配置而让 get 失败）。
func parseStoredModeScope(raw string) ModeScope {
	scope := ModeScope(strings.ToLower(strings.TrimSpace(raw)))
	if scope.Valid() {
		return scope
	}
	return ModeScopeSystem
}
