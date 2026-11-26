#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

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
    fuzzy_set: bool,
    traditional: bool,
    shuangpin: Option<String>,
    shuangpin_set: bool,
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
                args.fuzzy_set = true;
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
                args.shuangpin_set = true;
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

/// 解析用户词典：与 Go daemon 同口径——「用户词典永远存在」。
///
/// 三种形态：
///   1) 未传 `-userdict`：平台用户数据目录下的 userdict.json；
///   2) 显式传 `-userdict <路径>`：就用该文件；
///   3) 显式传 `-userdict ""`：**内存态**（学习/删除/导出可用，只是不落盘）。
///
/// 第 3 种是 IPC 测试与「临时干净环境」用的形态。此前 Rust 版把它当成
/// 「未启用用户词典」→ user* 方法全部报错，与 Go 行为不一致（三端壳的
/// core_test 依赖删除幂等语义；Go 的 NewUserDict("") 也是内存态）。
fn resolve_user_dict(args: &CliArgs) -> Option<Arc<UserDict>> {
    if args.user_dict_set {
        return match args.user_dict.as_deref() {
            Some("") | None => Some(Arc::new(UserDict::new::<&str>(None))),
            Some(p) => Some(Arc::new(UserDict::new(Some(PathBuf::from(p))))),
        };
    }
    match chaoset_config::paths::user_dict_path() {
        Ok(p) => Some(Arc::new(UserDict::new(Some(p)))),
        Err(e) => {
            eprintln!(
                "警告: 解析用户词典路径失败，改用内存态（本次学习不落盘）: {}",
                e
            );
            Some(Arc::new(UserDict::new::<&str>(None)))
        }
    }
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

/// 宽松加载词库（运行期健壮层）：每个文件优先用同目录同 stem 的 .bin 缓存。
///
/// 发布包同时带 TSV 源与预编译 .bin；三端安装脚本 / LaunchAgent / Run 项 /
/// 引擎参数都保持指向 .tsv 不变，由 load_with_cache 选缓存：传入路径是 .bin
/// 或文件头是 CSDB 时直接加载；否则缓存存在且不比源旧则加载缓存；缓存缺失/
/// 过期/损坏时回退 TSV，并在目录可写时自动重建缓存（用户导入词库只会重写
/// TSV，下次启动即自动重建）。-seeddict 的种子初始化仍是字节级复制（见
/// seed_dict_files），这里不生成种子缓存；被种出的目标词库随后走同一条缓存
/// 路径，首次加载按需建缓存。单个词库加载失败按警告跳过，由调用方用总词条数
/// 为 0 判断整体不可用。
fn load_dict_lenient(paths: &[String]) -> (Dictionary, usize) {
    let mut dict = Dictionary::new();
    let mut total_loaded = 0;

    for p in paths {
        match dict.load_with_cache(p) {
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

/// 把 daemon 日志接到 `-log` 指定的文件（与 Go daemon 同口径）。
///
/// 三端壳启动 daemon 时统一传 `-log <用户数据目录>/chaosetd.log`，排障依赖它；
/// 直接重定向进程 stderr 的好处是后续所有 eprintln（含 panic 信息）都落盘，
/// 不需要把每处日志改成 logger 调用。目录不存在时先建；打开失败退回「临时
/// 目录 + PID 后缀」的文件——日志不能反过来让 daemon 起不来。
fn attach_log_file(path: &str) {
    if path.is_empty() {
        return;
    }
    if let Some(parent) = std::path::Path::new(path).parent() {
        let _ = fs::create_dir_all(parent);
    }
    let file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)
        .or_else(|_| {
            let fallback =
                std::env::temp_dir().join(format!("chaosetd-{}.log", std::process::id()));
            std::fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(fallback)
        });
    let Ok(file) = file else {
        return;
    };
    #[cfg(unix)]
    {
        use std::os::unix::io::AsRawFd;
        unsafe {
            libc::dup2(file.as_raw_fd(), 2);
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::io::AsRawHandle;
        use windows_sys::Win32::System::Console::{SetStdHandle, STD_ERROR_HANDLE};
        unsafe {
            SetStdHandle(STD_ERROR_HANDLE, file.as_raw_handle() as _);
        }
    }
    std::mem::forget(file); // fd/句柄在进程生命周期内保持打开
}


/// 单行构建信息：版本优先取构建注入（scripts/version.sh，单一真源），
/// 本地裸 cargo 构建回退 Cargo 版本；rev/构建时间/脏工作区来自 build.rs 的 git 探测。
fn version_info() -> String {
    let version = option_env!("CHAOSET_BUILD_VERSION").unwrap_or(env!("CARGO_PKG_VERSION"));
    let mut text = format!("chaosetd {version} {}/{}", std::env::consts::OS, std::env::consts::ARCH);
    if let Some(rev) = option_env!("CHAOSET_BUILD_REV") {
        text.push_str(&format!(" rev={rev}"));
    }
    if let Some(built) = option_env!("CHAOSET_BUILD_TIME") {
        text.push_str(&format!(" built={built}"));
    }
    if option_env!("CHAOSET_BUILD_DIRTY") == Some("1") {
        text.push_str(" dirty");
    }
    if option_env!("CHAOSET_BUILD_REV").is_none() && option_env!("CHAOSET_BUILD_TIME").is_none() {
        text.push_str(" no-vcs");
    }
    text
}

fn main() {
    let args = parse_args();

    if args.show_version {
        println!("{}", version_info());
        return;
    }

    attach_log_file(args.log_path.as_deref().unwrap_or(""));

    // 用户词典：与 Go daemon 同口径——「用户词典永远存在」，见
    // resolve_user_dict 的三种形态说明。
    let user_dict = resolve_user_dict(&args);
    match user_dict
        .as_ref()
        .and_then(|u| u.path().map(|p| p.display().to_string()))
    {
        Some(p) => eprintln!("用户词典: {}", p),
        None => eprintln!("用户词典: 内存态（显式传入空路径，不落盘）"),
    }

    // 默认词库（TSV 源；同目录同名 .bin 作为缓存优先使用，缺失/过期时自动重建）
    let mut dict_paths = if args.dicts.is_empty() {
        vec!["data/dict_common.tsv".to_string()]
    } else {
        args.dicts
    };

    if let Some(ref seed) = args.seed_dict {
        dict_paths = seed_dict_files(seed, &dict_paths);
    }

    // 单实例与端点判断（Unix 与 Windows 均先加单实例锁再加载词库，避免并发懒启动重复 load_dict_lenient）
    #[cfg(unix)]
    let (socket_path, _unix_lock) = {
        let socket_path = if let Some(ref s) = args.socket {
            PathBuf::from(s)
        } else {
            match chaoset_config::paths::socket_path() {
                Ok(p) => p,
                Err(e) => {
                    eprintln!("获取 socket 路径失败: {}", e);
                    std::process::exit(1);
                }
            }
        };
        let lock = match LockHandle::acquire(&socket_path) {
            Ok(l) => l,
            Err(e) => {
                eprintln!("{}", e);
                std::process::exit(1);
            }
        };
        (socket_path, lock)
    };
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

    // 输入行为设置：显式命令行参数 > 平台设置（设置窗写入）> 缺省。
    // daemon 自己读平台设置的原因见 input_settings 模块注释（LaunchAgent /
    // 安装器 Run 项的静态参数无法随设置变化）。
    let platform_settings = chaoset_config::behavior::load();
    let fuzzy = if args.fuzzy_set {
        args.fuzzy
    } else {
        platform_settings.fuzzy
    };
    let shuangpin_raw = if args.shuangpin_set {
        args.shuangpin.clone().unwrap_or_default()
    } else {
        platform_settings.shuangpin.clone()
    };
    let (shuangpin_scheme, shuangpin_note) = match shuangpin_raw.as_str() {
        "" => (None, "全拼".to_string()),
        "zrm" => (Some(ShuangpinScheme::Zrm), "自然码".to_string()),
        "mspy" => (Some(ShuangpinScheme::Mspy), "微软双拼".to_string()),
        "flypy" => (Some(ShuangpinScheme::Flypy), "小鹤双拼".to_string()),
        other => {
            eprintln!("警告: 未知双拼方案 {other:?}（只支持 zrm/mspy/flypy），按全拼处理");
            (None, "全拼".to_string())
        }
    };
    let fuzzy_source = if args.fuzzy_set { "参数" } else { "设置" };
    let shuangpin_source = if args.shuangpin_set {
        "参数"
    } else {
        "设置"
    };
    eprintln!(
        "模糊音: {}（来源: {}）",
        if fuzzy { "开" } else { "关" },
        fuzzy_source
    );
    eprintln!("双拼: {}（来源: {}）", shuangpin_note, shuangpin_source);

    let running = Arc::new(AtomicBool::new(true));

    // 后台 2 秒异步写入脏数据
    let flush_running = Arc::clone(&running);
    let flush_user_dict = user_dict.clone();
    let flush_thread = thread::spawn(move || {
        while flush_running.load(Ordering::SeqCst) {
            thread::sleep(Duration::from_secs(2));
            if let Some(ref u) = flush_user_dict {
                // 与 IPC 的 userClear/userDelete 同步落盘共用一把锁（见 ipc 模块）。
                let _ = ipc::flush_user_dict_if_dirty(u);
            }
        }
    });

    // 监听与服务
    let config = ServerConfig {
        dict: Arc::new(dict),
        user_dict: user_dict.clone(),
        fuzzy,
        traditional: args.traditional,
        shuangpin: shuangpin_scheme,
    };

    #[cfg(unix)]
    {
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
        let pipe_name = match chaoset_config::paths::default_pipe_name() {
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
        // 正常退出同步落盘，同样走 ipc 模块的落盘锁。
        ipc::save_user_dict_now(u);
    }
}
#[cfg(test)]
mod tests {
    use super::*;

    fn args_with(userdict: Option<&str>) -> CliArgs {
        CliArgs {
            user_dict_set: true,
            user_dict: userdict.map(|s| s.to_string()),
            ..CliArgs::default()
        }
    }

    /// `-userdict ""` 必须是**内存态用户词典**（学习/删除/导出可用、不落盘），
    /// 不是「未启用」：Go daemon 的 NewUserDict("") 同语义，三端 core_test
    /// 依赖 userDelete 返回 deleted=true/false 而不是「未启用用户词典」错误。
    #[test]
    fn version_info_is_single_line_with_binary_name() {
        let line = super::version_info();
        assert!(line.starts_with("chaosetd "));
        assert_eq!(line.lines().count(), 1);
    }

    #[test]
    fn empty_userdict_flag_means_in_memory_dict() {
        let ud = resolve_user_dict(&args_with(Some(""))).expect("用户词典必须存在");
        assert!(ud.path().is_none(), "显式空路径应为内存态（不落盘）");
        // 内存态也要能学习并删除（dispatch 层不再报「未启用用户词典」）。
        ud.bump_word("你好", Some("ni hao"), None);
        assert!(ud.delete_word("你好", &["ni".to_string(), "hao".to_string()]));
        assert!(!ud.delete_word("你好", &["ni".to_string(), "hao".to_string()]));
    }

    #[test]
    fn explicit_userdict_path_is_used() {
        let ud = resolve_user_dict(&args_with(Some("/tmp/chaoset-test-userdict.json")))
            .expect("用户词典必须存在");
        assert_eq!(
            ud.path().and_then(|p| p.to_str()),
            Some("/tmp/chaoset-test-userdict.json")
        );
    }
}
