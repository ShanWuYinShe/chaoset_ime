#!/bin/sh
# chaoset TSF 壳构建与验证：
#   1) 原生编译核心层行为测试，并用真实 chaosetd（Unix socket，与 Windows
#      命名管道同为 ipc.Endpoint 本机传输，协议/帧格式一致）跑通
#   2) mingw-w64 交叉编译 ChaosetIME.dll，检查导出表（含 ChaosetRunUi）
#   3) mingw-w64 交叉编译 chaoset-ui.exe（单例 UI 宿主：托盘状态 + 设置窗）
# 前置：Go 工具链；brew install mingw-w64
set -e
cd "$(dirname "$0")"

mkdir -p build
echo "[1/4] 原生编译核心层测试..."
CXX=${CXX:-clang++}
"$CXX" -std=c++17 -O2 -Wall -Wextra -Isrc -I../common/ipc \
  tests/core_test.cpp ../common/ipc/core.cpp -o build/core_test

echo "[2/4] 启动真实 daemon（Unix socket）并跑行为测试..."
(cd ../.. && go build -o shims/windows/build/chaosetd ./cmd/chaosetd)
# 测试台用 Unix socket 连 daemon：Windows 壳本身只走命名管道（本机没有管道
# API），但两者同属 ipc.Endpoint 的本机传输，帧格式与协议完全一致。
# 路径必须短——macOS 的 sockaddr_un 上限 104 字节，长路径会连不上。
SOCKDIR=$(mktemp -d "${TMPDIR:-/tmp}/chaoset.XXXXXX")
SOCK="$SOCKDIR/core.sock"
# 必须用**发版词库** dict_common.tsv：打包时随包携带的是它，用它测才等于测
# 用户实际拿到的行为。此前这里用 558 条的 dict_seed.tsv，测试环境与发行环境
# 不一致（2026-10-26 审查指出；core_test 的断言曾对词库敏感而误报）。
./build/chaosetd -socket "$SOCK" -dict ../../data/dict_common.tsv -userdict "" &
DPID=$!
trap 'kill -INT $DPID 2>/dev/null || true; rm -rf "$SOCKDIR"' EXIT
# 等 daemon 真正监听（socket 文件出现），而不是盲等固定时长；
# 不重试 core_test：断言失败必须让脚本立刻红，重跑会掩盖真实回归。
i=0
while [ ! -S "$SOCK" ] && [ "$i" -lt 50 ]; do
  sleep 0.2
  i=$((i + 1))
done
if [ ! -S "$SOCK" ]; then
  echo "daemon 未在 10 秒内监听 $SOCK" >&2
  exit 1
fi
./build/core_test "$SOCK"
kill -INT $DPID 2>/dev/null || true
wait $DPID 2>/dev/null || true
trap - EXIT
rm -rf "$SOCKDIR"

echo "[3/4] mingw-w64 交叉编译 ChaosetIME.dll..."
VERSION=${VERSION:-dev}
CXXWIN=${CXXWIN:-x86_64-w64-mingw32-g++}
# 图标资源必须链进 DLL：注册表 IconFile 指向本 DLL（win32.cpp RegisterProfile），
# 此前 build.sh 从不编译 chaoset.rc，语言栏/切换菜单拿到的是空图标（2026-10-27 审查）。
WINDRES=${WINDRES:-x86_64-w64-mingw32-windres}
"$WINDRES" -I resources resources/chaoset.rc -O coff -o build/chaoset_rc.o
"$CXXWIN" -std=c++17 -O2 -Wall -Wextra -shared -static \
  -Isrc -I../common/ipc \
  -DCHAOSET_VERSION="\"$VERSION\"" \
  -o build/ChaosetIME.dll \
  src/win32.cpp ../common/ipc/core.cpp build/chaoset_rc.o \
  -lole32 -luuid -luser32 -lkernel32 -ladvapi32 -lgdi32 -lshell32 -lcomdlg32
if x86_64-w64-mingw32-objdump -h build/ChaosetIME.dll | command grep -qi "\.rsrc"; then
  echo "  图标资源已嵌入（.rsrc 段存在）"
else
  echo "  警告：.rsrc 段缺失，语言栏将没有图标" >&2
  exit 1
fi
x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -E "DllRegisterServer|DllGetClassObject|DllCanUnloadNow|DllUninstallServer|ChaosetRunUi" | while read -r line; do
  echo "  导出: $line"
done
# 单例 UI 宿主入口必须导出：chaoset-ui.exe 靠 LoadLibrary + ChaosetRunUi 复用
# 本 DLL 的设置窗/托盘/管道实现。缺了这个导出，设置窗与中英指示器整体不可用。
if ! x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -q "ChaosetRunUi"; then
  echo "错误：ChaosetIME.dll 缺少 ChaosetRunUi 导出（chaoset-ui.exe 无法工作）" >&2
  exit 1
fi

# 无障碍 UIA 必须动态加载（LoadLibrary/GetProcAddress），不能给 DLL 增加
# uiautomationcore.dll 静态导入依赖：旧/精简系统没有该 DLL 时输入法仍要能跑。
# 2026-10-31 加窗口级 UIA 宿主 provider 时立的约定，这里做构建期断言。
if x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -qi 'DLL Name:.*uiautomationcore'; then
  echo "错误：ChaosetIME.dll 静态导入了 uiautomationcore.dll（UIA 必须动态加载）" >&2
  exit 1
fi
echo "  UIA 动态加载自检通过（无 uiautomationcore 静态导入）"

echo "[4/4] 交叉编译单例 UI 宿主 chaoset-ui.exe..."
# 托盘指示器 + 设置窗的唯一宿主。exe 本身只有几十行：LoadLibrary 同目录的
# ChaosetIME.dll 后调用导出 ChaosetRunUi，UI 实现全部在 DLL 里（无第二套 UI）。
"$CXXWIN" -std=c++17 -O2 -Wall -Wextra -municode -mwindows -static \
  -o build/chaoset-ui.exe src/ui_main.cpp
# 不得静态导入 ChaosetIME.dll：必须 LoadLibrary，否则 DLL 与 exe 版本/路径
# 不一致时整个 UI 宿主起不来（且无法给出可读错误）。
if x86_64-w64-mingw32-objdump -p build/chaoset-ui.exe | command grep -qi 'DLL Name:.*ChaosetIME'; then
  echo "错误：chaoset-ui.exe 静态导入了 ChaosetIME.dll（必须 LoadLibrary 动态加载）" >&2
  exit 1
fi
if x86_64-w64-mingw32-objdump -p build/chaoset-ui.exe | command grep -qi 'ChaosetRunUi'; then
  echo "  警告：chaoset-ui.exe 不应静态引用 ChaosetRunUi 符号" >&2
fi
echo "  UI 宿主静态链接自检通过（未静态导入 ChaosetIME.dll）"

echo "完成：build/ChaosetIME.dll（编译级验证）、build/chaoset-ui.exe、build/core_test（行为级验证）"
