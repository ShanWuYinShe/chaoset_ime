#!/bin/bash
# chaoset ibus 原生引擎端到端验证（在 setup-container.sh 建好的容器内运行）。
# 流程：重启原生引擎（首键自动拉起 chaosetd）→ 固定总线名 → CreateEngine 创建两个独立实例 →
#       两实例交错打字（P1=ni、P2=hao）→ 各自空格上屏 →
#       dbus-monitor 验证：两路径各自 CommitText（你好 / 好），
#       证明组词状态按实例分离；另覆盖候选表/中英切换/翻页。
set -e
NAME=${NAME:-chaoset-ibus}

docker exec -d $NAME bash -c '
export DBUS_SESSION_BUS_ADDRESS=$(cat /tmp/dbus_addr)
export DISPLAY=:99
export IBUS_ADDRESS=$(ibus address 2>/dev/null || true)
pkill -x chaoset-engine 2>/dev/null || true
pkill -x chaosetd 2>/dev/null || true
rm -f /tmp/chaoset.sock /tmp/chaoset.sock.lock
sleep 0.5
nohup /usr/local/bin/chaoset-engine \
  -ibus -daemon /usr/local/bin/chaosetd \
  -socket /tmp/chaoset.sock -log /tmp/chaosetd.log \
  -dict /usr/local/share/chaoset/dict.tsv \
  -userdict /tmp/chaoset-userdict.json \
  > /tmp/engine_launch.log 2>&1 &
'

sleep 4
docker exec $NAME bash -c '
export DBUS_SESSION_BUS_ADDRESS=$(cat /tmp/dbus_addr)
export DISPLAY=:99
IBUS_ADDR=$(ibus address 2>/dev/null || true)
[ -n "$IBUS_ADDR" ] && export IBUS_ADDRESS="$IBUS_ADDR" && export DBUS_SESSION_BUS_ADDRESS="$IBUS_ADDR"
EPID=$(pgrep -x chaoset-engine | sort -n | tail -1)
echo "引擎 pid: $EPID"
UN=org.freedesktop.IBus.Chaoset
echo "引擎总线名: $UN"

FP=/org/freedesktop/IBus/Factory
CREAT="org.freedesktop.IBus.Factory.CreateEngine"

# ---- 多实例：CreateEngine 两次 → 两个独立对象路径 ----
P1=$(dbus-send --session --print-reply --dest=$UN $FP $CREAT string:"chaoset" 2>/dev/null | grep -o "/org/freedesktop/IBus/Engine/[0-9]*" | head -1)
P2=$(dbus-send --session --print-reply --dest=$UN $FP $CREAT string:"chaoset" 2>/dev/null | grep -o "/org/freedesktop/IBus/Engine/[0-9]*" | head -1)
[ -n "$P1" ] && [ -n "$P2" ] && [ "$P1" != "$P2" ] || { echo "FAIL: 多实例创建失败 ($P1 / $P2)"; exit 1; }
echo "PASS: 多实例 $P1 / $P2"

# 按键发送（实例路径, keyval, state）→ 输出 handled 计数
pk() { dbus-send --session --print-reply --dest=$UN "$1" \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:$2 uint32:0 uint32:$3 2>&1 | grep -c "boolean true"; }

rm -f /tmp/mon.log
dbus-monitor > /tmp/mon.log 2>/dev/null &
MON=$!
sleep 1

# P1 组词 n-i（暂存 ni）；P2 组词 h-a-o（独立会话，互不串扰）
for kv in 110 105; do pk $P1 $kv 0; done
for kv in 104 97 111; do pk $P2 $kv 0; done
sleep 1
# 两实例都应上报候选表（UpdateLookupTable 信号）
if grep -q "UpdateLookupTable" /tmp/mon.log && grep -q "IBusLookupTable" /tmp/mon.log; then
  echo "PASS: UpdateLookupTable 候选表信号已发出"
else
  echo "FAIL: 未捕获到候选表信号"; exit 1
fi

# P2 空格：好（来自其独立会话的 hao）
R=$(pk $P2 32 0)
echo "P2 空格 handled=$R"
# P1 空格：你好
R=$(pk $P1 32 0)
echo "P1 空格 handled=$R"
sleep 1
kill $MON 2>/dev/null || true

if grep -q "CommitText" /tmp/mon.log && grep -q "你好" /tmp/mon.log; then
  echo "PASS: P1 CommitText 中文上屏（你好）"
else
  echo "FAIL: 未捕获 P1 中文上屏"; exit 1
fi
# P2 的独立提交：CommitText 载荷为单独的「好」（若状态串扰则会是别的词）
if grep -A 6 "member=CommitText" /tmp/mon.log | grep -q "string \"好\""; then
  echo "PASS: P2 CommitText 中文上屏（好）"
else
  echo "FAIL: 未捕获 P2 中文上屏"; exit 1
fi

# ---- 中英切换：Shift 空按（按下→释放，中间无其他键）----
S=65505            # 0xFFE1 左 Shift
REL=1073741825     # 释放掩码(1<<30) | Shift 掩码(1)
press() { dbus-send --session --print-reply --dest=$UN "$1" \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:$2 uint32:0 uint32:$3 > /dev/null 2>&1; }
key_n() { dbus-send --session --print-reply --dest=$UN "$1" \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:110 uint32:0 uint32:0 2>&1 | grep -c "boolean true"; }

press $P2 $S 1; press $P2 65505 1073741825   # P2 Shift 空按 → 英文模式
R=$(key_n $P2)
if [ "$R" = "0" ]; then
  echo "PASS: P2 英文模式按键放行（handled=0）"
else
  echo "FAIL: P2 英文模式仍拦截按键"; exit 1
fi
R=$(key_n $P1)
if [ "$R" = "1" ]; then
  echo "PASS: P1 不受 P2 模式影响（handled=1）"
else
  echo "FAIL: P1 模式被 P2 牵连"; exit 1
fi

press $P2 $S 1; press $P2 65505 1073741825   # 切回中文
R=$(key_n $P2)
if [ "$R" = "1" ]; then
  echo "PASS: P2 切回中文恢复组词"
else
  echo "FAIL: P2 切回中文后未恢复"; exit 1
fi

# ---- 键盘翻页：Page Down 组词中消费 ----
for kv in 121 105; do press $P2 $kv 0; done   # y i
R=$(dbus-send --session --print-reply --dest=$UN $P2 \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:65366 uint32:0 uint32:0 2>&1 | grep -c "boolean true")
if [ "$R" = "1" ]; then
  echo "PASS: Page Down 组词中消费（翻页）"
else
  echo "FAIL: Page Down 未被处理"; exit 1
fi
press $P2 65307 0                             # Esc 清空，结束'

# daemon 是引擎拉起的独立进程，容器会保留；显式清理，避免下次测试连到旧实例。
docker exec "$NAME" sh -c 'pkill -x chaosetd 2>/dev/null || true'
