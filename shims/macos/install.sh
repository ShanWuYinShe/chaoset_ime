#!/bin/sh
# 把构建好的 ChaosetIME.app 安装为当前用户的输入法，并登记给 TIS。
#
# ⚠️ 必须注销重登：TISRegisterInputSource 只完成"登记"，macOS 的输入源表在
# **登录时**才扩充新输入法。未重登前 build/tis list 里不会有本输入法，系统
# 设置里也看不到。官方 Squirrel 安装包同样如此（PackageInfo 里
# postinstall-action="logout"；其 postinstall 亦调用 --register-input-source
# 与 --enable-input-source / --select-input-source）。
set -e
cd "$(dirname "$0")"

APP=build/ChaosetIME.app
IM="$HOME/Library/Input Methods"

[ -d "$APP" ] || { echo "缺少 $APP，请先运行 ./build.sh"; exit 1; }

mkdir -p "$IM"
# 同名 bundle 并存时系统可能沿用旧缓存，先移除旧输入法再拷入
rm -rf "$IM/ChaosetIME.app"
cp -R "$APP" "$IM/"
echo "已安装: $IM/ChaosetIME.app"

if [ -x build/tis ]; then
  build/tis register "$IM/ChaosetIME.app" || true
  # 与官方 Squirrel postinstall 的 --enable-input-source / --select-input-source 对应：
  # 尽力把本输入法加入"已启用"列表并选中。系统输入源表尚未收录时这两步会失败，
  # 属正常现象（表只在登录时扩充），失败不影响后续流程。
  build/tis enable "ime.chaoset.inputmethod" || true
  build/tis enable "ime.chaoset.inputmethod.Hans" || true
  build/tis select "ime.chaoset.inputmethod.Hans" || true
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
