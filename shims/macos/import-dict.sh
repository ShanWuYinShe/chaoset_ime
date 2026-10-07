#!/bin/sh
# 导入"别人编辑好的词库"：合并 → 备份 → 原子替换 → 重启 daemon，一条命令完成。
#
# 用法（在 shims/macos 目录下）:
#   ./import-dict.sh 别人给的词库.yaml            # 保留现用词库，并入新词库
#   ./import-dict.sh 词库A.yaml 词库B.tsv         # 可一次给多个
#   ./import-dict.sh --no-current 新词库.yaml     # 不并入现用词库（只 种子 + 新词库）
#
# 支持格式：Rime *.dict.yaml / CC-CEDICT 风格 txt / 原生 TSV（词<TAB>拼音<TAB>词频）。
# 环境变量（测试用）：CHAOSET_SUPPORT_DIR 覆盖用户数据目录，CHAOSET_SOCKET 覆盖套接字路径。
set -e
cd "$(dirname "$0")"

ROOT="../.."
IMPORTER="$ROOT/bin/chaoset-import"
SEED="$ROOT/data/dict_seed.tsv"
SUPPORT="${CHAOSET_SUPPORT_DIR:-$HOME/Library/Application Support/chaoset}"
DICT="$SUPPORT/dict.tsv"
SOCK="${CHAOSET_SOCKET:-$SUPPORT/chaoset.sock}"
# 中间产物放一次性目录：chaoset-import 会把 -o 指向的已存在文件吸收为底表
# （累积语义），共用固定路径会让同一份输入因历史残留产出不同结果，
# 破坏与图形工具的等价性。
TMP=$(mktemp -d)

use_current=yes
if [ "$1" = "--no-current" ]; then use_current=no; shift; fi
[ "$#" -ge 1 ] || { echo "用法: ./import-dict.sh [--no-current] <词库文件> [更多词库文件...]"; exit 2; }
[ -x "$IMPORTER" ] || { echo "缺少 ${IMPORTER}，请先在仓库根目录运行 make build"; exit 1; }
[ -f "$SEED" ] || { echo "缺少种子词库 $SEED"; exit 1; }
for f in "$@"; do [ -f "$f" ] || { echo "找不到词库文件: $f"; exit 1; }; done

mkdir -p "$SUPPORT"

printf '== 1) 合并词库 ==\n'
if [ "$use_current" = yes ] && [ -f "$DICT" ]; then
  "$IMPORTER" -o "$TMP/base.tsv" "$SEED" "$DICT"
else
  cp "$SEED" "$TMP/base.tsv"
  echo "   （不并入现用词库）"
fi
"$IMPORTER" -o "$TMP/merged.tsv" "$TMP/base.tsv" "$@"

printf '== 2) 备份并原子替换线上词库 ==\n'
if [ -f "$DICT" ]; then
  bak="$DICT.bak.$(date +%Y%m%d-%H%M%S)"
  cp "$DICT" "$bak"
  echo "   旧词库已备份: $bak"
else
  echo "   （原本没有线上词库，直接写入）"
fi
cp "$TMP/merged.tsv" "$DICT.tmp.$$"
mv "$DICT.tmp.$$" "$DICT"
echo "   已写入: ${DICT} （$(grep -c . "$DICT") 行，含注释行）"

printf '== 3) 重启 daemon 使其加载新词库 ==\n'
if [ "$SUPPORT" = "$HOME/Library/Application Support/chaoset" ]; then
  if killall chaosetd 2>/dev/null; then
    echo "   已请求重启（LaunchAgent 的 KeepAlive 会自动拉起）"
    sleep 2
  elif [ -x "$SUPPORT/chaosetd" ]; then
    # daemon 原本就没在跑：直接以已部署版本启动（与图形工具行为一致）
    "$SUPPORT/chaosetd" -socket "$SOCK" -dict "$DICT" \
      -userdict "$SUPPORT/userdict.json" >/dev/null 2>&1 &
    echo "   daemon 原未运行，已启动"
    sleep 2
  else
    echo "   daemon 未在运行且未部署；启动方式见 shims/macos/README.md"
  fi
else
  echo "   （自定义 CHAOSET_SUPPORT_DIR：跳过重启）"
fi

printf '== 4) 复核 IPC 是否正常 ==\n'
if [ -x build/smoke ] && [ -S "$SOCK" ]; then
  build/smoke "$SOCK" | tail -1 | sed 's/^/   /'
else
  echo "   （缺 build/smoke 或套接字，跳过；可运行 ./verify.sh）"
fi

printf '\n完成。词库前 5 条样例（可直接试打）:\n'
grep -v '^#' "$DICT" | head -5 | sed 's/^/   /'
rm -rf "$TMP"
