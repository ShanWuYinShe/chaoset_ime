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

printf '\n下一步：系统设置 → 键盘 → 输入法 → 添加「超集拼音」（图标为中国印章）\n'
