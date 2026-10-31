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
#   - Alt+Backspace 按音节回退 / Ctrl+Backspace 整段清空 / Alt+U 放行
#     （2026-10-31 三端统一后的真实 ibus 路由回归）
#
# 用法（Ubuntu，已装 ibus/dbus）：
#   ./shims/linux/test-ci.sh
set -e

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

WORK=$(mktemp -d)
# 清理：引擎收到 TERM 后还会同步落盘 userdict.json（延迟落盘），必须 wait 到
# 进程真正退出再删临时目录；否则删除与落盘并发会让 rm -rf 报
# "Directory not empty"（2026-10-31 CI linux-e2e 偶发失败的真实原因）。
# 删除本身再加短暂重试兜底（文件系统延迟释放）。
cleanup() {
  kill "${ENGINE_PID:-}" 2>/dev/null || true
  wait "${ENGINE_PID:-}" 2>/dev/null || true
  for _ in 1 2 3 4 5; do
    rm -rf "$WORK" 2>/dev/null && break
    sleep 0.2
  done
}
trap cleanup EXIT

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

# 用户导入词库（Linux 设置窗「导入词库」写入 XDG 数据目录；这里显式传
# -imported-dict 模拟，验证引擎启动时会与主词库一起加载）。
printf '测试词\tce shi ci\t99\n' > "$WORK/imported.tsv"

echo "==> 启动 chaoset-engine"
"$WORK/chaoset-engine" -ibus \
  -dict "$ROOT/data/dict_common.tsv" \
  -imported-dict "$WORK/imported.tsv" \
  -userdict "$WORK/userdict.json" > "$WORK/engine.log" 2>&1 &
ENGINE_PID=$!
sleep 3

if ! kill -0 "$ENGINE_PID" 2>/dev/null; then
  echo "FAIL: 引擎启动后立即退出，日志：" >&2
  cat "$WORK/engine.log" >&2
  exit 1
fi
echo "PASS: 引擎存活（pid=${ENGINE_PID}）"

# 词库必须真的加载成功（2026-10-25 发行缺陷正是「引擎要的路径没词库、
# 启动即退出」，这里显式断言）
if ! grep -q "加载词库" "$WORK/engine.log"; then
  echo "FAIL: 引擎日志里没有词库加载记录：" >&2
  cat "$WORK/engine.log" >&2
  exit 1
fi
echo "PASS: 词库已加载"
if ! grep -q "用户导入词库: $WORK/imported.tsv" "$WORK/engine.log"; then
  echo "FAIL: 引擎未加载用户导入词库：" >&2
  cat "$WORK/engine.log" >&2
  exit 1
fi
echo "PASS: 用户导入词库已加载（$WORK/imported.tsv）"

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
  echo "FAIL: 多实例创建失败（P1=${P1} P2=${P2}）" >&2
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

# ---- Alt+Backspace 按音节回退（三端统一，2026-10-31）----
# ibus 里 Ctrl=4、Mod1(Alt)=8。组词 "nihao"（ni+hao）：两次 Alt+Backspace
# 应各回退一个音节（第二次删空），第三次非组词态放行给宿主。
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65288 8)" = "1" ]; then
  echo "PASS: 组词中 Alt+Backspace 被消费（回退一个音节）"
else
  echo "FAIL: 组词中 Alt+Backspace 未被消费" >&2
  exit 1
fi
if [ "$(handled "$P2" 65288 8)" = "1" ]; then
  echo "PASS: 再次 Alt+Backspace 消费（删空第二个音节）"
else
  echo "FAIL: 第二次 Alt+Backspace 未被消费" >&2
  exit 1
fi
if [ "$(handled "$P2" 65288 8)" = "0" ]; then
  echo "PASS: 非组词 Alt+Backspace 放行给宿主"
else
  echo "FAIL: 非组词 Alt+Backspace 仍被拦截" >&2
  exit 1
fi

# Ctrl+Backspace 仍整段清空；Alt+U 是宿主快捷键必须放行（不得复用 Ctrl+U）。
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65288 4)" = "1" ]; then
  echo "PASS: Ctrl+Backspace 整段清空被消费"
else
  echo "FAIL: Ctrl+Backspace 未被消费" >&2
  exit 1
fi
for kv in 110 105; do press "$P2" "$kv" 0; done  # ni
if [ "$(handled "$P2" 117 8)" = "0" ]; then
  echo "PASS: Alt+U 放行给宿主"
else
  echo "FAIL: Alt+U 被输入法拦截" >&2
  exit 1
fi
press "$P2" 65307 0   # Esc 清空（Alt+U 透传已 flush，保险）

# ---- Delete 与修饰键映射（2026-10-31 用户要求）----
# Delete(65535) 组词中与 Backspace 同语义；Ctrl=4、Alt=8、Super=64。
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65535 0)" = "1" ]; then
  echo "PASS: 普通 Delete 组词中被消费（逐字符回退）"
else
  echo "FAIL: 普通 Delete 未被消费" >&2
  exit 1
fi
press "$P2" 65307 0
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65535 4)" = "1" ]; then
  echo "PASS: Ctrl+Delete 全部删除被消费"
else
  echo "FAIL: Ctrl+Delete 未被消费" >&2
  exit 1
fi
press "$P2" 65307 0
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65535 8)" = "1" ]; then
  echo "PASS: Alt+Delete 按音节回退被消费"
else
  echo "FAIL: Alt+Delete 未被消费" >&2
  exit 1
fi
press "$P2" 65307 0
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65288 64)" = "1" ]; then
  echo "PASS: Super+Backspace 全部删除被消费"
else
  echo "FAIL: Super+Backspace 未被消费" >&2
  exit 1
fi
press "$P2" 65307 0

# ---- 非退格键修饰键一致性（2026-10-31 三端收尾）----
# Shift+PageUp 组词中仍翻页（PageUp=65365, Shift=1）；Ctrl+Shift+U 是宿主快捷键
#（U=85, Ctrl|Shift=5）必须放行。
for kv in 110 105 104 97 111; do press "$P2" "$kv" 0; done  # nihao
if [ "$(handled "$P2" 65365 1)" = "1" ]; then
  echo "PASS: Shift+PageUp 组词中翻页被消费"
else
  echo "FAIL: Shift+PageUp 未被消费" >&2
  exit 1
fi
press "$P2" 65307 0
for kv in 110 105; do press "$P2" "$kv" 0; done  # ni
if [ "$(handled "$P2" 85 5)" = "0" ]; then
  echo "PASS: Ctrl+Shift+U 放行给宿主"
else
  echo "FAIL: Ctrl+Shift+U 被输入法拦截" >&2
  exit 1
fi
press "$P2" 65307 0
if [ "$(handled "$P2" 65535 0)" = "0" ]; then
  echo "PASS: 非组词 Delete 放行给宿主"
else
  echo "FAIL: 非组词 Delete 仍被拦截" >&2
  exit 1
fi

# ---- 用户导入词库真的参与匹配（2026-10-31）----
# 只断言「日志里有加载路径」不够：捕获引擎发出的 UpdateLookupTable 信号，
# 打 "ceshici"（测试词 = ce shi ci），断言候选列表里出现导入词。
MON="$WORK/monitor.log"
dbus-monitor --session "type='signal',interface='org.freedesktop.IBus.Engine'" > "$MON" 2>&1 &
MON_PID=$!
for kv in 99 101 115 104 105 99 105; do press "$P1" "$kv" 0; done  # ceshici
sleep 0.5
kill "$MON_PID" 2>/dev/null || true
wait "$MON_PID" 2>/dev/null || true
if grep -q "测试词" "$MON"; then
  echo "PASS: 用户导入词库候选已出现在 LookupTable（测试词）"
else
  echo "FAIL: 用户导入词库未参与候选（monitor 日志：）" >&2
  cat "$MON" >&2
  exit 1
fi
press "$P1" 65307 0  # Esc 清空

echo
echo "全部 Linux 端到端检查通过"
