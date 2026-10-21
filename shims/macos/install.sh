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
# 旧版备份（与图形工具「安装 / 更新」的行为保持一致）
if [ -d "$IM/ChaosetIME.app" ]; then
  bak="$IM/ChaosetIME.app.bak.$(date +%Y%m%d-%H%M%S)"
  mv "$IM/ChaosetIME.app" "$bak"
  echo "旧输入法已备份: $bak"
fi
cp -R "$APP" "$IM/"
echo "已安装: $IM/ChaosetIME.app"

if [ -x build/tis ]; then
  # 拷贝/替换 bundle 会触发 TIS 缓存（IntlDataCache）重建，而该重建只移除/刷新、
  # 不新增第三方输入源。若登记动作落在重建落盘之前，条目会被随后覆盖：表现为
  # 输入菜单里本输入法变灰/不可用（com.apple.inputsources 里仍有启用记录）。
  # 所以先等重建结束，再登记并确认系统表里能看到（2026-10-22 实测）。
  sleep 2
  for i in 1 2 3; do
    build/tis register "$IM/ChaosetIME.app" || true
    sleep 1
    if build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then
      break
    fi
    echo "（第 $i 次登记后系统表仍未见本输入法，重试…）"
  done
  # 与官方 Squirrel postinstall 的 --enable-input-source / --select-input-source 对应：
  # 尽力把本输入法加入"已启用"列表并选中。系统输入源表尚未收录时这两步会失败，
  # 属正常现象，失败不影响后续流程。
  build/tis enable "ime.chaoset.inputmethod.ChaosetIME" || true
  build/tis enable "ime.chaoset.inputmethod.ChaosetIME.Hans" || true
  build/tis select "ime.chaoset.inputmethod.ChaosetIME.Hans" || true
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
  if [ ! -f "$SUPPORT/dict.tsv" ]; then
    cp ../../data/dict_seed.tsv "$SUPPORT/dict.tsv"
  fi
  # LaunchAgent（KeepAlive）会自动拉起新版本；未配置自启时手动启动即可：
  #   "$SUPPORT/chaosetd" -socket "$SUPPORT/chaoset.sock" \
  #     -dict "$SUPPORT/dict.tsv" -userdict "$SUPPORT/userdict.json"
  killall chaosetd 2>/dev/null || true
  echo "已部署 daemon: $SUPPORT/chaosetd（旧版备份为 chaosetd.prev）"
else
  echo "（未找到 build/chaosetd，跳过 daemon 部署）"
fi

# 3) 安装图形化工具到 ~/Applications —— 之后安装/更新、导入词库、看状态都能点界面完成，
#    不必再用终端（面向不用命令行的用户）。
if [ -d build/ChaosetSetup.app ]; then
  mkdir -p "$HOME/Applications"
  rm -rf "$HOME/Applications/ChaosetSetup.app"
  cp -R build/ChaosetSetup.app "$HOME/Applications/"
  echo "已安装图形界面工具: ~/Applications/超集输入法.app（Finder/启动台里双击打开）"
fi

# 4) 自检并给出结论（一条命令跑完就能知道下一步）
echo
if [ -x build/tis ] && build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then
  echo "✓ 系统输入源表里已能看到本输入法。"
  echo "  下一步：系统设置 → 键盘 → 输入法 → 添加「超集拼音」（图标为中国印章）。"
else
  echo "✗ 系统输入源表里还看不到本输入法——这是正常现象：macOS 的输入源表只在【登录时】扩充。"
  echo "  下一步（必须）：注销并重新登录，然后运行 ./verify.sh 复核（第 2 段应变成「OK: 系统已收录」）。"
  echo "  想先试免重登：在普通终端里运行 ./reregister.sh（它会查权威输入源表，可见则自动 enable+select）。"
fi
