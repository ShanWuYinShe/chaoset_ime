# Windows 平台壳（TSF）

形态：**TSF COM DLL 薄壳（C++） + 命名管道连接 `chaosetd`**。引擎、词库、候选数据与
用户学习全部在 daemon；DLL 只负责按键路由、预编辑合成、上屏与原生候选窗。

## 目录

```
../common/ipc/core.hpp|cpp  跨平台 IPC 核心：4 字节大端长度 + JSON、会话控制；
                             Windows/Linux 原生壳共用。
src/win32.cpp               TSF COM、键盘/文本编辑会话、候选窗、传输、设置窗。
tests/core_test.cpp         核心层行为测试（Linux/CI 原生编译，真实 daemon）。
build.sh                    核心行为测试 + mingw 交叉编译（64/32 位 DLL）。
installer/chaoset.nsi       NSIS 安装器。
install.bat                 便携 ZIP 安装/卸载脚本。
```

## 构建

```sh
# macOS 上交叉编译（需要 brew install mingw-w64）。
# 产物：build/ChaosetIME.dll、build/ChaosetIME32.dll、build/core_test。
./shims/windows/build.sh

# 完整 Windows 分发包与 NSIS 安装包（需要 mingw-w64 + makensis）。
make package-windows
```

`make package-windows` 生成 `dist/chaoset-windows-x64.zip` 与 `dist/ChaosetSetup.exe`。
ZIP 内含 64/32 位 DLL、`chaosetd.exe`、`chaoset-cli.exe`、`chaoset-import.exe`、
`chaoset-userdict.exe`、`dict.tsv`、`install.bat` 和本 README。

## 安装

### 一键安装程序（推荐）

从 GitHub Release 下载 `ChaosetSetup.exe`，双击并允许 UAC。安装器会：

- 把程序安装到 `C:\Program Files\Chaoset`；
- 注册 64 位与 32 位 TSF COM 组件；
- 写 HKLM `Run` 项（`REG_EXPAND_SZ`，`%LOCALAPPDATA%` 由每个登录用户展开）；
- 用 Go 导入器把随包底库合并到当前用户 `%LOCALAPPDATA%\Chaoset\dict.tsv`
  （保留已导入词，旧文件备份）；
- 写入标准卸载信息。

安装器**不关闭任何已运行程序**：被占用的 DLL 由改名/重启替换处理。已打开程序继续
使用旧版 DLL，重新打开后加载新版；系统输入宿主在注销/重启后刷新。

### 便携 ZIP

解压后以管理员身份运行 `install.bat`；卸载运行 `install.bat uninstall`。
便携脚本不写 Run 项，daemon 在首次输入时懒启动；卸载会清理 HKLM/HKCU 的 Run 项。

### 从源码手动注册

```bat
regsvr32 ChaosetIME.dll
chaosetd.exe -pipe -dict dict.tsv -seeddict dict.tsv -userdict userdict.json -log chaosetd.log
```

然后在「设置 → 时间和语言 → 语言和区域」添加输入法 **Chaoset**，用 `Win+Space`
切换。卸载：`regsvr32 /u ChaosetIME.dll`。

## daemon 生命周期

安装器不启动 daemon。daemon 有两个启动入口，均运行在**当前交互用户**上下文：

1. 登录时 `Run` 项启动；
2. TSF 首次激活/首键时懒启动（`-pipe`，当前用户权限）。

daemon 参数：

```bat
"%ProgramFiles%\Chaoset\chaosetd.exe" -pipe ^
  -dict "%LOCALAPPDATA%\Chaoset\dict.tsv" ^
  -seeddict "%ProgramFiles%\Chaoset\dict.tsv" ^
  -userdict "%LOCALAPPDATA%\Chaoset\userdict.json" ^
  -log "%LOCALAPPDATA%\Chaoset\chaosetd.log"
```

`-seeddict` 只在目标词库不存在或为 0 字节时复制安装目录底库，不覆盖用户导入词。
TSF 懒启动只在尚未连接时尝试，失败后按退避重试，不会每个按键长时间阻塞宿主。

## 传输与安全

- 默认：命名管道 `\\.\pipe\chaoset-<当前用户 SID>`；DLL 与 daemon 用同一 SID 规则。
- ACL：当前用户 + SYSTEM + ALL APPLICATION PACKAGES 读写；其他本地用户不可连。
- 兼容：旧版 daemon 只开 TCP 时，壳会回退 `127.0.0.1:7949`；新安装统一使用管道。
- TCP 回退没有 Unix socket 权限等价物，本机其他用户进程可连，仅用于旧版过渡。

## 日志与排障

先确认二进制构建：

```bat
chaosetd.exe -version
chaoset-cli.exe -version
```

- `%LOCALAPPDATA%\Chaoset\chaosetd.log`：daemon 启动构建、用户词典路径、词库加载、
  每条 IPC 连接（`network=pipe`）与请求错误。
- `%LOCALAPPDATA%\Chaoset\tsf.log`：TIP 生命周期、宿主/AppContainer 标志、
  TSF 调用 HRESULT、候选窗失败、全屏抑制、Shift 跟踪、`daemon request failed`。
  AppContainer 宿主可能写到 `%LOCALAPPDATA%\Packages\<包族>\LocalState\Chaoset\`。
  每个进程最多 200 条。
- `%LOCALAPPDATA%\Chaoset\crash.log`：宿主未处理异常（异常码/地址/模块/调用栈/
  最后一次 TSF 操作），记录后继续交给 WER。

只有英文候选时：先看 daemon 日志是否启动、`tsf.log` 是否连接失败；用
`chaoset-cli.exe -pipe` 复验 IPC；只有旧版 daemon 才用 `-tcp 127.0.0.1:7949`。

## 命令行工具

```bat
REM 终端测试台（未运行 daemon 时首次调用会触发懒启动）
chaoset-cli.exe -pipe

REM 用户词典统计/清空/导出（传输需与 daemon 一致）
chaoset-userdict.exe -pipe stats
chaoset-userdict.exe -pipe export -o userdict-backup.tsv

REM 旧版 daemon 兼容
chaoset-cli.exe -tcp 127.0.0.1:7949
```

## 功能与验收

| 功能 | 当前状态 |
| :--- | :--- |
| 64/32 位 TSF COM，注册/卸载/类别配置 | 已实现；CI 校验导出 |
| 命名管道、当前用户按需启动、AppContainer ACL | 已实现；Windows 真机需验收 |
| 原生 GDI 候选窗、学习标记、鼠标点选、翻页 | 已实现；视觉静态契约由 `checkuiparity` 校验 |
| 设置窗：联想、学习数据、词库导入、深浅色、per-monitor DPI | 编译级实现；交互需真机验收 |
| Shift 空按中英切换、全/半角、双拼 `;`、渐进上屏 | 编译级 + 核心行为测试；Shift 投递需真机验收 |
| 全屏免打扰、UIA 窗口级 provider | 编译级实现 |
| 游戏/全屏、UWP/AppContainer 细节、多会话（RDP/快切）、混合 DPI 多屏、Narrator | 待 Windows 真机验收 |

候选窗默认全屏抑制由 `HKCU\Software\Chaoset\FullscreenSuppress` 控制（DWORD，缺省 1）。
设置窗唯一入口是候选栏齿轮，语言栏/托盘菜单未实现；设置项存
`HKCU\Software\Chaoset`。

## 已知限制

- 中文输入法激活时字母键会进入组词，这是 IME 固有行为；游戏前应切英文模式。
- 全屏免打扰只抑制候选窗，不改变按键/组词；无边框全屏不命中 D3D 全屏判定。
- 候选窗/设置窗的深色、DPI、无障碍子项仍需 Windows 真机验收。
- Shift 跟踪依赖 TSF Test 阶段；若宿主连 `OnTestKeyUp` 都不调用，需要退回
  `GetKeyState` 轮询。
- 中文标点按 US 布局字符级映射；其他键盘布局未接入 `ToAscii`。
- `GUID_TFCAT_CATEGORY_OF_TIP` 在 mingw uuid 库缺失，当前只登记键盘类。
- 多会话（RDP/快速用户切换）与同一用户多会话的管道命名仍待验证。

## 测试

```sh
./shims/windows/build.sh      # core_test + 交叉编译 + 导出检查
make package-windows          # ZIP + NSIS 安装器
```

CI 的 `windows-build` 每次 push/PR 运行上述构建与包内容自检；Release 工作流在打 tag 时
重新构建并上传 `ChaosetSetup.exe` / `chaoset-windows-x64.zip`。
