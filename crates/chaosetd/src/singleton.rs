#[cfg(unix)]
use std::path::Path;

#[cfg(unix)]
pub struct LockHandle {
    fd: std::os::unix::io::RawFd,
    #[allow(dead_code)]
    lock_path: std::path::PathBuf,
}

#[cfg(unix)]
impl LockHandle {
    pub fn acquire<P: AsRef<Path>>(socket_path: P) -> std::io::Result<Self> {
        use std::ffi::CString;
        use std::os::unix::ffi::OsStrExt;

        let lock_path = socket_path.as_ref().with_extension("sock.lock");
        if let Some(parent) = lock_path.parent() {
            std::fs::create_dir_all(parent)?;
        }

        let c_path = CString::new(lock_path.as_os_str().as_bytes())
            .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidInput, e))?;

        unsafe {
            let fd = libc::open(c_path.as_ptr(), libc::O_CREAT | libc::O_RDWR, 0o644);
            if fd < 0 {
                return Err(std::io::Error::last_os_error());
            }

            if libc::flock(fd, libc::LOCK_EX | libc::LOCK_NB) != 0 {
                let err = std::io::Error::last_os_error();
                libc::close(fd);
                if err.raw_os_error() == Some(libc::EWOULDBLOCK) {
                    return Err(std::io::Error::new(
                        std::io::ErrorKind::AlreadyExists,
                        format!(
                            "已有 chaosetd 在运行（{} 被占用），本次启动已放弃",
                            lock_path.display()
                        ),
                    ));
                }
                return Err(err);
            }

            Ok(Self { fd, lock_path })
        }
    }
}

#[cfg(unix)]
impl Drop for LockHandle {
    fn drop(&mut self) {
        if self.fd >= 0 {
            unsafe {
                libc::flock(self.fd, libc::LOCK_UN);
                libc::close(self.fd);
            }
            self.fd = -1;
        }
    }
}

#[cfg(windows)]
pub struct LockHandle {
    handle: windows_sys::Win32::Foundation::HANDLE,
}

#[cfg(windows)]
impl LockHandle {
    pub fn acquire(_endpoint: &str) -> std::io::Result<Self> {
        use windows_sys::Win32::Foundation::{CloseHandle, GetLastError, ERROR_ALREADY_EXISTS};
        use windows_sys::Win32::System::Threading::CreateMutexW;

        const MUTEX_NAME: &str = "Local\\ChaosetDaemon\0";
        let wide: Vec<u16> = MUTEX_NAME.encode_utf16().collect();

        unsafe {
            let h = CreateMutexW(std::ptr::null(), 0, wide.as_ptr());
            if h == 0 {
                return Err(std::io::Error::from_raw_os_error(GetLastError() as i32));
            }
            if GetLastError() == ERROR_ALREADY_EXISTS {
                CloseHandle(h);
                return Err(std::io::Error::new(
                    std::io::ErrorKind::AlreadyExists,
                    "已有 chaosetd 在运行（单实例互斥体 Local\\ChaosetDaemon 被占用），本次启动已放弃",
                ));
            }
            Ok(Self { handle: h })
        }
    }
}

#[cfg(windows)]
impl Drop for LockHandle {
    fn drop(&mut self) {
        if self.handle != 0 {
            unsafe {
                windows_sys::Win32::Foundation::CloseHandle(self.handle);
            }
            self.handle = 0;
        }
    }
}
