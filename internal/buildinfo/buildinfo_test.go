package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestInfoNonEmpty(t *testing.T) {
	info := Info()
	if info == "" {
		t.Fatal("Info() 不应为空")
	}
	for _, want := range []string{"chaoset", runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH} {
		if !strings.Contains(info, want) {
			t.Fatalf("Info()=%q 缺少 %q", info, want)
		}
	}
}

// Info 必须单行：-version 的输出会被脚本/真机排障直接 grep。
func TestInfoSingleLine(t *testing.T) {
	if strings.ContainsAny(Info(), "\r\n") {
		t.Fatalf("Info() 不应包含换行: %q", Info())
	}
}
