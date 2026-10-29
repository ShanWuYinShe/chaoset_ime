#!/bin/sh
# Go 视觉设计基准（skin/skin.go）↔ macOS 原生候选窗（CandidatePanel.swift）
# 的机器一致性检查。
#
# 为什么需要（2026-10-26）：skin/skin.go 与 CandidatePanel.swift 是同一套
# 视觉的两份独立实现，色值/间距全靠人工手抄同步，没有任何自动断言。审查已实证
# 漂移（分栏线 y、字号）。本脚本把两端的布局常量与色值导出后做差分，让漂移在
# CI 里暴露，而不是等用户肉眼看出候选窗「两个平台长得不一样」。
#
# 覆盖（2026-10-30 加固，三端一致性审计 G2/G3/G8）：
#   A. 导出两端度量（Go: tools/render_metrics.go；Swift: tools/panel_metrics.swift）
#   B. 布局常量 Go↔Swift 5 项（fontSize/padding/lineSpacing/maxWidth/cornerRadius）
#   C. macOS 参考常量自检：Layout 其余 12 项 + divider height/y 的期望值
#      （期望值来源 = docs/ui-parity.md 排版参数表；macOS 是视觉参考，只改一端
#      的静默漂移必须在这里 FAIL。divider 导出此前无人消费，C 段是它的消费者）
#   D. 色值 Go↔Swift 逐色比对：浅色 + 深色各 13 项（此前 Swift 端色值从未
#      导出/参与比对，D 段只自证 Go 侧 4 个浅色）
#
# 未覆盖（事实声明，勿在文档里夸大）：Windows 候选窗只有源码契约检查
# （tools/checkgdi 的 ExtCreatePen 非空指针）；Linux 用 ibus 原生面板、无自绘
# 参数。mac/Win 视觉参数的新增/改动仍需人工三端同步 + 更新 docs/ui-parity.md。
#
# 用法: ./tests/visual-parity.sh
# 退出码: 0 = 一致；1 = 有差异
set -e
cd "$(dirname "$0")/.."

ROOT=$(cd ../.. && pwd)
OUT=${TMPDIR:-/tmp}/chaoset-parity.$$
mkdir -p "$OUT" build/modulecache
trap 'rm -rf "$OUT"' EXIT

note() { printf '%s\n' "$*"; }

note "== A. 导出两端度量 =="
if ! (cd "$ROOT" && go run tools/render_metrics.go) > "$OUT/go.json" 2>"$OUT/go.err"; then
  note "FAIL 无法导出 Go 端度量："
  cat "$OUT/go.err" | sed 's/^/     /'
  exit 1
fi
note "ok   Go 端度量已导出"

# macOS 的 CI runner 是 arm64，本地开发机也可能有 Intel；target 按 uname 选择，
# 避免把 x86_64 机器上写死的 arm64 target 当成「唯一正确」。
case "$(uname -m)" in
  arm64) SWIFT_TARGET=arm64-apple-macos15.0 ;;
  *)     SWIFT_TARGET=x86_64-apple-macos15.0 ;;
esac
if ! swiftc -O -module-cache-path build/modulecache \
      -target "$SWIFT_TARGET" -framework AppKit \
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

def expect(name, got, want):
    if got == want:
        print(f"ok   {name}: {got}")
    else:
        fails.append((name, want, got))
        print(f"FAIL {name}: 期望 {want}  Swift={got}")

print("== B. 布局常量对比（Go skin/skin.go ↔ Swift CandidatePanel.Layout）==")
for k in ("fontSize", "padding", "lineSpacing", "maxWidth", "cornerRadius"):
    cmp(f"skin.{k}", go["skin"][k], sw["skin"][k])

print()
print("== C. macOS 参考常量自检（期望值见 docs/ui-parity.md 排版参数表）==")
# macOS 是视觉参考：这些值被误改会直接改变三端对齐基准，因此单独锁死。
# 改动 macOS 视觉参数 = 同时改这里 + docs/ui-parity.md + 另外两端。
layout_expect = {
    "indexFontSize": 12,
    "firstPadLeft": 9,
    "itemPadX": 7,
    "gap": 11,
    "chipPadX": 4,
    "chipPadY": 2,
    "chipRadius": 3,
    "chevronBoxW": 36,
    "gearBoxW": 26,
    "pillMargin": 2,
    "maxRows": 3,
    "textH": 21,
    "minWidth": 84,
}
for k, want in layout_expect.items():
    expect(f"layout.{k}", sw["skin"].get(k), want)

# divider：CandidatePanel.draw 用 textH*55/100 且垂直居中；期望值由
# 排版参数表推导（textH=21 → 11.55；单行面板高 = padding*2+lineH-lineSpacing
# = 14+25-4 = 35 → y=(35-11.55)/2=11.725）。
div = sw.get("divider", {})
if abs(div.get("height", -1) - 11.55) < 1e-9:
    print(f"ok   divider.height: {div.get('height')}")
else:
    fails.append(("divider.height", 11.55, div.get("height")))
    print(f"FAIL divider.height: 期望 11.55  Swift={div.get('height')}")
if abs(div.get("y", -1) - 11.725) < 1e-9:
    print(f"ok   divider.y: {div.get('y')}")
else:
    fails.append(("divider.y", 11.725, div.get("y")))
    print(f"FAIL divider.y: 期望 11.725（单行面板高 35pt 垂直居中）  Swift={div.get('y')}")

print()
print("== D. 色值对比（浅色 + 深色，Go skin ↔ Swift CandidateTheme）==")
color_keys = [
    "panelBg", "panelBorder", "text", "highlightBg", "highlightText",
    "chipBg", "chipBorder", "chipDigit", "highlightChipBg", "pageColor",
    "userMark", "divider", "chevron",
]
for section, label in (("colors", "浅色"), ("colorsDark", "深色")):
    print(f"-- {label}")
    for k in color_keys:
        g = go.get(section, {}).get(k)
        s = sw.get(section, {}).get(k)
        if g is None or s is None:
            fails.append((f"{section}.{k}", g, s))
            print(f"FAIL {section}.{k}: 缺失 Go={g}  Swift={s}")
        else:
            cmp(f"{section}.{k}", g, s)

print()
if fails:
    print(f"视觉一致性检查失败 {len(fails)} 项")
    sys.exit(1)
print("视觉一致性检查通过")
PY
