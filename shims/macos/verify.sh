#!/bin/sh
# ChaosetIME（macOS 壳）一键自检：装入状态 / TIS 枚举 / daemon / IPC 全链路。
# 用法: ./verify.sh
#
# 关键背景：macOS 的输入源表在**登录时**才扩充新输入法（官方 Squirrel 安装包同样
# 声明 postinstall-action="logout"）。装入后未注销重登，第 2 步会一直是"尚未收录"。
set -e
cd "$(dirname "$0")"

IM="$HOME/Library/Input Methods/ChaosetIME.app"
SUPPORT="$HOME/Library/Application Support/chaoset"
SOCK="$SUPPORT/chaoset.sock"

printf '== 1) 输入法装入 ==\n'
if [ -d "$IM" ]; then
  printf '   OK: %s\n' "$IM"
  plutil -p "$IM/Contents/Info.plist" 2>/dev/null | grep -E '"TISInputSourceID"|"CFBundleIdentifier"' | sed 's/^/   /'
else
  printf '   未装入：先运行 ./install.sh\n'
fi

printf '== 2) TIS 是否收录 ==\n'
if [ -x build/tis ]; then
  if build/tis list 2>/dev/null | grep -q 'ime.chaoset.inputmethod'; then
    printf '   OK: 系统已收录\n'
    build/tis list 2>/dev/null | grep -i chaoset | sed 's/^/   /'
  else
    printf '   尚未收录 —— 若刚装入，请【注销并重新登录】后重跑本脚本\n'
  fi
else
  printf '   缺 build/tis（先运行 ./build.sh）\n'
fi

printf '== 2b) 重复条目 / 旧模式巡检 ==\n'
if [ -x build/tis ]; then
  COUNT=$(build/tis list 2>/dev/null | grep -c 'ime.chaoset.inputmethod' || true)
  if [ "$COUNT" -eq 2 ]; then
    printf '   OK: 恰好 2 条（父容器 + Pinyin 模式），系统设置「添加输入法」不会重复\n'
  elif [ "$COUNT" -gt 2 ]; then
    printf '   ⚠ 发现 %s 条 Chaoset 条目（预期 2）——系统设置/菜单栏会出现重复条目。\n' "$COUNT"
    printf '     处理：普通终端运行 ./reregister.sh；仍重复则注销重登一次重建输入源表。\n'
  elif [ "$COUNT" -eq 1 ]; then
    printf '   ⚠ 只有 1 条（预期 2：父容器 + Pinyin 模式），Pinyin 模式可能尚未收录。\n'
  else
    printf '   未发现 Chaoset 条目（尚未登记/收录）。\n'
  fi
  if build/tis list 2>/dev/null | grep -q 'ime.chaoset.inputmethod.ChaosetIME.Hans'; then
    printf '   ⚠ 发现历史模式 .Hans 残留（会与 .Pinyin 并存成重复条目），请运行 ./reregister.sh 清理。\n'
  else
    printf '   OK: 无 .Hans 旧模式残留\n'
  fi
else
  printf '   跳过（缺 build/tis）\n'
fi

printf '== 2c) 输入源图标实际渲染尺寸（系统设置/菜单栏取的就是这个尺寸） ==\n'
if [ -x build/icon-size ]; then
  build/icon-size 2>/dev/null | sed 's/^/   /'
else
  printf '   （缺 build/icon-size，先运行 ./build.sh）\n'
fi

printf '== 3) daemon 套接字 ==\n'
if [ -S "$SOCK" ]; then
  printf '   OK: %s\n' "$SOCK"
else
  printf '   未发现 %s（daemon 未运行；./install.sh 会部署并启动）\n' "$SOCK"
fi

printf '== 4) IPC 全链路（组词/预编辑/上屏/歧义切分/退格/重置） ==\n'
if [ -x build/smoke ] && [ -S "$SOCK" ]; then
  build/smoke "$SOCK" | tail -1 | sed 's/^/   /'
else
  printf '   跳过（缺 build/smoke 或 socket）\n'
fi

printf '\n下一步：系统设置 → 键盘 → 输入法 → 添加「Chaoset」\n'