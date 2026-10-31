#!/bin/bash
# key_routing 回归测试（macOS 按键路由）：合成 NSEvent 驱动 ChaosetController.handle，
# 覆盖 keyUp 放行、字母进组词、Cmd/Ctrl 放行、Shift 空按切中英、Shift+字母不误切。
#
# 此前这个测试只存在于源码里、未接入 Makefile/CI（docs/ui-parity 自述的「死测试」，
# 审计 G7）。现在由本脚本负责：编译测试二进制 → 起临时 daemon → 跑断言 → 清理。
#
# 前置：先在 shims/macos 下跑过 ./build.sh（需要 build/chaosetd）。
# 用法：./tests/key_routing/run.sh（在 shims/macos 目录或任意目录均可，脚本自己定位）
set -euo pipefail

cd "$(dirname "$0")/../.."   # → shims/macos
MACOS_DIR=$PWD
ROOT=$(cd ../.. && pwd)
BUILD="$MACOS_DIR/build"

if [ ! -x "$BUILD/chaosetd" ]; then
  echo "FAIL: 缺少 $BUILD/chaosetd，请先在 shims/macos 下运行 ./build.sh" >&2
  exit 1
fi

# 与 build.sh 同口径的单架构目标（CI runner 是 arm64；Intel 本机是 x86_64）。
ARCH=$(uname -m)
case "$ARCH" in
  arm64)  TARGET="arm64-apple-macos15.0" ;;
  x86_64) TARGET="x86_64-apple-macos15.0" ;;
  *) echo "FAIL: 未知架构 $ARCH" >&2; exit 1 ;;
esac

mkdir -p "$BUILD/modulecache"
echo "==> 编译 key_routing（${ARCH}）"
swiftc -O -module-cache-path "$BUILD/modulecache" -target "$TARGET" \
  -framework InputMethodKit -framework AppKit \
  tests/key_routing/main.swift src/ChaosetController.swift src/EngineClient.swift \
  src/CandidatePanel.swift src/SettingsWindow.swift \
  -o "$BUILD/key_routing"

TMP=$(mktemp -d)
DAEMON_PID=""
cleanup() {
  if [ -n "$DAEMON_PID" ]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    # daemon 收到 TERM 会同步落盘 userdict；必须 wait 后再删临时目录，
    # 否则删除与落盘并发会让 rm -rf 报 Directory not empty（同 CI linux-e2e 教训）。
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  for _ in 1 2 3 4 5; do
    rm -rf "$TMP" 2>/dev/null && break
    sleep 0.2
  done
}
trap cleanup EXIT

echo "==> 启动临时 daemon"
"$BUILD/chaosetd" -socket "$TMP/chaoset.sock" \
  -dict "$ROOT/data/dict_common.tsv" -userdict "$TMP/userdict.json" \
  -log "$TMP/daemon.log" &
DAEMON_PID=$!

for _ in $(seq 1 50); do
  [ -S "$TMP/chaoset.sock" ] && break
  sleep 0.1
done
if [ ! -S "$TMP/chaoset.sock" ]; then
  echo "FAIL: daemon 未就绪，日志：" >&2
  cat "$TMP/daemon.log" >&2 || true
  exit 1
fi
sleep 0.2  # 让 daemon 完成 socket listen（文件出现后 listen 已就绪，保守起见）

echo "==> 运行 key_routing（临时 socket：$TMP/chaoset.sock）"
CHAOSET_SOCKET="$TMP/chaoset.sock" "$BUILD/key_routing"
echo "key_routing 全部通过"