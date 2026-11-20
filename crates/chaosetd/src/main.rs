mod appdirs;
mod ipc;
mod server;
mod singleton;
mod ui;

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use chaoset_engine::{Dictionary, ShuangpinScheme, UserDict};
use server::ServerConfig;
use singleton::LockHandle;

#[derive(Default, Debug)]
struct CliArgs {
    socket: Option<String>,
    use_pipe: bool,
    dicts: Vec<String>,
    seed_dict: Option<String>,
    user_dict: Option<String>,
    user_dict_set: bool,
    log_path: Option<String>,
    fuzzy: bool,
    traditional: bool,
    shuangpin: Option<String>,
    show_version: bool,
}

fn parse_args() -> CliArgs {
    let mut args = CliArgs::default();
    let raw: Vec<String> = std::env::args().skip(1).collect();
    let mut i = 0;

    while i < raw.len() {
        let arg = &raw[i];
        let normalized = arg.trim_start_matches('-');

        match normalized {
            "version" => {
                args.show_version = true;
            }
            "pipe" => {
                args.use_pipe = true;
            }
            "fuzzy" => {
                args.fuzzy = true;
            }
            "traditional" => {
                args.traditional = true;
            }
            "socket" => {
                if i + 1 < raw.len() {
                    i += 1;
                    args.socket = Some(raw[i].clone());
                }
            }
            "dict" => {
                if i + 1 < raw.len() {
                    i += 1;
                    args.dicts.push(raw[i].clone());
                }
            }
            "seeddict" => {
                if i + 1 < raw.len() {
                    i += 1;
                    args.seed_dict = Some(raw[i].clone());
                }
            }
            "userdict" => {
                args.user_dict_set = true;
                if i + 1 < raw.len() && !raw[i + 1].starts_with('-') {
                    i += 1;
                    args.user_dict = Some(raw[i].clone());
                } else {
                    args.user_dict = Some(String::new());
                }
            }
            "log" => {
                if i + 1 < raw.len() {
                    i += 1;
                    args.log_path = Some(raw[i].clone());
                }
            }
            "shuangpin" => {
                if i + 1 < raw.len() {
                    i += 1;
                    args.shuangpin = Some(raw[i].clone());
                }
            }
            _ => {}
        }
        i += 1;
    }

    args
}

fn seed_dict_files(seed: &str, targets: &[String]) -> Vec<String> {
    let seed_path = Path::new(seed);
    let data = match fs::read(seed_path) {
        Ok(d) => d,
        Err(e) => {
            eprintln!("警告: 读取种子词库 {} 失败: {}", seed, e);
            return targets.to_vec();
        }
    };

    let mut out = targets.to_vec();
    for (i, target) in targets.iter().enumerate() {
        if target.is_empty() {
            continue;
        }
        let target_path = Path::new(target);
        if target_path == seed_path {
            continue;
        }

        if let Ok(meta) = fs::metadata(target_path) {
            if meta.is_dir() {
                eprintln!("警告: 词库路径 {} 是目录；本次改用种子词库", target);
                out[i] = seed.to_string();
                continue;
            }
            if meta.len() > 0 {
                continue;
            }
            let _ = fs::remove_file(target_path);
        }

        if let Some(parent) = target_path.parent() {
            let _ = fs::create_dir_all(parent);
        }

        let tmp = format!("{}.seed.tmp", target);
        if fs::write(&tmp, &data).is_ok() {
            if fs::rename(&tmp, target_path).is_ok() {
                eprintln!("已从种子词库初始化 {}", target);
                continue;
            }
            let _ = fs::remove_file(&tmp);
        }
        out[i] = seed.to_string();
    }

    out
}

fn load_dict_lenient(paths: &[String]) -> (Dictionary, usize) {
    let mut dict = Dictionary::new();
    let mut total_loaded = 0;

    for p in paths {
        match dict.load_tsv(p) {
            Ok(count) => total_loaded += count,
            Err(e) => eprintln!("警告: 加载词库 {} 失败: {}", p, e),
        }
    }

    (dict, total_loaded)
}

#[cfg(unix)]
fn register_shutdown_handler(running: Arc<AtomicBool>, clean_path: Option<PathBuf>) {
    static SHUTDOWN_FLAG: AtomicBool = AtomicBool::new(false);

    extern "C" fn sig_handler(_sig: libc::c_int) {
        SHUTDOWN_FLAG.store(true, Ordering::SeqCst);
    }

    unsafe {
        libc::signal(libc::SIGINT, sig_handler as *const () as usize);
        libc::signal(libc::SIGTERM, sig_handler as *const () as usize);
    }

    thread::spawn(move || {
        while !SHUTDOWN_FLAG.load(Ordering::SeqCst) {
            thread::sleep(Duration::from_millis(50));
        }
        running.store(false, Ordering::SeqCst);
        if let Some(ref p) = clean_path {
            let _ = fs::remove_file(p);
        }
    });
}

#[cfg(windows)]
fn register_shutdown_handler(running: Arc<AtomicBool>, _clean_path: Option<PathBuf>) {
    static mut RUNNING_PTR: *const AtomicBool = std::ptr::null();

    unsafe extern "system" fn ctrl_handler(_ctrl_type: u32) -> i32 {
        if !RUNNING_PTR.is_null() {
            (*RUNNING_PTR).store(false, Ordering::SeqCst);
        }
        1
    }

    unsafe {
        RUNNING_PTR = Arc::as_ptr(&running);
        windows_sys::Win32::System::Console::SetConsoleCtrlHandler(Some(ctrl_handler), 1);
    }
}

fn main() {
    let args = parse_args();

    if args.show_version {
        let os = std::env::consts::OS;
        let arch = std::env::consts::ARCH;
        println!("chaosetd {} {}/{}", env!("CARGO_PKG_VERSION"), os, arch);
        return;
    }

    // 默认词库
    let mut dict_paths = if args.dicts.is_empty() {
        vec!["data/dict_common.tsv".to_string()]
    } else {
        args.dicts
    };

    if let Some(ref seed) = args.seed_dict {
        dict_paths = seed_dict_files(seed, &dict_paths);
    }

    // 用户词典路径
    let user_dict_path: Option<PathBuf> = if args.user_dict_set {
        args.user_dict.filter(|s| !s.is_empty()).map(PathBuf::from)
    } else {
        appdirs::default_user_dict_path().ok()
    };

    // 单实例与端点判断
    #[cfg(target_os = "windows")]
    let _win_lock = {
        let lock = match LockHandle::acquire("chaosetd") {
            Ok(l) => l,
            Err(e) => {
                eprintln!("{}", e);
                std::process::exit(1);
            }
        };
        lock
    };

    let (dict, line_count) = load_dict_lenient(&dict_paths);
    if line_count == 0 {
        eprintln!(
            "没有加载到任何词条，请检查 -dict 指定的词库是否为空或格式不对（{}）",
            dict_paths.join(", ")
        );
        std::process::exit(1);
    }
    eprintln!("已加载词典 {} 条（{}）", line_count, dict_paths.join(", "));

    let user_dict = user_dict_path.map(|p| Arc::new(UserDict::new(Some(p))));

    let shuangpin_scheme = args.shuangpin.as_deref().and_then(|s| match s {
        "zrm" => Some(ShuangpinScheme::Zrm),
        "mspy" => Some(ShuangpinScheme::Mspy),
        "flypy" => Some(ShuangpinScheme::Flypy),
        _ => None,
    });

    let running = Arc::new(AtomicBool::new(true));

    // 后台 2 秒异步写入脏数据
    let flush_running = Arc::clone(&running);
    let flush_user_dict = user_dict.clone();
    let flush_thread = thread::spawn(move || {
        while flush_running.load(Ordering::SeqCst) {
            thread::sleep(Duration::from_secs(2));
            if let Some(ref u) = flush_user_dict {
                let _ = u.flush_if_dirty();
            }
        }
    });

    // 监听与服务
    let config = ServerConfig {
        dict: Arc::new(dict),
        user_dict: user_dict.clone(),
        fuzzy: args.fuzzy,
        traditional: args.traditional,
        shuangpin: shuangpin_scheme,
    };

    #[cfg(unix)]
    {
        let socket_path = if let Some(ref s) = args.socket {
            PathBuf::from(s)
        } else {
            match appdirs::default_socket_path() {
                Ok(p) => p,
                Err(e) => {
                    eprintln!("获取 socket 路径失败: {}", e);
                    std::process::exit(1);
                }
            }
        };

        let _lock = match LockHandle::acquire(&socket_path) {
            Ok(l) => l,
            Err(e) => {
                eprintln!("{}", e);
                std::process::exit(1);
            }
        };

        if socket_path.exists() {
            let _ = fs::remove_file(&socket_path);
        }

        let listener = match std::os::unix::net::UnixListener::bind(&socket_path) {
            Ok(l) => l,
            Err(e) => {
                eprintln!("监听 {} 失败: {}", socket_path.display(), e);
                std::process::exit(1);
            }
        };

        eprintln!("chaosetd 就绪，unix: {}", socket_path.display());

        // 注册退出信号
        register_shutdown_handler(Arc::clone(&running), Some(socket_path.clone()));

        let _ = server::run_unix_server(listener, config, Arc::clone(&running));
        let _ = fs::remove_file(&socket_path);
    }

    #[cfg(target_os = "windows")]
    {
        let pipe_name = match appdirs::default_pipe_name() {
            Ok(p) => p,
            Err(e) => {
                eprintln!("生成命名管道名失败: {}", e);
                std::process::exit(1);
            }
        };

        eprintln!("chaosetd 就绪，pipe: {}", pipe_name);

        register_shutdown_handler(Arc::clone(&running), None);

        let _ = server::run_windows_server(&pipe_name, config, Arc::clone(&running));
    }

    running.store(false, Ordering::SeqCst);
    let _ = flush_thread.join();
    if let Some(ref u) = user_dict {
        let _ = u.save();
    }
}
