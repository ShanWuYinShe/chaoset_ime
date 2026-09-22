#!/bin/sh
# chaoset TSF 壳构建与验证：
#   1) 原生编译核心层行为测试，并用真实 chaosetd（TCP 回环）跑通
#   2) mingw-w64 交叉编译 ChaosetIME.dll，检查导出表
# 前置：Go 工具链；brew install mingw-w64
set -e
cd "$(dirname "$0")"

mkdir -p build
echo "[1/3] 原生编译核心层测试..."
CXX=${CXX:-clang++}
"$CXX" -std=c++17 -O2 -Wall -Wextra -Isrc tests/core_test.cpp src/core.cpp -o build/core_test

echo "[2/3] 启动真实 daemon（TCP 回环）并跑行为测试..."
(cd ../.. && go build -o shims/windows/build/chaosetd ./cmd/chaosetd)
PORT=${PORT:-7949}
./build/chaosetd -tcp "127.0.0.1:$PORT" -dict ../../data/dict_seed.tsv -userdict "" &
DPID=$!
trap 'kill -INT $DPID 2>/dev/null || true' EXIT
sleep 1
./build/core_test "127.0.0.1:$PORT"
kill -INT $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
trap - EXIT

echo "[3/3] mingw-w64 交叉编译 ChaosetIME.dll..."
CXXWIN=${CXXWIN:-x86_64-w64-mingw32-g++}
"$CXXWIN" -std=c++17 -O2 -Wall -Wextra -shared -static -o build/ChaosetIME.dll \
  src/win32.cpp src/core.cpp \
  -lole32 -luuid -lws2_32 -luser32 -lkernel32
x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -E "DllRegisterServer|DllGetClassObject|DllCanUnloadNow|DllUninstallServer" | while read -r line; do
  echo "  导出: $line"
done

echo "完成：build/ChaosetIME.dll（编译级验证）、build/core_test（行为级验证）"
