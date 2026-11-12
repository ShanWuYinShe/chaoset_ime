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
// allowSpawn：是否允许 worker 在管道不可用时懒启动 UI 宿主（CreateProcessW
// 只在 worker 线程）。调用方必须已确认 inputActive_（TSF 焦点 + 本线程激活
// 档案是 Chaoset）——这就是上报可信度的闸门；2026-11-11 起不再用
// GetForegroundWindow 的 pid 做第二道门限：UWP/ApplicationFrameHost 宿主的
// 前台窗口属于另一个进程，旧门限会把合法上报丢弃（「托盘有的情况下不显示」）。
//
// 上报失败不重发本次模式：下一次焦点/切换事件会再上报，UI 宿主
// 启动后还有 HKCU 记忆的上次模式兜底。
//
// 仍然只入队：管道写/重试/拉起 UI 宿主全部在 worker（零阻塞审计 T7）。
bool reportModeToUi(bool english, bool allowSpawn) {
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
    int arg2 = 0;  // kReportMode/kReportActive：当前中英模式（0=中文 1=英文）
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

class ShellWorker {
public:
    static ShellWorker& get() {
        static ShellWorker worker;
        return worker;
    }

    // 启动/停止的并发保护：TSF 在同进程里按**线程**各建一个 TIP 实例（explorer
    // 实测 4 个），每个实例的回调跑在各自的宿主线程上，而 worker 是进程级单例
    //——首次激活/首次托盘上报可能由两个宿主线程同时触发 start()。只靠 alive_
    // 快照不是幂等：两边都看到 false 会各建一个线程/事件（重复 worker、句柄泄漏、
    // stopEvent_ 数据竞争）。这里用一把只在生命周期路径上用的锁串行化；不进入
    // 任务队列热路径（post/loop 仍走 mu_），零阻塞语义不变。
    void start() {
        std::lock_guard<std::mutex> lk(lifecycleMu_);
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
        std::lock_guard<std::mutex> lk(lifecycleMu_);
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
                // 命令协议：mode <0|1> <pid>（发送进程 pid 由 TIP 侧进程附上，
                // UI 宿主按 pid 聚合可见性）。上报可信度由 TIP 的 TSF 焦点 +
                // 激活档案（inputActive_）决定，这里**不再**用 GetForegroundWindow
                // 的 pid 做门限：UWP/ApplicationFrameHost 宿主的前台窗口 pid
                // 与 TIP 进程 pid 不同，旧门限会丢弃合法上报。
                char cmd[64];
                wsprintfA(cmd, "mode %d %lu", task.arg != 0 ? 1 : 0,
                          static_cast<unsigned long>(GetCurrentProcessId()));
                if (uiSendCommandFast(cmd)) break;
                if (!task.allowSpawn) break;
                // UI 宿主没跑/被杀：事件驱动恢复（无定时器）——带当前模式懒启动
                // 首实例，并短重试本次命令；AC 宿主不能 CreateProcess，走 daemon
                // 代拉起。下一个 TSF 事件也会重发（active/mode 失败不丢状态）。
                const bool english = task.arg != 0;
                if (isAppContainer()) {
                    ensureUiHostByDaemonWorker(/*settings=*/false);
                } else {
                    ensureUiHostStarted(english ? L"--mode 1" : L"--mode 0");
                }
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
                // 命令协议（issue #3 语义）：active 1 <0|1> <pid> = 本输入法被系统
                // 选中（显示托盘并把该 pid 设为 owner）；active 0 <pid> = 用户切到了
                // 其它输入法（唯一隐藏入口，300ms 防抖）。失焦/停用/前台离开**不**
                // 发 active 0：托盘可见性与输入焦点解耦。
                // 发送失败且 allowSpawn 时重新拉起被杀/未启动的 UI 宿主（事件驱动
                // 恢复，无定时器；下一次 TSF 事件也会重发）。
                char cmd[64];
                if (task.arg != 0) {
                    const bool english = task.arg2 != 0;
                    wsprintfA(cmd, "active 1 %d %lu", english ? 1 : 0,
                              static_cast<unsigned long>(GetCurrentProcessId()));
                    if (uiSendCommandFast(cmd)) break;
                    if (!task.allowSpawn) break;
                    // 事件驱动恢复：UI 宿主被杀/未启动时懒启动并重试本次 active。
                    // AC 宿主走 daemon 代拉起（不能 CreateProcessW）。
                    if (isAppContainer()) {
                        ensureUiHostByDaemonWorker(/*settings=*/false);
                    } else {
                        ensureUiHostStarted(english ? L"--mode 1" : L"--mode 0");
                    }
                    // 首实例冷启动（CreateProcess + LoadLibrary + 管道监听）可能
                    // 超过 200ms：worker 线程一次性等最多 1s（不是轮询；失败后下一次
                    // TSF 事件会重发 active，事件驱动恢复）。
                    for (int i = 0; i < 40; ++i) {
                        if (uiSendCommandFast(cmd)) break;
                        Sleep(25);
                    }
                } else {
                    wsprintfA(cmd, "active 0 %lu",
                              static_cast<unsigned long>(GetCurrentProcessId()));
                    uiSendCommand(cmd, 4);
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

    std::mutex mu_;           // 任务队列保护（post/loop/flushLogs）
    std::mutex lifecycleMu_;  // start/stop 保护（同进程多宿主线程并发首次启动）
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

// 托盘可见性上报（issue #3）：active=1 = 本输入法被系统选中（激活迁移 / 模式
// 变化 / 输入法切回 / 前台回到本进程时上报；english 为当前模式，供 UI 宿主按
// pid 聚合显示）；active=0 = 用户切到了其它输入法（唯一隐藏入口）。失焦/停用/
// 前台离开**不**上报。命令由 worker 线程附上本进程 pid 后发送。
//
// 上报前按需启动 worker：这两个方向都可能来自「本实例还没有被激活迁移过」的
// TSF 事件——显示可能是「切回本输入法但窗口没有 TSF 文本焦点」（inputActive_
// 仍为 false），隐藏可能是「切走通知落在从未输入过的实例上」（如桌面/任务栏
// 线程的预激活实例）。只入队不启动就永远发不出去：托盘该出现时不出、该隐藏时
// 残留。启动的只是每进程一个 worker 线程（不建进程、不等管道，零阻塞契约内），
// 且每个进程最多一次。
void postActiveReport(bool active, bool english, bool allowSpawn) {
    if (!shellWorkerAlive()) shellWorkerStart();
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
