package setupctl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaoset.ime/engine"
)

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "chaoset", "config.json"); path != want {
		t.Fatalf("ConfigPath() = %q，期望 %q", path, want)
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.Associate != nil {
		t.Fatalf("缺省配置应无 Associate，实际 %+v err=%v", cfg, err)
	}
	off := false
	if err := SaveConfig(Config{Associate: &off}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Associate == nil || *loaded.Associate {
		t.Fatalf("保存后应读回 associate=false，实际 %+v", loaded)
	}
}

func TestLoadConfigCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.Associate != nil {
		t.Fatalf("损坏配置应回退缺省，实际 %+v err=%v", cfg, err)
	}
}

func TestUserDictLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	path, err := UserDictPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "chaoset", "userdict.json"); path != want {
		t.Fatalf("UserDictPath() = %q，期望 %q", path, want)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	u, err := engine.NewUserDict(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatal(err)
	}
	if err := u.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := Stats()
	if err != nil || st.Words == 0 {
		t.Fatalf("Stats() = %+v err=%v，期望有词", st, err)
	}
	var buf bytes.Buffer
	if err := ExportTSV(&buf, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "你好") {
		t.Fatalf("导出应含「你好」，实际 %q", buf.String())
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	st, err = Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("清空后统计应全 0，实际 %+v", st)
	}
}
