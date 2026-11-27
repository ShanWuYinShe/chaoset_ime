//! Windows 命名管道服务端：常驻监听实例 + 每连接一线程 + 管道 ACL（SDDL）。
//!
//! 从 server.rs 按职责拆出（server.rs 保留平台无关的连接处理与监听池骨架）。
//! 整个模块只在 Windows 下编译，条目不再逐个标 `cfg(windows)`。

use std::io::{Read, Write};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use super::{handle_connection, run_listener_pool, Refill, ServerConfig};

/// 常驻监听实例数。每个实例一个线程阻塞在 ConnectNamedPipe，客户端任意时刻
/// 都有实例可连；并发连接超过该数时多出的客户端拿到 ERROR_PIPE_BUSY，由壳
/// 下一键非阻塞重连——旧实现同一时刻只有 1 个监听实例。
const PIPE_LISTEN_INSTANCES: usize = 8;
/// 建实例失败后的退避（daemon 生命周期内一直重试）。
const CREATE_RETRY: Duration = Duration::from_millis(200);
/// 空闲时的等待上限：只为在 daemon 退出（running=false）时及时收尾。
const IDLE_WAIT: Duration = Duration::from_millis(250);

/// 命名管道服务端（Windows）。监听实例的补位、回收与失败退避见
/// `run_listener_pool`；本函数只负责「一个监听实例怎么建、连上后怎么服务」。
///
/// 旧实现在主线程上「建实例 → 阻塞等连接 → 起服务线程 → 再建下一实例」：
/// 客户端连上到下一实例建好之间没有任何实例在监听，并发重连的壳拿到
/// ERROR_FILE_NOT_FOUND 只能 fail-open；且 CreateNamedPipe 一旦失败就 `?`
/// 返回，daemon 主线程随即退出、所有已连客户端一起断开（issue #122）。
pub fn run_windows_server(
    pipe_name: &str,
    config: ServerConfig,
    running: Arc<AtomicBool>,
) -> std::io::Result<()> {
    let pipe_name = pipe_name.to_string();
    run_listener_pool(
        PIPE_LISTEN_INSTANCES,
        &running,
        IDLE_WAIT,
        CREATE_RETRY,
        |refill| spawn_pipe_worker(&pipe_name, &config, Arc::clone(&running), refill),
    );
    Ok(())
}

/// 建一个监听实例并起线程等连接；连上后该线程就地变成这条连接的服务线程。
/// 实例在 spawn 之前创建，CreateNamedPipe 的错误才能回报给监听池（退避重试）。
fn spawn_pipe_worker(
    pipe_name: &str,
    config: &ServerConfig,
    running: Arc<AtomicBool>,
    refill: Refill,
) -> std::io::Result<thread::JoinHandle<()>> {
    use windows_sys::Win32::Foundation::GetLastError;
    use windows_sys::Win32::System::Pipes::ConnectNamedPipe;

    let handle = create_named_pipe_instance(pipe_name)?;
    let dict = Arc::clone(&config.dict);
    let user_dict = config.user_dict.clone();
    let fuzzy = config.fuzzy;
    let traditional = config.traditional;
    let shuangpin = config.shuangpin;
    Ok(thread::spawn(move || {
        // 同步句柄：阻塞到客户端连上；客户端抢在 ConnectNamedPipe 之前连上时
        // 返回 ERROR_PIPE_CONNECTED（535），同样视为已连接。
        let connected =
            unsafe { ConnectNamedPipe(handle, std::ptr::null_mut()) != 0 || GetLastError() == 535 };
        {
            let (pending, signal) = &*refill;
            *pending.lock().unwrap_or_else(|p| p.into_inner()) += 1;
            signal.notify_one();
        }
        if connected && running.load(Ordering::SeqCst) {
            handle_connection(
                NamedPipeStream { handle },
                dict,
                user_dict,
                fuzzy,
                traditional,
                shuangpin,
            );
        } else {
            unsafe {
                windows_sys::Win32::Foundation::CloseHandle(handle);
            }
        }
    }))
}

pub struct NamedPipeStream {
    handle: windows_sys::Win32::Foundation::HANDLE,
}

impl Read for NamedPipeStream {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        use windows_sys::Win32::Storage::FileSystem::ReadFile;
        let mut bytes_read = 0;
        unsafe {
            let ok = ReadFile(
                self.handle,
                buf.as_mut_ptr() as _,
                buf.len() as u32,
                &mut bytes_read,
                std::ptr::null_mut(),
            );
            if ok == 0 {
                let err = std::io::Error::last_os_error();
                if err.raw_os_error() == Some(109) {
                    // ERROR_BROKEN_PIPE: 客户端断开连接
                    return Ok(0);
                }
                return Err(err);
            }
            Ok(bytes_read as usize)
        }
    }
}

impl Write for NamedPipeStream {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        use windows_sys::Win32::Storage::FileSystem::WriteFile;
        let mut bytes_written = 0;
        unsafe {
            let ok = WriteFile(
                self.handle,
                buf.as_ptr() as _,
                buf.len() as u32,
                &mut bytes_written,
                std::ptr::null_mut(),
            );
            if ok == 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(bytes_written as usize)
        }
    }

    fn flush(&mut self) -> std::io::Result<()> {
        // Windows 命名管道 WriteFile 同步返回时数据已交由内核管道缓冲；
        // FlushFileBuffers 会阻塞到客户端把字节读空（超时/断连时会把服务线程
        // 卡在同步等待上，客户端 crates/chaoset-ipc 已明文禁止调用它）。
        Ok(())
    }
}

impl Drop for NamedPipeStream {
    fn drop(&mut self) {
        use windows_sys::Win32::Foundation::CloseHandle;
        use windows_sys::Win32::System::Pipes::DisconnectNamedPipe;
        if self.handle != 0 {
            unsafe {
                DisconnectNamedPipe(self.handle);
                CloseHandle(self.handle);
            }
            self.handle = 0;
        }
    }
}

fn create_named_pipe_instance(
    pipe_name: &str,
) -> std::io::Result<windows_sys::Win32::Foundation::HANDLE> {
    use windows_sys::Win32::Foundation::{GetLastError, LocalFree};
    use windows_sys::Win32::Security::Authorization::ConvertStringSecurityDescriptorToSecurityDescriptorW;
    use windows_sys::Win32::Security::SECURITY_ATTRIBUTES;
    // PIPE_ACCESS_DUPLEX 是 FILE_FLAGS_AND_ATTRIBUTES，定义在 Storage::FileSystem
    // （System::Pipes 只有 NAMED_PIPE_MODE 那组常量）；导错模块在 CI 的 Windows
    // 构建里是硬错误，macOS/Linux 因 cfg(windows) 不编译而无法暴露。
    use windows_sys::Win32::Storage::FileSystem::PIPE_ACCESS_DUPLEX;
    use windows_sys::Win32::System::Pipes::{
        CreateNamedPipeW, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE, PIPE_UNLIMITED_INSTANCES, PIPE_WAIT,
    };

    let wide_name: Vec<u16> = pipe_name.encode_utf16().chain(std::iter::once(0)).collect();
    let sid = chaoset_config::paths::default_pipe_name()
        .map_err(|e| std::io::Error::new(std::io::ErrorKind::Other, e))?
        .strip_prefix(r"\\.\pipe\chaoset-")
        .unwrap_or("")
        .to_string();
    let sddl = format!(
        "D:P(A;;GA;;;SY)(A;;GA;;;{})(A;;GRGW;;;S-1-15-2-1)S:(ML;;NW;;;LW)\0",
        sid
    );
    let wide_sddl: Vec<u16> = sddl.encode_utf16().collect();

    let mut sec_desc = std::ptr::null_mut();
    let mut sa = SECURITY_ATTRIBUTES {
        nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
        lpSecurityDescriptor: std::ptr::null_mut(),
        bInheritHandle: 0,
    };

    unsafe {
        if ConvertStringSecurityDescriptorToSecurityDescriptorW(
            wide_sddl.as_ptr(),
            1, // SDDL_REVISION_1
            &mut sec_desc,
            std::ptr::null_mut(),
        ) != 0
        {
            sa.lpSecurityDescriptor = sec_desc;
        }

        let handle = CreateNamedPipeW(
            wide_name.as_ptr(),
            PIPE_ACCESS_DUPLEX,
            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
            PIPE_UNLIMITED_INSTANCES,
            65536,
            65536,
            0,
            &sa,
        );

        if !sec_desc.is_null() {
            LocalFree(sec_desc as _);
        }

        if handle == windows_sys::Win32::Foundation::INVALID_HANDLE_VALUE {
            return Err(std::io::Error::from_raw_os_error(GetLastError() as i32));
        }

        Ok(handle)
    }
}

