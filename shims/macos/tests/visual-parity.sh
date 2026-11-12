#!/bin/sh
# Go 视觉设计基准（skin/skin.go）↔ macOS 原生候选窗（CandidateTheme.swift/CandidatePanelView.swift）
# 的机器一致性检查。
#
# 为什么需要（2026-10-26）：skin/skin.go 与 CandidatePanelView.swift 是同一套
# 视觉的两份独立实现，色值/间距全靠人工手抄同步，没有任何自动断言。审查已实证
# 漂移（分栏线 y、字号）。本脚本把两端的布局常量与色值导出后做差分，让漂移在
# CI 里暴露，而不是等用户肉眼看出候选窗「两个平台长得不一样」。
#
# 覆盖（2026-10-30 加固，三端一致性审计 G2/G3/G8）：
#   A. 导出两端度量（Go: tools/render_metrics.go；Swift: tools/panel_metrics.swift）
#   B. 布局常量 Go↔Swift 5 项（fontSize/padding/lineSpacing/maxWidth/cornerRadius）
#   C. macOS 参考常量自检：Layout 其余 13 项 + divider height/y 的期望值
#      （期望值来源 = docs/ui-parity.md 排版参数表；macOS 是视觉参考，只改一端
#      的静默漂移必须在这里 FAIL。divider 导出此前无人消费，C 段是它的消费者）
#   D. 色值 Go↔Swift 逐色比对：浅色 + 深色各 13 项（此前 Swift 端色值从未
#      导出/参与比对，D 段只自证 Go 侧 4 个浅色）
#   E. 折叠区双箭头与页内预选高亮契约（源码级，issue#2）：首页只有下箭头、
#      末页只有上箭头、中间页两枚都有；两枚箭头命中区分开（-2 上 / -1 下）；
#      高亮 pill 跟随页内预选。macOS 是视觉参考实现，语义被删会让 Windows
#      端静态契约失去对照。
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

note "== A. 导出 Swift 端度量 =="
# 2026-11-06：原 skin/ 包（Go 侧「设计基准」）已删除——它在产品里零调用，
# 只在 macOS 测试里当对照物，容易让人误以为 Go 是跨端视觉真源。
# 基准值改为**内联在本脚本**（见下方 PY 段），macOS 是视觉参考实现，
# 断言方向变成「Swift 实现 ↔ 文档化期望值」，不再需要 Go 侧导出。

# macOS 的 CI runner 是 arm64，本地开发机也可能有 Intel；target 按 uname 选择，
# 避免把 x86_64 机器上写死的 arm64 target 当成「唯一正确」。
case "$(uname -m)" in
  arm64) SWIFT_TARGET=arm64-apple-macos15.0 ;;
  *)     SWIFT_TARGET=x86_64-apple-macos15.0 ;;
esac
# 2026-11-06 架构拆分：候选窗拆成 CandidateTheme（色值/布局常量/齿轮参数）+
# CandidatePanelView（视图实现）；panel_metrics 只需这两个文件。
if ! swiftc -O -module-cache-path build/modulecache \
      -target "$SWIFT_TARGET" -framework AppKit \
      src/CandidateTheme.swift src/CandidatePanelView.swift \
      tools/panel_metrics.swift -o "$OUT/panel_metrics" 2>"$OUT/swift.err"; then
  note "FAIL 无法构建 Swift 度量工具："
  cat "$OUT/swift.err" | sed 's/^/     /' | head -20
  exit 1
fi
"$OUT/panel_metrics" > "$OUT/swift.json" 2>>"$OUT/swift.err"
note "ok   Swift 端度量已导出"

# 用 python 做差分（jq 不一定可用）
python3 - "$OUT/swift.json" <<'PY'
import json, sys

sw = json.load(open(sys.argv[1]))

# Go 侧「设计基准」已内联至此（原 skin/skin.go，2026-11-06 删除该包）。
# macOS 是视觉参考实现：这些期望值即三端对齐的文档化基准，
# 改动必须同时更新这里 + docs/ui-parity.md + 另外两端。
GO_LAYOUT = {
    "fontSize": 15,
    "padding": 7,
    "lineSpacing": 4,
    "maxWidth": 720,
    "cornerRadius": 8,
}
# 浅/深各 13 色（ARGB 十六进制，原 skin.DefaultSkinFor 的 NRGBA 值）。
GO_COLORS = {
    "panelBg": "#F6F6F8F6", "panelBorder": "#0000001A", "text": "#1D1D1FFF",
    "highlightBg": "#007AFFFF", "highlightText": "#FFFFFFFF",
    "chipBg": "#FFFFFFFF", "chipBorder": "#0000003C", "chipDigit": "#1D1D1FFF",
    "highlightChipBg": "#FFFFFF45", "pageColor": "#8E8E93D0",
    "userMark": "#34C759FF", "divider": "#00000014", "chevron": "#8E8E93D0",
}
GO_COLORS_DARK = {
    "panelBg": "#2C2C2EF5", "panelBorder": "#FFFFFF24", "text": "#FFFFFFFF",
    "highlightBg": "#0066EBFF", "highlightText": "#FFFFFFFF",
    "chipBg": "#5A5A5EFF", "chipBorder": "#FFFFFF40", "chipDigit": "#FFFFFFFF",
    "highlightChipBg": "#FFFFFF45", "pageColor": "#98989FB8",
    "userMark": "#30D158FF", "divider": "#FFFFFF1E", "chevron": "#98989FB8",
}

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
    cmp(f"skin.{k}", GO_LAYOUT[k], sw["skin"][k])

print()
print("== C. macOS 参考常量自检（期望值见 docs/ui-parity.md 排版参数表）==")
# macOS 是视觉参考：这些值被误改会直接改变三端对齐基准，因此单独锁死。
# 改动 macOS 视觉参数 = 同时改这里 + docs/ui-parity.md + 另外两端。
layout_expect = {
    # 2026-10-30 审计 P2-9：lineSpacing/maxWidth/panelRadius 此前只在 B 段做
    # Go↔Swift 互比——两端「同步改错」不会被任何检查发现。加进 C 段锁定 macOS
    # 参考值（改动必须同时更新这里 + docs/ui-parity.md + 另外两端）。
    "lineSpacing": 4,
    "maxWidth": 720,
    "cornerRadius": 8,  # Swift 导出名（对应 Layout.panelRadius）
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
        g = (GO_COLORS if section == "colors" else GO_COLORS_DARK).get(k)
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

note ""
note "== E. 折叠区双箭头与预选高亮契约（源码级，issue#2）=="
# 期望值来源：docs/ui-parity.md「折叠区几何」+ issue#2 用户要求。
# 多页：首页只画下箭头、末页只画上箭头、中间页两枚都画；两枚箭头命中区分开
# （-2 翻上一页 / -1 翻下一页）；高亮 pill 跟随页内预选（不再固定第一项）。
# 单页没有翻页动作：两枚置灰且不注册命中区。
VIEW=src/CandidatePanelView.swift
CTRL=src/CandidatePanel.swift
fail_e=0
expect_src() {
  if grep -qF -- "$2" "$1"; then
    note "ok   $3"
  else
    note "FAIL $3（$1 缺少：$2）"
    fail_e=1
  fi
}
expect_src "$VIEW" "if hasPrev {" "上箭头绘制分支"
expect_src "$VIEW" "color: theme.chevron, dirDown: false" "多页上箭头（⌃）"
expect_src "$VIEW" "color: theme.chevron, dirDown: true" "多页下箭头（⌄）"
expect_src "$VIEW" "height: half), -2" "上箭头独立命中区（-2 翻上一页）"
expect_src "$VIEW" "height: blockH - half), -1" "下箭头独立命中区（-1 翻下一页）"
expect_src "$VIEW" "frame.index == selectedIndex" "高亮 pill 跟随页内预选"
expect_src "$CTRL" "static var selectedIndex" "候选面板页内预选状态"
expect_src "$CTRL" "static func moveSelection" "页内预选移动 API"
if [ "$fail_e" -ne 0 ]; then
  note "折叠区/预选契约检查失败"
  exit 1
fi
note "折叠区双箭头与预选高亮契约通过"
