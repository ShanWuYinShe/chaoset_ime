//! daemon IPC 客户端：Unix domain socket / Windows 命名管道，单连接串行一问一答。
//!
//! 协议见 crate::protocol（4 字节大端长度 + JSON，1 MiB 上限）。
//!
//! 契约（与 daemon crates/chaosetd/src/ipc.rs 的 19 个方法一一对应）：
//! - 单连接串行：同一时刻只有一个请求在途，请求 id 从 1 自增；
//! - 每次调用默认 5s 截止（Unix 用 socket 读写超时，Windows 用 overlapped I/O +
//!   WaitForSingleObject），调用结束清除；
//! - 响应 id 与请求 id 不匹配 → 关连接并报「响应 ID 不匹配」（流已错位，继续解析
//!   会把旧响应当成本次结果）；
//! - daemon 返回 error → 原样包成「daemon 返回错误: <e>」，**不**关连接；
//! - 其它 IO/解码/超时错误 → 关连接（流状态不可信），调用方需重新连接。

use std::io::{self, Read, Write};
use std::path::Path;
use std::time::{Duration, Instant};

use serde::de::DeserializeOwned;
use serde_json::{json, Value};

use crate::dto::{Candidate, Composition, UserStats};
use crate::protocol::{read_frame, write_frame, Request, Response, MAX_MESSAGE_SIZE};

/// 客户端调用结果：错误统一为可直接展示的中文消息。
pub type Result<T> = std::result::Result<T, String>;

/// 单次调用默认截止时间（含 userExport 单页请求）；正常请求是毫秒级。
pub const DEFAULT_CALL_TIMEOUT: Duration = Duration::from_secs(5);

/// userExport 分页循环的单页行数（daemon 侧按行 offset/limit 切片）。
const USER_EXPORT_PAGE_LIMIT: i64 = 2000;

/// userExport 分页循环保护上限，防御 daemon 返回异常 next 造成无限循环。
const USER_EXPORT_MAX_PAGES: usize = 1000;

/// daemon 客户端句柄。
///
/// 生命周期内复用同一条连接；任何 IO/解码/超时错误都会关闭连接（之后调用直接
/// 返回「连接已关闭」），需要重新 connect_unix / connect_pipe。
pub struct Client {
    conn: Option<Conn>,
    next_id: u64,
    call_timeout: Duration,
}

impl Client {
    /// 连接 Unix domain socket（macOS/Linux；测试可注入任意 socket 路径）。
    pub fn connect_unix(path: &Path) -> Result<Client> {
        #[cfg(unix)]
        {
            let stream = std::os::unix::net::UnixStream::connect(path)
                .map_err(|e| format!("连接 daemon 失败（unix: {}）: {e}", path.display()))?;
            Ok(Client {
                conn: Some(Conn::Unix(stream)),
                next_id: 0,
                call_timeout: DEFAULT_CALL_TIMEOUT,
            })
        }
        #[cfg(not(unix))]
        {
            let _ = path;
            Err("Unix socket 传输仅 Unix 平台支持".to_string())
        }
    }

    /// 连接 Windows 命名管道；ERROR_PIPE_BUSY 时每 10ms 重试、最多 2s，
    /// 其它错误立即失败。名字由 chaoset_config::paths::default_pipe_name()
    /// 按当前用户 SID 生成（形如 \\.\pipe\chaoset-<SID>）。
    pub fn connect_pipe(name: &str) -> Result<Client> {
        #[cfg(windows)]
        {
            let pipe = PipeStream::open(name)?;
            Ok(Client {
                conn: Some(Conn::Pipe(pipe)),
                next_id: 0,
                call_timeout: DEFAULT_CALL_TIMEOUT,
            })
        }
        #[cfg(not(windows))]
        {
            let _ = name;
            Err("命名管道传输仅 Windows 平台支持".to_string())
        }
    }

    /// 连接平台默认端点：macOS/Linux 为 paths::socket_path()，Windows 为
    /// paths::default_pipe_name()。
    pub fn connect_default() -> Result<Client> {
        #[cfg(unix)]
        {
            let path = chaoset_config::paths::socket_path()
                .map_err(|e| format!("解析 daemon socket 路径失败: {e}"))?;
            Client::connect_unix(&path)
        }
        #[cfg(windows)]
        {
            let name = chaoset_config::paths::default_pipe_name()
                .map_err(|e| format!("生成命名管道名失败: {e}"))?;
            Client::connect_pipe(&name)
        }
    }

    /// 显式关闭连接（幂等）。之后调用方法会报「连接已关闭」。
    pub fn close(&mut self) {
        self.conn = None;
    }

    /// 连接是否仍然可用（未因错误关闭）。
    pub fn is_connected(&self) -> bool {
        self.conn.is_some()
    }

    /// 测试用：缩短单次调用截止时间（生产代码保持 5s）。
    #[cfg(all(test, unix))]
    pub(crate) fn set_call_timeout(&mut self, timeout: Duration) {
        self.call_timeout = timeout;
    }

    // ---- 19 个 daemon 方法的封装（与 crates/chaosetd/src/ipc.rs 的 dispatch 一一对应）----

    /// 探活；daemon 未就绪时调用方应降级而不是等待。
    pub fn ping(&mut self) -> Result<()> {
        self.call_raw("ping", None).map(|_| ())
    }

    /// 追加输入。half_width 是壳的全/半角标点状态，只对本次请求生效。
    pub fn input(&mut self, text: &str, half_width: bool) -> Result<Composition> {
        self.call_typed("input", Some(json!({ "text": text, "half": half_width })))
    }

    /// 退格（删一个输入键）。
    pub fn backspace(&mut self) -> Result<Composition> {
        self.call_typed("backspace", None)
    }

    /// 按音节/词回退。
    pub fn backspace_word(&mut self) -> Result<Composition> {
        self.call_typed("backspaceWord", None)
    }

    /// 清空组词与学习上下文（Esc/失焦/新会话）。
    pub fn reset(&mut self) -> Result<Composition> {
        self.call_typed("reset", None)
    }

    /// 只清组词、保留 prev/history 学习上下文（切中英/透传等）。
    pub fn reset_composition(&mut self) -> Result<Composition> {
        self.call_typed("resetComposition", None)
    }

    /// 回车：返回原始拼音并清空组词。
    pub fn commit_raw(&mut self) -> Result<String> {
        let out: Committed = self.call_typed("commitRaw", None)?;
        Ok(out.committed)
    }

    /// 候选下一页。
    pub fn next_page(&mut self) -> Result<Composition> {
        self.call_typed("nextPage", None)
    }

    /// 候选上一页。
    pub fn prev_page(&mut self) -> Result<Composition> {
        self.call_typed("prevPage", None)
    }

    /// 选中第 index 个候选，返回上屏文本。
    pub fn select(&mut self, index: usize) -> Result<String> {
        let out: Committed = self.call_typed("select", Some(json!({ "index": index })))?;
        Ok(out.committed)
    }

    /// 渐进上屏：只消耗候选音节对应的 input 前缀，返回（剩余组词态, 本次上屏文本）。
    /// 剩余组词态的 preedit 是剩余拼音，壳用它继续组词。
    pub fn select_prefix(&mut self, index: usize) -> Result<(Composition, String)> {
        let comp: Composition = self.call_typed("selectPrefix", Some(json!({ "index": index })))?;
        let committed = comp.committed.clone();
        Ok((comp, committed))
    }

    /// 联想：按已上屏词音节的续接候选（返回 Composition 同构响应里的候选列表）。
    pub fn associate(&mut self, sylls: &[String]) -> Result<Vec<Candidate>> {
        let comp: Composition = self.call_typed("associate", Some(json!({ "sylls": sylls })))?;
        Ok(comp.candidates)
    }

    /// 联想词直接上屏，返回上屏文本。
    pub fn associate_select(&mut self, word: &str, sylls: &[String]) -> Result<String> {
        let out: Committed = self.call_typed(
            "associateSelect",
            Some(json!({ "word": word, "sylls": sylls })),
        )?;
        Ok(out.committed)
    }

    /// 标点键：组词中先上屏首选候选，再输出中文（或半角）标点。
    pub fn punct(&mut self, key: &str, half_width: bool) -> Result<String> {
        let out: Committed =
            self.call_typed("punct", Some(json!({ "key": key, "half": half_width })))?;
        Ok(out.committed)
    }

    /// 用户词典三类学习记录条目数。
    pub fn user_stats(&mut self) -> Result<UserStats> {
        self.call_typed("userStats", None)
    }

    /// 清空 daemon 内存与磁盘上的全部学习记录（daemon 返回前 best-effort 落盘）。
    pub fn user_clear(&mut self) -> Result<()> {
        self.call_raw("userClear", None).map(|_| ())
    }

    /// 按词删除学习数据，返回是否真的删到了东西。sylls 为空时省略该键。
    pub fn user_delete(&mut self, word: &str, sylls: &[String]) -> Result<bool> {
        let mut params = serde_json::Map::new();
        params.insert("word".to_string(), json!(word));
        if !sylls.is_empty() {
            params.insert("sylls".to_string(), json!(sylls));
        }
        let out: Deleted = self.call_typed("userDelete", Some(Value::Object(params)))?;
        Ok(out.deleted)
    }

    /// 导出学习记录 TSV（phrases_only 只导出用户词组）。
    ///
    /// 内部按 2000 行分页拉取后拼接，调用方无需感知分页：daemon 单帧有 1 MiB
    /// 上限，整份 TSV 在重度用户下必然超限，必须在客户端循环。
    pub fn user_export(&mut self, phrases_only: bool) -> Result<String> {
        let mut out = String::new();
        let mut offset: i64 = 0;
        for _ in 0..USER_EXPORT_MAX_PAGES {
            let page: ExportPage = self.call_typed(
                "userExport",
                Some(json!({
                    "phrases_only": phrases_only,
                    "offset": offset,
                    "limit": USER_EXPORT_PAGE_LIMIT,
                })),
            )?;
            if page.tsv.len() > MAX_MESSAGE_SIZE {
                // 正常不可达：单页 JSON 超限会先被 write_frame 拒绝；防御异常 daemon。
                self.close();
                return Err(format!(
                    "导出分页（offset={offset}）超过协议上限 {MAX_MESSAGE_SIZE} 字节"
                ));
            }
            out.push_str(&page.tsv);
            if page.next < 0 {
                return Ok(out);
            }
            if page.next <= offset {
                // 防御：next 必须严格前进，否则循环不终止；旧版 daemon 不返回 next
                //（字段缺省为 0）也会走到这里——报错而不是把首屏当完整导出返回。
                self.close();
                return Err(format!(
                    "导出分页 next={} 未前进（当前 offset={offset}；旧版 daemon 可能不支持分页，请升级两端）",
                    page.next
                ));
            }
            offset = page.next;
        }
        self.close();
        Err(format!(
            "导出分页超过 {USER_EXPORT_MAX_PAGES} 页保护上限（offset={offset}）"
        ))
    }

    /// 拉起单例 UI 宿主（Windows）；settings 为 true 时同时打开设置中心。
    pub fn ui(&mut self, settings: bool) -> Result<()> {
        self.call_raw("ui", Some(json!({ "settings": settings })))
            .map(|_| ())
    }

    // ---- 内部实现 ----

    /// 发一个请求并返回原始 result；错误分类决定是否关闭连接。
    fn call_raw(&mut self, method: &str, params: Option<Value>) -> Result<Value> {
        let id = self.next_id.wrapping_add(1);
        self.next_id = id;
        if self.conn.is_none() {
            return Err("与 daemon 的连接已关闭，请重新连接".to_string());
        }
        match self.exchange(id, method, params) {
            Ok(value) => Ok(value),
            Err(CallFailure::Daemon(msg)) => Err(format!("daemon 返回错误: {msg}")),
            Err(CallFailure::Fatal(msg)) => {
                self.close();
                Err(msg)
            }
        }
    }

    /// 发送请求/读取响应；id 关联失败与所有 IO/解码错误都是 Fatal（关连接）。
    fn exchange(
        &mut self,
        id: u64,
        method: &str,
        params: Option<Value>,
    ) -> std::result::Result<Value, CallFailure> {
        let req = Request {
            id,
            method: method.to_string(),
            params,
        };
        let payload = serde_json::to_vec(&req)
            .map_err(|e| CallFailure::Fatal(format!("序列化请求失败: {e}")))?;
        let timeout = self.call_timeout;
        let Some(conn) = self.conn.as_mut() else {
            return Err(CallFailure::Fatal(
                "与 daemon 的连接已关闭，请重新连接".to_string(),
            ));
        };
        let deadline = Instant::now() + timeout;
        if let Err(e) = conn.set_deadline(Some(deadline)) {
            return Err(CallFailure::Fatal(format!("设置请求超时失败: {e}")));
        }
        let result = exchange_io(conn, &payload, id, timeout);
        // 截止时间是连接级状态：本次调用结束必须清除，否则后续调用继承旧截止时间。
        let _ = conn.set_deadline(None);
        result
    }

    /// 发请求 + 解码 result 为 DTO；解码失败按 FATAL 处理（关连接）。
    fn call_typed<T: DeserializeOwned>(
        &mut self,
        method: &str,
        params: Option<Value>,
    ) -> Result<T> {
        let value = self.call_raw(method, params)?;
        match serde_json::from_value(value) {
            Ok(decoded) => Ok(decoded),
            Err(e) => {
                self.close();
                Err(format!("解析 {method} 响应失败: {e}"))
            }
        }
    }
}

/// 调用失败分类：daemon 协议错误连接仍可用；Fatal 必须关闭连接。
enum CallFailure {
    /// daemon 返回了 error 字段（协议层成功响应）。
    Daemon(String),
    /// IO/解码/超时/id 不匹配：流状态不可信。
    Fatal(String),
}

#[derive(serde::Deserialize)]
struct Committed {
    #[serde(default)]
    committed: String,
}

#[derive(serde::Deserialize)]
struct Deleted {
    #[serde(default)]
    deleted: bool,
}

#[derive(serde::Deserialize)]
struct ExportPage {
    #[serde(default)]
    tsv: String,
    #[serde(default)]
    next: i64,
}

/// 传输层连接：Unix socket 或 Windows 命名管道。
enum Conn {
    #[cfg(unix)]
    Unix(std::os::unix::net::UnixStream),
    #[cfg(windows)]
    Pipe(PipeStream),
}

impl Conn {
    /// 设置本次调用截止时间（None = 清除）。
    fn set_deadline(&mut self, deadline: Option<Instant>) -> io::Result<()> {
        match self {
            #[cfg(unix)]
            Conn::Unix(stream) => {
                // Unix socket 只有按次读写超时：用距离截止时间的剩余量，至少 1ms。
                let timeout = deadline.map(|d| {
                    d.saturating_duration_since(Instant::now())
                        .max(Duration::from_millis(1))
                });
                stream.set_read_timeout(timeout)?;
                stream.set_write_timeout(timeout)?;
                Ok(())
            }
            #[cfg(windows)]
            Conn::Pipe(pipe) => {
                pipe.deadline = deadline;
                Ok(())
            }
        }
    }
}

impl Read for Conn {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        match self {
            #[cfg(unix)]
            Conn::Unix(stream) => stream.read(buf).map_err(map_unix_timeout),
            #[cfg(windows)]
            Conn::Pipe(pipe) => pipe.read(buf),
        }
    }
}

impl Write for Conn {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        match self {
            #[cfg(unix)]
            Conn::Unix(stream) => stream.write(buf).map_err(map_unix_timeout),
            #[cfg(windows)]
            Conn::Pipe(pipe) => pipe.write(buf),
        }
    }

    fn flush(&mut self) -> io::Result<()> {
        match self {
            #[cfg(unix)]
            Conn::Unix(stream) => stream.flush(),
            #[cfg(windows)]
            Conn::Pipe(pipe) => pipe.flush(),
        }
    }
}

#[cfg(unix)]
fn map_unix_timeout(e: io::Error) -> io::Error {
    // 阻塞 socket 上的 WouldBlock 只可能来自 set_read/write_timeout。
    if e.kind() == io::ErrorKind::WouldBlock || e.kind() == io::ErrorKind::TimedOut {
        io::Error::new(io::ErrorKind::TimedOut, "请求超时")
    } else {
        e
    }
}

/// 发送请求并读取响应（不含截止时间管理）。
fn exchange_io(
    conn: &mut Conn,
    payload: &[u8],
    id: u64,
    timeout: Duration,
) -> std::result::Result<Value, CallFailure> {
    if let Err(e) = write_frame(conn, payload) {
        return Err(CallFailure::Fatal(format!("发送请求失败: {e}")));
    }
    let frame = match read_frame(conn) {
        Ok(Some(frame)) => frame,
        Ok(None) => return Err(CallFailure::Fatal("daemon 在响应前关闭了连接".to_string())),
        Err(e) => {
            if e.kind() == io::ErrorKind::TimedOut {
                return Err(CallFailure::Fatal(format!(
                    "等待 daemon 响应超时（{}）",
                    format_timeout(timeout)
                )));
            }
            return Err(CallFailure::Fatal(format!("读取响应失败: {e}")));
        }
    };
    let resp: Response = match serde_json::from_slice(&frame) {
        Ok(resp) => resp,
        Err(e) => return Err(CallFailure::Fatal(format!("解析响应失败: {e}"))),
    };
    if resp.id != id {
        return Err(CallFailure::Fatal(format!(
            "响应 ID 不匹配（期望 {id}，收到 {}），连接已关闭",
            resp.id
        )));
    }
    if let Some(err) = resp.error {
        return Err(CallFailure::Daemon(err));
    }
    Ok(resp.result.unwrap_or(Value::Null))
}

fn format_timeout(timeout: Duration) -> String {
    let secs = timeout.as_secs();
    if secs > 0 && timeout == Duration::from_secs(secs) {
        format!("{secs}s")
    } else {
        format!("{}ms", timeout.as_millis())
    }
}

// ---- Windows 命名管道（overlapped I/O + 每次调用截止时间）----

#[cfg(windows)]
struct PipeStream {
    handle: windows_sys::Win32::Foundation::HANDLE,
    /// 当前调用的截止时间（None = 尚未设置，按 5s 兜底）。
    deadline: Option<Instant>,
}

#[cfg(windows)]
impl PipeStream {
    /// 打开命名管道；ERROR_PIPE_BUSY 每 10ms 重试、最多 2s，其它错误立即失败。
    fn open(name: &str) -> Result<PipeStream> {
        use windows_sys::Win32::Foundation::{
            GetLastError, ERROR_PIPE_BUSY, GENERIC_READ, GENERIC_WRITE, INVALID_HANDLE_VALUE,
        };
        use windows_sys::Win32::Storage::FileSystem::{
            CreateFileW, FILE_FLAG_OVERLAPPED, OPEN_EXISTING,
        };

        let wide: Vec<u16> = name.encode_utf16().chain(std::iter::once(0)).collect();
        let open_deadline = Instant::now() + Duration::from_secs(2);
        loop {
            let handle = unsafe {
                CreateFileW(
                    wide.as_ptr(),
                    GENERIC_READ | GENERIC_WRITE,
                    0,
                    std::ptr::null(),
                    OPEN_EXISTING,
                    FILE_FLAG_OVERLAPPED,
                    0,
                )
            };
            if handle != INVALID_HANDLE_VALUE {
                return Ok(PipeStream {
                    handle,
                    deadline: None,
                });
            }
            let err = unsafe { GetLastError() };
            if err == ERROR_PIPE_BUSY && Instant::now() < open_deadline {
                std::thread::sleep(Duration::from_millis(10));
                continue;
            }
            return Err(format!(
                "连接 daemon 失败（pipe: {name}）: {}",
                io::Error::from_raw_os_error(err as i32)
            ));
        }
    }

    /// 距离当前调用截止时间的剩余量（超时则直接报 TimedOut）。
    fn remaining(&self) -> io::Result<Duration> {
        match self.deadline {
            Some(deadline) => {
                let now = Instant::now();
                if now >= deadline {
                    Err(io::Error::new(io::ErrorKind::TimedOut, "请求超时"))
                } else {
                    Ok(deadline - now)
                }
            }
            None => Ok(DEFAULT_CALL_TIMEOUT),
        }
    }

    /// overlapped 读：一次 ReadFile + WaitForSingleObject；超时 CancelIoEx 并回收请求。
    ///
    /// 返回 Ok(0) 表示对端关闭（EOF），与服务端 NamedPipeStream 的语义一致。
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let timeout = self.remaining()?;
        self.overlapped_io(buf.as_mut_ptr(), buf.len(), timeout, false)
    }

    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        if buf.is_empty() {
            return Ok(0);
        }
        let timeout = self.remaining()?;
        self.overlapped_io(buf.as_ptr() as *mut u8, buf.len(), timeout, true)
    }

    fn flush(&mut self) -> io::Result<()> {
        // 无用户态缓冲：WriteFile 直接把整帧交给管道。不调用 FlushFileBuffers——
        // 它在管道上没有意义还会阻塞到对端读走数据，破坏本次调用的截止时间。
        Ok(())
    }

    /// 一次 overlapped 读/写（read 与 write 共用）。
    ///
    /// 传裸指针而不是 &mut [u8]：写路径只有 &[u8]，而 ReadFile 需要 *mut u8；
    /// 两者共用同一段实现（生命周期由调用方在本次调用内保证）。
    fn overlapped_io(
        &mut self,
        buf: *mut u8,
        len: usize,
        timeout: Duration,
        is_write: bool,
    ) -> io::Result<usize> {
        use windows_sys::Win32::Foundation::{
            CloseHandle, GetLastError, ERROR_BROKEN_PIPE, ERROR_IO_PENDING,
            ERROR_PIPE_NOT_CONNECTED, WAIT_OBJECT_0, WAIT_TIMEOUT,
        };
        use windows_sys::Win32::Storage::FileSystem::{ReadFile, WriteFile};
        use windows_sys::Win32::System::Threading::{CreateEventW, WaitForSingleObject};
        use windows_sys::Win32::System::IO::{CancelIoEx, GetOverlappedResult, OVERLAPPED};

        // 每次操作单独建事件：调用级截止时间只影响本次调用，不跨调用残留。
        let event = unsafe { CreateEventW(std::ptr::null(), 1, 0, std::ptr::null()) };
        if event == 0 {
            return Err(io::Error::last_os_error());
        }

        let mut overlapped: OVERLAPPED = unsafe { std::mem::zeroed() };
        overlapped.hEvent = event;
        let mut transferred: u32 = 0;

        let started = unsafe {
            if is_write {
                WriteFile(
                    self.handle,
                    buf as *const u8,
                    len as u32,
                    &mut transferred,
                    &mut overlapped,
                )
            } else {
                ReadFile(
                    self.handle,
                    buf,
                    len as u32,
                    &mut transferred,
                    &mut overlapped,
                )
            }
        };

        if started == 0 {
            let err = unsafe { GetLastError() };
            if err != ERROR_IO_PENDING {
                unsafe { CloseHandle(event) };
                if !is_write && (err == ERROR_BROKEN_PIPE || err == ERROR_PIPE_NOT_CONNECTED) {
                    return Ok(0);
                }
                return Err(io::Error::from_raw_os_error(err as i32));
            }
        }

        // WaitForSingleObject 的毫秒上限取剩余量（至少 1ms，避免 0 = 不等待）。
        let wait_ms = timeout.as_millis().min(u32::MAX as u128).max(1) as u32;
        let wait_rc = unsafe { WaitForSingleObject(event, wait_ms) };
        if wait_rc == WAIT_TIMEOUT {
            // 取消还未完成的 IO 并回收 OVERLAPPED，防止缓冲区在返回后被内核写入。
            unsafe {
                CancelIoEx(self.handle, &overlapped);
            }
            let mut drained: u32 = 0;
            unsafe { GetOverlappedResult(self.handle, &overlapped, &mut drained, 1) };
            unsafe { CloseHandle(event) };
            return Err(io::Error::new(io::ErrorKind::TimedOut, "请求超时"));
        }
        if wait_rc != WAIT_OBJECT_0 {
            unsafe { CloseHandle(event) };
            return Err(io::Error::new(
                io::ErrorKind::Other,
                format!("等待管道读写完成失败: {}", io::Error::last_os_error()),
            ));
        }

        let mut bytes: u32 = 0;
        let ok = unsafe { GetOverlappedResult(self.handle, &overlapped, &mut bytes, 0) };
        let err = if ok == 0 {
            unsafe { GetLastError() }
        } else {
            0
        };
        unsafe { CloseHandle(event) };
        if ok == 0 {
            if !is_write && (err == ERROR_BROKEN_PIPE || err == ERROR_PIPE_NOT_CONNECTED) {
                return Ok(0);
            }
            return Err(io::Error::from_raw_os_error(err as i32));
        }
        Ok(bytes as usize)
    }
}

#[cfg(windows)]
impl Drop for PipeStream {
    fn drop(&mut self) {
        if self.handle != 0 {
            unsafe { windows_sys::Win32::Foundation::CloseHandle(self.handle) };
            self.handle = 0;
        }
    }
}

#[cfg(all(test, unix))]
mod tests;
