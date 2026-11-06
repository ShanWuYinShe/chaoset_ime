#!/bin/sh
# 版本号单一真源（2026-11-06 用户规则）：
#
#   * 环境变量 CHAOS_VERSION_TAG 有值（GitHub CI 上 push tag 触发时设置），
#     就用 tag 里的规范版本号（去掉前导 v），如 0.3.17；
#   * 没值（本地、日常 CI、workflow_dispatch、PR、源码直跑），一律用
#     「构建当天年月日 + git commit hash」，如 20261106-8b2eb0ca0c38。
#
# 就一个环境变量的事：CI 在 tag 触发时把它设成 tag 版本，其他任何地方都不设，
# 脚本读不到就自己生成日期+hash。不在脚本里判断 GITHUB_*，也不取「最近的 tag」。
# 工作区有未提交改动时追加 -dirty：同一提交但代码不同的构建也能区分。
#
# 用法（所有输出都是单行，供 Makefile / 打包脚本 / CI 注入）：
#   scripts/version.sh                 显示用版本
#   scripts/version.sh --quad          4 段数字版本（NSIS / PE VS_FIXEDFILEINFO）
#   scripts/version.sh --pkg           包管理器版本（deb / Arch，保证以数字开头）
#   scripts/version.sh --source        来源说明（tag / date-hash / nogit）
#
# 派生规则（纯函数，同一输入永远同一输出）：
#   --quad：tag 版 x.y.z → x.y.z.0；其余 → 0.0.A.B，A/B 取 hash 前 8 位
#           十六进制每 4 位转成的十进制数（≤65535）。Windows 版本资源只接受
#           4 段数字，日期与完整显示版本放在属性页字符串字段里。
#   --pkg ：dpkg 的 Version 与 Arch 的 pkgver 必须是合法版本串；裸 hash 可能以
#           字母开头（dpkg 直接拒绝）。统一为 0.0.0+<日期>.<hash>[.dirty]：
#           以数字开头、含日期与 hash、每次构建可区分；人类可读字段仍显示
#           完整版本（含年月日）。
#   不在 git 仓库（源码 tarball）时显示 nogit，--pkg 为 0.0.0+nogit。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

# 把 4 位十六进制串转成十进制（POSIX awk，不依赖 bash / printf 进制）。
hex4dec() {
    printf '%s' "$1" | awk '
        { h = tolower($0); n = 0;
          for (i = 1; i <= 4; i++) {
              d = index("0123456789abcdef", substr(h, i, 1)) - 1;
              if (d < 0) { exit 1 }
              n = n * 16 + d
          }
          print n }'
}

mode="${1:-display}"
case "$mode" in
    display|--quad|--pkg|--source) ;;
    *) echo "用法: $0 [display|--quad|--pkg|--source]" >&2; exit 2 ;;
esac

hash=""
date_stamp=""
dirty=""
dirty_pkg=""
if [ -n "${CHAOS_VERSION_TAG:-}" ]; then
    version=$(printf '%s' "$CHAOS_VERSION_TAG" | sed 's/^v//')
    source="tag"
else
    hash=$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || true)
    if [ -n "$hash" ]; then
        source="date-hash"
        date_stamp=$(date +%Y%m%d)
        # 未提交的本地改动（排除未跟踪文件，构建产物不会误判）。
        if [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=no 2>/dev/null)" ]; then
            dirty="-dirty"
            dirty_pkg=".dirty"
        fi
        version="${date_stamp}-${hash}${dirty}"
    else
        version="nogit"
        source="nogit"
    fi
fi

# 数字版本只认 hash 主体（四段数字窗口有限，日期放在字符串字段）。
quad_hash=$(printf '%s' "$hash" | tr -cd '0-9a-fA-F' | cut -c1-8)

case "$mode" in
    display)
        printf '%s\n' "$version"
        ;;
    --source)
        printf '%s\n' "$source"
        ;;
    --quad)
        if [ "$source" = "tag" ]; then
            printf '%s\n' "$version" \
                | sed -E 's/^v//; s/[^0-9.].*$//; s/[.]+$//' \
                | awk -F. '{print ($1+0)"."($2+0)"."($3+0)"."($4+0)}'
        else
            hex="$quad_hash"
            while [ "${#hex}" -lt 8 ]; do hex="${hex}0"; done
            printf '0.0.%s.%s\n' "$(hex4dec "$(printf '%s' "$hex" | cut -c1-4)")" \
                "$(hex4dec "$(printf '%s' "$hex" | cut -c5-8)")"
        fi
        ;;
    --pkg)
        if [ "$source" = "tag" ]; then
            # tag 号本身已合法（以数字开头）；保留预发布后缀，只去掉前导 v。
            printf '%s\n' "$(printf '%s' "$version" | sed 's/^v//')"
        elif [ "$source" = "nogit" ]; then
            printf '0.0.0+nogit\n'
        else
            # dpkg 要求以数字开头；Arch pkgver 也接受 + 与 .。
            # -dirty 用 .dirty 表示，避免被 deb 当成 debian_revision 分隔符。
            printf '0.0.0+%s.%s%s\n' "$date_stamp" "$hash" "$dirty_pkg"
        fi
        ;;
esac
