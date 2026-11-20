#!/usr/bin/env bash
# chaoset Windows 原生编译与打包脚本（基于 MSVC + NSIS）
# 用于 GitHub Actions windows-latest runner 原生秒级构建
set -euo pipefail

# 防止 Git Bash / MSYS2 将 Windows 编译器参数（如 /O2 /LD /DEF:...）转为 Unix 路径
export MSYS2_ARG_CONV_EXCL="*"

# 1. 确定版本号（单一真源 scripts/version.sh）
if [ -z "${VERSION:-}" ]; then
  VERSION=$(sh scripts/version.sh)
fi
if [ -z "${VERSION_QUAD:-}" ]; then
  QUAD=$(sh scripts/version.sh --quad)
else
  QUAD="$VERSION_QUAD"
fi
VMAJOR=$(echo "$QUAD" | cut -d. -f1)
VMINOR=$(echo "$QUAD" | cut -d. -f2)
VPATCH=$(echo "$QUAD" | cut -d. -f3)
VBUILD=$(echo "$QUAD" | cut -d. -f4)

echo "==> 构建版本: $VERSION ($QUAD)"

# 2. 准备输出目录
OUTDIR="dist/chaoset-windows-x64"
rm -rf "$OUTDIR"
mkdir -p "$OUTDIR"

# 3. 编译 Go 二进制 (chaosetctl.exe, chaosetd.exe)
echo "==> 编译 Go 核心与全能工具 (chaosetctl.exe, chaosetd.exe)..."
go build -ldflags "-s -w" -o "$OUTDIR/chaosetctl.exe" ./cmd/chaosetctl
go build -ldflags "-H=windowsgui -s -w" -o "$OUTDIR/chaosetd.exe" ./cmd/chaosetd

# 4. 拷贝 Slint 设置中心 (chaoset-settings.exe)
if [ -f "shims/settings/target/release/chaoset-settings.exe" ]; then
  echo "==> 拷贝 Slint 设置中心 (chaoset-settings.exe)..."
  cp "shims/settings/target/release/chaoset-settings.exe" "$OUTDIR/chaoset-settings.exe"
fi

# 5. 生成与编译 Windows 资源文件
echo "==> 生成与编译 Windows 资源 (chaoset64.res, chaoset32.res, chaoset-ui.res)..."
gen_rc() {
  local target_rc="$1"
  local desc="$2"
  local internal="$3"
  local orig="$4"
  local ftype="$5"
  local extra="$6"
  sed -e "s/@VERSION@/$VERSION/g" \
      -e "s/@VERSION_QUAD@/$QUAD/g" \
      -e "s/@VER_MAJOR@/$VMAJOR/g" \
      -e "s/@VER_MINOR@/$VMINOR/g" \
      -e "s/@VER_PATCH@/$VPATCH/g" \
      -e "s/@VER_BUILD@/$VBUILD/g" \
      -e "s/@FILE_DESCRIPTION@/$desc/g" \
      -e "s/@INTERNAL_NAME@/$internal/g" \
      -e "s/@ORIGINAL_FILENAME@/$orig/g" \
      -e "s/@FILE_TYPE@/$ftype/g" \
      -e "s|@EXTRA_RESOURCE@|$extra|g" \
      shims/windows/resources/chaoset.rc > "$target_rc"
}

gen_rc "$OUTDIR/chaoset64.rc" "Chaoset 拼音输入法 TSF 前端（64 位）" "ChaosetIME" "ChaosetIME.dll" 2 ""
gen_rc "$OUTDIR/chaoset32.rc" "Chaoset 拼音输入法 TSF 前端（32 位）" "ChaosetIME32" "ChaosetIME32.dll" 2 ""
gen_rc "$OUTDIR/chaoset-ui.rc" "Chaoset 拼音输入法 UI 宿主（托盘中英状态 + 设置窗）" "chaoset-ui" "chaoset-ui.exe" 1 '1 24 "chaoset-ui.manifest"'

rc.exe /c 65001 /l 0x0804 /i shims/windows/resources /fo "$OUTDIR/chaoset64.res" "$OUTDIR/chaoset64.rc"
rc.exe /c 65001 /l 0x0804 /i shims/windows/resources /fo "$OUTDIR/chaoset32.res" "$OUTDIR/chaoset32.rc"
rc.exe /c 65001 /l 0x0804 /i shims/windows/resources /fo "$OUTDIR/chaoset-ui.res" "$OUTDIR/chaoset-ui.rc"

# 6. 确定 C++ 源码列表 (排除 ui_main.cpp)
DLL_SRCS=""
for f in shims/windows/src/*.cpp; do
  if [ "$f" != "shims/windows/src/ui_main.cpp" ]; then
    DLL_SRCS="$DLL_SRCS $f"
  fi
done
DLL_SRCS="$DLL_SRCS shims/common/ipc/core.cpp"

# 7. MSVC 编译 64 位 ChaosetIME.dll
echo "==> MSVC 编译 64 位 ChaosetIME.dll..."
cl.exe /nologo /std:c++17 /O2 /W4 /LD /EHsc \
  /DCHAOSET_VERSION="\"$VERSION\"" \
  /DUNICODE /D_UNICODE /DNOMINMAX /D_CRT_SECURE_NO_WARNINGS \
  /Ishims/windows/src /Ishims/common/ipc \
  /Fe:"$OUTDIR/ChaosetIME.dll" \
  $DLL_SRCS "$OUTDIR/chaoset64.res" \
  /link /DEF:shims/windows/resources/ChaosetIME.def \
  ole32.lib oleaut32.lib uuid.lib user32.lib kernel32.lib advapi32.lib gdi32.lib shell32.lib comdlg32.lib imm32.lib

# 8. MSVC 编译 64 位单例 UI 宿主 chaoset-ui.exe
echo "==> MSVC 编译 64 位单例 UI 宿主 chaoset-ui.exe..."
cl.exe /nologo /std:c++17 /O2 /W4 /EHsc \
  /DCHAOSET_VERSION="\"$VERSION\"" \
  /DUNICODE /D_UNICODE /DNOMINMAX /D_CRT_SECURE_NO_WARNINGS \
  /Ishims/windows/src /Ishims/common/ipc \
  /Fe:"$OUTDIR/chaoset-ui.exe" \
  shims/windows/src/ui_main.cpp \
  "$OUTDIR/chaoset-ui.res" \
  /link /SUBSYSTEM:WINDOWS \
  user32.lib kernel32.lib shell32.lib advapi32.lib

# 9. MSVC 编译 32 位 ChaosetIME32.dll
echo "==> MSVC 编译 32 位 WOW64 ChaosetIME32.dll..."
VCVARS32=""
if command -v vswhere.exe >/dev/null 2>&1; then
  VCVARS32=$(vswhere.exe -latest -products * -find "VC\\Auxiliary\\Build\\vcvarsamd64_x86.bat" | head -n 1 | tr -d '\r')
  if [ -z "$VCVARS32" ]; then
    VCVARS32=$(vswhere.exe -latest -products * -find "VC\\Auxiliary\\Build\\vcvars32.bat" | head -n 1 | tr -d '\r')
  fi
fi
if [ -z "$VCVARS32" ] && [ -n "${VSINSTALLDIR:-}" ]; then
  if [ -f "${VSINSTALLDIR}VC\\Auxiliary\\Build\\vcvarsamd64_x86.bat" ]; then
    VCVARS32="${VSINSTALLDIR}VC\\Auxiliary\\Build\\vcvarsamd64_x86.bat"
  elif [ -f "${VSINSTALLDIR}VC\\Auxiliary\\Build\\vcvars32.bat" ]; then
    VCVARS32="${VSINSTALLDIR}VC\\Auxiliary\\Build\\vcvars32.bat"
  fi
fi
echo "==> 32 位编译环境脚本: $VCVARS32"

cat << EOF > build32.bat
@echo off
call "$VCVARS32"
if errorlevel 1 (
  echo Error: vcvars failed
  exit /b 1
)
cl.exe /nologo /std:c++17 /O2 /W4 /LD /EHsc /DCHAOSET_VERSION=\\"$VERSION\\" /DUNICODE /D_UNICODE /DNOMINMAX /D_CRT_SECURE_NO_WARNINGS /Ishims/windows/src /Ishims/common/ipc /Fe:"$OUTDIR/ChaosetIME32.dll" $DLL_SRCS "$OUTDIR/chaoset32.res" /link /DEF:shims/windows/resources/ChaosetIME.def ole32.lib oleaut32.lib uuid.lib user32.lib kernel32.lib advapi32.lib gdi32.lib shell32.lib comdlg32.lib imm32.lib
if errorlevel 1 (
  echo Error: 32-bit cl.exe failed
  exit /b 1
)
EOF
sed -i 's/\r$//' build32.bat
sed -i 's/$/\r/' build32.bat

cmd.exe /c build32.bat || { echo "32位构建失败"; exit 1; }
rm -f build32.bat

# 10. 拷贝其它发行文件
echo "==> 拷贝词库与配置文件..."
cp data/dict_common.tsv "$OUTDIR/dict.tsv"
cp shims/windows/resources/chaoset.ico "$OUTDIR/chaoset.ico"
cp shims/windows/install.bat "$OUTDIR/install.bat"
cp shims/windows/README.md "$OUTDIR/README.md"
cp LICENSE "$OUTDIR/LICENSE"

# 清理构建过程中的临时 .obj / .res / .rc / .lib / .exp
rm -f "$OUTDIR"/*.obj "$OUTDIR"/*.res "$OUTDIR"/*.rc "$OUTDIR"/*.lib "$OUTDIR"/*.exp

# 11. 校验生成文件与导出符号
echo "==> 校验生成的二进制与 COM 导出符号..."
for f in install.bat dict.tsv ChaosetIME.dll ChaosetIME32.dll chaosetd.exe chaosetctl.exe chaoset-ui.exe README.md; do
  test -f "$OUTDIR/$f" || { echo "缺少必要文件: $f"; ls -la "$OUTDIR"; exit 1; }
done

dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME.dll" | grep -q DllRegisterServer || { echo "ChaosetIME.dll 缺少 DllRegisterServer 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME.dll" | grep -q DllUnregisterServer || { echo "ChaosetIME.dll 缺少 DllUnregisterServer 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME.dll" | grep -q DllGetClassObject || { echo "ChaosetIME.dll 缺少 DllGetClassObject 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME.dll" | grep -q DllCanUnloadNow || { echo "ChaosetIME.dll 缺少 DllCanUnloadNow 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME.dll" | grep -q ChaosetRunUi || { echo "ChaosetIME.dll 缺少 ChaosetRunUi 导出"; exit 1; }

dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME32.dll" | grep -q DllRegisterServer || { echo "ChaosetIME32.dll 缺少 DllRegisterServer 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME32.dll" | grep -q DllUnregisterServer || { echo "ChaosetIME32.dll 缺少 DllUnregisterServer 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME32.dll" | grep -q DllGetClassObject || { echo "ChaosetIME32.dll 缺少 DllGetClassObject 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME32.dll" | grep -q DllCanUnloadNow || { echo "ChaosetIME32.dll 缺少 DllCanUnloadNow 导出"; exit 1; }
dumpbin.exe /EXPORTS "$OUTDIR/ChaosetIME32.dll" | grep -q ChaosetRunUi || { echo "ChaosetIME32.dll 缺少 ChaosetRunUi 导出"; exit 1; }

# 检查 chaoset-ui.exe 是否没有静态链接 ChaosetIME.dll
if dumpbin.exe /DEPENDENTS "$OUTDIR/chaoset-ui.exe" | grep -qi "ChaosetIME"; then
  echo "错误：chaoset-ui.exe 静态导入了 ChaosetIME.dll（必须 LoadLibrary）"; exit 1
fi

# 12. 编译 NSIS 安装包
echo "==> 编译 NSIS 安装包 (ChaosetSetup.exe)..."
MAKENSIS="makensis"
if ! command -v "$MAKENSIS" >/dev/null 2>&1; then
  for p in \
    "/c/Program Files (x86)/NSIS/makensis.exe" \
    "/c/Program Files/NSIS/makensis.exe" \
    "/c/ProgramData/chocolatey/bin/makensis.exe" \
    "C:/Program Files (x86)/NSIS/makensis.exe" \
    "C:/Program Files/NSIS/makensis.exe" \
    "makensis.exe"; do
    if [ -f "$p" ]; then
      MAKENSIS="$p"
      break
    fi
  done
fi

"$MAKENSIS" /DVERSION="$VERSION" /DVERSION_QUAD="$QUAD" shims/windows/installer/chaoset.nsi
test -f "dist/ChaosetSetup.exe" || { echo "缺少 ChaosetSetup.exe 安装程序"; exit 1; }

# 13. 生成 ZIP 压缩包
echo "==> 生成 ZIP 便携包 (chaoset-windows-x64.zip)..."
SEVENZIP="7z"
if ! command -v "$SEVENZIP" >/dev/null 2>&1; then
  for p in \
    "/c/Program Files/7-Zip/7z.exe" \
    "/c/ProgramData/chocolatey/bin/7z.exe" \
    "C:/Program Files/7-Zip/7z.exe" \
    "7z.exe"; do
    if [ -f "$p" ]; then
      SEVENZIP="$p"
      break
    fi
  done
fi

"$SEVENZIP" a -tzip "dist/chaoset-windows-x64.zip" "./$OUTDIR/*"
test -f "dist/chaoset-windows-x64.zip" || { echo "缺少 dist/chaoset-windows-x64.zip"; exit 1; }

echo "==> Windows 原生编译与打包全部成功！"
