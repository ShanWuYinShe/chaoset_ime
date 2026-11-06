package settings

import (
	"strings"
	"testing"
)

// 三端一致的缺省值：联想开启、作用域 system、UiEnglish 关闭。
func TestDefaults(t *testing.T) {
	d := Defaults()
	if !d.Associate {
		t.Error("Defaults().Associate 应为 true（三端壳：未配置 = 开启）")
	}
	if d.ModeScope != ModeScopeSystem {
		t.Errorf("Defaults().ModeScope = %q，期望 system", d.ModeScope)
	}
	if d.UiEnglish {
		t.Error("Defaults().UiEnglish 应为 false（Windows UiEnglish 缺省 0）")
	}
	if err := d.Validate(); err != nil {
		t.Errorf("Defaults().Validate() = %v，期望 nil", err)
	}
}

// JSON 契约：字段名、顺序、单行紧凑格式由三端设置窗/脚本依赖，改了就是破坏契约。
func TestDefaultsJSONContract(t *testing.T) {
	got, err := Defaults().JSON()
	if err != nil {
		t.Fatalf("JSON() 报错: %v", err)
	}
	want := `{"associate":true,"mode_scope":"system","ui_english":false}`
	if got != want {
		t.Errorf("JSON 契约不符：\n got  = %s\n want = %s", got, want)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("JSON 必须是单行（不含换行）: %q", got)
	}
}

func TestJSONContractAllValues(t *testing.T) {
	s := Settings{Associate: false, ModeScope: ModeScopeWindow, UiEnglish: true}
	got, err := s.JSON()
	if err != nil {
		t.Fatalf("JSON() 报错: %v", err)
	}
	want := `{"associate":false,"mode_scope":"window","ui_english":true}`
	if got != want {
		t.Errorf("JSON 契约不符：got = %s，want = %s", got, want)
	}
}

func TestJSONRejectsInvalidModeScope(t *testing.T) {
	s := Settings{Associate: true, ModeScope: "auto"}
	if _, err := s.JSON(); err == nil {
		t.Fatal("mode_scope 非法时 JSON() 应报错（不得输出非法契约）")
	}
}

func TestParseModeScope(t *testing.T) {
	cases := []struct {
		in   string
		want ModeScope
		ok   bool
	}{
		{"system", ModeScopeSystem, true},
		{"process", ModeScopeProcess, true},
		{"window", ModeScopeWindow, true},
		{" SYSTEM ", ModeScopeSystem, true}, // CLI 容忍空白/大小写
		{"Window", ModeScopeWindow, true},
		{"auto", "", false},
		{"", "", false},
		{"1", "", false},
		{"system window", "", false},
	}
	for _, c := range cases {
		got, err := ParseModeScope(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParseModeScope(%q) 报错: %v", c.in, err)
			} else if got != c.want {
				t.Errorf("ParseModeScope(%q) = %q，期望 %q", c.in, got, c.want)
			}
		} else if err == nil {
			t.Errorf("ParseModeScope(%q) 期望报错，实际 = %q", c.in, got)
		}
	}
}

func TestParseOnOff(t *testing.T) {
	cases := []struct {
		in   string
		want bool
		ok   bool
	}{
		{"on", true, true},
		{"off", false, true},
		{" ON ", true, true},
		{"Off", false, true},
		{"yes", false, false},
		{"1", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		got, err := ParseOnOff(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParseOnOff(%q) 报错: %v", c.in, err)
			} else if got != c.want {
				t.Errorf("ParseOnOff(%q) = %v，期望 %v", c.in, got, c.want)
			}
		} else if err == nil {
			t.Errorf("ParseOnOff(%q) 期望报错，实际 = %v", c.in, got)
		}
	}
}

func TestValidate(t *testing.T) {
	valid := []ModeScope{ModeScopeSystem, ModeScopeProcess, ModeScopeWindow}
	for _, scope := range valid {
		if err := (Settings{Associate: true, ModeScope: scope}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v，期望 nil", scope, err)
		}
	}
	for _, scope := range []ModeScope{"", "SYSTEM", "auto"} {
		if err := (Settings{ModeScope: scope}).Validate(); err == nil {
			t.Errorf("Validate(%q) 期望报错", scope)
		}
	}
}

// parseStoredModeScope：Windows 壳用 _wcsicmp（大小写不敏感）、未知值回退
// system；macOS/Linux 的存储读取也统一成这个语义。
func TestParseStoredModeScope(t *testing.T) {
	cases := []struct {
		in   string
		want ModeScope
	}{
		{"system", ModeScopeSystem},
		{"process", ModeScopeProcess},
		{"window", ModeScopeWindow},
		{"PROCESS", ModeScopeProcess},
		{" Window ", ModeScopeWindow},
		{"", ModeScopeSystem},
		{"auto", ModeScopeSystem},
	}
	for _, c := range cases {
		if got := parseStoredModeScope(c.in); got != c.want {
			t.Errorf("parseStoredModeScope(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// Windows 注册表子键必须逐字是 Software\\Chaoset（HKCU 下的两级子键）。
// Windows 存储代码无法在 Linux/主 CI 上运行，只能靠这个平台无关单测锁定：
// 少一个反斜杠会静默写到 "SoftwareChaoset" 扁平键名，与 C 壳完全不兼容
// （曾经真的丢过一次反斜杠，见 store_windows.go 的同名常量注释）。
func TestWindowsRegistrySubKey(t *testing.T) {
	const want = `Software\Chaoset`
	if windowsRegistrySubKey != want {
		t.Fatalf("windowsRegistrySubKey = %q，期望 %q", windowsRegistrySubKey, want)
	}
	parts := strings.Split(windowsRegistrySubKey, `\`)
	if len(parts) != 2 || parts[0] != "Software" || parts[1] != "Chaoset" {
		t.Fatalf("注册表子键应为 HKCU\\Software\\Chaoset（两级），实际 %q", windowsRegistrySubKey)
	}
}

func TestModeScopeValid(t *testing.T) {
	if !ModeScopeSystem.Valid() || !ModeScopeProcess.Valid() || !ModeScopeWindow.Valid() {
		t.Error("三个合法 ModeScope 都应 Valid()")
	}
	if ModeScope("").Valid() || ModeScope("System").Valid() || ModeScope("auto").Valid() {
		t.Error("非法 ModeScope 不应 Valid()")
	}
}
