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

mkdir -p "$IM" 2>/dev/null || true
# —— 运行环境预检 ——
# 本脚本要写 ~/Library/Input Methods、清 TIS 缓存并重启系统设置进程；这些操作在
# 沙盒/受限进程里会静默失败，表现就是「装完了但系统设置里的条目/图标还是旧的」。
# 因此先探测写权限，受限时直接给出明确指引，避免制造「看似成功」的假象。
if ! ( : > "$IM/.chaoset-write-test" ) 2>/dev/null; then
  echo "✗ 当前进程无权写入 ${IM}。"
  echo "  请在普通终端里直接运行：./shims/macos/install.sh"
  echo "  （沙盒/受限进程无法安装输入法，也无法可靠刷新 TIS 缓存与系统设置。）"
  exit 1
fi
rm -f "$IM/.chaoset-write-test"

# 同名 bundle 并存时系统可能沿用旧缓存，先移除旧输入法再拷入；
# 旧版备份移至 .workwork/backups（不能以 .app 后缀残留在 Input Methods 目录，
# 否则 LaunchServices 会并存多个同 ID 的 app，导致系统设置和图标服务持续命中旧备份）。
if [ -d "$IM/ChaosetIME.app" ]; then
  # 备份目录必须落在**用户数据目录**，不能是 ../../.workwork/backups：
  # 后者是开发仓库的中间产物目录，普通用户从下载包安装时根本不存在，
  # 且向上跳两级会指向任意路径（2026-10-26 审查指出）。
  BAK_DIR="$HOME/Library/Application Support/chaoset/backups"
  mkdir -p "$BAK_DIR"
  bak="$BAK_DIR/ChaosetIME-$(date +%Y%m%d-%H%M%S)"
  mv "$IM/ChaosetIME.app" "$bak"
  echo "旧输入法已备份: $bak"
  # 只保留最近 5 份备份：每份 app 约 15MB，无限累积会吃光用户磁盘。
  # 备份本身不带 .app 后缀，不会被 LaunchServices 当成第二个输入法索引。
  ls -1dt "$BAK_DIR"/ChaosetIME-* 2>/dev/null | tail -n +6 | while IFS= read -r old; do
    rm -rf "$old"
  done
fi
# 清理可能遗留的旧备份 app
rm -rf "$IM"/ChaosetIME.app.bak.* "$IM"/ChaosetIME.app.old.* "$IM/ChaosetIME.app" 2>/dev/null || true
ditto "$APP" "$IM/ChaosetIME.app"
echo "已安装: $IM/ChaosetIME.app"

# 让 LaunchServices 认识新拷贝的 bundle：imklaunchagent 按需拉起输入法走
# LaunchServices，未重新登记时会出现「菜单里能选，但选中后进程不启动、打字没反应」
# （2026-10-22 实测）。
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
"$LSREGISTER" -f "$IM/ChaosetIME.app" 2>/dev/null || true

# —— 系统 UI / TIS 缓存刷新 ——
# TIS 输入源表是「每进程一份」的 IntlDataCache.le[.kbdx]：系统设置、键盘设置扩展、
# 输入菜单与切换 HUD 各自持有一份。只清全局那份不够——旧图标/重复条目会留在
# 各进程的内存或容器缓存里。必须清掉全部副本并重启这些进程。
TIS_CACHE_DIR="$(dirname "${TMPDIR:-/tmp}")/C"

kill_input_source_ui() {
  killall "System Settings" 2>/dev/null || true
  pkill -9 -f "KeyboardSettings" 2>/dev/null || true
  pkill -9 -f "com.apple.Keyboard-Settings" 2>/dev/null || true
  pkill -9 -f "TextInputSwitcher" 2>/dev/null || true
  pkill -9 -f "TextInputMenuAgent" 2>/dev/null || true
  pkill -9 -f "CursorUIViewService" 2>/dev/null || true
}

clear_tis_caches() {
  find /private/var/folders -name "com.apple.IntlDataCache*" -delete 2>/dev/null || true
  [ -d "$TIS_CACHE_DIR" ] && find "$TIS_CACHE_DIR" -name "com.apple.IntlDataCache*" -delete 2>/dev/null || true
}

if [ -x build/tis ]; then
  # 拷贝/替换 bundle 会触发 TIS 缓存（IntlDataCache）重建，而该重建只移除/刷新、
  # 不新增第三方输入源。若登记动作落在重建落盘之前，条目会被随后覆盖：表现为
  # 输入菜单里本输入法变灰/不可用（com.apple.inputsources 里仍有启用记录）。
  # 所以先清缓存/重启 UI、等重建结束，再登记并确认系统表里能看到（2026-10-22 实测）。
  kill_input_source_ui
  clear_tis_caches
  sleep 2

  # 清理本地 build、tmp 与历史嵌套 app 在 LaunchServices 中的旧登记，
  # 杜绝同一 bundle ID 从多个路径被 TIS/系统设置索引（会表现为重复条目）。
  "$LSREGISTER" -u "$(pwd)/build/ChaosetIME.app" 2>/dev/null || true
  "$LSREGISTER" -u "$(pwd)/build/ChaosetSetup.app" 2>/dev/null || true
  "$LSREGISTER" -u "$HOME/Applications/ChaosetSetup.app/Contents/Resources/ChaosetIME.app" 2>/dev/null || true
  "$LSREGISTER" -u "/private/tmp/ChaosetIME.app" 2>/dev/null || true
  "$LSREGISTER" -u "/private/tmp/ChaosetIME.app/ChaosetIME.app" 2>/dev/null || true
  rm -rf /private/tmp/ChaosetIME.app 2>/dev/null || true

  # 登记是幂等的：系统表里已有就跳过，避免重复 TISRegisterInputSource 触发多余通知。
  if ! build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then
    for i in 1 2 3; do
      build/tis register "$IM/ChaosetIME.app" || true
      sleep 1
      if build/tis list 2>/dev/null | grep -q "ime.chaoset.inputmethod"; then
        break
      fi
      echo "（第 $i 次登记后系统表仍未见本输入法，重试…）"
    done
  fi

  # 只禁用历史 .Hans 旧模式；**父容器必须和 Pinyin 模式一起启用**。
  # 2026-10-26 实测：父容器是 TIS 的骨架节点（系统「简体拼音」同样是
  # mode + container 两条且都 enabled），把它禁用会让系统清空
  # com.apple.inputsources 的 AppleEnabledThirdPartyInputSources，
  # 表现为「装完输入法从菜单栏整个消失」。
  build/tis disable "ime.chaoset.inputmethod.ChaosetIME.Hans" 2>/dev/null || true
  build/tis enable "ime.chaoset.inputmethod.ChaosetIME" || true
  build/tis enable "ime.chaoset.inputmethod.ChaosetIME.Pinyin" || true

  # 消除 TIS 表里的重复实例：同一 InputSourceID 出现多条（属性相同、指针不同）
  # 会让菜单栏/系统设置列出多个 Chaoset。必须在 enable 之后跑——它会先全部
  # disable 让 TIS 合并记录，再只启用一个（见 tis.swift 的 dedupe 注释）。
  build/tis dedupe || true
  build/tis select "ime.chaoset.inputmethod.ChaosetIME.Pinyin" || true
  build/tis sync || true

  # 重启输入法进程以加载新 bundle（使用 open -a 单实例拉起，杜绝多重进程）
  pkill -9 -f "ChaosetIME.app/Contents/MacOS/ChaosetIME" 2>/dev/null || true
  pkill -9 -f "ChaosetIME" 2>/dev/null || true
  sleep 1
  open -a "$IM/ChaosetIME.app" 2>/dev/null || true

  # 收起所有系统输入源 UI 并清掉各进程的 TIS 缓存，保证下次打开系统设置是全新状态
  kill_input_source_ui
  clear_tis_caches

  # 自检：系统表里应恰好 2 条（父容器 + Pinyin 模式）。>2 说明有历史残留，
  # 系统设置「添加输入法」里就会看到重复条目，需要再清一次缓存或注销重登。
  CHAOSET_COUNT=$(build/tis list 2>/dev/null | grep -c "ime.chaoset.inputmethod" || true)
  if [ "$CHAOSET_COUNT" -eq 2 ]; then
    echo "✓ TIS 输入源表：恰好 2 条（父容器 + Pinyin 模式）"
  elif [ "$CHAOSET_COUNT" -gt 2 ]; then
    echo "⚠ TIS 输入源表里出现 ${CHAOSET_COUNT} 条 Chaoset 条目（预期 2）。"
    echo "  已清缓存并重启系统设置进程；若仍见重复，请注销重登一次重建输入源表。"
  else
    echo "⚠ TIS 输入源表里只有 ${CHAOSET_COUNT} 条 Chaoset 条目（预期 2），可能尚未登记完成。"
  fi
else
  echo "（未找到 build/tis，跳过显式注册；拷入该目录通常已足够）"
fi

# 2) 确认 app 内置 daemon 与种子词库（运行组件随 ChaosetIME.app 安装，
#    Application Support 只放用户数据/词库/socket）
SUPPORT="$HOME/Library/Application Support/chaoset"
DAEMON="$IM/ChaosetIME.app/Contents/Resources/chaosetd"
if [ -x "$DAEMON" ]; then
  mkdir -p "$SUPPORT"
  # 词库部署：app 内置 dict_common.tsv 是默认词库，复制/合并到
  # $SUPPORT/dict.tsv 供 daemon -dict 加载。
  #
  # 升级既不能盲目覆盖（用户导入过自己的词库），也不能无条件保留——旧内置
  # 词库会永远不更新（2026-11-01 用户实测：v0.3.5 仍加载 2026-10-22 的旧词库，
  # 常用字「长(chang)」缺失，长拼音下连常用单字都选不到；旧判据只看「是否含
  # 经常」，而旧种子副本恰好有该词，于是永远不升级）。
  #
  # 判据全部基于文件自身可验证的特征（chaoset-import 的两套文件头可区分状态）：
  #   ① 无 dict.tsv                        → 复制内置词库；
  #   ② 首行是内置词库头「# chaoset 内置常用词库」→ 从未经 chaoset-import 合并过
  #      的**纯种子副本**（任意旧版本），可安全替换（先备份）；
  #   ③ 首行是 chaoset-import 输出头（或其它内容）→ 用户导入/合并过词库，
  #      不能整体覆盖：用 chaoset-import -replace 把新内置词库合并进去，内置词条
  #      刷新为新词频，用户新增词条保留（合并/备份/原子替换仍在 Go 工具内）。
  # dict.builtin.sha256 记录「词库已合并到哪个内置版本」：版本没变就跳过，
  # 避免每次安装都重写词库。
  BUILTIN_DICT="$IM/ChaosetIME.app/Contents/Resources/dict_common.tsv"
  [ -f "$BUILTIN_DICT" ] || BUILTIN_DICT=../../data/dict_common.tsv
  if [ -f "$BUILTIN_DICT" ]; then
    DICT_MARKER="$SUPPORT/dict.builtin.sha256"
    BUILTIN_SHA=$(shasum -a 256 "$BUILTIN_DICT" | awk '{print $1}')
    CURRENT_VERSION=""
    if [ -f "$DICT_MARKER" ]; then
      CURRENT_VERSION=$(cat "$DICT_MARKER" 2>/dev/null || true)
    fi
    if [ ! -f "$SUPPORT/dict.tsv" ]; then
      cp "$BUILTIN_DICT" "$SUPPORT/dict.tsv"
      printf '%s\n' "$BUILTIN_SHA" > "$DICT_MARKER"
      echo "   已部署内置常用词库（原本没有词库）"
    elif [ "$CURRENT_VERSION" = "$BUILTIN_SHA" ]; then
      echo "   词库已是最新内置版本，保持不变"
    elif printf '%s' "$(head -1 "$SUPPORT/dict.tsv" 2>/dev/null || true)" | grep -q '^# chaoset' \
         && ! printf '%s' "$(head -1 "$SUPPORT/dict.tsv" 2>/dev/null || true)" | grep -q 'chaoset-import'; then
      cp "$SUPPORT/dict.tsv" "$SUPPORT/dict.tsv.bak.$(date +%Y%m%d-%H%M%S)"
      cp "$BUILTIN_DICT" "$SUPPORT/dict.tsv"
      printf '%s\n' "$BUILTIN_SHA" > "$DICT_MARKER"
      echo "   已把旧的纯种子词库升级为新内置词库（原文件已备份）"
    elif [ -x "$IM/ChaosetIME.app/Contents/Resources/chaoset-import" ]; then
      if "$IM/ChaosetIME.app/Contents/Resources/chaoset-import" \
          -o "$SUPPORT/dict.tsv" -backup -replace "$BUILTIN_DICT"; then
        printf '%s\n' "$BUILTIN_SHA" > "$DICT_MARKER"
        echo "   已把新内置词库合并进用户词库（保留导入词条，内置词频刷新）"
      else
        echo "⚠ 合并新内置词库失败，保留现用词库；可重跑安装或手动导入词库" >&2
      fi
    else
      echo "⚠ 找不到随包 chaoset-import，无法安全合并旧词库；保留现用词库" >&2
    fi
  fi
  # 安装（或修正）daemon 的 LaunchAgent：KeepAlive 保证 daemon 被 kill /
  # 崩溃 / 重启电脑后自动拉起。
  #
  # 2026-10-26 修复：此前 install.sh **从不写这个 plist**，只在一个「已经
  # 存在」的分支里 kickstart。全新机器上（或旧 plist 被清理后）装完输入法
  # 能出现在菜单里，但 daemon 没人拉起——用户看到的就是「能切过去但打不出
  # 中文」。开发机因为早先手工建过 plist 才没暴露这个缺陷。
  DAEMON_LA="$HOME/Library/LaunchAgents/ime.chaoset.daemon.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  cat > "$DAEMON_LA" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>ime.chaoset.daemon</string>
	<key>ProgramArguments</key>
	<array>
		<string>$DAEMON</string>
		<string>-socket</string>
		<string>$SUPPORT/chaoset.sock</string>
		<string>-dict</string>
		<string>$SUPPORT/dict.tsv</string>
		<string>-userdict</string>
		<string>$SUPPORT/userdict.json</string>
		<string>-log</string>
		<string>$SUPPORT/chaosetd.log</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
</dict>
</plist>
PLIST
  plutil -lint "$DAEMON_LA" >/dev/null || { echo "✗ LaunchAgent plist 校验失败：$DAEMON_LA"; exit 1; }

  # 卸载旧定义 → 加载新定义。
  #
  # 2026-10-26 实机踩坑：原先 bootout 后直接 bootstrap，再紧跟一个
  # `kickstart -k`。三层竞争叠在一起——bootout 是异步的，bootstrap 可能在
  # 旧服务还没退干净时执行（静默失败），随后 kickstart 又去杀掉刚被拉起的
  # 新进程，最终结果就是「plist 已写、进程也短暂出现过，但服务没注册、
  # 套接字消失」。表现正是本脚本自检报的「daemon 已部署但 IPC 未就绪」。
  #
  # 现在：bootout 后等它真正从域里消失，bootstrap 失败要报错，且不再额外
  # kickstart——bootstrap 本身就会按 RunAtLoad 启动，KeepAlive 负责后续。
  LA_LABEL="ime.chaoset.daemon"
  LA_DOMAIN="gui/$(id -u)"
  launchctl bootout "$LA_DOMAIN/$LA_LABEL" 2>/dev/null || true
  # 等卸载真正生效（最多 3 秒），避免 bootstrap 撞上正在退出的旧服务
  j=0
  while [ $j -lt 30 ]; do
    launchctl print "$LA_DOMAIN/$LA_LABEL" >/dev/null 2>&1 || break
    j=$((j + 1))
    sleep 0.1
  done
  # bootout 只卸载 LaunchAgent，不会杀掉旧 daemon（旧版本可能仍在跑）。
  # 先杀残留进程并等它真正退出，再 bootstrap；否则新旧两个 daemon 会同时
  # 存在一段时间，Activity Monitor 里能看到两个 chaosetd。
  /usr/bin/killall chaosetd 2>/dev/null || true
  j=0
  while [ $j -lt 30 ]; do
    /usr/bin/pgrep -U "$(id -u)" -x chaosetd >/dev/null 2>&1 || break
    j=$((j + 1))
    sleep 0.1
  done
  if /usr/bin/pgrep -U "$(id -u)" -x chaosetd >/dev/null 2>&1; then
    echo "✗ 旧 chaosetd 未能退出，未启动新 daemon（避免出现两个进程）" >&2
    exit 1
  fi
  if ! launchctl bootstrap "$LA_DOMAIN" "$DAEMON_LA" 2>/dev/null; then
    # bootstrap 对「已加载」会返回非零；此时用 kickstart 重启让它读新二进制
    launchctl kickstart -k "$LA_DOMAIN/$LA_LABEL" 2>/dev/null || true
  fi
  launchctl enable "$LA_DOMAIN/$LA_LABEL" 2>/dev/null || true

  # 等 daemon 真正就绪：用 IPC 往返判定（套接字文件存在不代表有进程 listen）。
  # 最多等 10 秒（首次启动要加载 14 万词库，约 300ms；留足余量给慢机器）。
  ready=no
  i=0
  while [ $i -lt 50 ]; do
    if build/smoke "$SUPPORT/chaoset.sock" >/dev/null 2>&1; then ready=yes; break; fi
    i=$((i + 1))
    sleep 0.2
  done
  if [ "$ready" = yes ]; then
    echo "已确认 app 内置 daemon 的 IPC 就绪（LaunchAgent: ${LA_LABEL}）"
  else
    echo "⚠ daemon 已部署但 IPC 未就绪（10 秒内未响应）。排查："
    echo "   launchctl print $LA_DOMAIN/$LA_LABEL | head -30"
    echo "   日志：log stream --predicate 'process == \"chaosetd\"' --last 1m"
    echo "   手动启动：$DAEMON -socket $SUPPORT/chaoset.sock -dict $SUPPORT/dict.tsv -userdict $SUPPORT/userdict.json"
  fi
  # 迁移旧版布局：daemon 曾单放在 Application Support；新版只从 app 内运行。
  rm -f "$SUPPORT/chaosetd" "$SUPPORT/chaosetd.prev"
else
  echo "（ChaosetIME.app 内缺少 daemon，跳过 daemon 部署；请重新构建）"
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
  rm -rf "$HOME/Applications/ChaosetSetup.app" "$HOME/Applications/Chaoset.app"
  cp -R build/ChaosetSetup.app "$HOME/Applications/Chaoset.app"
  ln -sfn "$HOME/Applications/Chaoset.app" "$HOME/Applications/ChaosetSetup.app"
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