use chaoset_engine::{Dictionary, Session, ShuangpinScheme, UserDict};
use std::io::{Read, Write};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
#[cfg(any(windows, test))]
use std::sync::{Condvar, Mutex};
use std::thread;
#[cfg(any(windows, test))]
use std::time::Duration;

#[cfg(windows)]
mod pipe;
#[cfg(windows)]
pub use pipe::run_windows_server;

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

/// 监听池的补充信号：监听线程连上客户端（或实例作废）后 +1 并唤醒主循环。
#[cfg(any(windows, test))]
type Refill = Arc<(Mutex<usize>, Condvar)>;

/// 常驻监听循环（平台无关骨架，Windows 命名管道服务端使用）：始终维持 `want`
/// 个监听 worker，主循环只做补位与回收，绝不阻塞在连接上。
///
/// * `spawn` 建一个监听实例并返回它的线程句柄；连上客户端后该线程自己转入
///   服务（或退出），并通过 [`Refill`] 发补充信号——补位期间到达的信号由
///   计数保留，不会丢，所以任意时刻未完成的 worker 不少于 `want` 个；
/// * `spawn` 失败只记日志并退避 `retry_wait` 后重试，循环不会因单次错误退出；
/// * 空闲时最多每 `idle_wait` 醒一次，只为在 `running` 变 false 后及时收尾。
#[cfg(any(windows, test))]
fn run_listener_pool<F>(
    want: usize,
    running: &AtomicBool,
    idle_wait: Duration,
    retry_wait: Duration,
    mut spawn: F,
) where
    F: FnMut(Refill) -> std::io::Result<thread::JoinHandle<()>>,
{
    let refill: Refill = Arc::new((Mutex::new(0), Condvar::new()));
    let mut workers: Vec<thread::JoinHandle<()>> = Vec::new();
    while running.load(Ordering::SeqCst) {
        // 回收已结束的线程（连接服务完毕，或实例被探测后秒断开）。
        workers.retain(|w| !w.is_finished());
        let mut spawn_failed = false;
        while workers.len() < want && running.load(Ordering::SeqCst) && !spawn_failed {
            match spawn(Arc::clone(&refill)) {
                Ok(w) => workers.push(w),
                Err(e) => {
                    eprintln!("创建监听实例失败，{}ms 后重试: {e}", retry_wait.as_millis());
                    spawn_failed = true;
                }
            }
        }
        if spawn_failed {
            thread::sleep(retry_wait);
            continue;
        }
        let (pending, signal) = &*refill;
        let guard = pending.lock().unwrap_or_else(|p| p.into_inner());
        let mut guard = if *guard == 0 && running.load(Ordering::SeqCst) {
            signal
                .wait_timeout(guard, idle_wait)
                .unwrap_or_else(|p| p.into_inner())
                .0
        } else {
            guard
        };
        *guard = 0;
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
        let started = std::time::Instant::now();
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
            // 壳侧按键请求有 150ms 截止，但**读超时不再断开连接**（连接与会话保留，
            // 响应迟到后由壳侧按帧丢弃），所以这里写失败通常是连接真的没了（客户端
            // 退出/断管）。带上方法名与本端处理耗时，日志能直接区分「daemon 处理慢」
            //（耗时大）与「客户端自己断开」（耗时小）（issue #122）。
            eprintln!(
                "ipc 响应写出失败 method={} 本端耗时={:.2}ms: {}",
                req.method,
                started.elapsed().as_secs_f64() * 1000.0,
                e
            );
            break;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::AtomicUsize;

    /// 假监听 worker 的统计：累计启动数与同时在活数的峰值。
    #[derive(Default)]
    struct Stats {
        spawned: AtomicUsize,
        alive: AtomicUsize,
        peak: AtomicUsize,
    }

    /// 跑一轮监听池：假 worker 活 `lifetime` 后退出并像真实监听线程一样发补充
    /// 信号；`fail_first` 让前几次建实例失败（模拟 CreateNamedPipe 报错）。
    /// `run_for` 后置 running=false 并等监听池退出，返回 (统计, 退出耗时)。
    fn run_pool(
        want: usize,
        idle_wait: Duration,
        retry_wait: Duration,
        fail_first: usize,
        run_for: Duration,
        lifetime: Duration,
    ) -> (Arc<Stats>, Duration) {
        let stats = Arc::new(Stats::default());
        let attempts = AtomicUsize::new(0);
        let worker_stats = Arc::clone(&stats);
        let spawn = move |refill: Refill| -> std::io::Result<thread::JoinHandle<()>> {
            if attempts.fetch_add(1, Ordering::SeqCst) < fail_first {
                return Err(std::io::Error::other("模拟 CreateNamedPipe 失败"));
            }
            worker_stats.spawned.fetch_add(1, Ordering::SeqCst);
            let now = worker_stats.alive.fetch_add(1, Ordering::SeqCst) + 1;
            worker_stats.peak.fetch_max(now, Ordering::SeqCst);
            let stats = Arc::clone(&worker_stats);
            Ok(thread::spawn(move || {
                thread::sleep(lifetime);
                stats.alive.fetch_sub(1, Ordering::SeqCst);
                let (pending, signal) = &*refill;
                *pending.lock().unwrap() += 1;
                signal.notify_one();
            }))
        };
        let running = Arc::new(AtomicBool::new(true));
        let pool_running = Arc::clone(&running);
        let pool = thread::spawn(move || {
            run_listener_pool(want, &pool_running, idle_wait, retry_wait, spawn)
        });
        thread::sleep(run_for);
        let stopped = std::time::Instant::now();
        running.store(false, Ordering::SeqCst);
        pool.join().expect("监听池线程不应 panic");
        (stats, stopped.elapsed())
    }

    /// 常驻容量：worker 不断连上退出时监听池持续补位，同时在活的 worker 永不
    /// 超过 `want`。空闲等待设成比整段运行还长：补位只能来自补充信号——若像
    /// 旧实现那样靠轮询，整段只来得及补第一轮。
    #[test]
    fn listener_pool_keeps_capacity_without_exceeding_it() {
        let (stats, _) = run_pool(3, Duration::from_millis(2000), Duration::from_millis(10), 0, Duration::from_millis(600), Duration::from_millis(40));
        let spawned = stats.spawned.load(Ordering::SeqCst);
        assert_eq!(stats.peak.load(Ordering::SeqCst), 3, "同时在活的监听 worker 不得超过 want");
        assert!(spawned >= 9, "监听池应随 worker 退出持续补位，实际只启动 {spawned} 个");
        assert!(spawned <= 120, "补位次数异常（疑似忙等循环）：{spawned}");
    }

    /// 建实例失败不退出：退避后重试，直到补满容量。
    #[test]
    fn listener_pool_retries_after_spawn_error() {
        let (stats, _) = run_pool(2, Duration::from_millis(200), Duration::from_millis(20), 5, Duration::from_millis(600), Duration::from_millis(10_000));
        let spawned = stats.spawned.load(Ordering::SeqCst);
        assert_eq!(spawned, 2, "失败后必须重试并最终恰好补满容量");
        assert_eq!(stats.peak.load(Ordering::SeqCst), spawned);
    }

    /// 停止：监听 worker 都还活着（相当于阻塞在等连接）时，监听池也必须在一个
    /// 空闲等待周期内退出，让 daemon 正常收尾。
    #[test]
    fn listener_pool_exits_promptly_when_stopped() {
        let (stats, exit_took) = run_pool(4, Duration::from_millis(50), Duration::from_millis(10), 0, Duration::from_millis(150), Duration::from_millis(10_000));
        assert_eq!(stats.spawned.load(Ordering::SeqCst), 4);
        assert!(
            exit_took < Duration::from_millis(500),
            "停止后监听池应及时退出，实际 {exit_took:?}（worker 仍在阻塞，不应等它们）"
        );
    }
}
