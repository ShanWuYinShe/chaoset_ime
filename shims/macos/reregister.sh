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
MODE=ime.chaoset.inputmethod.ChaosetIME.Pinyin
# 历史版本用过的旧模式 ID：必须禁用，否则状态栏/切换器里会出现重复条目
LEGACY_MODE=ime.chaoset.inputmethod.ChaosetIME.Hans

[ -d "$APP" ] || { echo "未装入：请先运行 ./install.sh"; exit 1; }
[ -x build/tis ] || { echo "缺 build/tis：请先运行 ./build.sh"; exit 1; }

# TIS 输入源缓存每进程一份；只重启进程不删缓存会留下旧图标/旧条目
TIS_CACHE_DIR="$(dirname "${TMPDIR:-/tmp}")/C"
clear_tis_caches() {
  find /private/var/folders -name "com.apple.IntlDataCache*" -delete 2>/dev/null || true
  [ -d "$TIS_CACHE_DIR" ] && find "$TIS_CACHE_DIR" -name "com.apple.IntlDataCache*" -delete 2>/dev/null || true
}
kill_input_source_ui() {
  killall "System Settings" 2>/dev/null || true
  pkill -9 -f "KeyboardSettings" 2>/dev/null || true
  pkill -9 -f "com.apple.Keyboard-Settings" 2>/dev/null || true
  pkill -9 -f "TextInputSwitcher" 2>/dev/null || true
  pkill -9 -f "TextInputMenuAgent" 2>/dev/null || true
  pkill -9 -f "CursorUIViewService" 2>/dev/null || true
  LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
  "$LSREGISTER" -u "/private/tmp/ChaosetIME.app" 2>/dev/null || true
  "$LSREGISTER" -u "/private/tmp/ChaosetIME.app/ChaosetIME.app" 2>/dev/null || true
  rm -rf /private/tmp/ChaosetIME.app 2>/dev/null || true
}

printf '== 1) 重新登记 bundle ==\n'
if build/tis list 2>/dev/null | grep -q "$ID"; then
  printf '  系统表已有登记，跳过重复注册\n'
else
  build/tis register "$APP" || true
fi

printf '== 2) 系统输入源表里是否可见 ==\n'
if build/tis list | grep -q "$ID"; then
  printf '  可见 ✓\n'
  build/tis list | grep "$ID" | sed 's/^/   /'
  printf '== 3) 清理旧模式、启用并选中唯一 Pinyin 模式 ==\n'
  build/tis disable "$LEGACY_MODE" 2>/dev/null || true
  build/tis enable "$ID" || true
  build/tis enable "$MODE" || true
  build/tis select "$MODE" || true
  build/tis sync || true
  pkill -9 -f "ChaosetIME" 2>/dev/null || true
  kill_input_source_ui
  clear_tis_caches
  COUNT=$(build/tis list 2>/dev/null | grep -c "ime.chaoset.inputmethod" || true)
  printf '  TIS 表内 Chaoset 条目数：%s（预期 2：父容器 + Pinyin 模式）\n' "$COUNT"
  if [ "$COUNT" -gt 2 ]; then
    printf '  ⚠ 多于 2 条说明有历史残留，系统设置「添加输入法」里可能出现重复条目；\n'
    printf '    请注销重登一次，让登录扫描重建输入源表。\n'
  fi
  printf '\n若菜单栏输入法菜单里已出现「Chaoset」即可直接使用；\n'
  printf '若仍看不到，请注销重登一次（系统菜单缓存可能需要重登刷新）。\n'
else
  printf '  不可见 —— 系统尚未把本输入法收进输入源表。\n'
  printf '  此时唯一触发点是【注销并重新登录】（官方 Squirrel 安装包同样要求 logout）。\n'
fi
