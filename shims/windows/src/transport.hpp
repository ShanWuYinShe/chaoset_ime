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
    // 单次 I/O 等待上限（TSF 路径 30ms；worker/设置窗 200ms）。
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

    // 熔断：失败后 5s 内 TSF 线程不再尝试（避免每个焦点/按键重复失败 → fail-open）。
    bool circuitOpen() const {
        if (brokenUntil_ == 0) return false;
        return static_cast<LONG>(GetTickCount() - brokenUntil_) < 0;
    }

    bool ensureConnected() {
        if (pipe_ != INVALID_HANDLE_VALUE) return true;
        DWORD now = GetTickCount();
        if (now - lastFail_ < 2000) return false;  // 失败冷却 2 秒，避免频繁重试阻塞
        if (pipeName_.empty()) {
            lastFail_ = now;
            return false;
        }
        HANDLE h = INVALID_HANDLE_VALUE;
        // daemon 可能尚未创建管道实例：最多等 3×20ms。只在 worker/设置窗线程调用。
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
        if (pipe_ == INVALID_HANDLE_VALUE) {
            if (!autoConnect_) return false;  // TSF 线程：fail-open，绝不在写路径里建连接
            if (!ensureConnected()) return false;
        }
        return pipeIo(true, data, len);
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
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
    // 重叠 I/O + 有界等待：单次 I/O 等待上限 deadlineMs_；超时立即 CancelIoEx +
    // 断开连接并返回（绝不 WaitForSingleObject(INFINITE)，审计 T2）。
    bool pipeIo(bool isWrite, const std::uint8_t* data, std::size_t len) {
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
                        // 有界失败：关闭管道会取消未完成 I/O；此处不再等待。
                        ::CancelIoEx(pipe_, &ov);
                        ::CloseHandle(ov.hEvent);
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
        brokenUntil_ = lastFail_ + 5000;  // 熔断 5s：TSF 线程 fail-open，后台重连
    }

    HANDLE pipe_ = INVALID_HANDLE_VALUE;
    DWORD lastFail_ = 0;
    DWORD brokenUntil_ = 0;
    DWORD deadlineMs_ = 200;
    bool autoConnect_ = true;
    std::wstring pipeName_;
};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_TRANSPORT_HPP_
