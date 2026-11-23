// mode_hotkey.cpp：无文本输入焦点时的中英切换热键实现（issue #132）。
//
// 组合键选 Ctrl+Shift+Space：语义直观（空格=切换），且在「没有输入框」这个真正
// 需要它的场景里几乎不冲突。代价必须写明：**RegisterHotKey 是全局独占**——有输入
// 焦点时这个组合也会被输入法抢走，占用它的应用（部分 IDE 的「参数提示」等）会收不到。
// 注册失败（已被别的程序或系统占用）只记日志、功能降级为托盘左键。

#include "mode_hotkey.hpp"

#include "diagnostics.hpp"

namespace chaoset::win {
namespace {

constexpr int kToggleHotkeyId = 1;
// MOD_NOREPEAT：按住不放不重复触发（否则长按会快速来回切中英）。
constexpr UINT kToggleHotkeyMods = MOD_CONTROL | MOD_SHIFT | MOD_NOREPEAT;
constexpr UINT kToggleHotkeyVk = VK_SPACE;
// UI 宿主进程内单例：只用于避免盲目 Unregister。
bool g_toggleHotkeyRegistered = false;

}  // namespace

void uiRegisterToggleModeHotkey(HWND hwnd) {
    if (g_toggleHotkeyRegistered || hwnd == nullptr) return;
    if (::RegisterHotKey(hwnd, kToggleHotkeyId, kToggleHotkeyMods, kToggleHotkeyVk)) {
        g_toggleHotkeyRegistered = true;
        diagPrintf(L"tsf.log", "ui host: Ctrl+Shift+Space 中英切换热键已注册\n");
        return;
    }
    const DWORD err = ::GetLastError();
    diagPrintf(L"tsf.log",
               "ui host: 中英切换热键注册失败 err=%lu（可能已被其它程序占用），"
               "降级为托盘左键切换\n",
               static_cast<unsigned long>(err));
    g_toggleHotkeyRegistered = false;
}

void uiUnregisterToggleModeHotkey(HWND hwnd) {
    if (!g_toggleHotkeyRegistered) return;
    ::UnregisterHotKey(hwnd, kToggleHotkeyId);
    g_toggleHotkeyRegistered = false;
}

bool uiIsToggleModeHotkey(WPARAM wParam) {
    return wParam == static_cast<WPARAM>(kToggleHotkeyId);
}

}  // namespace chaoset::win