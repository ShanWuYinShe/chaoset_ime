#!/bin/bash
# macOS 联想端到端回归：真实 daemon + EngineClient（不依赖 UI）。
# 用法：./shims/macos/tests/associate/run.sh（需先跑过 shims/macos/build.sh）
set -euo pipefail

cd "$(dirname "$0")/../.."   # → shims/macos
MACOS_DIR=$PWD
ROOT=$(cd ../.. && pwd)
BUILD="$MACOS_DIR/build"

if [ ! -x "$BUILD/chaosetd" ]; then
  echo "FAIL: 缺少 $BUILD/chaosetd，请先在 shims/macos 下运行 ./build.sh" >&2
  exit 1
fi

ARCH=$(uname -m)
case "$ARCH" in
  arm64)  TARGET="arm64-apple-macos15.0" ;;
  x86_64) TARGET="x86_64-apple-macos15.0" ;;
  *) echo "FAIL: 未知架构 $ARCH" >&2; exit 1 ;;
esac

mkdir -p "$BUILD/modulecache"
echo "==> 编译 associate（${ARCH}）"
swiftc -O -module-cache-path "$BUILD/modulecache" -target "$TARGET" \
  src/EngineClient.swift tests/associate/main.swift \
  -o "$BUILD/associate"

TMP=$(mktemp -d)
DAEMON_PID=""
cleanup() {
  if [ -n "$DAEMON_PID" ]; then
    kill "$DAEMON_PID" 2>/dev/null || true
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

echo "==> 运行 associate 端到端"
CHAOSET_SOCKET="$TMP/chaoset.sock" "$BUILD/associate"