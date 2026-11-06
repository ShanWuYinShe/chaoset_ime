// daemon 命名管道名/探测（模块实现；PipeTransport 定义在 transport.hpp）。
// 叶子模块：不得 include/引用 win32.cpp（TIP TU）的类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <sddl.h>  // ConvertSidToStringSidW（管道名按当前用户 SID）
#include <string>
#include <vector>

#include "transport.hpp"

namespace chaoset::win {

// ---- 传输：Windows 命名管道 ----
//
// 壳与 daemon 之间唯一的通道：\\.\pipe\chaoset-<SID>（daemon 用 -pipe 启动，
// 管道名按当前用户 SID 生成）。命名管道无端口、ACL 限当前用户 + AppContainer
// 包；没有第二传输、没有回退——管道连不上即 daemon 不可达，按键原样放行。
//
// 2026-11-06 零阻塞审计后：连接建立/懒启动在 ShellWorker 后台线程完成；TSF
// 线程只允许 tryConnectOnce()（一次非阻塞 CreateFileW）与有截止时间的请求。

// 当前用户命名管道名：\\.\pipe\chaoset-<SID>，与 Go daemon 的
// ipc.DefaultPipeName() 同规则（SID 而非用户名，多用户/非 ASCII 安全）。
std::wstring daemonPipeName() {
    HANDLE token = nullptr;
    if (!::OpenProcessToken(::GetCurrentProcess(), TOKEN_QUERY, &token)) return L"";
    DWORD len = 0;
    ::GetTokenInformation(token, TokenUser, nullptr, 0, &len);
    if (len == 0) {
        ::CloseHandle(token);
        return L"";
    }
    std::vector<BYTE> buf(len);
    if (!::GetTokenInformation(token, TokenUser, buf.data(), len, &len)) {
        ::CloseHandle(token);
        return L"";
    }
    auto* user = reinterpret_cast<TOKEN_USER*>(buf.data());
    LPWSTR sid = nullptr;
    if (!::ConvertSidToStringSidW(user->User.Sid, &sid)) {
        ::CloseHandle(token);
        return L"";
    }
    std::wstring name = L"\\\\.\\pipe\\chaoset-";
    name += sid;
    ::LocalFree(sid);
    ::CloseHandle(token);
    return name;
}

// 非阻塞探测：daemon 管道实例是否已存在/可连（一次 CreateFileW，失败立即返回）。
// 用途：spawn 前判断「其实已经在运行」，修掉 115/125 次空转 spawn（审计 T8）。
bool daemonPipeExists() {
    const std::wstring name = daemonPipeName();
    if (name.empty()) return false;
    HANDLE h = ::CreateFileW(name.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr,
                             OPEN_EXISTING, FILE_FLAG_OVERLAPPED, nullptr);
    if (h == INVALID_HANDLE_VALUE) return false;
    ::CloseHandle(h);
    return true;
}

}  // namespace chaoset::win
