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
- **Win32 胶水层：编译级验证**——`x86_64-w64-mingw32-g++` 零警告编译，
  导出 `DllRegisterServer/DllUninstallServer/DllGetClassObject/DllCanUnloadNow`。
- **未验证（需 Windows 真机）**：COM 注册表启用流程、TSF 按键拦截与合成
  渲染的实际行为、UWP/AppContainer 场景。

## 安装（Windows 真机）

### 方式一：用发布包（推荐）

从 GitHub Release 下载 `chaoset-windows-x64.zip`，解包后：

1. **右键 `install.bat` → 以管理员身份运行**（TSF 输入法必须 regsvr32 注册 COM，需要管理员权限）；
2. 按提示打开「设置 → 时间和语言 → 语言和区域」，在中文(简体)下「添加键盘」选 **Chaoset**；
3. `Win+Space` 切到 Chaoset 即可打字。

卸载：同样右键管理员运行 `install.bat uninstall`。

脚本做的事：`regsvr32 ChaosetIME.dll` 注册 COM/CTF TIPs → 把词库复制到
`%LOCALAPPDATA%\Chaoset\dict.tsv` → 以绝对路径 `-dict` 启动 daemon
（监听 `127.0.0.1:7949`）→ 日志写 `%LOCALAPPDATA%\Chaoset\chaosetd.log`。
用绝对路径是刻意的：daemon 不能依赖启动时的工作目录（Linux 端踩过同类坑）。

### 方式二：从源码

```sh
# macOS 上交叉编译（brew install mingw-w64）
./build.sh    # 产出 build/ChaosetIME.dll

# Windows 机器上（管理员）：
regsvr32 ChaosetIME.dll
chaosetd.exe -tcp 127.0.0.1:7949 -dict dict.tsv -userdict userdict.json
# 系统设置 → 时间和语言 → 语言和区域 → 添加输入法 ChaosetIME
# 卸载：regsvr32 /u ChaosetIME.dll
```

> `make package-windows` 产出的是可直接分发的 zip，内含
> `dict.tsv`（14 万条常用词库）、`install.bat`、DLL 与三个 exe。
| 预编辑经 ITfContextComposition 合成；上屏即合成文本替换 | 已实现（编译级） |
| UAC 完整性级别：v0 无自绘候选窗（数字盲选 + 预编辑显示），规避候选窗完整性问题 | 已实现（编译级） |
| `-` / `=` 候选翻页（US 布局，有候选时；边界 daemon 钳制） | 已实现（核心行为级） |
| Shift 空按切换中英（英文模式全放行；跟踪在按键事件层完成） | 已实现（编译级） |
| Shift+空格切换全角/半角标点（GetKeyState 查 Shift 状态） | 已实现（编译级） |
| UWP/AppContainer 场景、多会话（RDP/快切） | 待真机验证 |
| 候选窗 UI（ITfCandidateListUIElement 或自绘） | v1 规划 |

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

- v0 无候选窗：输入拼音看预编辑，数字/空格盲选候选（编号与 CLI 一致）。
  系统候选窗 UI 是下一个里程碑。
- 传输用 TCP 回环（stdlib 可监听，协议不变）；命名管道（会话隔离更干净）
  留待 Windows 环境验证后切换。
- 中文标点按 US 布局 OEM 虚键直映（`,` `.` `;` `[` `]` `\` `'`，已接
  core 行为测试）；shift 组合标点（`?` `!` `:` `"` `《》》` 等）需字符级
  识别（ToAscii），待真机联调时接入。
- mingw 头文件不含 `ITfCandidateListUIElement`（`ctffunc.h` 缺失），
  候选窗实施时需自带官方 SDK 接口声明（值从 Windows SDK 头文件原样提取）。
- `GUID_TFCAT_CATEGORY_OF_TIP` 的值不在 mingw uuid 库中，注册分类暂只
  登记键盘类；注册完备性待真机确认（`regsvr32` 报错与否即可判断）。
