// 单例 UI 宿主（chaoset-ui.exe）管道低层：命令/查询/拉起原语。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 TIP TU（tip.hpp /
// tip_*.cpp）的类型。worker 调度/orchestrator（reportModeToUi /
// openSettingsViaUi）在 shell_worker.cpp，由 TIP TU 调用这里的原语。实现见
// ui_client.cpp。
//
// 为什么托盘指示器与设置窗必须放进独立进程（2026-11-05 用户反馈的根因）：
//   1) TSF TIP 由系统加载进**每个宿主进程**——实测 explorer.exe 一个进程就有 4 个
//      TIP 实例（4 个按钮窗口）。进程内自绘托盘 = 每个实例一个图标，而且各实例
//      enMode_ 互相独立，任务栏会同时出现「中 英 中 英」多个图标；
//   2) 设置窗若由宿主进程创建：开始菜单/搜索（SearchApp.exe 等 Low IL
//      AppContainer）里会被沉浸式 z 序 band 压住，且文件对话框、导入器进程、
//      重启 daemon 在 AC 里都不可用；宿主退出还会带走设置窗。
// 因此任务栏中英指示器与设置窗统一交给「每用户会话唯一」的后台 UI 进程
// （chaoset-ui.exe，**不随登录启动**：TIP 首次激活时带 --mode 懒启动；托盘
// 可见性只由 TIP 的 active 上报控制——active=1（本输入法被系统选中）显示、
// active=0（切到其它输入法）防抖后隐藏，见 ui_host.cpp 的 kUiActiveMsg/UiTray）。
// 失焦/停用/前台离开/owner 进程退出都不隐藏托盘（issue #3：可见性与输入焦点
// 解耦）。TIP 只通过命名管道上报模式与激活状态、请求打开设置，不持有任何 UI。
//
// 管道安全描述符与 daemon 完全同规则：DACL 当前用户 + SYSTEM +
// ALL APPLICATION PACKAGES(S-1-15-2-1)，SACL 低完整性标签 ML;;NW;;;LW。
// 这是 AppContainer 宿主（开始菜单/搜索）能「连上并写入」的前提；设置 SACL
// 失败时降级为仅 DACL（与 ipc/pipe_sddl.go 的降级策略一致）。服务端实现见
// ui_host.cpp 的 uiPipeSecurityDescriptor。

#ifndef CHAOSET_WIN_UI_CLIENT_HPP_
#define CHAOSET_WIN_UI_CLIENT_HPP_

#include <windows.h>

#include <string>

namespace chaoset::win {

// UI 宿主命令管道名 / 单实例互斥体名（都带「当前用户 SID + 会话 ID」）。
// SID 拿不到时返回空串：调用方按「UI 不可用」降级。
const std::wstring& uiPipeName();
std::wstring uiMutexName();

// 命令集（纯文本行 + '\n'，服务端 ui_host.cpp 的 uiHandleCommand 解析；
// 2026-11-11 起 mode/active 命令尾部带发送进程的 pid，UI 宿主按 pid 聚合
// 可见性——pid 由 TIP 自己附在命令里，不依赖服务端取客户端 pid 的 API）：
//   mode <0|1> <pid>         当前进程的中英模式：System 档写 HKCU + 图标
//                            （托盘是权威）；Process 档只更新该 pid 条目与图标
//   active 1 <0|1> <pid>     本输入法被系统选中（激活迁移/输入法切回/焦点获得/
//                            前台回到本进程；TSF 信号事件驱动，无心跳）：取消待
//                            隐藏并显示托盘，把该 pid 设为 owner；System 档显示
//                            权威 g_uiEnglish，Process 档显示该 pid 的模式
//   active 0 <pid>           用户切到其它输入法（唯一隐藏入口）：300ms 防抖后
//                            隐藏托盘；失焦/停用/前台离开不发送（托盘可见性与
//                            输入焦点解耦，issue #3）
//   query                    查询 UI 宿主持有的权威模式，应答 "mode 0\n"/"mode 1\n"
//   toggle                   等价托盘左键：System 档翻转权威模式，Process 档忽略
//   settings                 打开设置窗
//
// 信任模型：pid 由 TIP 在命令载荷里提供（不依赖服务端取客户端 pid 的 API）；
// 命令管道 ACL 允许 ALL APPLICATION PACKAGES（AC 宿主必须能写），因此同机同
// 用户进程理论上可伪造 pid 抢占/隐藏托盘——影响面仅限托盘显示，不涉及按键与
// 输入路径；如需更强校验，后续可在服务端交叉校验客户端进程身份。active 0 不再
// 要求 sender==owner（issue #3：owner 刚退出/不是前台进程时切走也必须能隐藏），
// 因此伪造 active 0 可隐藏托盘，但下一次 active 1（TSF 事件）会立即恢复。
//
// 发一条命令（每次短连接，几十字节）；服务端同一时刻只有一个实例在监听。
bool uiSendCommandOnce(const char* command);
// attempts=4（≈40ms）用于按键路径；设置窗点击路径用更多次（≈200ms）。
bool uiSendCommand(const char* command, int attempts = 4);
// 宿主 UI 线程专用版本：**完全不重试、不 Sleep**（单次非阻塞尝试）。
bool uiSendCommandFast(const char* command);

// 向 UI 宿主查权威中英状态（system 作用域）。只在 ShellWorker 后台线程调用。
bool uiQueryMode(bool* english);

// 按需拉起 UI 宿主（5s 节流）：托盘指示器 / 设置窗都靠它。只在 ShellWorker
// 后台线程调用——CreateProcessW 出现在 TSF 宿主 UI 线程上就是卡顿源（审计 T7）。
// args 可为 "--mode 0/1" 或 "--settings"。
void ensureUiHostStarted(const wchar_t* args);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_UI_CLIENT_HPP_
