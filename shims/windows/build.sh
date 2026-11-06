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
# 版本资源单一真源 scripts/version.sh：tag 触发的 CI 用 tag 号，其余一律用
# commit hash（本地测试不需要累加版本号，又能从 hash 定位构建）。Makefile 通过
# VERSION 环境变量注入；直接跑 build.sh 时自己调同一个脚本。QUAD 是 4 段数字
# 版本（NSIS / PE VS_FIXEDFILEINFO 只接受数字），显示用 @VERSION@ 仍是原始 hash。
ROOT=$(cd ../.. && pwd)
if [ -z "${VERSION:-}" ]; then
  VERSION=$(sh "$ROOT/scripts/version.sh")
fi
if [ -z "${VERSION_QUAD:-}" ]; then
  QUAD=$(sh "$ROOT/scripts/version.sh" --quad)
else
  QUAD="$VERSION_QUAD"
fi
VMAJOR=$(echo "$QUAD" | cut -d. -f1); VMINOR=$(echo "$QUAD" | cut -d. -f2)
VPATCH=$(echo "$QUAD" | cut -d. -f3); VBUILD=$(echo "$QUAD" | cut -d. -f4)
export VERSION
CXXWIN=${CXXWIN:-x86_64-w64-mingw32-g++}
WINDRES=${WINDRES:-x86_64-w64-mingw32-windres}
# chaoset.rc 是模板（图标 + VS_VERSION_INFO，含 @VERSION@ 等占位符）：先按
# sed 注入版本与文件名生成 chaoset.gen.rc，再交给 windres。图标必须链进 DLL
# （注册表 IconFile 指向本 DLL），版本信息让「右键属性 → 详细信息」可见。
gen_rc() { # $1=输出 rc  $2=FileDescription  $3=InternalName  $4=OriginalFilename  $5=FileType
  sed -e "s/@VERSION@/$VERSION/g" -e "s/@VERSION_QUAD@/$QUAD/g" \
      -e "s/@VER_MAJOR@/$VMAJOR/g" -e "s/@VER_MINOR@/$VMINOR/g" \
      -e "s/@VER_PATCH@/$VPATCH/g" -e "s/@VER_BUILD@/$VBUILD/g" \
      -e "s/@FILE_DESCRIPTION@/$2/g" -e "s/@INTERNAL_NAME@/$3/g" \
      -e "s/@ORIGINAL_FILENAME@/$4/g" -e "s/@FILE_TYPE@/$5/g" \
      resources/chaoset.rc > "$1"
}
gen_rc build/chaoset.gen.rc "Chaoset 拼音输入法 TSF 前端" "ChaosetIME" "ChaosetIME.dll" 2
# -c 65001：模板里的中文说明是 UTF-8，windres 默认按 ANSI 解释会存成
# mojibake（属性页乱码），显式指定 UTF-8 代码页。
"$WINDRES" -c 65001 -I resources build/chaoset.gen.rc -O coff -o build/chaoset_rc.o
# ChaosetRunUi 的 __declspec(dllexport) 会让 mingw 停止自动导出其余全局
# 符号，COM 注册入口（DllRegisterServer 等）随之消失、regsvr32 失败；显式
# --export-all-symbols 恢复全部导出（与 Makefile package-windows 一致）。
"$CXXWIN" -std=c++17 -O2 -Wall -Wextra -shared -static -Wl,--export-all-symbols \
  -Isrc -I../common/ipc \
  -DCHAOSET_VERSION="\"$VERSION\"" \
  -o build/ChaosetIME.dll \
  src/win32.cpp ../common/ipc/core.cpp build/chaoset_rc.o \
  -lole32 -luuid -luser32 -lkernel32 -ladvapi32 -lgdi32 -lshell32 -lcomdlg32
if x86_64-w64-mingw32-objdump -h build/ChaosetIME.dll | command grep -qi "\.rsrc"; then
  echo "  图标与版本资源已嵌入（.rsrc 段存在）"
else
  echo "  警告：.rsrc 段缺失，语言栏将没有图标、属性页看不到版本信息" >&2
  exit 1
fi
# 版本资源自检：strings -el 会在 UTF-16 中文处截断，直接用 python3 查
# UTF-16LE 字节，确认 VS_VERSION_INFO、产品名、中文与注入的版本号都真的
# 进了资源；QUAD 通过环境变量传入，避开 shell 引号。
if ! CHAOSET_QUAD="$QUAD" python3 - <<'PY'
import os, sys
d = open("build/chaoset_rc.o", "rb").read()
for s in ("VS_VERSION_INFO", "ProductName", "ShanWuYinShe", "拼音输入法",
          os.environ["CHAOSET_QUAD"]):
    if s.encode("utf-16-le") not in d:
        print("版本资源缺少: " + s, file=sys.stderr)
        sys.exit(1)
PY
then
  echo "错误：版本资源缺少产品名/版本号（VS_VERSION_INFO 未生成）" >&2
  exit 1
fi
echo "  版本资源自检通过（产品名/公司/中文/版本号均为 UTF-16）"
x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -E "DllRegisterServer|DllGetClassObject|DllCanUnloadNow|DllUninstallServer|ChaosetRunUi" | while read -r line; do
  echo "  导出: $line"
done
# COM 注册入口缺一不可：regsvr32 只认 DllRegisterServer/DllUnregisterServer，
# 缺了安装后输入法完全不可用。此前只打印不校验，静默漏掉过导出表回退。
for sym in DllRegisterServer DllUnregisterServer DllGetClassObject DllCanUnloadNow; do
  if ! x86_64-w64-mingw32-objdump -p build/ChaosetIME.dll | command grep -q "$sym"; then
    echo "错误：ChaosetIME.dll 缺少 COM 导出 ${sym}（regsvr32 将无法注册）" >&2
    exit 1
  fi
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
# 同一份版本模板再生成一份 UI 宿主专用资源：独立 EXE 的属性页也要显示
# 产品名/公司/版本，且 FileDescription 指向「UI 宿主」而非前端 DLL。
gen_rc build/ui.gen.rc "Chaoset 拼音输入法 UI 宿主（托盘中英状态 + 设置窗）" "chaoset-ui" "chaoset-ui.exe" 1
"$WINDRES" -c 65001 -I resources build/ui.gen.rc -O coff -o build/ui_rc.o
"$CXXWIN" -std=c++17 -O2 -Wall -Wextra -municode -mwindows -static \
  -o build/chaoset-ui.exe src/ui_main.cpp build/ui_rc.o
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
