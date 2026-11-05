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

- 唯一传输：命名管道 `\\.\pipe\chaoset-<当前用户 SID>`；DLL 与 daemon 用同一 SID 规则。
- ACL：当前用户 + SYSTEM + ALL APPLICATION PACKAGES 读写；其他本地用户不可连。
- 强制完整性标签：低完整性可写（`S:(ML;;NW;;;LW)`）。AppContainer 宿主（开始菜单、
  搜索框、UWP）是低完整性进程，缺这个标签时 DACL 放行了也只能连不能写，表现就是
  「按了键、daemon 也在、没有候选」。见 `ipc/pipe_sddl.go`。
- 没有网络端口、没有 TCP 回退：管道连不上就是连不上，壳按约定降级放行按键。

## 日志与排障

先确认二进制构建。**用 `chaoset-cli.exe -version`**：`chaosetd.exe` 以
`-H=windowsgui` 构建（不挂控制台），在 cmd 里跑 `-version` 没有任何输出，
不能据此判断程序坏了。

```bat
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

### 装完只能打英文、完全没有候选

这是「壳连不上 daemon」的既定降级：连不上时字母原样透传给宿主（绝不吞键），
候选窗根本不会创建。判断 daemon 有没有起来，按诊断力从强到弱：

1. **daemon 日志断点**（最强）：`%LOCALAPPDATA%\Chaoset\chaosetd.log`

   | 日志内容 | 结论 |
   | :--- | :--- |
   | 文件不存在 | daemon 从未启动（CreateProcess 失败/exe 缺失/起在别的账户） |
   | 有「chaosetd 启动」但**没有**「chaosetd 就绪」 | 起来了但在监听前就退出，末行即退出原因（词库/单实例/端点解析错误；v0.3.0–v0.3.12 是「Windows 使用命名管道…没有 Unix socket 路径」） |
   | 有「chaosetd 就绪，pipe: \\.\pipe\chaoset-S-1-5-…」 | daemon 正常，问题在壳侧 |
   | 还有「ipc 连接: network=pipe」 | IPC 已通，转查候选渲染 |

2. **进程与命名管道**：

   ```bat
   tasklist /FI "IMAGENAME eq chaosetd.exe"
   powershell -NoProfile -Command "[System.IO.Directory]::GetFiles('\\.\pipe\') | Where-Object { $_ -like '*chaoset*' }"
   ```

   进程活着不代表在监听；管道列表里出现 `chaoset-S-1-5-…` 才说明到了 listen 阶段。

3. **直接验 IPC**（最硬）：

   ```bat
   chaoset-cli.exe -pipe
   ```

   能进交互测试台并输入拼音出候选 = daemon + IPC + 引擎全通，问题只在 TSF 壳；
   立刻报「连接输入法 daemon 失败」= 连不上。

4. **`tsf.log` 关键字**：`daemon lazy start ok=0 err=N`（CreateProcess 失败）、
   `pipe connect failed err=N`（N=2 管道不存在、N=5 ACL 拒绝）、
   `daemon request failed op=input connected=0`（连不上）与 `connected=1`
   （连上了但请求失败/超时，性质不同）。注意 exe 不存在时不会写 lazy start 行，
   且每进程只有 200 条预算，日志里没有不代表 TIP 没激活。

排查后仍只有英文，用 `chaoset-cli.exe -pipe` 的结果区分「daemon 侧」与「壳侧」，
再按上表定位。

## 命令行工具

```bat
REM 终端测试台（注意：CLI 本身不会拉起 daemon。daemon 未运行时先在任意
REM 程序里打一次拼音，或在安装目录手动运行 chaosetd.exe -pipe）
chaoset-cli.exe -pipe

REM 用户词典统计/清空/导出
chaoset-userdict.exe -pipe stats
chaoset-userdict.exe -pipe export -o userdict-backup.tsv
```

## 功能与验收

| 功能 | 当前状态 |
| :--- | :--- |
| 64/32 位 TSF COM，注册/卸载/类别配置 | 已实现；CI 校验导出 |
| 命名管道、当前用户按需启动、AppContainer ACL | 已实现；Windows 真机需验收 |
| 原生 GDI 候选窗、学习标记、鼠标点选、翻页 | 已实现；视觉静态契约由 `checkuiparity` 校验 |
| 设置窗：联想、学习数据、词库导入、深浅色、per-monitor DPI | 编译级实现；交互需真机验收 |
| Shift 空按中英切换、全/半角、双拼 `;`、渐进上屏 | 编译级 + 核心行为测试；Shift 记账有静态契约断言（`checkuiparity`）；投递层仍需真机验收 |
| 全屏免打扰、UIA 窗口级 provider | 编译级实现 |
| 游戏/全屏、UWP/AppContainer 细节、多会话（RDP/快切）、混合 DPI 多屏、Narrator | 待 Windows 真机验收 |

候选窗默认全屏抑制由 `HKCU\Software\Chaoset\FullscreenSuppress` 控制（DWORD，缺省 1）。
设置窗唯一入口是候选栏齿轮，语言栏/托盘菜单未实现；设置项存
`HKCU\Software\Chaoset`。

## 已知限制

- 中文输入法激活时字母键会进入组词，这是 IME 固有行为；游戏前应切英文模式。
- 全屏免打扰只抑制候选窗，不改变按键/组词；无边框全屏不命中 D3D 全屏判定。
- 候选窗/设置窗的深色、DPI、无障碍子项仍需 Windows 真机验收。
- Shift 跟踪依赖 TSF Test 阶段。**2026-11-05 已补两层兜底**：
  1. 记账改用幂等位掩码（`shiftHeld_`）而不是 ++/-- 计数器，并在 `Deactivate`、
     `OnSetFocus(FALSE)` 复位、每次按键前用 `GetAsyncKeyState` 自愈丢失的 KeyUp。
     此前若宿主漏投递一次 Shift KeyUp（典型：按住 Shift 切窗口，KeyUp 交给新前台
     窗口），计数器永远回不到 0，该进程内 Shift 空按会**永久失效**直到重启应用。
  2. **物理状态轮询兜底**（`onShiftPollTick`，40ms 的 message-only 窗口计时器）：
     Windows Terminal、cmd/conhost、Windows 11 开始菜单等宿主对修饰键的按下/释放
     投递不完整（只给按下不给释放，或干脆不送进按键下沉），光靠
     `OnTestKeyDown`+`OnTestKeyUp` 成对到达无法结算。轮询按
     `GetAsyncKeyState` 的物理状态自行推断「按住→松开」，并在结算前用
     `GetKeyboardState` + 鼠标键状态确认期间没有别的输入（避免 Shift+点击被误判
     成空按）。与事件路径共用同一套状态，任一路径先结算另一条都会自然跳过。
     排障证据：`tsf.log` 里的 `shift poll:` 行——出现 `宿主未投递 Shift 按下`
     说明该宿主完全不送修饰键；出现 `释放事件缺失` 说明只缺 KeyUp。
- **console（cmd/旧 PowerShell）走的是 IMM32，不是 TSF**：本壳是纯 TSF TIP，
  没有 IMM32 入口（`ImeInquire`/`ImeProcessKey`），若 `tsf.log` 里完全没有
  `activate ... host=conhost.exe`，说明 TIP 根本没被加载——那不是本 Bug，
  需要单独实现 IMM32 壳才可能支持。
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
