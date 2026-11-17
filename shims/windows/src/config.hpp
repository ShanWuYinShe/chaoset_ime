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

// 外观主题（三档，三端一致）：跟随系统 / 强制浅色 / 强制深色。
// 持久化在 HKCU\Software\Chaoset\Theme（REG_SZ：system/light/dark，大小写不敏感）；
// 缺省/未知值归一化为 System。uiEffectiveDark() 是唯一「当前是否深色」判定出口：
// light→false、dark→true、system→系统 AppsUseLightTheme。
enum class Theme { System, Light, Dark };

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
// UILess 线程激活开关（HKCU UILessActivate，缺省 1）：游戏/全屏宿主里照常
// 激活本输入法；0 = 旧「不激活」行为（UILess 宿主完全隐身）。
bool cachedUilessActivate();
// UILess 官方协商开关（HKCU UiElementRender，缺省 0）：UILess 宿主候选默认
// GDI 自绘（通用，无宿主名单）；1 = 启用微软《UI less mode》官方协商
//（ITfUIElementMgr/ITfCandidateListUIElement）。
bool cachedUiElementRender();
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

// 外观主题持久化（HKCU\Software\Chaoset\Theme，REG_SZ，大小写不敏感）：
// 读取宽容归一化（system/light/dark，未知/空→System），写入只写小写。
Theme uiLoadTheme();
void uiSaveTheme(Theme theme);
// 唯一「当前是否深色」判定：light→false、dark→true、system→isSystemDarkTheme()。
bool uiEffectiveDark();

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_CONFIG_HPP_