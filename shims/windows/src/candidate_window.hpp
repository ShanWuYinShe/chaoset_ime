// 候选词悬浮窗（Windows GDI 自绘）——Windows 壳独立 TU。
//
// 从 win32.cpp 拆出（wave1 续）：双缓冲 GDI 渲染、DPI 感知、光标跟随、圆角
// 胶囊与 macOS CandidatePanel 视觉一致；无屏幕阅读器时的 UIA 宿主 provider
// 动态加载、齿轮圆头几何笔、左右键/右键菜单交互与联想开关广播处理都在本模块。
//
// 模块边界（docs/architecture.md §3.1 依赖方向）：
//   * 本模块**不得** include/引用 TIP 类型（ChaosetTIP）：对 TIP 的唯一接触是
//     CandidateWindowHost 抽象接口（TIP 在同一 DLL 内实现并继承它，不影响
//     COM QI 返回的接口指针）。
//   * 依赖叶子模块 winutil（owner/DPI/类名/宽字符转换）、theme（配色）、
//     config（TTL 缓存/广播消息）、diagnostics（预算日志）、candidate_layout
//     （纯布局数学）与 core（chaoset::Comp 纯数据）。
//   * 存活计数 g_liveCandidateWindows 参与 DllCanUnloadNow 防卸载判定：窗口
//     过程指向本 DLL 代码，只要窗口还活着就不能让宿主 FreeLibrary。

#ifndef CHAOSET_WIN_CANDIDATE_WINDOW_HPP_
#define CHAOSET_WIN_CANDIDATE_WINDOW_HPP_

#include <windows.h>

#include <string>
#include <vector>

#include "core.hpp"

namespace chaoset::win {

// 候选窗对 TIP 的唯一接触面（抽象接口，避免候选窗 TU 反向依赖 TIP 类型）。
// 实现方：ChaosetTIP（同 DLL 内部类型，可继承；不影响 COM QI 返回的接口指针）。
class CandidateWindowHost {
public:
    virtual ~CandidateWindowHost() = default;
    // 打开设置窗（单例 UI 宿主进程创建，TIP 只发管道请求）。
    virtual void showSettingsWindow() = 0;
    // 候选点选/删除（鼠标抬起后调用，语义与数字键一致）。
    virtual void selectCandidateByIndex(int index) = 0;
    virtual void deleteCandidateByIndex(int index) = 0;
    // 翻页：组词页与联想页数据源不同，候选窗箭头据此二选一。
    virtual void flipPage(bool next) = 0;
    virtual void flipAssociateFromPanel(bool next) = 0;
    virtual bool isAssociatePanel() const = 0;
    virtual void clearAssociateFromSettings() = 0;
};

// 存活中的候选窗口数量（定义在 candidate_window.cpp）。用于 DllCanUnloadNow——
// 候选窗的窗口过程指向本 DLL 内的代码，只要还开着窗口就不能让宿主 FreeLibrary
//（否则下一次 WM_PAINT 会跳到已卸载内存，崩掉 explorer / TextInputHost）。
extern volatile LONG g_liveCandidateWindows;

// 候选词悬浮窗：双缓冲原生 GDI 渲染、DPI 感知、光标跟随、圆角胶囊与 macOS 视觉一致。
// 成员名 tip_ 沿用拆出前的名字，类型已收敛为 CandidateWindowHost*。
class CandidateWindow {
public:
    CandidateWindow();
    ~CandidateWindow();

    CandidateWindow(const CandidateWindow&) = delete;
    CandidateWindow& operator=(const CandidateWindow&) = delete;

    HWND hwnd() const;

    // 设置候选窗 owner（宿主窗口）。窗口已创建时用 GWLP_HWNDPARENT 改 owner。
    // 必须在 show() 之前设置才能作用于 CreateWindowExW；已创建时改 owner 会
    // 重建 z 序关系，同样有效。
    void setOwner(HWND owner);

    // 外部（TIP）设置：当前输入法是否真的处于激活状态。非激活时**任何**显示
    // 请求都必须被拒绝——候选窗是 WS_EX_TOPMOST 且要接收鼠标点选，绝不能
    // 在后台留着挡住别的应用（2026-11-06 用户反馈）。
    void setActiveGate(bool active);

    // 显示候选窗：非激活/无候选/全屏免打扰直接 hide，绝不建窗。
    void show(HINSTANCE hinst, POINT pt, int lineH, const chaoset::Comp& comp,
              CandidateWindowHost* host);

    void hide();
    // 主动销毁走 DestroyWindow，统一在 WM_NCDESTROY 回收（见 onWindowDestroyed）。
    void destroy();
    // 窗口被销毁时统一回收：归还 g_liveCandidateWindows 计数并清裸指针。
    void onWindowDestroyed();

    void onLButtonDown(int x, int y);
    // 设置齿轮在**鼠标抬起**（WM_LBUTTONUP）才打开设置窗（2026-11-06 用户要求：
    // 按下即弹窗属于误触语义）。按下只记录待触发状态，抬起时仍在齿轮区才生效；
    // 候选词点选保持按下即选的既有语义。
    void onLButtonUp(int x, int y);
    // 右键候选行：弹出上下文菜单「删除该词」（先记录命中项，菜单返回后
    // 由 TIP 删除学习记录并刷新候选；详见候选窗 TU 内定义与 docs/ui-parity.md）。
    void onRButtonUp(int x, int y);

private:
    struct ItemPos {
        int index = 0;
        std::wstring indexStr;
        std::wstring textStr;
        RECT itemRect{};
        RECT pillRect{};
        RECT chipRect{};
        RECT textRect{};
        bool isFirst = false;
        // 学习标记：daemon Candidate.FromUser → 文本后绿色「*」。
        // fromUser 来自 core::Candidate（IPC "from_user"），markW 是布局宽度
        //（"*" 文本宽 + 2pt，含绘制起点 +1pt），与 mac markW 同口径。
        bool fromUser = false;
        int markW = 0;
    };

    // 候选窗按**锚点（光标）所在显示器**的 DPI 布局，而不是主屏 DPI。
    int getDpi() const;
    int layoutDpi_ = 0;  // layoutAndPosition 时按 targetPos_ 计算并缓存，onPaint 复用

    // 候选窗统一以 **pt** 为布局单位：MulDiv(pt, dpi, 72)（与 macOS 1:1）。
    static int pt(int v, int dpi);

    bool isInteractiveArea() const;
    // 设置窗关闭联想后的跨进程广播处理（实现在候选窗 TU，经 CandidateWindowHost
    // 转发 clearAssociateFromSettings；只查注册表，不信任消息参数）。
    void onAssociateChanged();

    void ensureWindow(HINSTANCE hinst);
    static LRESULT CALLBACK CandidateWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam);
    void layoutAndPosition();
    void onPaint(HWND hwnd);

    HWND hwnd_ = nullptr;
    // 只在输入法真正激活时才允许显示（见 setActiveGate）。默认 false：
    // 建窗/Activate 之前不存在任何窗口，从根上杜绝「后台 app 里留一个
    // TOPMOST 窗口挡鼠标」。
    bool activeGate_ = false;
    // 候选窗的 owner 窗口（宿主窗口）。非空时候选窗是其 owned window，
    // 恒在 owner 之上，可跨越 Windows 11 开始菜单/搜索的沉浸式 z 序 band。
    // 由 show() 的调用方经 setOwner() 设置（来源见 TIP TU 的 render 里
    // 的 ITfContextView::GetWnd 探测）。
    HWND ownerHwnd_ = nullptr;
    chaoset::Comp comp_;
    CandidateWindowHost* tip_ = nullptr;
    POINT targetPos_{0, 0};
    int targetLineHeight_ = 20;

    std::vector<ItemPos> items_;
    RECT chevronRect_{};
    int dividerX_ = 0;
    RECT settingsRect_{};  // 设置齿轮命中区
    // 鼠标左键按下命中的目标，抬起时才真正执行（2026-11-06 用户要求：
    // 「鼠标用抬起触发才正常，按下触发太奇怪」）。按下只记录，抬起点仍在
    // 同一目标上才触发；按下后拖走再松开不触发。
    bool gearPressed_ = false;     // 按在设置齿轮上
    int pressedItemIndex_ = -1;    // 按在候选词上（当前页切片内序号）
    bool chevronPressed_ = false;  // 按在翻页箭头上
};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_CANDIDATE_WINDOW_HPP_
