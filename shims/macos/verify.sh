#!/bin/sh
# ChaosetIME（macOS 壳）一键自检：装入状态 / TIS 枚举 / daemon / IPC 全链路。
# 用法: ./verify.sh
#
# 退出码：0 = 全部关键项通过；1 = 有失败项（供 CI / 用户脚本判定）。
#
# 为什么要有退出码（2026-10-26 修复）：本脚本此前全篇 printf、从不 exit 1，
# 即使输入法没装入、daemon 没跑、IPC 全挂也返回 0。CI 与用户脚本据此判断
# 「装好了」会得到完全错误的结论——自检必须能失败。
#
# 关键背景：macOS 的输入源表在**登录时**才扩充新输入法（官方 Squirrel 安装包同样
# 声明 postinstall-action="logout"）。装入后未注销重登，第 2 步会一直是"尚未收录"。
set -e
cd "$(dirname "$0")"

IM="$HOME/Library/Input Methods/ChaosetIME.app"
SUPPORT="$HOME/Library/Application Support/chaoset"
SOCK="$SUPPORT/chaoset.sock"

# 失败计数：ok/warn/fail 三类，只有 fail 影响退出码。
fails=0
warns=0
fail() { fails=$((fails + 1)); printf '   ✗ %s\n' "$*"; }
warn() { warns=$((warns + 1)); printf '   ⚠ %s\n' "$*"; }
ok()   { printf '   OK: %s\n' "$*"; }

# ── 1) 输入法装入 ───────────────────────────────────────────────
printf '== 1) 输入法装入 ==\n'
if [ -d "$IM" ]; then
  ok "$IM"
  plutil -p "$IM/Contents/Info.plist" 2>/dev/null | grep -E '"TISInputSourceID"|"CFBundleIdentifier"' | sed 's/^/   /'
else
  fail "未装入 $IM（先运行 ./install.sh）"
fi

# ── 2) TIS 收录 ─────────────────────────────────────────────────
printf '== 2) TIS 是否收录 ==\n'
if [ -x build/tis ]; then
  if build/tis list 2>/dev/null | grep -q 'ime.chaoset.inputmethod'; then
    ok "系统已收录"
    build/tis list 2>/dev/null | grep -i chaoset | sed 's/^/   /'
  else
    fail "尚未收录——若刚装入，请【注销并重新登录】后重跑本脚本"
  fi
else
  fail "缺 build/tis（先运行 ./build.sh）"
fi

# ── 2b) 重复条目 / 旧模式巡检 ───────────────────────────────────
printf '== 2b) 重复条目 / 旧模式巡检 ==\n'
if [ -x build/tis ]; then
  COUNT=$(build/tis list 2>/dev/null | grep -c 'ime.chaoset.inputmethod' || true)
  if [ "$COUNT" -eq 2 ]; then
    ok "恰好 2 条（父容器 + Pinyin 模式），系统设置「添加输入法」不会重复"
  elif [ "$COUNT" -gt 2 ]; then
    fail "发现 $COUNT 条 Chaoset 条目（预期 2）——菜单栏/系统设置会显示重复。修复：./build/tis dedupe"
  elif [ "$COUNT" -eq 1 ]; then
    warn "只有 1 条（预期 2：父容器 + Pinyin 模式），Pinyin 模式可能尚未收录"
  else
    fail "未发现 Chaoset 条目（尚未登记/收录）"
  fi
  if build/tis list 2>/dev/null | grep -q 'ime.chaoset.inputmethod.ChaosetIME.Hans'; then
    warn "发现历史模式 .Hans 残留（会与 .Pinyin 并存成重复条目），请运行 ./reregister.sh"
  else
    ok "无 .Hans 旧模式残留"
  fi
else
  printf '   跳过（缺 build/tis）\n'
fi

# ── 2c) 输入源图标实际渲染尺寸 ──────────────────────────────────
printf '== 2c) 输入源图标实际渲染尺寸（系统设置/菜单栏取的就是这个尺寸） ==\n'
if [ -x build/icon-size ]; then
  build/icon-size 2>/dev/null | sed 's/^/   /'
else
  printf '   （缺 build/icon-size，先运行 ./build.sh）\n'
fi

# ── 3) daemon 套接字 ────────────────────────────────────────────
printf '== 3) daemon 套接字 ==\n'
if [ -S "$SOCK" ]; then
  ok "$SOCK"
else
  fail "未发现 $SOCK（daemon 未运行；./install.sh 会部署并启动）"
fi

# ── 3b) LaunchAgent 自启（daemon 崩了能否自动拉起）──────────────
printf '== 3b) daemon 自启（LaunchAgent）==\n'
if launchctl print "gui/$(id -u)/ime.chaoset.daemon" >/dev/null 2>&1; then
  ok "ime.chaoset.daemon 已注册（KeepAlive，崩溃/重启后自动拉起）"
else
  warn "未注册 LaunchAgent：daemon 崩溃或重启电脑后不会自动拉起（./install.sh 会安装）"
fi

# ── 4) IPC 全链路 ───────────────────────────────────────────────
printf '== 4) IPC 全链路（组词/预编辑/上屏/歧义切分/退格/重置） ==\n'
if [ -x build/smoke ] && [ -S "$SOCK" ]; then
  # 注意：套接字文件存在 ≠ 有进程在 listen（daemon 被 unlink 后路径会残留成
  # 僵尸文件，connect 得到 ECONNREFUSED）。所以以 smoke 的真实往返结果为准。
  if SMOKE_OUT=$(build/smoke "$SOCK" 2>&1); then
    printf '%s\n' "$SMOKE_OUT" | tail -1 | sed 's/^/   /'
    ok "IPC 全链路通过"
  else
    fail "IPC 自检失败：$(printf '%s' "$SMOKE_OUT" | tail -1)"
  fi
else
  fail "跳过 IPC 自检（缺 build/smoke 或套接字）"
fi

printf '\n'
if [ "$fails" -eq 0 ]; then
  [ "$warns" -gt 0 ] && printf '自检通过（%d 项警告）\n' "$warns"
  [ "$warns" -eq 0 ] && printf '自检全部通过\n'
  printf '下一步：系统设置 → 键盘 → 输入法 → 添加「Chaoset」\n'
  exit 0
fi
printf '自检失败 %d 项\n' "$fails"
exit 1
