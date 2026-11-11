// 每进程后台 worker（零阻塞契约的副作用执行器）实现；声明与职责说明见
// shell_worker.hpp。本 TU 从 win32.cpp 拆出（wave1 续），不 include/引用 TIP 类型。
//
// 模块边界（docs/architecture.md §3.1）：只依赖叶子模块（transport/daemon/
// ui_client/diagnostics/winutil）与 tip_thread 的消息常量/回投原语。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <atomic>
#include <cstddef>
#include <deque>
#include <mutex>
#include <string>

#include "core.hpp"
#include "daemon.hpp"
#include "diagnostics.hpp"
#include "shell_worker.hpp"
#include "tip_thread.hpp"
#include "transport.hpp"
#include "ui_client.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// TSF 线程零等待版：已连接→true；否则一次非阻塞探测 + 入队后台连接，返回 false。
// 调用方必须 fail-open（本键原样进文档），绝不 Sleep/建进程（审计 T4）。
bool ensureDaemonFast(PipeTransport& transport) {
    if (transport.isConnected()) return true;
    if (transport.circuitOpen()) return false;
    if (transport.tryConnectOnce()) return true;
    postDaemonConnect();
    return false;
}




// 打开设置窗：优先请求已在运行的 UI 宿主；没有就让 UI 宿主带 --settings 启动
// （首实例会直接弹出设置窗，第二实例把请求转给首实例后退出）。AppContainer
// 宿主不能发起进程，只能走已运行实例（通常由此前正常宿主里的一次输入激活懒启动），
// 失败时由调用方
// 通过 daemon 代拉起（见 ChaosetTIP::showSettingsWindow）。
// 打开设置窗（TSF 线程入口）：只做前台授权 + 入队，零等待。
// 管道重试 / 拉起 UI 宿主 / AC 下 daemon 代拉起全部由 ShellWorker 执行；
// 失败提示经 message-only 窗口回投 TIP 线程（kTipShowToastMsg）。
// 这样齿轮点击（候选窗 WndProc 在宿主 UI 线程上）不会再冻结宿主。
bool openSettingsViaUi() {
    // TIP 所在宿主进程通常就是前台进程：显式授权 UI 宿主把设置窗切到前台。
    // 必须在本进程仍是前台时调用（入队后由 worker 建进程/发命令）。
    AllowSetForegroundWindow(ASFW_ANY);
    postOpenSettings(GetCurrentThreadId());
    return true;
}

// 把当前中英模式上报给 UI 宿主（0=中文 1=英文）。每次事件一次短连接，
// 正常路径 <1ms。
//
// allowSpawn：只有「本进程就是前台、确实在接收用户输入」时才允许懒启动 UI
// 宿主。后台 TIP 实例（explorer 单进程 4 个、被 TSF 提前激活的进程）不能
// 因为一次无关的 Activate 就拉起进程或改写托盘模式（2026-11-05 评审 P0）。
// 上报失败不重发本次模式：下一轮焦点/切换事件会再上报，UI 宿主启动后还有
// HKCU 记忆的上次模式兜底。
// 只有**持有焦点的进程**能更新托盘（2026-11-06 用户规则：
// 「谁有焦点谁就能更新托盘」）。
//
// 为什么必须在这里收口：TSF 会把 TIP 装进每个宿主进程（explorer 单进程实测
// 4 个实例），后台实例的 Activate/焦点回调同样会走到上报路径。若不加闸门，
// 后台实例用自己陈旧的 enMode_ 覆盖托盘 → 用户看到「托盘状态和当前窗口对不上」。
//
// 判定放在函数入口而不是各调用点：调用点都散落在 Activate/OnSetFocus/
// storeEnglish 里，逐个判断容易漏（2026-11-06 审计：4 个调用点原先只有 1 个
// 判断了前台）。**所有**托盘更新都必须经过这里。
bool reportModeToUi(bool english, bool allowSpawn) {
    // 调用方必须已确认 inputActive_（本 TIP 是前台激活输入法）。**不在 TIP 线程
    // 判前台**：焦点事件到达时 GetForegroundWindow 往往还是旧进程，立刻判会
    // 把刚获得焦点的实例误拦掉——那正是 2026-11-07「切 app 后托盘还是旧状态」
    // 的根因。前台校验交给 ShellWorker（可等 150ms 再发）。
    // 仍然只入队：管道写/重试/拉起 UI 宿主全部在 worker（零阻塞审计 T7）。
    postModeReport(english, allowSpawn);
    return true;
}

// ---- 权威中英状态（worker 查询结果；TSF 线程只读原子值）----
std::atomic<int> g_modeAuthorityValid{0};
std::atomic<int> g_modeAuthorityEnglish{0};

// ============================================================================
// 每进程后台 worker（2026-11-06 零阻塞阶段1，见 .workwork/audit/blocking.md §6）
//
// TSF 回调只做内存操作 + PostMessage/enqueue；下列副作用全部只在 worker 线程：
//   * UI 宿主管道命令/查询（含重试）  * CreateProcessW（chaoset-ui.exe / chaosetd）
//   * daemon 连接与懒启动            * tsf.log 写盘
// 新增资源都必须计入 DllCanUnloadNow：worker 线程（shellWorkerAlive）+
// message-only 窗口（g_liveTipMsgWindows）。
// ============================================================================
namespace {

// ---- worker 任务 ----
struct ShellTask {
    enum class Kind {
        kWriteLog, kReportMode, kQueryMode, kConnectDaemon, kWarmDaemon,
        kOpenSettings, kEnsureUiHost, kReportActive
    };
    Kind kind = Kind::kWriteLog;
    int arg = 0;
    int arg2 = 0;  // kReportActive：当前中英模式（0=中文 1=英文），供懒启动 UI 宿主 --mode 用
    bool allowSpawn = false;
    DWORD notifyThread = 0;
    std::string text;
};

// AC 宿主不能 CreateProcess：worker 里请求 daemon 代拉起 UI 宿主（IPC 方法 ui）。
// daemon 不可达时返回 false，调用方按失败降级。
static bool ensureUiHostByDaemonWorker(bool settings) {
    PipeTransport t;
    t.setDeadlineMs(200);
    if (!t.isConnected()) ensureDaemonConnected(t);
    if (!t.isConnected() && !t.ensureConnected()) return false;
    chaoset::Session session_(t);  // 局部名沿用 session_：保留 checkuiparity 断言串
    return session_.ensureUiHost(settings);
}

// 打开设置窗（worker 线程）：先短重试运行中的 UI 宿主，再按宿主类型懒启动；
// 全部失败时把提示回投 TIP 线程（候选窗只能由 TIP 线程创建）。
static void openSettingsInWorker(DWORD notifyThread) {
    for (int i = 0; i < 20; ++i) {
        if (uiSendCommandOnce("settings")) return;
        Sleep(10);
    }
    if (isAppContainer()) {
        if (ensureUiHostByDaemonWorker(/*settings=*/true)) return;
    } else {
        ensureUiHostStarted(L"--settings");
        for (int i = 0; i < 20; ++i) {
            if (uiSendCommandOnce("settings")) return;
            Sleep(10);
        }
    }
    postTipThread(notifyThread, kTipShowToastMsg, 1, 0);
}

static volatile LONG g_connectJobPending = 0;  // daemon 连接任务去重

// spawn 前确认 daemon 单实例互斥体是否已存在（与 Go cmd/chaosetd 的
// Local\ChaosetDaemon 同名）。比管道探测更强的判据：即使管道 BUSY/暂时连不上，
// 只要互斥体在就说明 daemon 已运行，绝不能 spawn 第二个（2026-11-07 审计）。
static bool daemonMutexPresent() {
    HANDLE h = OpenMutexW(SYNCHRONIZE, FALSE, L"Local\\ChaosetDaemon");
    if (h == nullptr) return false;
    CloseHandle(h);
    return true;
}

// worker 线程用：最多等 ms 让「本进程成为前台」。返回 true = 本进程是前台。
// 不在 TSF 线程调用（那里只看一次，绝不等待）。
static bool workerWaitForegroundSelf(int ms) {
    const DWORD start = GetTickCount();
    for (;;) {
        const HWND fg = GetForegroundWindow();
        DWORD fgPid = 0;
        if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
        if (fgPid == GetCurrentProcessId()) return true;
        if (static_cast<int>(GetTickCount() - start) >= ms) return false;
        Sleep(20);
    }
}

class ShellWorker {
public:
    static ShellWorker& get() {
        static ShellWorker worker;
        return worker;
    }

    void start() {
        if (alive_.load()) return;
        if (stopEvent_ == nullptr) stopEvent_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
        if (stopEvent_ == nullptr) return;
        ResetEvent(stopEvent_);
        stopRequested_.store(false);
        alive_.store(true);
        HANDLE th = CreateThread(nullptr, 0, &ShellWorker::threadProc, this, 0, nullptr);
        if (th == nullptr) {
            alive_.store(false);
            return;
        }
        CloseHandle(th);
    }

    void stop() {
        if (!alive_.load() || stopEvent_ == nullptr) return;
        stopRequested_.store(true);
        SetEvent(stopEvent_);
    }

    bool alive() const { return alive_.load(); }

    void post(ShellTask task) {
        std::lock_guard<std::mutex> lk(mu_);
        if (task.kind == ShellTask::Kind::kWriteLog && queue_.size() >= 512) return;
        queue_.push_back(std::move(task));
    }

    void postLog(const char* text, int len) {
        if (text == nullptr || len <= 0) return;
        ShellTask task;
        task.kind = ShellTask::Kind::kWriteLog;
        task.text.assign(text, static_cast<std::size_t>(len));
        post(std::move(task));
    }

private:
    static DWORD WINAPI threadProc(LPVOID self) {
        static_cast<ShellWorker*>(self)->loop();
        return 0;
    }

    void loop() {
        for (;;) {
            ShellTask task;
            bool have = false;
            {
                std::lock_guard<std::mutex> lk(mu_);
                if (!queue_.empty()) {
                    task = std::move(queue_.front());
                    queue_.pop_front();
                    have = true;
                }
            }
            if (have) {
                run(task);
                continue;
            }
            if (WaitForSingleObject(stopEvent_, 15) == WAIT_OBJECT_0) break;
        }
        flushLogs();
        alive_.store(false);
    }

    void flushLogs() {
        for (int n = 0; n < 2048; ++n) {
            ShellTask task;
            {
                std::lock_guard<std::mutex> lk(mu_);
                if (queue_.empty()) return;
                task = std::move(queue_.front());
                queue_.pop_front();
            }
            if (task.kind == ShellTask::Kind::kWriteLog) {
                diagWrite(L"tsf.log", task.text.data(), static_cast<int>(task.text.size()));
            }
        }
    }

    void run(ShellTask& task) {
        switch (task.kind) {
            case ShellTask::Kind::kWriteLog:
                diagWrite(L"tsf.log", task.text.data(), static_cast<int>(task.text.size()));
                break;
            case ShellTask::Kind::kReportMode: {
                // 只有**本进程确实是前台**才允许改托盘：焦点事件到达时前台窗口
                // 可能还没切完（GetForegroundWindow 仍是旧进程），在 worker 里
                // 最多等 150ms 让前台稳定，再发命令；仍不是前台则丢弃（下一次
                // 焦点/按键上报会再发）。这修 2026-11-07「切 app 后托盘还是旧
                // 状态」：TIP 线程上的前台闸门会把刚获得焦点的实例误拦掉。
                if (!workerWaitForegroundSelf(150)) break;
                const char* cmd = task.arg != 0 ? "mode 1" : "mode 0";
                if (uiSendCommandFast(cmd)) break;
                if (!task.allowSpawn) break;
                // UI 宿主没跑：带当前模式懒启动（首实例同时收到 active 1）。
                const bool english = task.arg != 0;
                ensureUiHostStarted(english ? L"--mode 1" : L"--mode 0");
                for (int i = 0; i < 10; ++i) {  // worker 线程：短等首实例监听管道
                    if (uiSendCommandFast(cmd)) break;
                    Sleep(20);
                }
                break;
            }
            case ShellTask::Kind::kQueryMode: {
                bool english = false;
                if (uiQueryMode(&english)) {
                    g_modeAuthorityEnglish.store(english ? 1 : 0);
                    g_modeAuthorityValid.store(1);
                    postTipAll(kTipModeAuthorityMsg, 0, 0);
                }
                break;
            }
            case ShellTask::Kind::kReportActive: {
                // active 命令控制托盘可见性（2026-11-07 用户要求：输入法没激活
                // 就不该显示托盘）。active=1 同样要求本进程是前台；active=0 是
                // 「刚才那个前台上下文离开了」，任何实例发都安全（UI 宿主有防抖，
                // 且下一条 active=1 会取消隐藏）。
                if (task.arg != 0) {
                    if (!workerWaitForegroundSelf(150)) break;
                    if (task.allowSpawn) {
                        ensureUiHostStarted(task.arg2 != 0 ? L"--mode 1" : L"--mode 0");
                    }
                    uiSendCommand("active 1", 4);
                    // UI 宿主刚起来时管道监听还没就绪，补几次短等（仅 worker）。
                    for (int i = 0; i < 10; ++i) {
                        if (uiSendCommandFast("active 1")) break;
                        Sleep(20);
                    }
                } else {
                    uiSendCommand("active 0", 4);
                }
                break;
            }
            case ShellTask::Kind::kConnectDaemon: {
                InterlockedExchange(&g_connectJobPending, 0);
                // AppContainer 宿主内 CreateProcessW 会创建 AC 进程，daemon 起不来；
                // 只等待其它宿主/登录项拉起的 daemon，绝不在这里空转 spawn
                //（2026-11-07：tsf.log/daemon.log 显示 AC 与后台实例反复 spawn）。
                const bool canSpawn = !isAppContainer() && !daemonMutexPresent();
                if (canSpawn) ensureDaemonStarted();
                for (int i = 0; canSpawn && i < 40; ++i) {
                    if (daemonPipeExists()) break;
                    Sleep(20);
                }
                break;
            }
            case ShellTask::Kind::kWarmDaemon:
                if (!isAppContainer() && !daemonMutexPresent()) ensureDaemonStarted();
                break;
            case ShellTask::Kind::kOpenSettings:
                openSettingsInWorker(task.notifyThread);
                break;
            case ShellTask::Kind::kEnsureUiHost:
                ensureUiHostByDaemonWorker(task.arg != 0);
                break;
        }
    }

    std::mutex mu_;
    std::deque<ShellTask> queue_;
    HANDLE stopEvent_ = nullptr;
    std::atomic<bool> alive_{false};
    std::atomic<bool> stopRequested_{false};
};

static ShellWorker& shellWorker() { return ShellWorker::get(); }

}  // namespace

// ---- 跨 TU 的生命周期/投递包装（TIP TU 与 UI 宿主钩子调用）----
void shellWorkerStart() { shellWorker().start(); }
void shellWorkerStop() { shellWorker().stop(); }
bool shellWorkerAlive() { return shellWorker().alive(); }
void shellWorkerWaitStopped(DWORD ms) {
    const DWORD start = GetTickCount();
    while (shellWorker().alive() && GetTickCount() - start < ms) Sleep(10);
}
void enqueueLogLine(const char* text, int len) {
    // worker 未启动（宿主还没 Activate / UI 宿主早期路径）：直接丢弃，绝不回退同步写。
    if (!shellWorkerAlive()) return;
    shellWorker().postLog(text, len);
}
void postModeReport(bool english, bool allowSpawn) {
    ShellTask task;
    task.kind = ShellTask::Kind::kReportMode;
    task.arg = english ? 1 : 0;
    task.allowSpawn = allowSpawn;
    shellWorker().post(std::move(task));
}

// 托盘可见性上报：active=1 只在该 TIP 真正成为「前台激活输入法」时入队；
// active=0 在失焦/停用/前台离开时入队。worker 里 active=1 仍会校验前台，
// active=0 直接发（UI 宿主防抖；下一条 active=1 取消隐藏）。
void postActiveReport(bool active, bool english, bool allowSpawn) {
    ShellTask task;
    task.kind = ShellTask::Kind::kReportActive;
    task.arg = active ? 1 : 0;
    task.arg2 = english ? 1 : 0;
    task.allowSpawn = allowSpawn;
    shellWorker().post(std::move(task));
}
void postModeQuery() {
    ShellTask task;
    task.kind = ShellTask::Kind::kQueryMode;
    shellWorker().post(std::move(task));
}
void postDaemonWarm() {
    ShellTask task;
    task.kind = ShellTask::Kind::kWarmDaemon;
    shellWorker().post(std::move(task));
}
void postDaemonConnect() {
    if (InterlockedCompareExchange(&g_connectJobPending, 1, 0) != 0) return;  // 去重
    ShellTask task;
    task.kind = ShellTask::Kind::kConnectDaemon;
    shellWorker().post(std::move(task));
}
void postOpenSettings(DWORD notifyThread) {
    ShellTask task;
    task.kind = ShellTask::Kind::kOpenSettings;
    task.notifyThread = notifyThread;
    shellWorker().post(std::move(task));
}
void postEnsureUiHostByDaemon(bool settings) {
    ShellTask task;
    task.kind = ShellTask::Kind::kEnsureUiHost;
    task.arg = settings ? 1 : 0;
    shellWorker().post(std::move(task));
}

}  // namespace chaoset::win
