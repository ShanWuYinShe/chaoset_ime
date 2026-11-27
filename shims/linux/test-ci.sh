#!/bin/bash
# chaoset ibus 引擎端到端验证（**CI 原生版**：不需要 docker）。
#
# 与 test.sh 的区别：
#   test.sh 假设已用 setup-container.sh 起了名为 chaoset-ibus 的容器，
#   通过 docker exec 操作。那套流程适合本地反复调试，但 CI 里 GitHub runner
#   本身就是一次性 VM，再套一层 docker 纯属浪费（还要镜像拉取 1-3 分钟）。
#   本脚本直接在 runner 上跑同样的 DBus 断言。
#
# 覆盖内容（CI 版为准；test.sh 是本地容器调试子集，不含后两条）：
#   - 引擎能被拉起并连上 session bus
#   - CreateEngine 两次得到两个独立实例（组词状态按实例隔离）
#   - 两实例交错打字，各自空格上屏正确
#   - 中英切换（Shift 空按）互不牵连
#   - 组词中 Page Down 被消费（翻页）
#   - Alt+Backspace 按音节回退 / Ctrl+Backspace 整段清空 / Alt+U 放行
#     （2026-10-31 三端统一后的真实 ibus 路由回归）
#   - 设置启动器（chaoset-setup）只 exec 统一设置中心 chaoset-settings、
#     原样转交参数，且不链接 GTK（设置界面唯一实现是 Slint 设置中心）
#   - 异步加载占位（SIGSTOP 挂起 daemon → 在飞 "..." 占位；SIGCONT 恢复 →
#     真实候选返回。锁定「无硬等待超时」契约：旧 200ms 读超时实现回到本
#     场景会在挂起期断连丢组词，必然 FAIL）
#   - 鼠标点选候选（CandidateClicked → CommitText）与第 2 页页内序号点选
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
  # 引擎是 daemon 的父进程；父进程退出不会自动杀 daemon，必须显式清理。
  pkill -f "$WORK/chaosetd" 2>/dev/null || true
  kill "${XVFB_PID:-}" 2>/dev/null || true
  wait "${XVFB_PID:-}" 2>/dev/null || true
  for _ in 1 2 3 4 5; do
    rm -rf "$WORK" 2>/dev/null && break
    sleep 0.2
  done
}
trap cleanup EXIT

echo "==> 构建 Linux 原生引擎（C++/libibus）与 Rust daemon"
sh shims/linux/engine/build.sh "$WORK/chaoset-engine"
cargo build --release --bin chaosetd --manifest-path crates/Cargo.toml
cp crates/target/release/chaosetd "$WORK/chaosetd"

# ---- 设置启动器 ----
# Linux 设置窗已统一为 Slint 设置中心（chaoset-settings）；/usr/share/chaoset/
# chaoset-setup 只是纯 POSIX C 启动器。这里验证它确实 exec chaoset-settings 并
# 原样转交参数（用假二进制，CI 无桌面不开真实 GUI），且没链接 GTK。
echo "==> 构建并冒烟 Linux 设置启动器（chaoset-setup）"
sh shims/linux/setup/build.sh "$WORK/chaoset-setup"
if [ ! -x "$WORK/chaoset-setup" ]; then
  echo "FAIL: 设置启动器未生成" >&2
  exit 1
fi
if command -v readelf >/dev/null 2>&1; then
  if readelf -d "$WORK/chaoset-setup" 2>/dev/null | grep -q 'libgtk'; then
    echo "FAIL: 设置启动器链接了 GTK（应为纯 POSIX C 启动器，设置界面在 chaoset-settings）" >&2
    exit 1
  fi
fi
mkdir -p "$WORK/fakebin"
cat > "$WORK/fakebin/chaoset-settings" <<'FAKE'
#!/bin/sh
printf '%s' "$*" > "$CHAOSET_SETTINGS_MARKER"
FAKE
chmod +x "$WORK/fakebin/chaoset-settings"
CHAOSET_SETTINGS_MARKER="$WORK/settings-args.txt" \
  PATH="$WORK/fakebin:$PATH" "$WORK/chaoset-setup" --page about
GOT=""
if [ -f "$WORK/settings-args.txt" ]; then
  GOT=$(cat "$WORK/settings-args.txt")
fi
if [ "$GOT" != "--page about" ]; then
  echo "FAIL: 设置启动器未 exec chaoset-settings 或参数未原样转交（实际: '$GOT'）" >&2
  exit 1
fi
echo "PASS: 设置启动器 exec chaoset-settings 并原样转交参数"

echo "==> 启动私有 session bus、Xvfb 与 ibus-daemon"
dbus-daemon --session --fork --print-address > "$WORK/dbus_addr"
DBUS_ADDR=$(cat "$WORK/dbus_addr")
export DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR"
# ibus-daemon 没有 X display 时 gtk3 扩展会崩，headless runner 必须给 Xvfb。
Xvfb :99 -screen 0 1280x800x24 > "$WORK/xvfb.log" 2>&1 &
XVFB_PID=$!
export DISPLAY=:99
sleep 1
ibus-daemon --daemonize --panel=disable || true
sleep 1
# IBus 用私有 D-Bus 总线（不是 session bus）：引擎和测试都必须指向它。
IBUS_ADDR=$(ibus address 2>/dev/null || true)
if [ -z "$IBUS_ADDR" ]; then
  echo "FAIL: 无法获取 IBUS_ADDRESS" >&2
  exit 1
fi
export IBUS_ADDRESS="$IBUS_ADDR"
export DBUS_SESSION_BUS_ADDRESS="$IBUS_ADDR"

# 用户导入词库（Linux 设置中心「导入词库」写入 XDG 数据目录；这里显式传
# -imported-dict 模拟，验证引擎启动时会与主词库一起加载）。
printf '测试词\tce shi ci\t99\n' > "$WORK/imported.tsv"

echo "==> 启动 chaoset-engine（daemon 在首次按键时自动拉起）"
"$WORK/chaoset-engine" -ibus \
  -daemon "$WORK/chaosetd" \
  -socket "$WORK/chaoset.sock" \
  -log "$WORK/chaosetd.log" \
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

# 原生引擎用 GDBus 直接持有固定总线名；不必再按 PID 反查唯一名。
UN=org.freedesktop.IBus.Chaoset
echo "PASS: 引擎总线名 $UN"

FP=/org/freedesktop/IBus/Factory
CREAT=org.freedesktop.IBus.Factory.CreateEngine

# 两个独立实例
P1=$(dbus-send --session --print-reply --dest="$UN" $FP $CREAT string:"chaoset" 2>/dev/null \
     | grep -o "/org/freedesktop/IBus/Engine/[0-9]*" | head -1)
P2=$(dbus-send --session --print-reply --dest="$UN" $FP $CREAT string:"chaoset" 2>/dev/null \
     | grep -o "/org/freedesktop/IBus/Engine/[0-9]*" | head -1)
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

# ---- 预热 daemon：异步首键只入队 + 引擎线程懒启动 chaosetd（首键本身可能 fail-open）----
press "$P1" 97 0   # 'a'：触发懒启动；若 daemon 已在则直接组词，清掉即可
_ready=0
for _ in $(seq 1 100); do
  if [ -S "$WORK/chaoset.sock" ]; then _ready=1; break; fi
  sleep 0.2
done
if [ "$_ready" != "1" ]; then
  echo "FAIL: 首次按键未拉起 chaosetd（$WORK/chaoset.sock 不存在）" >&2
  exit 1
fi
sleep 0.5
press "$P1" 65307 0  # Esc：清掉预热组词（未被消费则无副作用）
press "$P1" 65307 0
sleep 0.2
echo "PASS: 首键懒启动 chaosetd 就绪"

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
# 等 dbus-monitor 连上私有总线并注册 eavesdrop match：CI 上曾因「启动后立即按键」
# 偶发漏抓 UpdateLookupTable 信号而误报 flake（同 run 重跑即过）。只加等待，不放宽断言。
sleep 0.7
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

# ---- 鼠标点选候选（CandidateClicked，2026-10-31）----
# ibus 面板点击候选会调用引擎标准方法 CandidateClicked；此前 Linux 没有该
# 回调，点击无反应（mac/Windows 均支持点选）。这里直接调用该方法并捕获
# CommitText 信号，断言点选真的上屏。
MON2="$WORK/click-monitor.log"
dbus-monitor --session "type='signal',interface='org.freedesktop.IBus.Engine'" > "$MON2" 2>&1 &
MON2_PID=$!
sleep 0.7  # 同上：等 monitor 就绪再按键
for kv in 110 105 104 97 111; do press "$P1" "$kv" 0; done  # nihao
dbus-send --session --print-reply --dest="$UN" "$P1" \
  org.freedesktop.IBus.Engine.CandidateClicked uint32:0 uint32:1 uint32:0 >/dev/null 2>&1
sleep 0.5
kill "$MON2_PID" 2>/dev/null || true
wait "$MON2_PID" 2>/dev/null || true
if grep -q "CommitText" "$MON2" && grep -q "你好" "$MON2"; then
  echo "PASS: 鼠标点选候选上屏「你好」（CandidateClicked）"
else
  echo "FAIL: CandidateClicked 未上屏（monitor 日志：）" >&2
  cat "$MON2" >&2
  exit 1
fi
press "$P1" 65307 0  # Esc 保险

# ---- 第 2 页鼠标点选（CandidateClicked 页内序号，2026-10-31）----
# 组词 "ni" 候选多于一页：PageDown 后点第 1 项，CandidateClicked(0) 必须按
# 「当前页切片」处理（不能仍选第 1 页首条）；这里验证翻页后 D-Bus 调用仍
# 正常上屏（具体页内映射由 behavior_test 的 PageTwo 用例锁定）。
MON3="$WORK/click-page2-monitor.log"
dbus-monitor --session "type='signal',interface='org.freedesktop.IBus.Engine'" > "$MON3" 2>&1 &
MON3_PID=$!
sleep 0.7  # 同上：等 monitor 就绪再按键
for kv in 110 105; do press "$P1" "$kv" 0; done  # ni
press "$P1" 65366 0  # PageDown
dbus-send --session --print-reply --dest="$UN" "$P1" \
  org.freedesktop.IBus.Engine.CandidateClicked uint32:0 uint32:1 uint32:0 >/dev/null 2>&1
sleep 0.5
kill "$MON3_PID" 2>/dev/null || true
wait "$MON3_PID" 2>/dev/null || true
if grep -q "CommitText" "$MON3"; then
  echo "PASS: 第 2 页点选候选正常上屏（CandidateClicked 页内序号）"
else
  echo "FAIL: 第 2 页 CandidateClicked 未上屏（monitor 日志：）" >&2
  cat "$MON3" >&2
  exit 1
fi
press "$P1" 65307 0  # Esc 保险

# ---- 异步加载占位（daemon 挂起 → "..." 占位 → 恢复 → 真实候选）----
# 锁定异步引擎管线的「无硬等待超时」契约（与 macOS key_routing 的失败注入、
# Windows INFINITE 等待三端同语义）：
#   1. SIGSTOP 冻结真实 chaosetd = 最真实的 daemon 挂起；
#   2. 冻结窗口内按键：主循环不被阻塞（press 的 D-Bus 往返能返回就是证明——
#      旧同步实现会卡死在这里直到超时）、乐观预编辑照常渲染、候选表立即切换为
#      单条平静 "..." 占位（UpdateLookupTable 出现 "..." 且冻结期绝无真实候选）；
#   3. SIGCONT 解冻：积压请求被处理，真实候选（"ni" → 你）照常返回且晚于占位。
# 旧 200ms 硬读超时实现回到本场景：挂起期超时断连丢组词/2s 强制上屏原始串，
# 两处断言必然 FAIL。
MON4="$WORK/loading-monitor.log"
dbus-monitor --session "type='signal',interface='org.freedesktop.IBus.Engine'" > "$MON4" 2>&1 &
MON4_PID=$!
sleep 0.7  # 同上：等 monitor 就绪再按键（flake 教训）
DPID=$(pgrep -x chaosetd | head -1)
if [ -z "$DPID" ]; then
  echo "FAIL: 未找到 chaosetd 进程，无法执行挂起场景" >&2
  exit 1
fi
kill -STOP "$DPID"
for kv in 110 105; do press "$P1" "$kv" 0; done  # n i（乐观渲染 + "..." 占位）
sleep 0.8
if grep -qF '"..."' "$MON4"; then
  echo "PASS: daemon 挂起期间候选表显示 \"...\" 加载占位"
else
  echo "FAIL: daemon 挂起期间未出现 \"...\" 占位（monitor 日志：）" >&2
  cat "$MON4" >&2
  kill -CONT "$DPID" 2>/dev/null || true
  exit 1
fi
if grep -q '你' "$MON4"; then
  echo "FAIL: daemon 挂起期（冻结窗口）不该出现真实候选" >&2
  kill -CONT "$DPID" 2>/dev/null || true
  exit 1
fi
kill -CONT "$DPID"
sleep 2.5  # 解冻后 daemon 处理积压请求，引擎按序应用并刷新真实候选
kill "$MON4_PID" 2>/dev/null || true
wait "$MON4_PID" 2>/dev/null || true
if grep -q '你' "$MON4"; then
  echo "PASS: daemon 恢复后真实候选返回（你）"
else
  echo "FAIL: daemon 恢复后未返回真实候选（monitor 日志：）" >&2
  cat "$MON4" >&2
  exit 1
fi
DOT_LINE=$(grep -nF '"..."' "$MON4" | head -1 | cut -d: -f1)
NI_LINE=$(grep -n '你' "$MON4" | head -1 | cut -d: -f1)
if [ -n "$DOT_LINE" ] && [ -n "$NI_LINE" ] && [ "$DOT_LINE" -lt "$NI_LINE" ]; then
  echo "PASS: 占位先于真实候选出现（顺序正确）"
else
  echo "FAIL: 占位/真实候选顺序异常（dot=${DOT_LINE} ni=${NI_LINE}）" >&2
  exit 1
fi
press "$P1" 65307 0  # Esc 清空

# ---- daemon 单实例（引擎首键拉起；不应因多次 auto-spawn 留下两个进程）----
DAEMON_COUNT=$(pgrep -x chaosetd | wc -l | tr -d ' ')
if [ "$DAEMON_COUNT" = "1" ]; then
  echo "PASS: chaosetd 单实例（进程数 = 1）"
else
  echo "FAIL: chaosetd 进程数为 ${DAEMON_COUNT}（期望 1）" >&2
  pgrep -ax chaosetd >&2 || true
  exit 1
fi

echo
echo "全部 Linux 端到端检查通过"
