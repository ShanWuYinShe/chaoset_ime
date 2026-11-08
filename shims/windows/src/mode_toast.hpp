// 模式切换提示（短暂 toast）——Windows 壳叶子模块。
//
// 中英/全半角切换此前零反馈，用户只能靠打字试探（2026-10-27 审查，与
// macOS/ibus 壳统一补齐）。独立小窗 + 1.2s 自动隐藏；连续切换时重置
// 定时器，不会提前收起新提示。深浅色跟随系统（与候选窗同源）。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 win32.cpp（TIP TU）
// 的类型；只依赖 winutil/theme/diagnostics 叶子模块。窗口过程指向本 DLL 代码，
// 存活计数 g_liveToastWindows 参与 DllCanUnloadNow 防卸载判定。

#ifndef CHAOSET_WIN_MODE_TOAST_HPP_
#define CHAOSET_WIN_MODE_TOAST_HPP_

#include <windows.h>

#include <string>

namespace chaoset::win {

// 存活中的模式提示窗数量（定义在 mode_toast.cpp）。
// TIP TU 的 DllCanUnloadNow 读取它；计数不归零前禁止宿主 FreeLibrary。
extern volatile LONG g_liveToastWindows;

class ModeToast {
public:
    ~ModeToast();
    ModeToast() = default;
    ModeToast(const ModeToast&) = delete;
    ModeToast& operator=(const ModeToast&) = delete;

    // 位置语义与 mac ModeToastController.show 一致：调用方传光标行下方的锚点，
    // 本函数测量出胶囊尺寸后按工作区钳制（mac 用 candidateOrigin 的
    // clampTopLeft；Windows 此前完全不钳制，贴近屏幕边缘时提示会被截掉）。
    void show(HINSTANCE hinst, const std::wstring& text, int x, int y);

    void hide();

    // 彻底销毁（非激活退场用）：只 hide() 的话窗口对象还活着、定时器也可能
    // 残留；用户要求「感觉不到输入法存在」，所以退场走销毁。
    void destroy();

private:
    static constexpr UINT kTimerId = 1;
    static constexpr UINT kDurationMs = 1200;

    // 提示条与候选窗同用 pt 口径（mac ModeToastView.size：文本 +28/+14，
    // 圆角 10pt；此前 px@96 口径只有 0.75 倍，见 2026-10-30 审计 A1/R28）。
    int pt(int v) const;

    bool ensureWindow(HINSTANCE hinst);
    void measure();
    HFONT makeFont() const;
    void onPaint(HWND hwnd);
    static LRESULT CALLBACK wndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam);

    // 窗口销毁回收：清句柄并归还 DLL 防卸载计数（owner 连带销毁也会走到）。
    void onWindowDestroyed();

    HWND hwnd_ = nullptr;
    std::wstring text_;
    int dpi_ = 96;
    int w_ = 0;
    int h_ = 0;
};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_MODE_TOAST_HPP_
