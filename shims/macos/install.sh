#!/bin/sh
# 把构建好的 ChaosetIME.app 安装为当前用户的输入法，并登记给 TIS。
#
# 2026-10-21 实测：bundle id 有效时无需注销重登——拷入 + TISRegisterInputSource
# 后系统立即枚举到本输入法，可直接在系统设置里添加。早前"必须注销重登"的结论
# 源于无效 bundle id（以 inputmethod 结尾）导致的 TIS 审讯拒绝，详见 README。
# 若改过 bundle id 后列表暂不可见，注销重登一次即可刷新。
set -e
cd "$(dirname "$0")"

APP=build/ChaosetIME.app
IM="$HOME/Library/Input Methods"

[ -d "$APP" ] || { echo "缺少 ${APP}，请先运行 ./build.sh"; exit 1; }

mkdir -p "$IM"
# 同名 bundle 并存时系统可能沿用旧缓存，先移除旧输入法再拷入；
# 旧版备份移至 .workwork/backups（不能以 .app 后缀残留在 Input Methods 目录，
# 否则 LaunchServices 会并存多个同 ID 的 app，导致系统设置和图标服务持续命中旧备份）。
if [ -d "$IM/ChaosetIME.app" ]; then
  BAK_DIR="../../.workwork/backups"
  mkdir -p "$BAK_DIR"
  bak="$BAK_DIR/ChaosetIME-$(date +%Y%m%d-%H%M%S)"
  mv "$IM/ChaosetIME.app" "$bak"
  echo "旧输入法已备份: $bak"
fi
# 清理可能遗留的旧备份 app
rm -rf "$IM"/ChaosetIME.app.bak.* "$IM"/ChaosetIME.app.old.* 2>/dev/null || true
cp -R "$APP" "$IM/"
echo "已安装: $IM/ChaosetIME.app"

# 让 LaunchServices 认识新拷贝的 bundle：imklaunchagent 按需拉起输入法走
# LaunchServices，未重新登记时会出现「菜单里能选，但选中后进程不启动、打字没反应」
# （2026-10-22 实测）。
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
"$LSREGISTER" -f "$IM/ChaosetIME.app" 2>/dev/null || true

if [ -x build/tis ]; then
  # 拷贝/替换 bundle 会触发 TIS 缓存（IntlDataCache）重建，而该重建只移除/刷新、
  # 不新增第三方输入源。若登记动作落在重建落盘之前，条目会被随后覆盖：表现为
  # 输入菜单里本输入法变灰/不可用（com.apple.inputsources 里仍有启用记录）。
  # 所以先等重建结束，再登记并确认系统表里能看到（2026-10-22 实测）。
  sleep 2
  # 清理本地 build 目录与历史嵌套 app 在 LaunchServices 中的旧登记，杜绝多路径被 TIS 索引导致重复输入法
  "$LSREGISTER" -u "$(pwd)/build/ChaosetIME.app" 2>/dev/null || true
  "$LSREGISTER" -u "$(pwd)/build/ChaosetSetup.app" 2>/dev/null || true
  "$LSREGISTER" -u "$HOME/Applications/ChaosetSetup.app/Contents/Resources/ChaosetIME.app" 2>/dev/null || true
  rm -f /var/folders/*/*/*/com.apple.IntlDataCache* 2>/dev/null || true

  for i in 1 2 3; do
    build/tis register "$IM/ChaosetIME.app" || true
    sleep 1
    if build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then
      break
    fi
    echo "（第 $i 次登记后系统表仍未见本输入法，重试…）"
  done
  # 仅启用具体的输入模式 Hans，绝不单独启用父级容器 bundle（否则状态栏与切换 HUD 会出现两个相同的 Chaoset 图标）
  build/tis enable "ime.chaoset.inputmethod.ChaosetIME.Hans" || true
  build/tis select "ime.chaoset.inputmethod.ChaosetIME.Hans" || true
  build/tis sync || true

  # 重启输入法进程以加载新 bundle（使用 open -a 单实例拉起，杜绝多重进程）
  pkill -9 -f "ChaosetIME.app/Contents/MacOS/ChaosetIME" 2>/dev/null || true
  pkill -9 -f "ChaosetIME" 2>/dev/null || true
  sleep 1
  open -a "$IM/ChaosetIME.app" 2>/dev/null || true

  # 刷新系统输入菜单、系统设置与快捷键切换 HUD（彻底清除内存中残留的旧图标缓存）
  killall "System Settings" 2>/dev/null || true
  pkill -9 -f "KeyboardSettings" 2>/dev/null || true
  pkill -9 -f "TextInputSwitcher" 2>/dev/null || true
  pkill -9 -f "TextInputMenuAgent" 2>/dev/null || true
  pkill -9 -f "CursorUIViewService" 2>/dev/null || true
else
  echo "（未找到 build/tis，跳过显式注册；拷入该目录通常已足够）"
fi

# 2) 部署 daemon 与种子词库（壳靠它出候选；已存在则备份旧版并热替换）
SUPPORT="$HOME/Library/Application Support/chaoset"
if [ -x build/chaosetd ]; then
  mkdir -p "$SUPPORT"
  if [ -f "$SUPPORT/chaosetd" ]; then
    cp "$SUPPORT/chaosetd" "$SUPPORT/chaosetd.prev"
  fi
  cp build/chaosetd "$SUPPORT/chaosetd"
  chmod 755 "$SUPPORT/chaosetd"
  # 词库部署：内置常用词库（data/dict_common.tsv，约 14 万条）是默认词库。
  # 已存在的旧词库不能盲目覆盖（用户可能导入过自己的词库），但 2026-10 之前的
  # 早期安装会留下只含 Rime terra 单字表的旧 dict.tsv（约 7 万条、缺「经常」等
  # 常用二字词，表现为「输入 jingchang 无候选」）——这类明显退化的词库要自动升级。
  # 判据（可验证、不猜）：旧词库缺「经常」条目，且条目数不到内置词库的一半。
  BUILTIN_DICT=../../data/dict_common.tsv
  if [ -f "$BUILTIN_DICT" ]; then
    need_upgrade=no
    reason=""
    if [ ! -f "$SUPPORT/dict.tsv" ]; then
      need_upgrade=yes
      reason="原本没有词库"
    elif grep -q "^经常	" "$SUPPORT/dict.tsv"; then
      reason="旧词库已含「经常」，保留用户现用词库"
    else
      old_lines=$(grep -c "" "$SUPPORT/dict.tsv" || true)
      builtin_lines=$(grep -c "" "$BUILTIN_DICT" || true)
      if [ "$old_lines" -lt $((builtin_lines / 2)) ]; then
        need_upgrade=yes
        reason="旧词库只有 ${old_lines} 行（内置 ${builtin_lines} 行）且缺「经常」"
      else
        reason="旧词库条目足够（${old_lines} 行），保留用户现用词库"
      fi
    fi
    if [ "$need_upgrade" = yes ]; then
      if [ -f "$SUPPORT/dict.tsv" ]; then
        cp "$SUPPORT/dict.tsv" "$SUPPORT/dict.tsv.bak.$(date +%Y%m%d-%H%M%S)"
        echo "   旧词库已备份"
      fi
      cp "$BUILTIN_DICT" "$SUPPORT/dict.tsv"
      echo "   已部署内置常用词库（$reason）"
    else
      echo "   词库保持不变（$reason）"
    fi
  fi
  # LaunchAgent（KeepAlive）会自动拉起新版本；未配置自启时手动启动即可：
  #   "$SUPPORT/chaosetd" -socket "$SUPPORT/chaoset.sock" \
  #     -dict "$SUPPORT/dict.tsv" -userdict "$SUPPORT/userdict.json"
  killall chaosetd 2>/dev/null || true
  echo "已部署 daemon: $SUPPORT/chaosetd（旧版备份为 chaosetd.prev）"
else
  echo "（未找到 build/chaosetd，跳过 daemon 部署）"
fi

# 3) 清理旧版 LaunchAgent（已弃用：系统 IMK 框架会自动拉起输入法进程，
# 额外的 LaunchAgent 会导致活动监视器中出现两个 ChaosetIME 进程）。
LA="$HOME/Library/LaunchAgents/ime.chaoset.inputmethod.plist"
if [ -f "$LA" ]; then
  launchctl bootout "gui/$(id -u)/ime.chaoset.inputmethod" 2>/dev/null || true
  rm -f "$LA"
  echo "已清理旧版输入法常驻 LaunchAgent（系统会自动管理输入法进程）"
fi

# 4) 安装图形化工具到 ~/Applications —— 之后安装/更新、导入词库、看状态都能点界面完成，
#    不必再用终端（面向不用命令行的用户）。
if [ -d build/ChaosetSetup.app ]; then
  mkdir -p "$HOME/Applications"
  rm -rf "$HOME/Applications/ChaosetSetup.app"
  cp -R build/ChaosetSetup.app "$HOME/Applications/"
  echo "已安装图形界面工具: ~/Applications/Chaoset.app（Finder/启动台里双击打开）"
fi

# 4) 自检并给出结论（一条命令跑完就能知道下一步）
echo
if [ -x build/tis ] && build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then
  echo "✓ 系统输入源表里已能看到本输入法。"
  echo "  下一步：系统设置 → 键盘 → 输入法 → 添加「Chaoset」。"
else
  echo "✗ 系统输入源表里还看不到本输入法——这是正常现象：macOS 的输入源表只在【登录时】扩充。"
  echo "  下一步（必须）：注销并重新登录，然后运行 ./verify.sh 复核（第 2 段应变成「OK: 系统已收录」）。"
  echo "  想先试免重登：在普通终端里运行 ./reregister.sh（它会查权威输入源表，可见则自动 enable+select）。"
fi