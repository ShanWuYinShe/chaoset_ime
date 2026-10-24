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
APP=build/ChaosetIME.app
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
  note "== D. 图标规范检查：系统同款扁平 keycap / 纯字形 / 应用图标三大契约 =="
  for f in ChaosetIME-input.tiff ChaosetIME-palette.tiff; do
    [ -f "$APP/Contents/Resources/$f" ] && ok "D.$f 已随 app 打包" || fail "D.$f 缺失"
  done
  for f in ChaosetIME-menu.tiff ChaosetIME-icon2.tiff; do
    [ ! -e "$APP/Contents/Resources/$f" ] && ok "D.$f 旧资源已移除" || fail "D.$f 旧资源仍残留"
  done

  # 1) input.tiff 必须是系统同款扁平 keycap：满幅实心圆角按键 + 挖空字形（不是描边空心块）
  swift -e '
  import AppKit
  let p = "build/ChaosetIME.app/Contents/Resources/ChaosetIME-input.tiff"
  guard let img = NSImage(contentsOfFile: p),
        let rep = img.representations.first(where: { $0.pixelsWide == 16 }) as? NSBitmapImageRep,
        img.representations.contains(where: { $0.pixelsWide == 32 }) else { exit(1) }
  func a(_ x: Int, _ y: Int) -> CGFloat { rep.colorAt(x: x, y: y)!.alphaComponent }
  var sum = 0.0
  var holes = 0
  for y in 2..<14 {
    for x in 2..<14 {
      let v = a(x, y)
      sum += Double(v)
      if v < 0.1 { holes += 1 }
    }
  }
  let mean = sum / 144.0
  let corner = [a(0, 0), a(15, 0), a(0, 15), a(15, 15)].max()!
  let edge = [a(0, 8), a(15, 8), a(8, 0), a(8, 15)].min()!
  if mean >= 0.60 && holes >= 8 && corner < 0.5 && edge > 0.9 { exit(0) } else { exit(2) }
  ' && ok "D.input.tiff 符合「实心圆角按键 + 挖空 C」系统同款扁平 keycap 契约" || fail "D.input.tiff 不是系统同款扁平 keycap"

  # 2) palette.tiff 必须无边框且无背景（仅单字符 C，供快捷键切换 HUD 使用）
  swift -e '
  import AppKit
  let p = "build/ChaosetIME.app/Contents/Resources/ChaosetIME-palette.tiff"
  guard let img = NSImage(contentsOfFile: p),
        let rep = img.representations.first(where: { $0.pixelsWide == 16 }) as? NSBitmapImageRep else { exit(1) }
  let edgeAlpha1 = rep.colorAt(x: 0, y: 7)!.alphaComponent
  let edgeAlpha2 = rep.colorAt(x: 3, y: 3)!.alphaComponent
  let cAlpha = rep.colorAt(x: 4, y: 8)!.alphaComponent
  if edgeAlpha1 == 0 && edgeAlpha2 == 0 && cAlpha > 0.8 { exit(0) } else { exit(2) }
  ' && ok "D.palette.tiff 符合「无边框仅单字符 C」契约" || fail "D.palette.tiff 未通过契约检查"

  # 3) 应用图标 icns 必须包含方案 3+4 结合版特征（极光蓝底 + 白色 C + 霓虹青 s）
  swift -e '
  import AppKit
  let p = "build/Resources/ChaosetIME.icns"
  guard let img = NSImage(contentsOfFile: p),
        let rep = img.representations.first(where: { $0.pixelsWide == 256 }) as? NSBitmapImageRep else { exit(1) }
  let bgCol = rep.colorAt(x: 128, y: 200)!
  let cCol = rep.colorAt(x: 84, y: 128)!
  let sCol = rep.colorAt(x: 156, y: 128)!
  let isBgBlue = bgCol.blueComponent > 0.7 && bgCol.redComponent < 0.3
  let isWhiteC = cCol.redComponent > 0.9 && cCol.greenComponent > 0.9 && cCol.blueComponent > 0.9
  let isCyanS = sCol.greenComponent > 0.7 && sCol.blueComponent > 0.8 && sCol.redComponent < 0.5
  if isBgBlue && isWhiteC && isCyanS { exit(0) } else { exit(2) }
  ' && ok "D.ChaosetIME.icns 符合「方案3与方案4结合版」契约" || fail "D.ChaosetIME.icns 未通过契约检查"

  # 4) Info.plist 的输入源图标键必须全部指向上述两份模板图（系统设置/菜单栏/系统栏取这里）
  PB=/usr/libexec/PlistBuddy
  P="$APP/Contents/Info.plist"
  MODE="ComponentInputModeDict:tsInputModeListKey:ime.chaoset.inputmethod.ChaosetIME.Pinyin"
  expect_plist() {
    got=$($PB -c "Print :$1" "$P" 2>/dev/null || true)
    if [ "$got" = "$2" ]; then ok "D.plist $1 -> $2"; else fail "D.plist $1 = ${got:-<空>}（期望 $2）"; fi
  }
  expect_plist "tsInputMethodIconFileKey" "ChaosetIME-input.tiff"
  expect_plist "tsInputMethodAlternateIconFileKey" "ChaosetIME-input.tiff"
  expect_plist "$MODE:tsInputModeMenuIconFileKey" "ChaosetIME-input.tiff"
  expect_plist "$MODE:tsInputModeAlternateMenuIconFileKey" "ChaosetIME-input.tiff"
  expect_plist "$MODE:tsInputModePaletteIconFileKey" "ChaosetIME-palette.tiff"
fi

if [ "$fails" -eq 0 ]; then
  note "等价性检查全部通过"
else
  note "等价性检查失败 $fails 项"
fi
exit "$fails"
