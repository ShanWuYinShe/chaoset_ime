package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 辅助：写临时 workflow 并跑 checkFile，返回 (错误列表, jobs)。
func check(t *testing.T, content string) ([]string, []string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "wf.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return checkFile(p, nil)
}

func checkExpect(t *testing.T, content, expect string) ([]string, []string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "wf.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var expectJobs []string
	for _, x := range strings.Split(expect, ",") {
		if x = strings.TrimSpace(x); x != "" {
			expectJobs = append(expectJobs, x)
		}
	}
	return checkFile(p, expectJobs)
}

// 最小合法 workflow（覆盖现有 ci.yml 的全部结构特性）。
const goodWF = "name: CI" + "\n" +
	"on:" + "\n" +
	"  push:" + "\n" +
	"    branches: [main]" + "\n" +
	"jobs:" + "\n" +
	"  build:" + "\n" +
	"    runs-on: ubuntu-24.04" + "\n" +
	"    steps:" + "\n" +
	"      - uses: actions/checkout@v7" + "\n" +
	"      - name: 构建" + "\n" +
	"        run: |" + "\n" +
	"          echo '多行脚本里有 YAML 形状文本'" + "\n" +
	"          jobs: fake" + "\n" +
	"          needs: [不存在的job]" + "\n" +
	"  test:" + "\n" +
	"    needs: build" + "\n" +
	"    runs-on: ubuntu-24.04" + "\n" +
	"    steps:" + "\n" +
	"      - run: go test ./..." + "\n"

func TestGoodWorkflowPasses(t *testing.T) {
	errs, jobs := check(t, goodWF)
	if len(errs) != 0 {
		t.Fatalf("合法 workflow 不应报错: %v", errs)
	}
	// jobs 必须按文档顺序；块标量里的「jobs: fake」不得被解析成 job
	want := "build,test"
	if got := strings.Join(jobs, ","); got != want {
		t.Fatalf("jobs 应为 %q（文档顺序，且块标量内容不算），实际 %q", want, got)
	}
}

func TestMissingRunsOnAndSteps(t *testing.T) {
	errs, _ := check(t, "jobs:\n  a:\n    steps: []\n  b:\n    runs-on: ubuntu\n")
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "a 缺少 runs-on") {
		t.Fatalf("应报 a 缺少 runs-on: %v", errs)
	}
	if !strings.Contains(joined, "b 缺少 steps") {
		t.Fatalf("应报 b 缺少 steps: %v", errs)
	}
}

func TestStepNeedsUsesOrRun(t *testing.T) {
	errs, _ := check(t, "jobs:\n  a:\n    runs-on: ubuntu\n    steps:\n      - name: 只有名字\n")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), "既没有 uses 也没有 run") {
		t.Fatalf("应报 step 缺 uses/run: %v", errs)
	}
}

func TestNeedsReferenceAndFlowList(t *testing.T) {
	// flow 序列 needs: [x, y] 必须拆开校验（2026-10-30 Go 版初版漏了它，
	// release.yml 的 needs: [package-macos,...] 被当成一个字符串）
	errs, _ := check(t, "jobs:\n  a:\n    runs-on: ubuntu\n    steps:\n      - run: x\n  b:\n    needs: [a, 不存在]\n    runs-on: ubuntu\n    steps:\n      - run: y\n")
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "不存在的 job: 不存在") {
		t.Fatalf("应报 flow 序列里不存在的 job: %v", errs)
	}
	if strings.Contains(joined, "[") {
		t.Fatalf("needs 不应被当整体字符串: %v", errs)
	}
}

func TestNeedsCycle(t *testing.T) {
	errs, _ := check(t, "jobs:\n  a:\n    needs: b\n    runs-on: ubuntu\n    steps:\n      - run: x\n  b:\n    needs: a\n    runs-on: ubuntu\n    steps:\n      - run: y\n")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), "环") {
		t.Fatalf("应报 needs 成环: %v", errs)
	}
}

func TestExpectJobs(t *testing.T) {
	_, jobs := checkExpect(t, goodWF, "build,test")
	if len(jobs) != 2 {
		t.Fatalf("应有 2 个 job")
	}
	errs, _ := checkExpect(t, goodWF, "build,不存在的job")
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "缺少预期 job: 不存在的job") {
		t.Fatalf("应报缺少预期 job: %v", errs)
	}
}

func TestNoJobs(t *testing.T) {
	errs, _ := check(t, "name: 只有名字\n")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "; "), "没有 jobs") {
		t.Fatalf("应报没有 jobs: %v", errs)
	}
}
