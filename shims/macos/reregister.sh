#!/bin/sh
# 在不注销重登的前提下"尽力"让系统登记并启用本输入法。
#
# ⚠️ 必须在**普通终端**（无沙盒）里运行：沙盒进程既不能写 $TMPDIR/C 的 TIS 缓存，
# 也读不到输入源表，因此只有在无沙盒进程里才能得到权威结果。
# 用法: ./reregister.sh
set -e
cd "$(dirname "$0")"

APP="$HOME/Library/Input Methods/ChaosetIME.app"
ID=ime.chaoset.inputmethod.ChaosetIME
MODE=ime.chaoset.inputmethod.ChaosetIME.Hans

[ -d "$APP" ] || { echo "未装入：请先运行 ./install.sh"; exit 1; }
[ -x build/tis ] || { echo "缺 build/tis：请先运行 ./build.sh"; exit 1; }

printf '== 1) 重新登记 bundle ==\n'
build/tis register "$APP" || true

printf '== 2) 系统输入源表里是否可见 ==\n'
if build/tis list | grep -q "$ID"; then
  printf '  可见 ✓\n'
  build/tis list | grep "$ID" | sed 's/^/   /'
  printf '== 3) 启用并选中 ==\n'
  build/tis enable "$ID" || true
  build/tis enable "$MODE" || true
  build/tis select "$MODE" || true
  killall "System Settings" 2>/dev/null || true
  pkill -9 -f "KeyboardSettings" 2>/dev/null || true
  pkill -9 -f "TextInputSwitcher" 2>/dev/null || true
  pkill -9 -f "TextInputMenuAgent" 2>/dev/null || true
  pkill -9 -f "CursorUIViewService" 2>/dev/null || true
  printf '\n若菜单栏输入法菜单里已出现「Chaoset」即可直接使用；\n'
  printf '若仍看不到，请注销重登一次（系统菜单缓存可能需要重登刷新）。\n'
else
  printf '  不可见 —— 系统尚未把本输入法收进输入源表。\n'
  printf '  此时唯一触发点是【注销并重新登录】（官方 Squirrel 安装包同样要求 logout）。\n'
fi