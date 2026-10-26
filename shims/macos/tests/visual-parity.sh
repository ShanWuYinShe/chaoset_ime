#!/bin/sh
# 双端候选窗视觉一致性对比：Go 贴图端（Windows/Linux）vs macOS 原生端。
#
# 为什么需要（2026-10-26）：renderer/renderer.go 与 CandidatePanel.swift 是同一套
# 视觉的两份独立实现，色值/间距全靠人工手抄同步，没有任何自动断言。审查已实证
# 漂移（分栏线 y、字号）。本脚本把两端的布局常量与固定场景渲染尺寸导出后做差分，
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
print("== C. 固定场景渲染尺寸 ==")
# 宽度允许 1-2px 差异：两端字体度量（NSFont vs golang.org/x/image）本就不同，
# 像素级完全一致不现实。这里只断言「量级一致」，防止布局参数改坏导致的数百 px 偏差。
gw, sw_w = go["scene"]["width"], sw["scene"]["width"]
if abs(gw - sw_w) <= 4:
    print(f"ok   scene.width: Go={gw} Swift={sw_w}（差 {abs(gw-sw_w)}px，阈值 4）")
else:
    fails.append(("scene.width", gw, sw_w))
    print(f"FAIL scene.width: Go={gw} Swift={sw_w} 差 {abs(gw-sw_w)}px 超过阈值 4")

gh, sh_h = go["scene"]["height"], sw["scene"]["height"]
# 高度差异来自行高算法：Go 用字体 metrics，Swift 用 ceil(fontSize*1.4)。
# 允许 8px 内差异（一行高度量级），超出说明布局逻辑被改坏。
if abs(gh - sh_h) <= 8:
    print(f"ok   scene.height: Go={gh} Swift={sh_h}（差 {abs(gh-sh_h)}px，阈值 8）")
else:
    fails.append(("scene.height", gh, sh_h))
    print(f"FAIL scene.height: Go={gh} Swift={sh_h} 差 {abs(gh-sh_h)}px 超过阈值 8")

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
