#!/bin/bash
# chaoset ibus 引擎端到端验证（在 setup-container.sh 建好的容器内运行）。
# 流程：重启引擎 → 动态定位唯一名 → 发拼音按键序列 → 空格上屏 →
#       dbus-monitor 抓取 CommitText 信号，验证中文上屏。
set -e
NAME=${NAME:-chaoset-ibus}

docker exec -d $NAME bash -c '
export DBUS_SESSION_BUS_ADDRESS=$(cat /tmp/dbus_addr)
pkill -x chaoset-engine 2>/dev/null || true
sleep 0.5
python3 - <<"EOF" > /tmp/engine_launch.log 2>&1 &
import subprocess, time, os
addr = open("/tmp/dbus_addr").read().strip()
env = dict(os.environ, DBUS_SESSION_BUS_ADDRESS=addr)
# 常驻启动引擎
subprocess.Popen(["/usr/local/bin/chaoset-engine",
                  "-dict", "/usr/local/share/chaoset/dict.tsv",
                  "-userdict", "/tmp/chaoset-userdict.json"], env=env)
time.sleep(2)
EOF
'

sleep 4
docker exec $NAME bash -c '
export DBUS_SESSION_BUS_ADDRESS=$(cat /tmp/dbus_addr)
EPID=$(pgrep -x chaoset-engine | sort -n | tail -1)
echo "引擎 pid: $EPID"
UN=""
for n in $(dbus-send --session --print-reply --dest=org.freedesktop.DBus / org.freedesktop.DBus.ListNames 2>/dev/null | grep -o ":1\.[0-9]*" | sort -u); do
  PID=$(dbus-send --session --print-reply --dest=org.freedesktop.DBus / org.freedesktop.DBus.GetConnectionUnixProcessID string:$n 2>/dev/null | awk "/uint32/{print \$2}" | head -1)
  [ "$PID" = "$EPID" ] && UN=$n
done
echo "引擎唯一名: $UN"
[ -z "$UN" ] && { echo "FAIL: 引擎无总线连接"; exit 1; }
rm -f /tmp/mon.log
dbus-monitor > /tmp/mon.log 2>/dev/null &
MON=$!
sleep 1
# 拼音 n-i-h-a-o（keyval = 小写字母 ASCII）
for kv in 110 105 104 97 111; do
  R=$(dbus-send --session --print-reply --dest=$UN /org/freedesktop/IBus/Engine/chaoset \
      org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:$kv uint32:0 uint32:0 2>&1 | grep -c "boolean true")
  echo "键 $kv handled=$R"
done
sleep 1
# 组词中应已上报候选表（UpdateLookupTable 信号携带 IBusLookupTable 元组）
if grep -q "UpdateLookupTable" /tmp/mon.log && grep -q "IBusLookupTable" /tmp/mon.log; then
  echo "PASS: UpdateLookupTable 候选表信号已发出"
else
  echo "FAIL: 未捕获到候选表信号"; exit 1
fi
# 空格：首选候选上屏
R=$(dbus-send --session --print-reply --dest=$UN /org/freedesktop/IBus/Engine/chaoset \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:32 uint32:0 uint32:0 2>&1 | grep -c "boolean true")
echo "空格 handled=$R"
sleep 1
kill $MON 2>/dev/null || true
if grep -q "CommitText" /tmp/mon.log && grep -q "你好" /tmp/mon.log; then
  echo "PASS: CommitText 信号已发出且包含中文上屏"
else
  echo "FAIL: 未捕获到中文上屏信号"; exit 1
fi

# ---- 中英切换：Shift 空按（按下→释放，中间无其他键）----
S=65505            # 0xFFE1 左 Shift
REL=1073741825     # 释放掩码(1<<30) | Shift 掩码(1)
press() { dbus-send --session --print-reply --dest=$UN /org/freedesktop/IBus/Engine/chaoset \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:$1 uint32:0 uint32:$2 > /dev/null 2>&1; }
key_n() { dbus-send --session --print-reply --dest=$UN /org/freedesktop/IBus/Engine/chaoset \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:110 uint32:0 uint32:0 2>&1 | grep -c "boolean true"; }

press $S 1; press $S $REL      # Shift 空按 → 切到英文
R=$(key_n)
if [ "$R" = "0" ]; then
  echo "PASS: 英文模式按键放行（handled=0）"
else
  echo "FAIL: 英文模式仍拦截按键"; exit 1
fi

press $S 1; press $S $REL      # 再切回中文
R=$(key_n)
if [ "$R" = "1" ]; then
  echo "PASS: 切回中文后恢复组词（handled=1）"
else
  echo "FAIL: 切回中文后按键未恢复"; exit 1
fi

# ---- 键盘翻页：Page Down 组词中消费（yi 共 12 候选 → 2 页）----
for kv in 121 105; do press $kv 0; done   # y i
R=$(dbus-send --session --print-reply --dest=$UN /org/freedesktop/IBus/Engine/chaoset \
    org.freedesktop.IBus.Engine.ProcessKeyEvent uint32:65366 uint32:0 uint32:0 2>&1 | grep -c "boolean true")
if [ "$R" = "1" ]; then
  echo "PASS: Page Down 组词中消费（翻页）"
else
  echo "FAIL: Page Down 未被处理"; exit 1
fi
press 65307 0                              # Esc 清空，结束'
