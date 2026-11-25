// daemon 命名管道：管道名、存在性探测、带截止时间/熔断的 PipeTransport。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 TIP TU（tip.hpp /
// tip_*.cpp）的类型；实现见 transport.cpp。
//
// 2026-11-06 零阻塞审计后：连接建立/懒启动在 ShellWorker 后台线程完成；
// TSF 线程只允许 tryConnectOnce()（一次非阻塞 CreateFileW）与有截止时间的请求。

#ifndef CHAOSET_WIN_TRANSPORT_HPP_
#define CHAOSET_WIN_TRANSPORT_HPP_

#include <windows.h>

#include <algorithm>
#include <cstdint>
#include <string>

#include "core.hpp"
#include "diagnostics.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// 按键路径的请求截止时间（毫秒，issue #149）。TSF 回调在宿主 UI 线程上同步执行，
// 所以截止值同时是「长尾容忍」与「宿主最坏阻塞」的折中；截止是上限而非固定等待：
// 正常请求 1–5ms 就返回，只有落在 30–150ms 的长尾会真的多等——而它们正是旧 30ms
// 会误杀成失步的那批。
//   * 正常按键 150ms：旧 30ms 低于整链路长尾（本仓 macOS 壳 8 核满载实测 p99
//     17–20ms、最大 31–57ms），正常输入会被误判成失步（失步期的选词/翻页键只刷新
//     候选、不执行本次选词）；150ms 给长尾留约 2.6 倍余量，同时把宿主 UI 线程的最坏
//     阻塞限制在 150ms——远小于 macOS/Linux 独立进程壳的 250/300ms，不对齐它们。
//   * 失步重建 30ms：失步期用户已在本地组词，重建只为尽快回到同步态，短截止保证
//     daemon 繁忙/挂起时不会把宿主 UI 线程按在长截止上（见 tip_desync.cpp）。
constexpr DWORD kKeyRequestDeadlineMs = 150;
constexpr DWORD kDesyncRebuildDeadlineMs = 30;

// 当前用户命名管道名：\\.\pipe\chaoset-<SID>，与 Go daemon 的
// ipc.DefaultPipeName() 同规则（SID 而非用户名，多用户/非 ASCII 安全）。
std::wstring daemonPipeName();

// 非阻塞探测：daemon 管道实例是否已存在/可连（一次 CreateFileW，失败立即返回）。
bool daemonPipeExists();

class PipeTransport : public chaoset::Transport {
public:
    PipeTransport() : pipeName_(daemonPipeName()) { deadlineMs_ = 200; }
    ~PipeTransport() override { closeConn(); }

    bool isConnected() const { return pipe_ != INVALID_HANDLE_VALUE; }

    // TSF 线程上的 TIP 用 setAutoConnect(false)：writeAll 绝不自行发起需要等待的连接。
    void setAutoConnect(bool on) { autoConnect_ = on; }
    // 单次 I/O 等待上限（TSF 正常按键 150ms、失步重建 30ms；worker 200ms）。
    void setDeadlineMs(DWORD ms) { deadlineMs_ = (ms == 0) ? 200 : ms; }

    // 单次非阻塞连接：成功即返回；失败（含管道忙）立即返回 false，不做任何等待。
    // 这是 TSF 线程唯一允许的连接动作（一次 CreateFileW + 交回句柄）。
    bool tryConnectOnce() {
        if (pipe_ != INVALID_HANDLE_VALUE) return true;
        if (pipeName_.empty()) return false;
        HANDLE h = ::CreateFileW(pipeName_.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr,
                                 OPEN_EXISTING, FILE_FLAG_OVERLAPPED, nullptr);
        if (h == INVALID_HANDLE_VALUE) return false;
        pipe_ = h;
        lastFail_ = 0;
        brokenUntil_ = 0;
        return true;
    }

    // 熔断：失败后 5s 内不再走慢连接路径 ensureDaemonConnected（worker 专用）。
    // TSF 按键路径**不使用**它：那里每键只做一次非阻塞 tryConnectOnce，超时后
    // 必须能立刻重连重建组词，否则会退化成整段时间只能打英文（issue #122）。
    bool circuitOpen() const {
        if (brokenUntil_ == 0) return false;
        return static_cast<LONG>(GetTickCount() - brokenUntil_) < 0;
    }

    // 上一次 I/O 失败是否为「等到截止仍无应答」（区别于断管/写失败/连不上）。
    // TSF 按键路径据此决定超时后保留本地组词、下一键重建（issue #122）。
    bool lastIoWasTimeout() const { return lastIoWasTimeout_; }
    bool lastReadTimedOut() const override { return lastReadTimedOut_; }

    bool ensureConnected() {
        if (pipe_ != INVALID_HANDLE_VALUE) return true;
        DWORD now = GetTickCount();
        if (now - lastFail_ < 2000) return false;  // 失败冷却 2 秒，避免频繁重试阻塞
        if (pipeName_.empty()) {
            lastFail_ = now;
            return false;
        }
        HANDLE h = INVALID_HANDLE_VALUE;
        // daemon 可能尚未创建管道实例：最多等 3×20ms。只在 worker 线程调用。
        for (int i = 0; i < 3; ++i) {
            h = ::CreateFileW(pipeName_.c_str(), GENERIC_READ | GENERIC_WRITE,
                              0, nullptr, OPEN_EXISTING, FILE_FLAG_OVERLAPPED, nullptr);
            if (h != INVALID_HANDLE_VALUE) break;
            if (GetLastError() != ERROR_PIPE_BUSY) break;
            if (!::WaitNamedPipeW(pipeName_.c_str(), 20)) break;
        }
        if (h == INVALID_HANDLE_VALUE) {
            lastFail_ = now;
            // 每进程只记一次：区分 daemon 未起 与 管道 ACL 拒绝。
            static volatile LONG pipeFailLogged = 0;
            if (InterlockedCompareExchange(&pipeFailLogged, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log",
                           "pipe connect failed err=%lu appcontainer=%d\n",
                           GetLastError(), isAppContainer() ? 1 : 0);
            }
            return false;
        }
        pipe_ = h;
        return true;
    }

    bool writeAll(const std::uint8_t* data, std::size_t len) override {
        // 未连接时的直接失败不是超时：先清标记，避免残留上一次超时的值。
        lastIoWasTimeout_ = false;
        if (pipe_ == INVALID_HANDLE_VALUE) {
            if (!autoConnect_) return false;  // TSF 线程：fail-open，绝不在写路径里建连接
            if (!ensureConnected()) return false;
        }
        return pipeIo(true, data, len);
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
        lastIoWasTimeout_ = false;
        if (pipe_ == INVALID_HANDLE_VALUE) return false;
        return pipeIo(false, buf, len);
    }

    void closeConn() override {
        if (pipe_ != INVALID_HANDLE_VALUE) {
            ::CloseHandle(pipe_);
            pipe_ = INVALID_HANDLE_VALUE;
        }
        lastFail_ = 0;
    }

private:
    // 重叠 I/O + 有界等待：单次 I/O 等待上限 deadlineMs_（绝不
    // WaitForSingleObject(INFINITE)，审计 T2）。
    //
    // 超时的处置按方向区分（2026-11-24 用户要求「不要因为超时就断开连接」）：
    //   * **写超时 → 断连**：请求帧可能只写了一半，连接上的字节流再也对不齐；
    //   * **读超时 → 保留连接**：请求已完整发出，daemon 只是算得慢，响应会迟到。
    //     断连会让 daemon 会话随连接消失（用户已输入的拼音全丢），而保留连接时
    //     daemon 的 Session 原地不动，下一键就是正常增量输入。迟到响应由
    //     Session::call 在下次请求前按帧长丢弃（core.cpp drainStaleResponse）。
    bool pipeIo(bool isWrite, const std::uint8_t* data, std::size_t len) {
        lastIoWasTimeout_ = false;
        lastReadTimedOut_ = false;
        std::size_t off = 0;
        while (off < len) {
            DWORD chunk = static_cast<DWORD>(
                std::min<std::size_t>(len - off, 1u << 20));
            OVERLAPPED ov{};
            ov.hEvent = ::CreateEventW(nullptr, TRUE, FALSE, nullptr);
            if (ov.hEvent == nullptr) {
                failConn();
                return false;
            }
            DWORD done = 0;
            BOOL ok = isWrite
                          ? ::WriteFile(pipe_, data + off, chunk, &done, &ov)
                          : ::ReadFile(pipe_, const_cast<std::uint8_t*>(data) + off,
                                       chunk, &done, &ov);
            if (!ok) {
                DWORD err = ::GetLastError();
                if (err == ERROR_IO_PENDING) {
                    DWORD wait = ::WaitForSingleObject(ov.hEvent, deadlineMs_);
                    if (wait == WAIT_OBJECT_0) {
                        ok = ::GetOverlappedResult(pipe_, &ov, &done, FALSE);
                    } else {
                        // 有界失败（不等待）：先取消本次重叠 I/O，再取回取消前已实际
                        // 读进缓冲的字节数——这决定这次超时能不能保留连接。
                        ::CancelIoEx(pipe_, &ov);
                        DWORD cancelled = 0;
                        // 取消后 GetOverlappedResult 返回 FALSE，但出参给出已传输字节数；
                        // 忽略返回值，只取字节数。
                        (void)::GetOverlappedResult(pipe_, &ov, &cancelled, TRUE);
                        ::CloseHandle(ov.hEvent);
                        lastIoWasTimeout_ = true;
                        // **只有「一个字节都没消费」的读超时才能保留连接**：如果这次
                        // readAll 已经消费了字节（前面的 chunk 或本次部分传输），这些字节
                        // 已从管道取出却被丢弃，帧边界就永久错位了——保留连接只会让后续
                        // 每次读取都解析到垃圾。所以部分消费必须按「连接已坏」处理。
                        const bool nothingConsumed = off == 0 && cancelled == 0;
                        lastReadTimedOut_ = !isWrite && nothingConsumed;
                        if (!isWrite && nothingConsumed) {
                            // 干净读超时：保留连接（见函数头注释）。daemon 的响应会迟到，
                            // 由上层记为「待丢弃」并在下次请求前 drain。
                            return false;
                        }
                        failConn();
                        return false;
                    }
                }
            }
            ::CloseHandle(ov.hEvent);
            if (!ok || done == 0) {
                failConn();
                return false;
            }
            off += done;
        }
        return true;
    }

    void failConn() {
        if (pipe_ != INVALID_HANDLE_VALUE) {
            ::CloseHandle(pipe_);
            pipe_ = INVALID_HANDLE_VALUE;
        }
        lastFail_ = GetTickCount();
        brokenUntil_ = lastFail_ + 5000;  // worker 慢连接路径熔断 5s；TSF 按键路径忽略
    }

    HANDLE pipe_ = INVALID_HANDLE_VALUE;
    DWORD lastFail_ = 0;
    DWORD brokenUntil_ = 0;
    DWORD deadlineMs_ = 200;
    bool autoConnect_ = true;
    bool lastIoWasTimeout_ = false;
    /// 仅读方向：上一次 readAll 失败是「等到截止仍无数据」（区别于写超时/断管）。
    bool lastReadTimedOut_ = false;
    std::wstring pipeName_;
};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_TRANSPORT_HPP_
