#!/bin/sh
# 命令行与图形界面的等价性测试（持续自证的根基）。
#
# 用户要求：UI 与 CLI 必须完全等价，CLI 的验证结论才对 UI 成立。
# 本脚本从三个层面断言等价，任何一层破坏都非零退出：
#   A. 同源——GUI 随包的 tis/smoke/chaoset-import/ChaosetIME 与构建产物的
#      CLI 版本逐字节相同（同源即同行为，杜绝实现漂移）；
#   B. 导入等价——同一份词库分别走 import-dict.sh 与 ChaosetSetup --selftest，
#      产出的 dict.tsv 必须逐字节相同；
#   C. 判定等价——GUI --verify 对「系统是否收录」的结论必须与 tis list
#      （独立 CLI 实现）一致，且 GUI 内嵌 TIS API 与 tis list 互检一致。
# 用法: ./tests/equivalence.sh
set -e
cd "$(dirname "$0")/.."

SETUP_APP=build/ChaosetSetup.app
SETUP_BIN="$SETUP_APP/Contents/MacOS/ChaosetSetup"
RES="$SETUP_APP/Contents/Resources"
fails=0
note() { printf '%s\n' "$*"; }
fail() { fails=$((fails + 1)); printf 'FAIL %s\n' "$*"; }
ok() { printf 'ok   %s\n' "$*"; }

[ -d "$SETUP_APP" ] || { echo "缺 ${SETUP_APP}，先跑 build.sh"; exit 2; }

note "== A. 同源检查：GUI 随包工具与构建产物的 CLI 版本逐字节相同 =="
for b in tis smoke chaoset-import chaosetd; do
  if cmp -s "$RES/$b" "build/$b"; then ok "A.$b 与 CLI 同源"; else fail "A.$b 与 CLI 版本不同源"; fi
done
if cmp -s "$RES/ChaosetIME.embedded/Contents/MacOS/ChaosetIME" build/ChaosetIME.app/Contents/MacOS/ChaosetIME; then
  ok "A.ChaosetIME 主程序与 CLI 构建同源"
else
  fail "A.ChaosetIME 主程序不同源"
fi

note "== B. 导入等价：同一词库走 CLI 脚本 vs GUI --selftest =="
FRIEND=${1:-../../.workwork/dict-import/friend.yaml}
[ -f "$FRIEND" ] || FRIEND=$(mktemp -d)/friend.yaml
if [ ! -s "$FRIEND" ]; then
  printf '# Rime dictionary\n---\nname: eq\nversion: "1"\n...\n混沌输入法\thundun shurufa\t88\n好朋友\thaopengyou\t77\n' > "$FRIEND"
fi
T1=$(mktemp -d); T2=$(mktemp -d)
cp ../../data/dict_seed.tsv "$T1/dict.tsv"
cp ../../data/dict_seed.tsv "$T2/dict.tsv"
CHAOSET_SUPPORT_DIR="$T1" CHAOSET_SOCKET="$T1/chaoset.sock" ./import-dict.sh "$FRIEND" >/dev/null 2>&1 || fail "B.CLI 导入脚本执行失败"
"$SETUP_BIN" --selftest "$T2" "$FRIEND" >/dev/null 2>&1 || fail "B.GUI 自检导入执行失败"
if cmp -s "$T1/dict.tsv" "$T2/dict.tsv"; then
  ok "B.两条路径产出的 dict.tsv 逐字节相同"
else
  fail "B.两条路径产出不一致（见 cmp $T1/dict.tsv $T2/dict.tsv）"
fi
rm -rf "$T1" "$T2"

note "== C. 判定等价：GUI --verify 与独立 CLI 实现互相印证 =="
CLI_HIT=$(build/tis list 2>/dev/null | grep -c "ime.chaoset.inputmethod" || true)
VOUT=$("$SETUP_BIN" --verify 2>/dev/null || true)
echo "$VOUT" | sed 's/^/   /'
echo "$VOUT" | grep -E -q "(两条实现结论一致|结论一致)" \
  && ok "C.GUI 内嵌 TIS 与 tis list 结论一致" \
  || fail "C.GUI 内嵌 TIS 与 tis list 结论不一致"
GUI_HIT=$(echo "$VOUT" | grep -c "PASS 2" || true)
if { [ "$CLI_HIT" -gt 0 ] && [ "$GUI_HIT" -gt 0 ]; } || { [ "$CLI_HIT" -eq 0 ] && [ "$GUI_HIT" -eq 0 ]; }; then
  ok "C.收录判定与独立 CLI 实现一致（tis 命中=${CLI_HIT}）"
else
  fail "C.收录判定不一致（tis 命中=${CLI_HIT}，GUI=${GUI_HIT}）"
fi

if [ "$fails" -eq 0 ]; then
  note "等价性检查全部通过"
else
  note "等价性检查失败 $fails 项"
fi
exit "$fails"
