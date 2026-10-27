#!/bin/sh
# 双端候选窗视觉一致性对比：Go 视觉真源（skin/skin.go）vs macOS 原生端。
#
# 为什么需要（2026-10-26）：skin/skin.go 与 CandidatePanel.swift 是同一套
# 视觉的两份独立实现，色值/间距全靠人工手抄同步，没有任何自动断言。审查已实证
# 漂移（分栏线 y、字号）。本脚本把两端的布局常量导出后做差分，
# 让漂移在 CI 里暴露，而不是等用户肉眼看出候选窗「两个平台长得不一样」。
#
# 用法: ./tests/visual-parity.sh
# 退出码: 0 = 一致；1 = 有差异
set -e
cd "$(dirname "$0")/.."

ROOT=$(cd ../.. && pwd)
OUT=${TMPDIR:-/tmp}/chaoset-parity.$$
mkdir -p "$OUT"
trap 'rm -rf "$OUT"' EXIT

fail=0
note() { printf '%s\n' "$*"; }
diffitem() { # diffitem <名称> <go 值> <swift 值>
  if [ "$2" = "$3" ]; then
    note "ok   $1: $2"
  else
    fail=$((fail + 1))
    note "FAIL $1: Go=$2  Swift=$3"
  fi
}

note "== A. 导出两端度量 =="
if ! (cd "$ROOT" && go run tools/render_metrics.go) > "$OUT/go.json" 2>"$OUT/go.err"; then
  note "FAIL 无法导出 Go 端度量："
  cat "$OUT/go.err" | sed 's/^/     /'
  exit 1
fi
note "ok   Go 端度量已导出"

if ! swiftc -O -module-cache-path build/modulecache \
      -target arm64-apple-macos15.0 -framework AppKit \
      src/CandidatePanel.swift tools/panel_metrics.swift -o "$OUT/panel_metrics" 2>"$OUT/swift.err"; then
  note "FAIL 无法构建 Swift 度量工具："
  cat "$OUT/swift.err" | sed 's/^/     /' | head -20
  exit 1
fi
"$OUT/panel_metrics" > "$OUT/swift.json" 2>>"$OUT/swift.err"
note "ok   Swift 端度量已导出"

# 用 python 做 JSON 差分（jq 不一定可用）
python3 - "$OUT/go.json" "$OUT/swift.json" <<'PY'
import json, sys

go = json.load(open(sys.argv[1]))
sw = json.load(open(sys.argv[2]))

fails = []
def cmp(name, a, b):
    if a == b:
        print(f"ok   {name}: {a}")
    else:
        fails.append((name, a, b))
        print(f"FAIL {name}: Go={a}  Swift={b}")

print("== B. 布局常量对比 ==")
for k in ("fontSize", "padding", "lineSpacing", "maxWidth", "cornerRadius"):
    cmp(f"skin.{k}", go["skin"][k], sw["skin"][k])

print()
print("== D. 色值对比（浅色皮肤）==")
# Go 侧导出的色值格式 #RRGGBBAA；Swift 侧没有导出色值（NSColor 需在
# CandidateTheme 里暴露），这里先只断言 Go 侧内部一致性：关键色非空且
# 与既有设计一致。色值双端对比待 CandidateTheme 暴露后补上。
expect = {
    "panelBg": "#F6F6F8F6",
    "highlightBg": "#007AFFFF",
    "text": "#1D1D1FFF",
    "chipBg": "#FFFFFFFF",
}
for k, v in expect.items():
    got = go["colors"].get(k)
    if got == v:
        print(f"ok   color.{k}: {got}")
    else:
        fails.append((f"color.{k}", v, got))
        print(f"FAIL color.{k}: 期望 {v} 实际 {got}")

print()
if fails:
    print(f"视觉一致性检查失败 {len(fails)} 项")
    sys.exit(1)
print("视觉一致性检查通过")
PY
