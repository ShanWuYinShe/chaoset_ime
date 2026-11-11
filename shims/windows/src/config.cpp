// HKCU 设置读写、配置 TTL 缓存、全屏抑制、中英状态作用域（模块实现）。
// 叶子模块：不得 include/引用 TIP TU（tip.hpp / tip_*.cpp）的类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <shellapi.h>  // SHQueryUserNotificationState（全屏免打扰）
#include <cwchar>

#include "config.hpp"
#include "theme.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// 全屏免打扰（2026-10-31 审计 A7）：D3D 全屏游戏/演示模式下不弹候选窗——
// 置顶候选窗盖在全屏游戏上会闪屏、干扰焦点，用户明确要求游戏场景不能被
// 输入法打扰。只抑制候选窗显示，不改变按键/组词逻辑（WASD 仍会被组词，
// 这是任何中文输入法激活时的固有行为，README 已说明游戏前切英文模式）。
// 注册表 HKCU\Software\Chaoset\FullscreenSuppress（DWORD，缺省 1）可关闭：
// 部分全屏浏览器/视频也会被判定为 D3D 全屏，此时设 0 即可恢复候选窗。
static bool isFullscreenSuppressEnabled() {
    DWORD v = 1;
    DWORD sz = sizeof(v);
    if (RegGetValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"FullscreenSuppress",
                     RRF_RT_REG_DWORD, nullptr, &v, &sz) != ERROR_SUCCESS) {
        v = 1;  // 未配置 = 开启（用户 2026-10-30 明确要求游戏场景优先）
    }
    return v != 0;
}

// 只认「全屏独占 D3D / 演示模式」两种状态；普通最大化、无边框全屏返回
// QUNS_ACCEPTS_NOTIFICATIONS，不在抑制范围（边界见 README 已知限制）。
static bool isFullscreenAppActive() {
    QUERY_USER_NOTIFICATION_STATE state = QUNS_ACCEPTS_NOTIFICATIONS;
    if (FAILED(SHQueryUserNotificationState(&state))) return false;
    return state == QUNS_RUNNING_D3D_FULL_SCREEN || state == QUNS_PRESENTATION_MODE;
}

// ---- 设置：词组联想开关（HKCU\Software\Chaoset，DWORD Associate）----
// 未写入时默认开启。候选栏齿轮菜单切换（参考微信输入法候选栏设置入口）；
// Linux 用 config.json、macOS 用 UserDefaults，三端语义一致。

bool RegGetAssociate() {
    HKEY hKey = nullptr;
    DWORD val = 1, sz = sizeof(val);
    if (RegOpenKeyExW(HKEY_CURRENT_USER, L"Software\\Chaoset",
                      0, KEY_READ, &hKey) == ERROR_SUCCESS) {
        RegQueryValueExW(hKey, L"Associate", nullptr, nullptr,
                         reinterpret_cast<LPBYTE>(&val), &sz);
        RegCloseKey(hKey);
    }
    return val != 0;
}

void RegSetAssociate(bool on) {
    HKEY hKey = nullptr;
    if (RegCreateKeyExW(HKEY_CURRENT_USER, L"Software\\Chaoset", 0, nullptr,
                        REG_OPTION_NON_VOLATILE, KEY_WRITE, nullptr,
                        &hKey, nullptr) == ERROR_SUCCESS) {
        DWORD v = on ? 1 : 0;
        RegSetValueExW(hKey, L"Associate", 0, REG_DWORD,
                       reinterpret_cast<const BYTE*>(&v), sizeof(v));
        RegCloseKey(hKey);
    }
}

// 「联想开关变化」跨进程广播消息（2026-10-31）。
//
// 设置窗与候选窗可能在不同宿主进程（用户从 A 应用打开设置窗后切到 B 应用打字），
// 且设置窗**不能**持有 TIP 对象指针：TSF 随时可能 Release TIP，而设置窗还开着，
// 裸指针就是宿主进程内 UAF（2026-10-30 三端一致性审计 P1-2）。
// 因此用 RegisterWindowMessageW：同一字符串在整个会话内返回同一消息 id，
// HWND_BROADCAST 可送达所有顶层窗口（含各宿主进程里的候选窗），候选窗再转发给
// 同进程的 TIP 收起联想。与 macOS 设置窗 post 通知后立即收起对齐。
UINT associateChangedMessage() {
    static const UINT id = RegisterWindowMessageW(L"Chaoset.AssociateChanged");
    return id;
}

// ---- 配置缓存（每键注册表/Shell 查询 → TTL 缓存，审计 T10）----
// slot: 0=ModeScope 1=Associate 2=DarkTheme 3=FullscreenSuppress 4=FullscreenActive
static volatile LONG g_cfgStamp[5] = {0};
static ModeScope g_cfgModeScope = ModeScope::System;
static bool g_cfgAssociate = true;
static bool g_cfgDark = false;
static bool g_cfgFullscreenSuppress = true;
static bool g_cfgFullscreenActive = false;

static bool cacheFresh(int slot, DWORD ttlMs) {
    const LONG s = g_cfgStamp[slot];
    if (s == 0) return false;
    return static_cast<DWORD>(GetTickCount() - static_cast<DWORD>(s)) < ttlMs;
}

static void cacheTouch(int slot) {
    DWORD t = GetTickCount();
    g_cfgStamp[slot] = static_cast<LONG>(t == 0 ? 1 : t);
}

ModeScope cachedModeScope() {
    if (!cacheFresh(0, 2000)) {
        g_cfgModeScope = uiLoadModeScope();
        cacheTouch(0);
    }
    return g_cfgModeScope;
}

bool cachedAssociate() {
    if (!cacheFresh(1, 1000)) {
        g_cfgAssociate = RegGetAssociate();
        cacheTouch(1);
    }
    return g_cfgAssociate;
}

void invalidateAssociateCache() { g_cfgStamp[1] = 0; }
static void invalidateModeScopeCache() { g_cfgStamp[0] = 0; }


bool cachedDarkTheme() {
    if (!cacheFresh(2, 1000)) {
        g_cfgDark = isSystemDarkTheme();
        cacheTouch(2);
    }
    return g_cfgDark;
}

void invalidateThemeCache() { g_cfgStamp[2] = 0; }

bool cachedFullscreenSuppress() {
    if (!cacheFresh(3, 5000)) {
        g_cfgFullscreenSuppress = isFullscreenSuppressEnabled();
        cacheTouch(3);
    }
    return g_cfgFullscreenSuppress;
}

bool cachedFullscreenActive() {
    if (!cacheFresh(4, 1000)) {
        g_cfgFullscreenActive = isFullscreenAppActive();
        cacheTouch(4);
    }
    return g_cfgFullscreenActive;
}

// TSF 线程：HKCU UiEnglish 镜像读（注册表微秒级读，绝不跨进程等待）。
// UI 宿主每次模式变化都会把权威值写回它（uiSaveEnglish）；worker 的异步
// uiQueryMode 结果会经 message-only 窗口再覆盖一次。
bool readHkcuEnglish(bool* english) {
    if (english == nullptr) return false;
    DWORD value = 0;
    DWORD size = sizeof(value);
    if (RegGetValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"UiEnglish",
                     RRF_RT_REG_DWORD, nullptr, &value, &size) != ERROR_SUCCESS) {
        return false;
    }
    *english = value != 0;
    return true;
}

// 上次中英模式的持久化：UI 宿主是中等完整性进程，可写 HKCU；TIP 在
// AppContainer 里写不了。宿主被杀/升级重启后靠它恢复「英」而不是跳回「中」。
bool uiLoadSavedEnglish() {
    // 与 TIP 的 syncEnglishFromAuthority 共用同一份 HKCU UiEnglish 镜像读。
    bool english = false;
    if (!readHkcuEnglish(&english)) return false;  // 未写入时默认中文（同旧实现）
    return english;
}
void uiSaveEnglish(bool english) {
    const DWORD value = english ? 1 : 0;
    RegSetKeyValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"UiEnglish",
                    REG_DWORD, &value, sizeof(value));
}

// ModeScope 的定义见文件前部（TIP 侧要先用到）。读取失败一律按 system
// （最符合用户直觉：切一次全系统生效）。
ModeScope uiLoadModeScope() {
    wchar_t buf[32] = {0};
    DWORD size = sizeof(buf) - sizeof(wchar_t);
    if (RegGetValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"ModeScope",
                     RRF_RT_REG_SZ, nullptr, buf, &size) == ERROR_SUCCESS) {
        if (_wcsicmp(buf, L"process") == 0) return ModeScope::Process;
        if (_wcsicmp(buf, L"window") == 0) return ModeScope::Window;
    }
    return ModeScope::System;
}

void uiSaveModeScope(ModeScope scope) {
    const wchar_t* text = L"system";
    if (scope == ModeScope::Process) text = L"process";
    else if (scope == ModeScope::Window) text = L"window";
    RegSetKeyValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"ModeScope",
                    REG_SZ, text, static_cast<DWORD>((wcslen(text) + 1) * sizeof(wchar_t)));
    invalidateModeScopeCache();  // 设置窗改了作用域：TIP 侧缓存立即失效
}

}  // namespace chaoset::win
