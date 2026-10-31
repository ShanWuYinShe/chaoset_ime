#!/bin/sh
# 构建 Linux 原生 GTK3 设置窗（不依赖 Python/PyGObject）。
# 用法：shims/linux/setup/build.sh [输出路径]
set -eu

cd "$(dirname "$0")"
OUT=${1:-chaoset-setup}
CC=${CC:-cc}

# shellcheck disable=SC2086
$CC -std=c11 -O2 -Wall -Wextra -Werror \
    $(pkg-config --cflags gtk+-3.0) \
    -o "$OUT" chaoset-setup.c setup_ctl.c \
    $(pkg-config --libs gtk+-3.0)

echo "built $OUT"