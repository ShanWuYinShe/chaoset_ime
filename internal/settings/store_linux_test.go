//go:build linux

package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupXDG 把 XDG_CONFIG_HOME 指向测试临时目录，返回期望的 config.json 路径。
// 用 t.Setenv（自动恢复），保证不会读写用户真实配置（$HOME/.config/chaoset）。
func setupXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "chaoset", "config.json")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写 %s 失败: %v", path, err)
	}
}

// 文件不存在 = 全缺省，且 Load 不得顺手创建文件。
func TestLinuxLoadDefaultsWhenMissing(t *testing.T) {
	path := setupXDG(t)
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	if s != Defaults() {
		t.Errorf("Load() = %+v，期望缺省 %+v", s, Defaults())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load() 不应创建 %s（stat err = %v）", path, err)
	}
}

// 空文件按缺省处理（C 设置窗 strstr 读空文件同样是缺省）。
func TestLinuxEmptyFileDefaults(t *testing.T) {
	path := setupXDG(t)
	writeFile(t, path, "")
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	if s != Defaults() {
		t.Errorf("Load() = %+v，期望缺省 %+v", s, Defaults())
	}
}

// 读写往返：associate 与 mode_scope 互不覆盖。
func TestLinuxRoundTrip(t *testing.T) {
	path := setupXDG(t)

	if err := SetAssociate(false); err != nil {
		t.Fatalf("SetAssociate(false) 报错: %v", err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	if s.Associate {
		t.Error("SetAssociate(false) 后 Load().Associate 仍为 true")
	}
	if s.ModeScope != ModeScopeSystem {
		t.Errorf("SetAssociate 后 mode_scope = %q，期望保持缺省 system", s.ModeScope)
	}

	if err := SetModeScope(ModeScopeProcess); err != nil {
		t.Fatalf("SetModeScope(process) 报错: %v", err)
	}
	s, err = Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	if s.Associate {
		t.Error("SetModeScope 后 associate 被覆盖了（应保留 false）")
	}
	if s.ModeScope != ModeScopeProcess {
		t.Errorf("Load().ModeScope = %q，期望 process", s.ModeScope)
	}

	// 写出的文件对 C 设置窗的字符串读取（strstr "\"associate\""）友好：
	// 键名与 true/false、字符串字面量格式必须与 setup_ctl.c 一致。
	text := readFile(t, path)
	for _, want := range []string{`"associate": false`, `"mode_scope": "process"`} {
		if !strings.Contains(text, want) {
			t.Errorf("config.json 应包含 %q，实际:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		t.Error("config.json 应以换行结尾（与 C 的 g_strdup_printf 输出一致）")
	}
}

// 与 C 设置窗（shims/linux/setup/setup_ctl.c）写入格式逐字节一致：默认两项时。
func TestLinuxDefaultFileMatchesCFormat(t *testing.T) {
	path := setupXDG(t)
	if err := SetAssociate(true); err != nil {
		t.Fatalf("SetAssociate(true) 报错: %v", err)
	}
	if err := SetModeScope(ModeScopeSystem); err != nil {
		t.Fatalf("SetModeScope(system) 报错: %v", err)
	}
	// setup_config_save_mode_scope 的输出（与 C 的 JSON 格式逐字节一致）：
	// {
	//   "associate": true,
	//   "mode_scope": "system"
	// }

	want := "{\n  \"associate\": true,\n  \"mode_scope\": \"system\"\n}\n"
	if got := readFile(t, path); got != want {
		t.Errorf("与 C 设置窗的 JSON 格式不一致：\n got  = %q\n want = %q", got, want)
	}
}

// 读 C 设置窗写出的文件（老用户升级场景）：旧三档 window 归一化为 process，
// 不能因为值是旧格式而 get 失败。
func TestLinuxReadsFileWrittenByCSetup(t *testing.T) {
	path := setupXDG(t)
	writeFile(t, path, "{\n  \"associate\": false,\n  \"mode_scope\": \"window\"\n}\n")
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() 报错: %v", err)
	}
	if s.Associate {
		t.Error("C 文件 associate=false 被读成 true")
	}
	if s.ModeScope != ModeScopeProcess {
		t.Errorf("C 文件旧值 mode_scope=window 应归一化为 process，实际 %q", s.ModeScope)
	}
}

// 写 associate 必须保留其它设置键（C 的 setup_config_save 同语义；
// 回归 shims/linux/setup/test_setup_ctl.c 的「保存 associate 不能丢键」契约）。
func TestLinuxSetAssociatePreservesOtherKeys(t *testing.T) {
	path := setupXDG(t)
	writeFile(t, path, "{\n  \"fuzzy\": true,\n  \"associate\": true\n}\n")
	if err := SetAssociate(false); err != nil {
		t.Fatalf("SetAssociate(false) 报错: %v", err)
	}
	text := readFile(t, path)
	for _, want := range []string{`"fuzzy": true`, `"associate": false`} {
		if !strings.Contains(text, want) {
			t.Errorf("config.json 应保留 %q，实际:\n%s", want, text)
		}
	}
}

// 写 mode_scope 也保留其它键（C 的 setup_config_save_mode_scope 会重建最小
// JSON 丢掉 fuzzy/traditional/shuangpin；Go 单一真源后选择不丢数据，
// 对 C 的 strstr 读取无影响——这是 A1 报告里记录的有意差异）。
func TestLinuxSetModeScopePreservesOtherKeys(t *testing.T) {
	path := setupXDG(t)
	writeFile(t, path, "{\n  \"fuzzy\": true,\n  \"traditional\": false,\n  \"shuangpin\": \"mspy\"\n}\n")
	if err := SetModeScope(ModeScopeProcess); err != nil {
		t.Fatalf("SetModeScope(process) 报错: %v", err)
	}
	text := readFile(t, path)
	for _, want := range []string{`"fuzzy": true`, `"traditional": false`, `"shuangpin": "mspy"`, `"mode_scope": "process"`} {
		if !strings.Contains(text, want) {
			t.Errorf("config.json 应保留 %q，实际:\n%s", want, text)
		}
	}
}

// 存储读取的三端统一归一化：system/process 保持原值；旧三档 window→process；
// 大小写/空白容忍；未知/空回退缺省 system，不报错。
func TestLinuxModeScopeNormalized(t *testing.T) {
	cases := []struct {
		raw  string
		want ModeScope
	}{
		{"system", ModeScopeSystem},
		{"SYSTEM", ModeScopeSystem},
		{"process", ModeScopeProcess},
		{"PROCESS", ModeScopeProcess},
		{"window", ModeScopeProcess}, // 旧值迁移
		{"WINDOW", ModeScopeProcess},
		{"auto", ModeScopeSystem},
		{"", ModeScopeSystem},
	}
	for _, c := range cases {
		path := setupXDG(t)
		writeFile(t, path, `{"mode_scope": "`+c.raw+`"}`)
		s, err := Load()
		if err != nil {
			t.Fatalf("Load() 报错: %v", err)
		}
		if s.ModeScope != c.want {
			t.Errorf("mode_scope=%q 归一化后 = %q，期望 %q", c.raw, s.ModeScope, c.want)
		}
	}
}

// 损坏 JSON：读取报错，写入也拒绝（不得静默覆盖用户配置）。
func TestLinuxCorruptJSON(t *testing.T) {
	path := setupXDG(t)
	writeFile(t, path, "{not json")
	if _, err := Load(); err == nil {
		t.Error("Load() 对损坏 JSON 应报错")
	}
	if err := SetAssociate(true); err == nil {
		t.Error("SetAssociate() 对损坏 JSON 应报错（不静默覆盖）")
	}
	if got := readFile(t, path); got != "{not json" {
		t.Errorf("损坏文件被改写: %q", got)
	}
}

// 字段类型非法：报错而不是静默当默认值。
func TestLinuxInvalidFieldTypes(t *testing.T) {
	for _, content := range []string{
		`{"associate": "yes"}`,
		`{"associate": 1}`,
		`{"mode_scope": 3}`,
		`{"mode_scope": true}`,
	} {
		path := setupXDG(t)
		writeFile(t, path, content)
		if _, err := Load(); err == nil {
			t.Errorf("Load() 对 %s 应报错", content)
		}
	}
}

// 非法枚举写入必须拒绝（CLI 层是退出码 2；store 层也不接受非法值）。
func TestLinuxSetModeScopeRejectsInvalid(t *testing.T) {
	setupXDG(t)
	// 旧三档值不在写入契约里：写入只接受 system|process（旧值 window 只允许读取/CLI 归一化）。
	for _, scope := range []ModeScope{"", "SYSTEM", "PROCESS", "window", "Window", "auto"} {
		if err := SetModeScope(scope); err == nil {
			t.Errorf("SetModeScope(%q) 应报错", scope)
		}
	}
}

// ui_english 在 Linux 没有持久化：必须返回 ErrUnsupported（CLI 退出码 1）。
func TestLinuxUiEnglishUnsupported(t *testing.T) {
	path := setupXDG(t)
	err := SetUiEnglish(true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetUiEnglish(true) = %v，期望 ErrUnsupported", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("SetUiEnglish 不应创建配置文件（stat err = %v）", statErr)
	}
}

// JSON 读取路径：RawMessage 保留未知键的原始写法（数字/嵌套对象不得被重排）。
func TestLinuxUnknownKeysRoundTripRawJSON(t *testing.T) {
	path := setupXDG(t)
	writeFile(t, path, `{"extra": {"n": 1.50}, "associate": true}`)
	if err := SetAssociate(false); err != nil {
		t.Fatalf("SetAssociate(false) 报错: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, path)), &m); err != nil {
		t.Fatalf("写出的 JSON 不合法: %v", err)
	}
	var extra struct {
		N json.Number `json:"n"`
	}
	if err := json.Unmarshal(m["extra"], &extra); err != nil {
		t.Fatalf("extra 键被破坏: %v", err)
	}
	if extra.N.String() != "1.50" {
		t.Errorf("未知键的原始写法被改写: %s", string(m["extra"]))
	}
}
