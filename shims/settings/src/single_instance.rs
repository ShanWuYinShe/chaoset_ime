//! 设置中心单实例。
//!
//! 原生设置窗删除后，三端入口都直接拉起本程序；这里保证重复启动不会开出
//! 多个窗口：第二次启动把「目标页」转发给已运行实例后退出。
//!
//! - Unix（macOS/Linux）：首个实例监听本地 socket（优先 `$XDG_RUNTIME_DIR`，
//!   否则临时目录 + 用户名），第二个实例连上后写一行页号并退出；首实例在 UI
//!   线程用定时器取页并 `show()`，不引入跨线程 UI 调用。
//! - Windows：命名互斥体 `Local\\ChaosetSettingsCenter` 判重；重复启动用
//!   `FindWindowW(L"Chaoset 设置")` + `ShowWindow(SW_RESTORE)` +
//!   `SetForegroundWindow` 把已有窗口拉到前台。
//!
//! 单实例不可用（目录不可写等）时不影响主流程：只退化成本次可打开窗口。

use std::sync::mpsc;

/// 首实例守卫：持有「其他实例请求的页」接收端（Unix）。
pub struct Guard {
    #[cfg(unix)]
    receiver: Option<mpsc::Receiver<i32>>,
}

impl Guard {
    /// 取出页号通道；调用方负责在 UI 线程消费（Unix）。Windows 返回 None。
    pub fn pages(&mut self) -> Option<mpsc::Receiver<i32>> {
        #[cfg(unix)]
        {
            return self.receiver.take();
        }
        #[cfg(not(unix))]
        {
            None
        }
    }
}

/// 尝试成为首实例。
/// - `page`：本次启动想展示的页，第二个实例会转发给首实例。
/// - 返回 `None` 表示已有实例在运行（已转发并应退出）。
/// - 返回 `Some` 表示本进程继续启动窗口；单实例不可用时同样返回 `Some`。
pub fn acquire(page: i32) -> Option<Guard> {
    imp::acquire(page)
}

#[cfg(unix)]
mod imp {
    use super::*;
    use std::io::{Read, Write};
    use std::net::Shutdown;
    use std::os::unix::net::{UnixListener, UnixStream};
    use std::path::PathBuf;

    fn socket_path() -> PathBuf {
        if let Ok(dir) = std::env::var("XDG_RUNTIME_DIR") {
            if !dir.is_empty() {
                return PathBuf::from(dir).join("chaoset-settings.sock");
            }
        }
        let user = std::env::var("USER")
            .or_else(|_| std::env::var("LOGNAME"))
            .unwrap_or_else(|_| "user".to_string());
        std::env::temp_dir().join(format!("chaoset-settings-{}.sock", user))
    }

    pub fn acquire(page: i32) -> Option<Guard> {
        let path = socket_path();
        match UnixListener::bind(&path) {
            Ok(listener) => Some(start(listener)),
            Err(err) if err.kind() == std::io::ErrorKind::AddrInUse => {
                if forward(&path, page) {
                    return None;
                }
                // 无进程监听的残留 socket：清掉后重试一次（崩溃/强杀会留下）。
                let _ = std::fs::remove_file(&path);
                match UnixListener::bind(&path) {
                    Ok(listener) => Some(start(listener)),
                    Err(retry) => {
                        eprintln!("警告：设置中心单实例不可用（{}），本次仍打开窗口", retry);
                        Some(Guard { receiver: None })
                    }
                }
            }
            Err(err) => {
                eprintln!("警告：设置中心单实例不可用（{}），本次仍打开窗口", err);
                Some(Guard { receiver: None })
            }
        }
    }

    /// 把页号转发给已运行实例；成功返回 true。
    fn forward(path: &PathBuf, page: i32) -> bool {
        match UnixStream::connect(path) {
            Ok(mut stream) => {
                let _ = stream.write_all(format!("{}\n", page).as_bytes());
                let _ = stream.shutdown(Shutdown::Write);
                true
            }
            Err(_) => false,
        }
    }

    fn start(listener: UnixListener) -> Guard {
        let (tx, rx) = mpsc::channel::<i32>();
        std::thread::spawn(move || {
            for stream in listener.incoming() {
                let Ok(mut stream) = stream else { break };
                let mut buf = [0u8; 32];
                let n = stream.read(&mut buf).unwrap_or(0);
                let page = std::str::from_utf8(&buf[..n])
                    .ok()
                    .and_then(|text| text.trim().parse::<i32>().ok())
                    .unwrap_or(0);
                if tx.send(page).is_err() {
                    break;
                }
            }
        });
        Guard { receiver: Some(rx) }
    }
}

#[cfg(windows)]
mod imp {
    use super::*;
    use std::ffi::c_void;

    type Handle = *mut c_void;
    const ERROR_ALREADY_EXISTS: u32 = 183;
    const SW_RESTORE: i32 = 9;

    #[link(name = "kernel32")]
    extern "system" {
        fn CreateMutexW(attrs: *mut c_void, initial_owner: i32, name: *const u16) -> Handle;
        fn GetLastError() -> u32;
        fn CloseHandle(handle: Handle) -> i32;
        fn FindWindowW(class: *const u16, title: *const u16) -> Handle;
    }

    #[link(name = "user32")]
    extern "system" {
        fn ShowWindow(handle: Handle, cmd: i32) -> i32;
        fn SetForegroundWindow(handle: Handle) -> i32;
    }

    fn wide(text: &str) -> Vec<u16> {
        text.encode_utf16().chain(std::iter::once(0)).collect()
    }

    pub fn acquire(_page: usize) -> Option<Guard> {
        let name = wide("Local\\ChaosetSettingsCenter");
        let handle = unsafe { CreateMutexW(std::ptr::null_mut(), 0, name.as_ptr()) };
        if handle.is_null() {
            return Some(Guard {});
        }
        if unsafe { GetLastError() } == ERROR_ALREADY_EXISTS {
            unsafe { CloseHandle(handle) };
            let title = wide("Chaoset 设置");
            let window = unsafe { FindWindowW(std::ptr::null(), title.as_ptr()) };
            if !window.is_null() {
                unsafe {
                    ShowWindow(window, SW_RESTORE);
                    SetForegroundWindow(window);
                }
            }
            return None;
        }
        Some(Guard {})
    }
}

#[cfg(not(any(unix, windows)))]
mod imp {
    use super::*;
    pub fn acquire(_page: usize) -> Option<Guard> {
        Some(Guard {})
    }
}
