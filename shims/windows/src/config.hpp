// HKCU 设置读写、配置 TTL 缓存与失效、全屏抑制、中英状态作用域。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 TIP TU（tip.hpp /
// tip_*.cpp）的类型。TIP 热路径全部读这里的 TTL 缓存（零阻塞契约：
// 注册表/Shell 查询不发生在按键回调里）。实现见 config.cpp。

#ifndef CHAOSET_WIN_CONFIG_HPP_
#define CHAOSET_WIN_CONFIG_HPP_

#include <windows.h>

namespace chaoset::win {

// 中英状态作用域（两档）。定义放这里：TIP 主体与配置缓存都在 TIP 类之前
// 使用（原 win32.cpp 文件前部定义，wave2 后跨 TU 由本头声明）。
//   System  —— 系统全局共享（默认）：托盘是权威，获得焦点时从权威同步；
//   Process —— 进程各自独立：各进程分别记住中英（同进程多窗口共享一个值），
//              获得焦点时把本进程状态送到托盘。
// 旧存储值只在读取时归一化（旧 window→Process；未知/空→System），
// 新写入只有 system/process。
enum class ModeScope { System, Process };

// 词组联想开关（HKCU\Software\Chaoset，DWORD Associate；未写入时默认开启）。
bool RegGetAssociate();
void RegSetAssociate(bool on);

// 「联想开关变化」跨进程广播消息 id（RegisterWindowMessageW）。
UINT associateChangedMessage();

// 配置 TTL 缓存：每键注册表/Shell 查询 → 带 TTL 的内存缓存（审计 T10）。
ModeScope cachedModeScope();
bool cachedAssociate();
bool cachedDarkTheme();
bool cachedFullscreenSuppress();
bool cachedFullscreenActive();
void invalidateAssociateCache();
void invalidateThemeCache();

// TSF 线程：HKCU UiEnglish 镜像读（注册表微秒级读，绝不跨进程等待）。
bool readHkcuEnglish(bool* english);

// UI 宿主持久化的中英状态（HKCU UiEnglish；UI 宿主是中等完整性进程，TIP 在
// AppContainer 里写不了，宿主被杀/升级重启后靠它恢复「英」）。
bool uiLoadSavedEnglish();
void uiSaveEnglish(bool english);

// 中英状态作用域持久化。读取宽容归一化（三端一致）：system→System；
// process→Process；旧 window→Process；未知/空→System（默认）。
ModeScope uiLoadModeScope();
void uiSaveModeScope(ModeScope scope);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_CONFIG_HPP_
