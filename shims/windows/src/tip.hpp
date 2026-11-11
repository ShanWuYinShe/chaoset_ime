// TIP 主体：class ChaosetTIP 的完整声明与跨 TU 常量（Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 拆出：类内 inline 定义按职责拆到 tip_core/keys/composition/
// candidates.cpp，本文件只保留声明、成员与跨 TU 常量。方法定义一律改为类外
// 定义，仅嵌套类型 ContextRef 与 TipThreadSink 的一行状态查询保留在类内。
//
// 模块边界（docs/architecture.md §3.1）：
//   * TIP 是 Windows 壳最高层：只被 win32.cpp 编排、被 tip_*.cpp 展开实现。
//   * 叶子模块（candidate_window/mode_toast/shell_worker/tip_thread/winutil/
//     theme/config/transport/daemon/ui_client/settings_window/ui_host/diagnostics）
//     严禁 include/引用本文件；tip_thread 对 TIP 的唯一接触是 TipThreadSink
//     抽象接口（本类实现），因此 tip_thread.cpp 不依赖具体 TIP 类型。
//   * 依赖方向：tip.hpp → candidate_window.hpp（CandidateWindowHost）/
//     mode_toast.hpp / tip_thread.hpp（TipThreadSink）/ tip_ids.hpp + 叶子模块。
//
// 构建见 build.sh（x86_64-w64-mingw32-g++）。

#ifndef CHAOSET_WIN_TIP_HPP_
#define CHAOSET_WIN_TIP_HPP_

#include <windows.h>
#include <msctf.h>

#include <string>
#include <vector>

#include "candidate_window.hpp"
#include "config.hpp"
#include "core.hpp"
#include "daemon.hpp"
#include "diagnostics.hpp"
#include "mode_toast.hpp"
#include "shell_worker.hpp"
#include "tip_ids.hpp"
#include "tip_thread.hpp"
#include "transport.hpp"
#include "winutil.hpp"

namespace chaoset::win {

constexpr DWORD kVkShift = 0x10;
constexpr DWORD kVkBackspace = 0x08;
constexpr DWORD kVkDelete = 0x2E;  // VK_DELETE：组词中与 Backspace 同语义（对齐 mac）
constexpr DWORD kVkEscape = 0x1B;
constexpr DWORD kVkReturn = 0x0D;
constexpr DWORD kVkSpace = 0x20;
constexpr DWORD kVkPrior = 0x21;           // Page Up
constexpr DWORD kVkNext = 0x22;            // Page Down
constexpr DWORD kVkFirstLetter = 0x41;  // 'A'
constexpr DWORD kVkLastLetter = 0x5A;   // 'Z'
constexpr DWORD kVkFirstDigit = 0x31;   // '1'
constexpr DWORD kVkLastDigit = 0x39;    // '9'
// 小键盘数字（VK_NUMPAD0..9）。Windows 的大小键盘虚键完全独立，此前只认
// VK_1-9：按小键盘数字落到「不可处理键」清空组词后放行，三端不一致
//（macOS 按字符、Linux 认 KP_1-9，2026-10-30 审计 A7）。
constexpr DWORD kVkNumpad1 = 0x61;      // VK_NUMPAD1
constexpr DWORD kVkNumpad9 = 0x69;      // VK_NUMPAD9
// 每页候选数：与 engine.PageSize() 一致（数字键 1-6 指位）。此前联想分页
// 处散落 6 个字面量，引擎改页容量时会静默漂移（2026-10-30 审计 R20）。
constexpr int kPageSize = 6;
// OEM 标点虚键（US 键盘布局假设；真机联调时按实际布局核验）
constexpr DWORD kVkOemSemicolon = 0xBA;      // ';'
constexpr DWORD kVkOemComma = 0xBC;          // ','
constexpr DWORD kVkOemPeriod = 0xBE;         // '.'
constexpr DWORD kVkOemOpenBrackets = 0xDB;   // '['
constexpr DWORD kVkOemBackslash = 0xDC;      // '\\'
constexpr DWORD kVkOemCloseBrackets = 0xDD;  // ']'
constexpr DWORD kVkOemQuotes = 0xDE;         // '\''
constexpr DWORD kVkOemPlus = 0xBB;           // '='（US 布局；shift 为 '+'）
constexpr DWORD kVkOemMinus = 0xBD;          // '-'
constexpr DWORD kVkOem2 = 0xBF;              // '/'（US 布局；shift 为 '?'）
constexpr DWORD kVkOem3 = 0xC0;              // '`'（US 布局；shift 为 '~'）


// mingw-w64 的 msctf.h 只声明了 ITfTextInputProcessor，没有微软 SDK 里的
// ITfTextInputProcessorEx（UILess 模式所需）。这里按 Windows SDK 10 的
// msctf.idl 原样补声明，IID 与虚表顺序都取自 SDK：
//   [
//     object, local,
//     uuid(6e4e2102-f9cd-433d-b496-303ce03a6507),
//   ]
//   interface ITfTextInputProcessorEx : ITfTextInputProcessor
//   {
//       HRESULT ActivateEx([in] ITfThreadMgr *ptim, [in] TfClientId tid, [in] DWORD dwFlags);
//   };
//
// 为什么需要（2026-11-05，微软《UILess Mode Overview》原文）：
//   "To be UI less aware, TIP needs to implement ITfTextInputProcessorEx.
//    If this interface is not implemented, TIP will not be activated under
//    the UI less mode thread."
// 不实现它就等于对 UILess 宿主（游戏/全屏/部分 WinUI 应用）完全不可见，
// 而且连日志都不会产生——我们此前看到的 "uiless=0" 正是「被排除」而非
// 「不存在 UILess 宿主」。
inline const GUID kIID_ITfTextInputProcessorEx = {
    0x6e4e2102, 0xf9cd, 0x433d, {0xb4, 0x96, 0x30, 0x3c, 0xe0, 0x3a, 0x65, 0x07}};

struct ITfTextInputProcessorEx : public ITfTextInputProcessor {
    virtual HRESULT STDMETHODCALLTYPE ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
                                                 DWORD dwFlags) = 0;
};

// 存活 TIP 实例数（定义在 win32.cpp，DllCanUnloadNow 与 ctor/dtor 共用）。
extern volatile LONG g_liveObjects;

}  // namespace chaoset::win

class ChaosetTIP : public chaoset::win::ITfTextInputProcessorEx,
                  public ITfKeyEventSink,
                  public ITfCompositionSink,
                  public ITfThreadFocusSink,
                  public ITfActiveLanguageProfileNotifySink,
                  public chaoset::win::CandidateWindowHost,
                  public chaoset::win::TipThreadSink {
public:
    ChaosetTIP();
    virtual ~ChaosetTIP();
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override;
    ULONG STDMETHODCALLTYPE AddRef() override;
    ULONG STDMETHODCALLTYPE Release() override;
    HRESULT STDMETHODCALLTYPE Activate(ITfThreadMgr* ptim, TfClientId tid) override;
    HRESULT STDMETHODCALLTYPE ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
                                         DWORD dwFlags) override;
    HRESULT STDMETHODCALLTYPE Deactivate() override;
    HRESULT STDMETHODCALLTYPE OnSetFocus(BOOL fForeground) override;
    // ITfThreadFocusSink：TSF 线程 UI 焦点（替代 GetForegroundWindow 的 pid
    // 启发式——UWP/ApplicationFrameHost 宿主的前台窗口属于另一个进程）。
    HRESULT STDMETHODCALLTYPE OnSetThreadFocus() override;
    HRESULT STDMETHODCALLTYPE OnKillThreadFocus() override;
    // ITfActiveLanguageProfileNotifySink：TSF 的「输入法之间切换」通知。
    // 宿主在用户切走本输入法时常常不调用 Deactivate，靠它立即收口。
    HRESULT STDMETHODCALLTYPE OnActivated(REFCLSID clsid, REFGUID guidProfile,
                                          BOOL fActivated) override;
    HRESULT STDMETHODCALLTYPE OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                            BOOL* pfEaten) override;
    HRESULT STDMETHODCALLTYPE OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                        BOOL* pfEaten) override;
    HRESULT STDMETHODCALLTYPE OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                          BOOL* pfEaten) override;
    HRESULT STDMETHODCALLTYPE OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                      BOOL* pfEaten) override;
    HRESULT STDMETHODCALLTYPE OnPreservedKey(ITfContext* /*pic*/, REFGUID /*rguid*/, BOOL* pfEaten) override;
    HRESULT STDMETHODCALLTYPE OnCompositionTerminated(TfEditCookie /*ec*/,
                                                      ITfComposition* /*pComposition*/) override;
    void selectCandidateByIndex(int index) override;
    void deleteCandidateByIndex(int index) override;
    void flipPage(bool next) override;

private:
    ITfKeyEventSink* keySink();
    ITfCompositionSink* compositionSink();
    bool isComposing() const;
    bool hasCandidates() const;

    // ---- 中英切换：Shift 空按（按下后未按其他键即释放）----
    // 语义与 ibus 壳的 shiftTracker（shims/linux/engine/main.cpp:50-72）一致；
    // 事件路径见 OnTestKeyDown 等分流。
    //
    // **记账方式 2026-11-05 重写：按「一次按住会话」记账，不再按事件累加。**
    // 旧实现用 shiftDown_++/--：每收到一次 Shift 按下就 +1、每收到一次释放才 -1，
    // 只有计数回到 0 才结算。只要丢失**任意一次** KeyUp，计数就只增不减、永远
    // 回不到 0 —— 该进程内 Shift 空按**永久失效**，只能重启应用恢复。
    // 丢失 KeyUp 的典型场景（都不是宿主 bug，而是 Windows 的既定行为）：
    //   1. 按住 Shift 时切换窗口（Alt+Tab / 点别的窗口）：KeyUp 交给**新前台
    //      窗口**，本线程永远收不到；
    //   2. TIP 被 TSF 停用期间发生的按键（Deactivate → 再 Activate）；
    //   3. 宿主重复投递同一次按下（auto-repeat / TSF 与宿主各投递一次）。
    // 这正好解释实机症状：Terminal / 开始菜单 / Chrome（长驻进程 + 频繁切走
    // 窗口）切不了，Firefox（同一会话里连续空按）可以。
    // ---- 中英模式状态机（两档作用域：System/Process）----
    //
    // 本地中英模式缓存：所有判断都走 currentEnglish()，不要直接读 enMode_。
    //   System  —— 权威在 UI 宿主 g_uiEnglish + HKCU UiEnglish 镜像。获得焦点时
    //              从权威同步，enMode_ 跟随；按键路径只读本地缓存，距上次权威
    //              同步 >300ms 才补一次 HKCU 镜像读（注册表微秒级，无跨进程）。
    //   Process —— 进程各自独立：进程级 static bool（同进程多 TIP 实例共享、
    //              同进程多窗口也共享），获得焦点时把本进程模式上报托盘。
    bool enMode_ = false;
    static bool& processEnglish();
    // 获得焦点/激活/切换输入法时按作用域应用模式（两档语义唯一出口）。
    void applyModeForCurrentScope();
    bool currentEnglish();
    void syncEnglishFromAuthority();
    void storeEnglish(bool english);
    // 上次从权威源同步的时刻：currentEnglish 的 300ms TTL 复读用。
    DWORD lastAuthoritySyncTick_ = 0;
    // 本地 Shift 切中英的时刻：500ms 内不得被权威旧值回退（HKCU 由 UI 宿主
    // 异步写回，刚切完立刻复读会翻回旧状态）。
    DWORD lastLocalToggleTick_ = 0;
    // 当前按住的 Shift 物理键位掩码（bit0=左、bit1=右、bit2=泛化 VK_SHIFT）。
    // 用**掩码**而不是计数器：置位/清位天然幂等，键盘 auto-repeat 或宿主重复
    // 投递同一次按下都不会把它推到「永远回不到 0」——这正是旧实现 ++/-- 的
    // 致命缺陷。同时掩码能正确区分左右 Shift 同时按住的场景。
    unsigned shiftHeld_ = 0;
    bool sinceShift_ = true;    // 按住期间是否出现过其它键（true = 不是空按）
    // 按住期间焦点离开过。浏览器会在**窗口内部**把焦点在地址栏/页面元素之间
    // 搬来搬去，一样触发 OnSetFocus(FALSE)——此时不能直接丢弃按住状态，
    // 否则「按下 Shift → 焦点抖一下 → 松开」就永远切换不了（2026-11-05）。
    // 改为挂起：松开前焦点回来就照常切换，否则松开时只清状态不切换
    // （真·切走应用时不该切模式）。
    bool shiftHoldSuspended_ = false;

    // ---- 前台切换兜底（2026-11-05）----
    //
    // 为什么需要：Windows 11 开始菜单/搜索这类宿主**不投递 OnSetFocus(FALSE)**
    //（实机：关闭开始菜单后重开，上一轮的拼音与候选窗都还在）。TSF 焦点事件
    // 在这类宿主上不可靠，改用系统级的前台窗口事件（SetWinEventHook，
    // 事件驱动、非轮询）作为「用户已经离开本窗口」的可靠信号。
    //
    // 回调里**不碰 TSF**（可能不在本 TIP 的焦点上下文中，贸然 RequestEditSession
    // 会失败甚至干扰宿主）：只收起自己的浮窗并打标记，真正的状态清理由下一次
    // 按键在 TIP 线程上完成。
    HWINEVENTHOOK fgHook_ = nullptr;
    bool pendingStaleReset_ = false;

    // 线程级隔间（GUID_COMPARTMENT_KEYBOARD_OPENCLOSE）：把中英模式写进 TSF
    // 线程隔间（其他 TSF 客户端可读，语义与微软拼音一致）。任务栏「中/英」
    // 指示器由单例 UI 进程绘制，TIP 不再持有任何托盘状态。
    ITfCompartment* modeCompartment_ = nullptr;

    // **当前激活的输入法是不是我们**（2026-11-05 用户反馈：选定的输入法不是
    // chaoset 时按 Shift 仍会出中英提示）。
    //
    // 实测：切到其它输入法/英文键盘布局后，TSF 仍会把按键投给本 TIP 的按键
    // 下沉（我们的 Deactivate 未被调用或不及时），于是 Shift 照样触发切换。
    // 正确判据是查询本线程的**激活输入处理器档案**：
    //   ITfInputProcessorProfileMgr::GetActiveProfile(GUID_TFCAT_TIP_KEYBOARD)
    // 返回的 clsid 不是我们 → 直接放行所有按键，绝不参与输入。
    ITfInputProcessorProfileMgr* profileMgr_ = nullptr;
    bool isActiveTip();
    static void CALLBACK fgHookProc(HWINEVENTHOOK /*hook*/, DWORD event, HWND hwnd, LONG, LONG,
                                    DWORD, DWORD);
    void onForegroundChanged(HWND fg);
    bool shouldWatchForeground() const;
    void ensureForegroundHook();
    void unhookForeground();
    // ---- 激活状态机（2026-11-07 用户要求：未激活时不做任何操作）----
    //
    // inputActive_ 是**所有 TSF 按键/焦点逻辑的唯一闸门**：只有「TSF 焦点在本
    // 上下文 + 本线程激活档案是 Chaoset」时才为 true。为 false 时按键回调立即
    // 放行、不查询/不清理/不上报；清理已在上一次 active→inactive 迁移时完成。
    bool focused_ = false;      // OnSetFocus 记录的 TSF 焦点
    bool inputActive_ = false;  // 前台激活输入法（真正的“在用”）
    bool warmed_ = false;       // daemon 预热每实例只发一次（修重复 spawn）
    static bool foregroundIsSelf();
    void updateInputActive(bool fromFocusEvent);
    void warmDaemonInBackgroundOnce();
    void retireUiCompletely();
    void switchToInactiveProfile();
    bool bailOutIfInactive();
    void applyPendingStaleReset();
    bool ensureUiHostStartedByDaemon(bool settings = false);
    static unsigned shiftBit(WPARAM vk);
    static bool shiftPhysicallyDown();
    void resetShiftTracking();
    void suspendShiftHoldOnFocusLost();
    void beginShiftHold(unsigned bit);
    void finishShiftHold(ITfContext* pic);
    void endShiftHold(unsigned bit, ITfContext* pic = nullptr);
    void settleAllShiftHolds();
    void syncModeCompartment();
    void toggleEnMode(ITfContext* pic);
    void onShiftEvent(bool release, WPARAM vk, ITfContext* pic);
    void reconcileShiftState();
    static const char* keyPunctChar(WPARAM vk, bool shift);
    static bool isDpSemicolonKey(WPARAM vk, bool shift);
    static int digitIndex(WPARAM vk);
    static bool shiftActive();
    static bool isModifierKey(WPARAM vk);
    bool wouldHandle(WPARAM vk) const;
    bool handleKey(WPARAM vk, ITfContext* pic);
    ITfContext* focusContext();

    // focusContext() 返回的引用计数上下文：析构时 Release。此前大多数调用点
    // 直接丢弃返回值，每次鼠标选词/翻页都泄漏一个 ITfContext（2026-10-27
    // 审查发现，本次一并修正）。
    struct ContextRef {
        explicit ContextRef(ITfContext* c) : ctx(c) {}
        ~ContextRef() {
            if (ctx != nullptr) ctx->Release();
        }
        ContextRef(const ContextRef&) = delete;
        ContextRef& operator=(const ContextRef&) = delete;
        ITfContext* get() const { return ctx; }
        ITfContext* ctx;
    };
    static bool canInsert(ITfContext* ctx);
    static bool canCompose(ITfContext* ctx);

    // 合成所属上下文（render/applyPreedit 成功建立 composition_ 时记录）。
    // 生命周期与 composition_ 一致：建立时记下，OnCompositionTerminated /
    // commit / endComposition 清空。此后对合成的一切清理都必须对它做，
    // 不能再用 focusContext() 现查——焦点可能已经切到别的文档。
    ITfContext* compCtx_ = nullptr;

    // composition_ 所属上下文的清理 cookie：同步编辑会话成功后写入
    // （RequestEditSession 返回前 DoEditSession 已执行完）。endComposition
    // 在会话外被调用时无法拿 cookie，TF_ES_SYNC 失败（TS_E_SYNCHRONOUS）
    // 时保留上一次成功值比用 0 强——0 一定失败，旧值多半仍有效。
    TfEditCookie compEditCookie_ = 0;

    // canStartComposition 按上下文缓存：同一上下文反复按键时避免每键
    // GetStatus + 双 QueryInterface（不可合成宿主此前每键三次 COM 调用）。
    // 持有引用保证指针不悬垂；合成建立/上下文切换由指针比较自然失效。
    ITfContext* probeCtx_ = nullptr;
    bool probeCan_ = false;
    static bool canStartComposition(ITfContext* ctx);
    bool bailOutNoComposition();
    void render(ITfContext* pic);
    static HWND contextHostWindow(ITfContext* ctx);
    static HWND hostWindowOf(ITfContext* ctx);
    void updateCaretFromRange(ITfContext* ctx, TfEditCookie ec, ITfRange* range);
    void applyPreedit(ITfContext* ctx, TfEditCookie ec);
    bool commit(ITfContext* pic, const std::string& text);
    void applyCommit(ITfContext* ctx, TfEditCookie ec);
    void syncDpScheme(const chaoset::Comp& comp);
    void resetSession();
    void resetComposition();
    void flushOrCancel(ITfContext* pic);
    void endComposition();
    void applyEndComposition(ITfContext* /*ctx*/, TfEditCookie ec);

    LONG ref_ = 1;
    ITfThreadMgr* tm_ = nullptr;
    TfClientId clientId_ = 0;
    bool keySinkAdvised_ = false;
    // TSF 两个 sink 的 ITfSource cookie（Activate 里 AdviseSink 保存，
    // Deactivate/析构成对 UnadviseSink；幂等、初始为无效 cookie）：
    //   threadFocusCookie_    ITfThreadFocusSink（线程 UI 焦点）
    //   languageProfileCookie_ ITfActiveLanguageProfileNotifySink（输入法切换）
    DWORD threadFocusCookie_ = TF_INVALID_COOKIE;
    DWORD languageProfileCookie_ = TF_INVALID_COOKIE;
    void adviseThreadSinks();
    void unadviseThreadSinks();

    chaoset::win::CandidateWindow candidateWnd_;
    chaoset::win::ModeToast toast_;
    void showToast(const wchar_t* text, bool allowMouseAnchor = false);
    POINT caretPos_{0, 0};
    int caretLineHeight_ = 20;
    // 本次渲染是否拿到了**真实** GDI 光标矩形（caretPos_ 可信）。
    bool caretFound_ = false;
    // caretPos_ 是否来自鼠标兜底（宿主没有真实光标时）。提示条据此拒绝
    // 在鼠标位置显示，避免「提示跟着鼠标跑」（2026-11-05 用户反馈）。
    bool caretFromMouse_ = false;

    chaoset::win::PipeTransport transport_;
    chaoset::Session session_{transport_};
    chaoset::Comp comp_;
    // 渐进上屏的「已确认段」缓冲（2026-10-30 兼容性整改，与 macOS 壳
    // confirmedText 同语义）：SelectPrefix 返回的已确认文本**不再立即
    // 结束合成写盘**，而是并入当前合成一起展示（applyPreedit 写
    // confirmedPreedit_ + comp_.preedit），直到整条组词结束才一次落盘。
    //
    // 为什么（用户实测：长串组词中选 1（今），部分 App 里「今」丢失、
    // 剩余拼音卡死）：旧实现每次渐进选择都「EndComposition + 立刻
    // StartComposition」——对正确跟踪选区的 App 没问题，但对合成结束/
    // 重建时选区记账有缺陷的 App（部分 Chromium/Java/旧 Win32 宿主），
    // 新合成的锚点可能落回旧文本处，随后的 SetText 直接覆盖掉刚上屏的
    // 「今」，只剩剩余拼音。改单合成模型后，渐进上屏与逐键输入使用完全
    // 相同的原语（同一条合成内 SetText），不再中途结束/重建合成；落盘只剩
    // 「整条组词结束」一个时机，与微软拼音「合成内已转换段」的行为一致。
    // 清空时机：commit（落盘）、endComposition（取消）、OnCompositionTerminated
    // （宿主终止，文本已随合成留在文档）、activateServer 之外的一切
    // 会话结束路径——绝不能串进下一轮组词。
    std::string confirmedPreedit_;
    // 引擎双拼方案名（zrm/mspy/flypy；空 = 未启用）。只用于按键路由：
    // 双拼时 ';' 是引擎的组词/解码键，必须走 input 而不是 punct。
    // 来源见 syncDpScheme（IPC Composition.shuangpin）。
    std::string dpScheme_;
    bool halfWidth_ = false; // 全/半角标点开关（壳本地持有，随 punct 请求下发）
    // 词组联想（对齐 macOS 壳）：candidates 非空即联想中。选词上屏后按
    // 上屏词音节查续接词组继续展示，数字键可直接连选。
    chaoset::Comp assoc_;
    int assocPage_ = 0; // 联想当前页（0 起，本地分页每页 6 条）
    // 联想来源音节（撇号分隔）：右键删除联想候选后按同一来源重新查询续接
    // 词组，保证删除刷新不改变联想链（与 macOS associateSylls 同语义）。
    std::string assocSylls_;
    ITfComposition* composition_ = nullptr;
    // 合成所属宿主窗口（render 时缓存；前台事件回调只读，避免回调里碰 TSF）。
    HWND composeHostWnd_ = nullptr;
    ITfComposition* endTarget_ = nullptr;  // endComposition 编辑会话内的目标
    std::string committed_;
    bool commitWritten_ = false;
    bool preeditWritten_ = false;  // 预编辑确实写进了文档（渲染兜底判定用）
    void releaseCompositionContext();
    static std::vector<std::string> splitSyllables(const std::string& syllables);

    // 选词上屏后进入联想：查续接词组并复用候选窗展示（单页，页码 1/1）。
    // 候选栏齿轮 → 设置菜单（微信输入法交互参考）：菜单项带勾选态，
    // 点击即切换并立即生效（关闭时收起联想窗，开启后下次选词生效）。
    // TrackPopupMenu 前需 SetForegroundWindow（否则菜单点外部不消失，
    // MSDN KB135788 的经典要求），返回后补发 WM_NULL 复位。
public:
    void onWorkerForeground(HWND fg) override;
    void onWorkerModeAuthority() override;
    void onWorkerShowToast(int id) override;

    // 设置窗口入口（三端一致的独立设置窗口，macOS SettingsWindow 同构；
    // 定义见 tip_candidates.cpp）。
    void showSettingsWindow() override;
    bool isAssociatePanel() const override;

    // 联想翻页入口（供候选窗箭头调用；实现见 tip_candidates.cpp 的 flipAssociate）。
    void flipAssociateFromPanel(bool next) override;

    // 设置窗关闭联想后立即收起（候选窗收到跨进程广播后转发；macOS 同语义）。
    void clearAssociateFromSettings() override;

private:
    void enterAssociate(const std::string& syllables);
    void showAssociatePage();
    bool flipAssociate(bool next);
    void clearAssociate();
};

#endif  // CHAOSET_WIN_TIP_HPP_
