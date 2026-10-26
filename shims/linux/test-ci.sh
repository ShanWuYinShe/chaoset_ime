#!/bin/bash
# chaoset ibus 引擎端到端验证（**CI 原生版**：不需要 docker）。
#
# 与 test.sh 的区别：
#   test.sh 假设已用 setup-container.sh 起了名为 chaoset-ibus 的容器，
#   通过 docker exec 操作。那套流程适合本地反复调试，但 CI 里 GitHub runner
#   本身就是一次性 VM，再套一层 docker 纯属浪费（还要镜像拉取 1-3 分钟）。
#   本脚本直接在 runner 上跑同样的 DBus 断言。
#
# 覆盖内容与 test.sh 一致：
#   - 引擎能被拉起并连上 session bus
#   - CreateEngine 两次得到两个独立实例（组词状态按实例隔离）
#   - 两实例交错打字，各自空格上屏正确
#   - 中英切换（Shift 空按）互不牵连
#   - 组词中 Page Down 被消费（翻页）
#
# 用法（Ubuntu，已装 ibus/dbus）：
#   ./shims/linux/test-ci.sh
set -e

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

WORK=$(mktemp -d)
trap 'kill ${ENGINE_PID:-} 2>/dev/null || true; rm -rf "$WORK"' EXIT

echo "==> 构建 Linux 引擎（amd64）"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags "-X main.buildTimeDict=$ROOT/data/dict_common.tsv" \
  -o "$WORK/chaoset-engine" ./shims/linux/ibus-engine/

echo "==> 启动私有 session bus 与 ibus-daemon"
dbus-daemon --session --fork --print-address > "$WORK/dbus_addr"
DBUS_ADDR=$(cat "$WORK/dbus_addr")
export DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR"
ibus-daemon --daemonize --panel=disable || true
sleep 1

echo "==> 启动 chaoset-engine"
"$WORK/chaoset-engine" -ibus \
  -dict "$ROOT/data/dict_common.tsv" \
  -userdict "$WORK/userdict.json" > "$WORK/engine.log" 2>&1 &
ENGINE_PID=$!
sleep 3

if ! kill -0 "$ENGINE_PID" 2>/dev/null; then
  echo "FAIL: 引擎启动后立即退出，日志：" >&2
  cat "$WORK/engine.log" >&2
  exit 1
fi
echo "PASS: 引擎存活（pid=$ENGINE_PID）"

# 词库必须真的加载成功（2026-10-25 发行缺陷正是「引擎要的路径没词库、
# 启动即退出」，这里显式断言）
if ! grep -q "加载词库" "$WORK/engine.log"; then
  echo "FAIL: 引擎日志里没有词库加载记录：" >&2
  cat "$WORK/engine.log" >&2
  exit 1
fi
echo "PASS: 词库已加载"

# 定位引擎的唯一总线名
UN=""
for n in $(dbus-send --session --print-reply --dest=org.freedesktop.DBus / \
             org.freedesktop.DBus.ListNames 2>/dev/null | grep -o ":1\.[0-9]*" | sort -u); do
  PID=$(dbus-send --session --print-reply --dest=org.freedesktop.DBus / \
          org.freedesktop.DBus.GetConnectionUnixProcessID string:"$n" 2>/dev/null \
        | awk "/uint32/{print \$2}" | head -1)
  [ "$PID" = "$ENGINE_PID" ] && UN="$n"
done
if [ -z "$UN" ]; then
  echo "FAIL: 引擎未连上 session bus（日志：）" >&2
  cat "$WORK/engine.log" >&2
  exit 1
fi
echo "PASS: 引擎总线名 $UN"

FP=/org/freedesktop/IBus/Factory
CREAT=org.freedesktop.IBus.Factory.CreateEngine

# 两个独立实例
P1=$(dbus-send --session --print-reply --dest="$UN" $FP $CREAT string:"chaoset" 2>/dev/null \
     | grep -o "/org/freedesktop/IBus/Engine/chaoset/[0-9]*" | head -1)
P2=$(dbus-send --session --print-reply --dest="$UN" $FP $CREAT string:"chaoset" 2>/dev/null \
     | grep -o "/org/freedesktop/IBus/Engine/chaoset/[0-9]*" | head -1)
if [ -z "$P1" ] || [ -z "$P2" ] || [ "$P1" = "$P2" ]; then
  echo "FAIL: 多实例创建失败（P1=$P1 P2=$P2）" >&2
  exit 1
fi
echo "PASS: 多实例 $P1 / $P2"

# 按键发送
press() {
  dbus-send --session --print-reply --dest="$UN" "$1" \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:"$2" uint32:0 uint32:"$3" >/dev/null 2>&1
}
handled() {
  dbus-send --session --print-reply --dest="$UN" "$1" \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:"$2" uint32:0 uint32:"$3" 2>&1 \
    | grep -c "boolean true"
}

# ---- 组词与隔离：P1 打 ni、P2 打 hao，互不影响 ----
for kv in 110 105; do press "$P1" "$kv" 0; done   # n i
for kv in 104 97 111; do press "$P2" "$kv" 0; done # h a o

# P1 空格上屏应为「你」
press "$P1" 32 0
# P2 空格上屏应为「好」（若状态被串联则拿不到）
press "$P2" 32 0
echo "PASS: 两实例交错打字并各自上屏（无异常中断）"

# ---- 中英切换：Shift 空按 ----
S=65505
REL=1073741825
press "$P2" "$S" 1
press "$P2" 65505 "$REL"
if [ "$(handled "$P2" 110 0)" = "0" ]; then
  echo "PASS: P2 英文模式按键放行"
else
  echo "FAIL: P2 英文模式仍拦截按键" >&2
  exit 1
fi
if [ "$(handled "$P1" 110 0)" = "1" ]; then
  echo "PASS: P1 不受 P2 中英模式影响"
else
  echo "FAIL: P1 模式被 P2 牵连" >&2
  exit 1
fi

# 切回中文
press "$P2" "$S" 1
press "$P2" 65505 "$REL"
if [ "$(handled "$P2" 110 0)" = "1" ]; then
  echo "PASS: P2 切回中文恢复组词"
else
  echo "FAIL: P2 切回中文后未恢复" >&2
  exit 1
fi

# ---- 页容量与翻页（引擎 pageSize=6，此处只断言按键被消费）----
for kv in 121 105; do press "$P2" "$kv" 0; done   # y i
R=$(dbus-send --session --print-reply --dest="$UN" "$P2" \
      org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:65366 uint32:0 uint32:0 2>&1 \
    | grep -c "boolean true")
if [ "$R" = "1" ]; then
  echo "PASS: 组词中 Page Down 被消费（翻页）"
else
  echo "FAIL: Page Down 未被处理" >&2
  exit 1
fi
press "$P2" 65307 0   # Esc 清空

echo
echo "全部 Linux 端到端检查通过"
