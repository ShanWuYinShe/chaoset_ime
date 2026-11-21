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
//	  Fuzzy      REG_DWORD  缺省 0（未写入 = 关闭；模糊音：zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）
//	  Shuangpin  REG_SZ     缺省 ""；只写入 ""|zrm|mspy|flypy（空 = 全拼）
//	  ModeScope  REG_SZ     缺省 "system"；只写入 system|process，读取大小写不敏感
//	                        并归一化旧值（旧三档 window→process，未知→system）
//	  UiEnglish  REG_DWORD  缺省 0（UI 宿主上次的中英模式镜像）
//	  Theme      REG_SZ     缺省 "system"；只写入 system|light|dark，读取大小写
//	                        不敏感并归一化未知值为 system
//
// macOS（shims/macos/src/CandidateSettings.swift 的 AssociateSetting 供候选窗
// 热路径；Slint 统一设置中心经 chaosetctl 写同一 UserDefaults 域，因此
// UserDefaults.standard 的域 = 输入法 bundle id，见 shims/macos/build.sh 的
// CFBundleIdentifier；不是安装器 Setup.app 的 ime.chaoset.setup）：
//
//	域 ime.chaoset.inputmethod.ChaosetIME
//	  AssociateEnabled    Bool    缺省 true
//	  ChaosetFuzzy        Bool    缺省 false
//	  ChaosetShuangpin    String  缺省 ""（""|zrm|mspy|flypy；空 = 全拼）
//	  ChaosetModeScope    String  缺省 "system"（system|process；
//	                              旧值 window 读取时归一化）
//	  ChaosetTheme        String  缺省 "system"（system|light|dark；读取宽容归一化）
//	  （UiEnglish：macOS 壳没有持久化中英状态，不支持，见 ErrUnsupported）
//
// Linux（config.json 由设置中心经 chaosetctl 写入、daemon 启动时读取；路径口径与
// internal/appdirs LinuxConfigPath 一致）：
//
//	$XDG_CONFIG_HOME/chaoset/config.json（缺省 $HOME/.config/chaoset/config.json）
//	  "associate"   bool    缺省 true
//	  "fuzzy"       bool    缺省 false（引擎 main.cpp 同键读取后传给 daemon）
//	  "shuangpin"   string  缺省 ""（""|zrm|mspy|flypy；引擎同键读取）
//	  "mode_scope"  string  缺省 "system"（system|process；旧值 window 读取时归一化）
//	  "theme"       string  缺省 "system"（system|light|dark；读取宽容归一化）
//	  其它键（traditional 等）原样保留。
//	  （UiEnglish：Linux 壳没有持久化中英状态，不支持）
//
// # JSON 契约
//
// 单行紧凑 JSON，字段顺序固定：
//
//	{"associate":true,"fuzzy":false,"shuangpin":"","mode_scope":"system","ui_english":false,"theme":"system"}
//
// 供 chaoset-settingsctl get 输出与三端设置窗解析；改字段名即破坏契约。
// theme 是用户可见的外观选择：system 跟随系统（默认）/ light 浅色 / dark 深色；
// 三端存储键名见下方各平台段落（与壳逐字兼容，禁止改键名）。
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
//   - system：全局共享（默认）——切一次所有程序都生效；
//   - process：每进程独立——每个程序各自记住中英状态。
type ModeScope string

const (
	ModeScopeSystem  ModeScope = "system"
	ModeScopeProcess ModeScope = "process"
)

// Valid 报告作用域是否为两种合法取值之一（大小写敏感；存储读取路径用
// parseStoredModeScope 做宽容归一化，CLI 解析用 ParseModeScope）。
func (m ModeScope) Valid() bool {
	switch m {
	case ModeScopeSystem, ModeScopeProcess:
		return true
	}
	return false
}

// Theme 是用户可见的外观选择，三端统一三档（与 Windows HKCU Theme、
// macOS ChaosetTheme、Linux config.json theme 同语义）：
//
//   - system：跟随系统外观（默认）；
//   - light：强制浅色（蓝白）；
//   - dark：强制深色。
//
// 只影响 UI（候选窗/设置窗/托盘），不影响输入引擎行为。
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Valid 报告主题是否为三档合法取值之一（大小写敏感；存储读取路径用
// parseStoredTheme 做宽容归一化，CLI 解析用 ParseTheme）。
func (t Theme) Valid() bool {
	switch t {
	case ThemeSystem, ThemeLight, ThemeDark:
		return true
	}
	return false
}

// Shuangpin 是双拼方案选择（三端统一；空串 = 全拼）。取值与 daemon
// -shuangpin 参数、Rust/Go 引擎的 ShuangpinScheme 名逐字一致：
//
//   - ""      ：全拼（默认）；
//   - "zrm"   ：自然码；
//   - "mspy"  ：微软双拼；
//   - "flypy" ：小鹤双拼。
//
// 只影响引擎匹配口径（双拼两键一音节），不改候选算法其它行为。
type Shuangpin string

const (
	ShuangpinFull  Shuangpin = ""
	ShuangpinZrm   Shuangpin = "zrm"
	ShuangpinMspy  Shuangpin = "mspy"
	ShuangpinFlypy Shuangpin = "flypy"
)

// Valid 报告是否为四种合法取值之一（空串是全拼，属于合法值）。
func (s Shuangpin) Valid() bool {
	switch s {
	case ShuangpinFull, ShuangpinZrm, ShuangpinMspy, ShuangpinFlypy:
		return true
	}
	return false
}

// ParseShuangpin 解析 CLI/调用方传入的双拼方案：full/none/"" 归一化为全拼
// （空串），zrm/mspy/flypy 原样；大小写与首尾空白容忍。非法输入报参数错误。
func ParseShuangpin(raw string) (Shuangpin, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "full", "none", "quanpin":
		return ShuangpinFull, nil
	case string(ShuangpinZrm):
		return ShuangpinZrm, nil
	case string(ShuangpinMspy):
		return ShuangpinMspy, nil
	case string(ShuangpinFlypy):
		return ShuangpinFlypy, nil
	}
	return ShuangpinFull, fmt.Errorf("shuangpin 只接受 full|zrm|mspy|flypy（full=全拼），收到 %q", raw)
}

// parseStoredShuangpin 是存储读取路径的宽容归一化：大小写/空白容忍，未知值
// 回退全拼（存储嗅探到脏值不应让输入法起不来；写路径 Validate 保证不写脏值）。
func parseStoredShuangpin(raw string) Shuangpin {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ShuangpinZrm):
		return ShuangpinZrm
	case string(ShuangpinMspy):
		return ShuangpinMspy
	case string(ShuangpinFlypy):
		return ShuangpinFlypy
	}
	return ShuangpinFull
}

// Settings 是三端统一设置项。JSON 字段名/顺序即 CLI 契约。
type Settings struct {
	Associate bool      `json:"associate"`
	Fuzzy     bool      `json:"fuzzy"`
	Shuangpin Shuangpin `json:"shuangpin"`
	ModeScope ModeScope `json:"mode_scope"`
	UiEnglish bool      `json:"ui_english"`
	Theme     Theme     `json:"theme"`
}

// Defaults 返回三端一致的缺省值（与三端现有壳的缺省行为一致）。
func Defaults() Settings {
	return Settings{
		Associate: true,
		Fuzzy:     false,
		Shuangpin: ShuangpinFull,
		ModeScope: ModeScopeSystem,
		UiEnglish: false,
		Theme:     ThemeSystem,
	}
}

// Validate 校验设置项取值。bool 项无需校验；mode_scope 必须是合法枚举
// （只允许新值 system|process；旧值 window 只在读取/CLI 解析时归一化，不进入写入）。
func (s Settings) Validate() error {
	if !s.Shuangpin.Valid() {
		return fmt.Errorf("shuangpin 非法: %q（只允许 \"\"|zrm|mspy|flypy）", string(s.Shuangpin))
	}
	if !s.ModeScope.Valid() {
		return fmt.Errorf("mode_scope 非法: %q（只允许 system|process）", string(s.ModeScope))
	}
	if !s.Theme.Valid() {
		return fmt.Errorf("theme 非法: %q（只允许 system|light|dark）", string(s.Theme))
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

// normalizeModeScope 把大小写/首尾空白容忍的取值归一化为两档：
// system→system、process→process；旧三档 window→process（「每窗口」并入
// 「每进程」）；未知/空值 ok=false（由调用方决定报错还是回退缺省）。
func normalizeModeScope(raw string) (ModeScope, bool) {
	switch ModeScope(strings.ToLower(strings.TrimSpace(raw))) {
	case ModeScopeSystem:
		return ModeScopeSystem, true
	case ModeScopeProcess, "window":
		return ModeScopeProcess, true
	}
	return "", false
}

// ParseModeScope 解析 CLI/调用方传入的作用域：只接受新值 system|process，
// 同时接受旧三档值 window（归一化为 process）；容忍首尾空白与大小写。
// 非法输入报参数错误。
func ParseModeScope(raw string) (ModeScope, error) {
	if scope, ok := normalizeModeScope(raw); ok {
		return scope, nil
	}
	return "", fmt.Errorf("mode-scope 只接受 system|process（兼容旧值 window），收到 %q", raw)
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

// parseStoredModeScope 归一化从原生存储读到的 mode_scope（三端一致）。
//
// 旧三档迁移：system/process 保持原值，window→process（「每窗口」并入
// 「每进程」）；未知/空值回退缺省 system（不会因为用户手改配置而让 get 失败）。
// 大小写与首尾空白容忍（Windows 壳用 _wcsicmp，macOS 只认小写枚举，Linux
// 设置窗原样返回——这里统一成同一套宽容语义）。
func parseStoredModeScope(raw string) ModeScope {
	if scope, ok := normalizeModeScope(raw); ok {
		return scope
	}
	return ModeScopeSystem
}

// ParseTheme 解析 CLI/调用方传入的主题：只接受 system|light|dark；
// 容忍首尾空白与大小写。非法输入报参数错误。
func ParseTheme(raw string) (Theme, error) {
	if t, ok := normalizeTheme(raw); ok {
		return t, nil
	}
	return "", fmt.Errorf("theme 只接受 system|light|dark，收到 %q", raw)
}

// normalizeTheme 把大小写/首尾空白容忍的取值归一化为三档主题。
func normalizeTheme(raw string) (Theme, bool) {
	switch Theme(strings.ToLower(strings.TrimSpace(raw))) {
	case ThemeSystem:
		return ThemeSystem, true
	case ThemeLight:
		return ThemeLight, true
	case ThemeDark:
		return ThemeDark, true
	}
	return "", false
}

// parseStoredTheme 归一化从原生存储读到的 theme（三端一致）：
// system/light/dark 保持原值，大小写/首尾空白容忍；未知/空值回退缺省 system。
func parseStoredTheme(raw string) Theme {
	if t, ok := normalizeTheme(raw); ok {
		return t
	}
	return ThemeSystem
}
