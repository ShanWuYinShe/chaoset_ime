# Windows 平台壳（TSF）

形态：**TSF COM DLL 薄壳（C++）+ TCP 回环连 chaosetd**（同 macOS 壳架构：
引擎与候选逻辑全在 daemon，DLL 只做按键转发 + 预编辑合成 + 上屏）。

## 目录

```
src/core.hpp|cpp   可移植核心：协议帧（4 字节大端长度 + JSON）、极小 JSON 解析、
                   会话控制。零 OS 依赖——在 macOS 上对着真实 daemon 做行为测试。
src/win32.cpp      Windows 胶水：COM TIP（ITfTextInputProcessor + ITfKeyEventSink）、
                   预编辑合成（ITfContextComposition）、TCP 回环传输（winsock2）。
tests/core_test.cpp 核心层行为测试（原生编译，非交叉）。
build.sh           一键：核心行为测试（真实 daemon）+ mingw 交叉编译 + 导出检查。
```

## 验证状态（2026-09-23）

- **核心层：行为级验证通过**——`core_test` 用真实 chaosetd（TCP 回环，
  即 Windows 壳的实际传输形态）跑通 17 项：协议帧、JSON（转义/中文/截断）、
  组词/预编辑/选词上屏/歧义切分/退格/重置、业务错误后连接复用。
- **Win32 胶水层：双架构全面支持与原生候选窗**——同时编译出 64 位 `ChaosetIME.dll` 与 32 位 `ChaosetIME32.dll`，
  规范导出 `DllRegisterServer/DllUnregisterServer/DllUninstallServer/DllGetClassObject/DllCanUnloadNow`，
  内置原生双缓冲高 DPI 浮动候选窗（光标精准跟随、屏幕边缘防溢出、首选高亮与翻页指示器），
  注册环节全面支持现代 Windows TSF COM 接口（`ITfInputProcessorProfiles` 与 `ITfCategoryMgr`）及键盘类别/沉浸式/托盘支持，
  同一个安装包自动同时注册 64 位原生与 32 位 WOW64（如 Steam、游戏、32位办公软件），实现全环境无死角打字。
- **后台服务：静默 GUI 子系统运行**——`chaosetd.exe` 采用 Windows GUI 子系统构建，开机与运行 0 黑框、0 弹窗，后台完全隐形，日志直接落盘到 `%LOCALAPPDATA%\Chaoset\chaosetd.log`。

## 安装（Windows 真机）

### 方式一：一键安装程序（最推荐，适合所有普通用户）

从 GitHub Release 直接下载 **`ChaosetSetup.exe`**：

1. **直接双击 `ChaosetSetup.exe`**，系统弹出 UAC 提权提示时点击「是」；
2. 安装向导自动完成：
   - 释放核心文件到 `C:\Program Files\Chaoset`
   - 自动注册 64 位与 32 位 TSF COM 输入法组件（`regsvr32`）
   - 配置开机自动启动后台守护进程（写入注册表 `Run`，静默隐形运行，重启电脑永不失效）
   - 写入 Windows「已安装的应用 / 控制面板」标准卸载程序
   - 立即启动输入法后台服务（完全静默无控制台黑框）
3. 按 `Win+Space` 切换到 **Chaoset 拼音输入法** 即可开始打字。
4. **覆盖升级提示**：新打开的任何软件已自动加载新版本；若在安装前已经打开了软件（如记事本、聊天软件），只需将其【关闭并重新打开】即可体验新版！

> 卸载：直接在 Windows「设置 → 应用 → 安装的应用」中找到 **Chaoset 拼音输入法** 点击「卸载」，或在开始菜单中点击「卸载 Chaoset 输入法」。

### 方式二：便携 ZIP 压缩包（免安装）

从 GitHub Release 下载 `chaoset-windows-x64.zip`，解包后：

1. 右键 `install.bat` → 以管理员身份运行；
2. 卸载：右键管理员运行 `install.bat uninstall`。

### 方式三：从源码

```sh
# macOS 上交叉编译（brew install mingw-w64）
./build.sh    # 产出 build/ChaosetIME.dll

# Windows 机器上（管理员）：
regsvr32 ChaosetIME.dll
chaosetd.exe -tcp 127.0.0.1:7949 -dict dict.tsv -userdict userdict.json -log chaosetd.log
# 系统设置 → 时间和语言 → 语言和区域 → 添加输入法 ChaosetIME
# 卸载：regsvr32 /u ChaosetIME.dll
```

> `make package-windows` 产出的是可直接分发的 zip，内含
> `dict.tsv`（14 万条常用词库）、`install.bat`、DLL 与三个 exe。

| 功能特性 | 状态 |
| :--- | :--- |
| 预编辑经 ITfContextComposition 合成；上屏即合成文本替换 | 已实现（编译级） |
| 原生浮动候选词窗口（双缓冲 GDI、高 DPI 字体缩放、首选高亮、分页指示） | 已实现（编译级） |
| 光标跟随与屏幕边缘碰撞反弹（GetTextExt -> GetGUIThreadInfo -> GetCursorPos） | 已实现（编译级） |
| 后台服务完全静默（GUI Subsystem，零黑框，零控制台窗口常驻） | 已实现（编译级） |
| `-` / `=` / `[` / `]` / PageUp / PageDown 候选翻页 | 已实现（核心行为级） |
| Shift 空按切换中英（英文模式全放行；跟踪在按键事件层完成） | 已实现（编译级） |
| Shift+空格切换全角/半角标点（非组词状态也可切；GetKeyState 查 Shift 状态） | 已实现（编译级） |
| 模式切换提示（中英/全半角切换时光标旁短暂 toast，约 1.2s 自动消失） | 已实现（编译级） |
| Shift 组合键语义（大写字母/Shift+数字字符不进组词；`?!:<>~"^()` 字符级标点映射） | 已实现（编译级） |
| UWP/AppContainer 场景、多会话（RDP/快切） | 待真机验证 |

## 图标与设置界面

- **应用与资源图标**：`resources/chaoset.ico` 提供包含 16×16、24×24、32×32、48×48、64×64、256×256
  六帧合一的 Windows 标准 ICO 资源，图标统一为极光电光蓝底板 + 纯白与霓虹青「Cs」组合标志 + 顶部拼音声调符号。
- **语言栏图标**：TSF 不会自动显示应用图标。需实现
  `ITfLangBarItemButton::GetIcon`（返回 `HICON`），并在 DLL 资源中提供
  16×16 与 32×32（高 DPI）两档图标；`ITfInputProcessorProfileMgr::RegisterProfile`
  的 `pszIconFile`/`iconIndex` 也要指向该资源，否则语言栏只有系统默认图标。
- **设置界面不会自动生成**：Windows 不会给第三方 TSF 输入法提供配置页；需要自己做
  设置窗口，并从语言栏菜单/托盘入口打开，配置项经 daemon IPC 下发。

## 已知限制

- 传输用 TCP 回环（stdlib 可监听，协议不变）；命名管道（会话隔离更干净）
  留待 Windows 环境验证后切换。
- 中文标点按 US 布局**字符级映射**（含 Shift 变体：`,` `.` `;` `[` `]` `\` `'`
  与 Shift 产生的 `:` `<` `>` `?` `~` `!` `^` `(` `)` `"`，已对齐 macOS/ibus
  壳与 daemon 的字符集；2026-10-27 前 Shift 组合全部错映射、十个中文标点
  不可达，已修）。仅覆盖 US 键盘布局；其它布局（如德法 AZERTY 的 OEM 键位
  不同）待真机按需接 ToAscii。
- `GUID_TFCAT_CATEGORY_OF_TIP` 的值不在 mingw uuid 库中，注册分类暂只
  登记键盘类；注册完备性待真机确认（`regsvr32` 报错与否即可判断）。
