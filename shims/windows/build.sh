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
# 必须用**发版词库** dict_common.tsv：打包时随包携带的是它，用它测才等于测
# 用户实际拿到的行为。此前这里用 558 条的 dict_seed.tsv，测试环境与发行环境
# 不一致（2026-10-26 审查指出；core_test 的断言曾对词库敏感而误报）。
./build/chaosetd -tcp "127.0.0.1:$PORT" -dict ../../data/dict_common.tsv -userdict "" &
DPID=$!
trap 'kill -INT $DPID 2>/dev/null || true' EXIT
sleep 1
./build/core_test "127.0.0.1:$PORT"
kill -INT $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
trap - EXIT

echo "[3/3] mingw-w64 交叉编译 ChaosetIME.dll..."
CXXWIN=${CXXWIN:-x86_64-w64-mingw32-g++}
# 图标资源必须链进 DLL：注册表 IconFile 指向本 DLL（win32.cpp RegisterProfile），
# 此前 build.sh 从不编译 chaoset.rc，语言栏/切换菜单拿到的是空图标（2026-10-27 审查）。
WINDRES=${WINDRES:-x86_64-w64-mingw32-windres}
"$WINDRES" -I resources resources/chaoset.rc -O coff -o build/chaoset_rc.o
"$CXXWIN" -std=c++17 -O2 -Wall -Wextra -shared -static -o build/ChaosetIME.dll \
  src/win32.cpp src/core.cpp build/chaoset_rc.o \
  -lole32 -luuid -lws2_32 -luser32 -lkernel32 -ladvapi32 -lgdi32 -lshell32 -lcomdlg32
if x86_64-w64-mingw32-objdump -h build/ChaosetIME.dll | command grep -qi "\.rsrc"; then
  echo "  图标资源已嵌入（.rsrc 段存在）"
else
  echo "  警告：.rsrc 段缺失，语言栏将没有图标" >&2
  exit 1
fi
x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -E "DllRegisterServer|DllGetClassObject|DllCanUnloadNow|DllUninstallServer" | while read -r line; do
  echo "  导出: $line"
done

echo "完成：build/ChaosetIME.dll（编译级验证）、build/core_test（行为级验证）"
