use chaoset_engine::{Dictionary, Session, ShuangpinScheme, UserDict};
use std::io::{Read, Write};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;

use crate::ipc::{dispatch, read_message, write_message, Request, Response};

pub struct ServerConfig {
    pub dict: Arc<Dictionary>,
    pub user_dict: Option<Arc<UserDict>>,
    pub fuzzy: bool,
    pub traditional: bool,
    pub shuangpin: Option<ShuangpinScheme>,
}

#[cfg(unix)]
pub fn run_unix_server(
    listener: std::os::unix::net::UnixListener,
    config: ServerConfig,
    running: Arc<AtomicBool>,
) -> std::io::Result<()> {
    listener.set_nonblocking(true)?;

    while running.load(Ordering::SeqCst) {
        match listener.accept() {
            Ok((stream, _addr)) => {
                let dict = Arc::clone(&config.dict);
                let user_dict = config.user_dict.clone();
                let fuzzy = config.fuzzy;
                let traditional = config.traditional;
                let shuangpin = config.shuangpin;

                thread::spawn(move || {
                    let _ = stream.set_nonblocking(false);
                    handle_connection(stream, dict, user_dict, fuzzy, traditional, shuangpin);
                });
            }
            Err(ref e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                thread::sleep(std::time::Duration::from_millis(10));
            }
            Err(e) => {
                if !running.load(Ordering::SeqCst) {
                    break;
                }
                eprintln!("接受连接失败: {}", e);
            }
        }
    }
    Ok(())
}

#[cfg(windows)]
pub fn run_windows_server(
    pipe_name: &str,
    config: ServerConfig,
    running: Arc<AtomicBool>,
) -> std::io::Result<()> {
    use windows_sys::Win32::Foundation::GetLastError;
    use windows_sys::Win32::System::Pipes::ConnectNamedPipe;

    while running.load(Ordering::SeqCst) {
        let handle = create_named_pipe_instance(pipe_name)?;
        let connected =
            unsafe { ConnectNamedPipe(handle, std::ptr::null_mut()) != 0 || GetLastError() == 535 }; // ERROR_PIPE_CONNECTED = 535
        if connected {
            let dict = Arc::clone(&config.dict);
            let user_dict = config.user_dict.clone();
            let fuzzy = config.fuzzy;
            let traditional = config.traditional;
            let shuangpin = config.shuangpin;
            let stream = NamedPipeStream { handle };

            thread::spawn(move || {
                handle_connection(stream, dict, user_dict, fuzzy, traditional, shuangpin);
            });
        } else {
            unsafe {
                windows_sys::Win32::Foundation::CloseHandle(handle);
            }
        }
    }
    Ok(())
}

#[cfg(windows)]
pub struct NamedPipeStream {
    handle: windows_sys::Win32::Foundation::HANDLE,
}

#[cfg(windows)]
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

#[cfg(windows)]
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
        use windows_sys::Win32::Storage::FileSystem::FlushFileBuffers;
        unsafe {
            FlushFileBuffers(self.handle);
        }
        Ok(())
    }
}

#[cfg(windows)]
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

#[cfg(windows)]
fn create_named_pipe_instance(
    pipe_name: &str,
) -> std::io::Result<windows_sys::Win32::Foundation::HANDLE> {
    use windows_sys::Win32::Foundation::{GetLastError, LocalFree};
    use windows_sys::Win32::Security::Authorization::ConvertStringSecurityDescriptorToSecurityDescriptorW;
    use windows_sys::Win32::Security::SECURITY_ATTRIBUTES;
    use windows_sys::Win32::System::Pipes::{
        CreateNamedPipeW, PIPE_ACCESS_DUPLEX, PIPE_READMODE_BYTE, PIPE_TYPE_BYTE,
        PIPE_UNLIMITED_INSTANCES, PIPE_WAIT,
    };

    let wide_name: Vec<u16> = pipe_name.encode_utf16().chain(std::iter::once(0)).collect();
    let sid = crate::appdirs::default_pipe_name()?
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

fn handle_connection<S: Read + Write>(
    mut stream: S,
    dict: Arc<Dictionary>,
    user_dict: Option<Arc<UserDict>>,
    fuzzy: bool,
    traditional: bool,
    shuangpin: Option<ShuangpinScheme>,
) {
    let mut session = Session::new(dict, user_dict.clone());
    if fuzzy {
        session.set_fuzzy(true);
    }
    if traditional {
        session.set_traditional(true);
    }
    session.set_shuangpin(shuangpin);

    while let Ok(Some(req)) = read_message::<_, Request>(&mut stream) {
        let (result, error) = match dispatch(&mut session, &user_dict, &req) {
            Ok(val) => (Some(val), None),
            Err(e) => (None, Some(e)),
        };
        let resp = Response {
            id: req.id,
            result,
            error,
        };
        if let Err(e) = write_message(&mut stream, &resp) {
            eprintln!("ipc 响应写出失败: {}", e);
            break;
        }
    }
}
