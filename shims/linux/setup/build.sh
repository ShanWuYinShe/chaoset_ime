#!/bin/sh
# 构建 Linux 原生 GTK3 设置窗（不依赖 Python/PyGObject）。
# 用法：shims/linux/setup/build.sh [输出路径]
set -eu

OUT=${1:-chaoset-setup}
# 输出路径必须在 cd 前解析成绝对路径：打包脚本传的是相对仓库根目录的路径。
case "$OUT" in
    /*) ;;
    *) OUT="$(pwd)/$OUT" ;;
esac
cd "$(dirname "$0")"
CC=${CC:-cc}
# 版本号单一真源 scripts/version.sh：tag 触发的 CI 用 tag 号，其余用 commit
# hash；Makefile 通过 VERSION 环境变量注入，直接跑时自己调同一个脚本。
ROOT=$(cd ../../.. && pwd)
if [ -z "${VERSION:-}" ]; then
    VERSION=$(sh "$ROOT/scripts/version.sh")
fi

# 源文件用通配：按页面/职责拆分后的 *.c 自动纳入，新增文件不用手改清单
# （2026-11-06 拆分）。test_setup_ctl.c 是无 GTK 单测（CI 单独编译运行），
# 不能链进设置窗。
SOURCES=""
for src in *.c; do
    if [ "$src" = "test_setup_ctl.c" ]; then
        continue
    fi
    SOURCES="$SOURCES $src"
done

# shellcheck disable=SC2086
$CC -std=c11 -O2 -Wall -Wextra -Werror \
    -DCHAOSET_VERSION="\"$VERSION\"" \
    $(pkg-config --cflags gtk+-3.0) \
    -o "$OUT" $SOURCES \
    $(pkg-config --libs gtk+-3.0)

echo "built $OUT"
