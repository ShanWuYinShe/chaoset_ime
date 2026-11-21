//! Client 单测：用 UnixListener + 手写 fake daemon（线程内读帧回包）验证协议契约。
//!
//! 覆盖：ping、error 不关连接、响应 ID 不匹配关连接、userExport 两页拼接、
//! next 不前进报错、调用超时（100ms 缩短版）关连接、19 个方法的路由与参数。

use super::*;
use crate::protocol::{read_frame, write_frame, Request, Response};
use std::os::unix::net::UnixListener;
use std::path::PathBuf;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

static SOCKET_SEQ: AtomicUsize = AtomicUsize::new(0);

/// 收到的 userExport 参数（分页行为断言用）。
type ParamsLog = Arc<Mutex<Vec<Option<Value>>>>;
/// 收到的 (方法名, 参数) 记录（19 方法路由断言用）。
type RequestLog = Arc<Mutex<Vec<(String, Option<Value>)>>>;

fn socket_path() -> PathBuf {
    std::env::temp_dir().join(format!(
        "csipc{}-{}.sock",
        std::process::id(),
        SOCKET_SEQ.fetch_add(1, Ordering::SeqCst)
    ))
}

fn fake_composition() -> Value {
    json!({
        "input": "ni",
        "preedit": "ni",
        "candidates": [
            { "text": "你", "syllables": "ni", "score": 9, "from_user": true }
        ],
        "page": 0,
        "page_count": 1,
        "committed": "你",
        "shuangpin": "",
    })
}

enum FakeAction {
    Reply(Value),
    Error(String),
    WrongId,
    Hang,
}

struct FakeDaemon {
    path: PathBuf,
    handle: Option<JoinHandle<()>>,
}

impl FakeDaemon {
    fn join(mut self) {
        if let Some(handle) = self.handle.take() {
            handle.join().expect("fake daemon 线程 panic");
        }
    }
}

impl Drop for FakeDaemon {
    fn drop(&mut self) {
        if let Some(handle) = self.handle.take() {
            let _ = handle.join();
        }
        let _ = std::fs::remove_file(&self.path);
    }
}

fn fake_daemon<F>(mut handler: F) -> FakeDaemon
where
    F: FnMut(&Request) -> FakeAction + Send + 'static,
{
    let path = socket_path();
    let _ = std::fs::remove_file(&path);
    let listener = UnixListener::bind(&path).expect("bind fake daemon socket");
    let handle = thread::spawn(move || {
        let Ok((mut stream, _)) = listener.accept() else {
            return;
        };
        loop {
            let frame = match read_frame(&mut stream) {
                Ok(Some(frame)) => frame,
                _ => return,
            };
            let req: Request = match serde_json::from_slice(&frame) {
                Ok(req) => req,
                Err(_) => return,
            };
            let resp = match handler(&req) {
                FakeAction::Reply(value) => Response {
                    id: req.id,
                    result: Some(value),
                    error: None,
                },
                FakeAction::Error(msg) => Response {
                    id: req.id,
                    result: None,
                    error: Some(msg),
                },
                FakeAction::WrongId => Response {
                    id: req.id + 100,
                    result: Some(json!("pong")),
                    error: None,
                },
                FakeAction::Hang => {
                    thread::sleep(Duration::from_millis(300));
                    return;
                }
            };
            let data = match serde_json::to_vec(&resp) {
                Ok(data) => data,
                Err(_) => return,
            };
            if write_frame(&mut stream, &data).is_err() {
                return;
            }
        }
    });
    FakeDaemon {
        path,
        handle: Some(handle),
    }
}

fn connect(path: &Path) -> Client {
    Client::connect_unix(path).expect("连接 fake daemon 失败")
}

#[test]
fn ping_roundtrip() {
    let daemon = fake_daemon(|_req| FakeAction::Reply(json!("pong")));
    let mut client = connect(&daemon.path);
    client.ping().expect("ping 应成功");
    assert!(client.is_connected());
    client.close();
    daemon.join();
}

#[test]
fn daemon_error_keeps_connection() {
    let daemon = fake_daemon(|req| {
        if req.method == "ping" {
            FakeAction::Reply(json!("pong"))
        } else {
            FakeAction::Error("boom".to_string())
        }
    });
    let mut client = connect(&daemon.path);
    let err = client.call_raw("testError", None).unwrap_err();
    assert_eq!(err, "daemon 返回错误: boom");
    // error 是协议层成功响应：连接必须保持可用。
    assert!(client.is_connected());
    client.ping().expect("error 之后同一连接仍应可用");
    assert!(client.is_connected());
    client.close();
    daemon.join();
}

#[test]
fn id_mismatch_closes_connection() {
    let daemon = fake_daemon(|_req| FakeAction::WrongId);
    let mut client = connect(&daemon.path);
    let err = client.ping().unwrap_err();
    assert!(
        err.contains("响应 ID 不匹配"),
        "错误消息应含「响应 ID 不匹配」: {err}"
    );
    assert!(!client.is_connected(), "ID 不匹配后连接必须关闭");
    let err = client.ping().unwrap_err();
    assert!(err.contains("已关闭"), "连接关闭后调用应直接报错: {err}");
    daemon.join();
}

#[test]
fn user_export_concatenates_pages() {
    let log: ParamsLog = Arc::new(Mutex::new(Vec::new()));
    let log_thread = Arc::clone(&log);
    let daemon = fake_daemon(move |req| {
        log_thread.lock().unwrap().push(req.params.clone());
        let offset = req
            .params
            .as_ref()
            .and_then(|p| p.get("offset"))
            .and_then(|v| v.as_i64())
            .unwrap_or(-1);
        match offset {
            0 => FakeAction::Reply(json!({ "tsv": "词A\tni\t1\t2\n", "next": 1 })),
            1 => FakeAction::Reply(json!({ "tsv": "词B\tshi\t3\t4\n", "next": -1 })),
            other => FakeAction::Error(format!("意外 offset {other}")),
        }
    });
    let mut client = connect(&daemon.path);
    let tsv = client.user_export(false).expect("两页导出应成功");
    assert_eq!(tsv, "词A\tni\t1\t2\n词B\tshi\t3\t4\n");
    client.close();
    daemon.join();

    let log = log.lock().unwrap();
    assert_eq!(log.len(), 2, "应按 offset=0/1 拉两页");
    for params in log.iter() {
        let params = params.as_ref().expect("userExport 必须带 params");
        assert_eq!(params["limit"].as_i64(), Some(2000), "单页行数必须是 2000");
        assert_eq!(params["phrases_only"].as_bool(), Some(false));
    }
    assert_eq!(log[0].as_ref().unwrap()["offset"].as_i64(), Some(0));
    assert_eq!(log[1].as_ref().unwrap()["offset"].as_i64(), Some(1));
}

#[test]
fn user_export_rejects_stalled_next() {
    let daemon = fake_daemon(|_req| FakeAction::Reply(json!({ "tsv": "x\n", "next": 0 })));
    let mut client = connect(&daemon.path);
    let err = client.user_export(true).unwrap_err();
    assert!(
        err.contains("导出分页 next=0 未前进"),
        "错误消息应含「导出分页 next=... 未前进」: {err}"
    );
    assert!(!client.is_connected(), "分页异常必须关闭连接");
    daemon.join();
}

#[test]
fn call_timeout_closes_connection() {
    let daemon = fake_daemon(|_req| FakeAction::Hang);
    let mut client = connect(&daemon.path);
    client.set_call_timeout(Duration::from_millis(100));
    let started = Instant::now();
    let err = client.ping().unwrap_err();
    assert!(err.contains("超时"), "超时错误消息应含「超时」: {err}");
    assert!(
        started.elapsed() < Duration::from_secs(2),
        "100ms 截止应快速返回，实际 {:?}",
        started.elapsed()
    );
    assert!(!client.is_connected(), "超时后连接必须关闭");
    daemon.join();
}

#[test]
fn user_delete_omits_empty_sylls() {
    let log: ParamsLog = Arc::new(Mutex::new(Vec::new()));
    let log_thread = Arc::clone(&log);
    let daemon = fake_daemon(move |req| {
        log_thread.lock().unwrap().push(req.params.clone());
        FakeAction::Reply(json!({ "deleted": true }))
    });
    let mut client = connect(&daemon.path);
    assert!(client.user_delete("你好", &[]).unwrap());
    assert!(client
        .user_delete("你好", &["ni".to_string(), "hao".to_string()])
        .unwrap());
    client.close();
    daemon.join();

    let log = log.lock().unwrap();
    assert_eq!(log.len(), 2);
    let empty = log[0].as_ref().unwrap();
    assert!(
        empty.get("sylls").is_none(),
        "sylls 为空时必须省略该键: {empty}"
    );
    let with_sylls = log[1].as_ref().unwrap();
    assert_eq!(with_sylls["sylls"], json!(["ni", "hao"]));
}

#[test]
fn all_19_methods_route_to_daemon() {
    let log: RequestLog = Arc::new(Mutex::new(Vec::new()));
    let log_thread = Arc::clone(&log);
    let daemon = fake_daemon(move |req| {
        log_thread
            .lock()
            .unwrap()
            .push((req.method.clone(), req.params.clone()));
        match req.method.as_str() {
            "ping" => FakeAction::Reply(json!("pong")),
            "input" | "backspace" | "backspaceWord" | "reset" | "resetComposition" | "nextPage"
            | "prevPage" | "selectPrefix" => FakeAction::Reply(fake_composition()),
            "commitRaw" => FakeAction::Reply(json!({ "committed": "raw" })),
            "select" => FakeAction::Reply(json!({ "committed": "你" })),
            "associate" => FakeAction::Reply(json!({
                "input": "",
                "preedit": "",
                "page": 0,
                "page_count": 1,
                "candidates": [
                    { "text": "你好", "syllables": "ni'hao", "score": 9 }
                ],
            })),
            "associateSelect" => FakeAction::Reply(json!({ "committed": "你好" })),
            "punct" => FakeAction::Reply(json!({ "committed": "，" })),
            "userStats" => FakeAction::Reply(json!({ "words": 3, "phrases": 2, "bigrams": 1 })),
            "userClear" => FakeAction::Reply(json!({ "cleared": true })),
            "userDelete" => FakeAction::Reply(json!({ "deleted": true })),
            "userExport" => FakeAction::Reply(json!({ "tsv": "# 导出\n", "next": -1 })),
            "ui" => FakeAction::Reply(json!({ "started": true })),
            other => FakeAction::Error(format!("未知方法 {other}")),
        }
    });
    let mut client = connect(&daemon.path);

    client.ping().unwrap();
    assert_eq!(client.input("ni", false).unwrap().input, "ni");
    assert_eq!(client.backspace().unwrap().preedit, "ni");
    assert_eq!(client.backspace_word().unwrap().input, "ni");
    assert_eq!(client.reset().unwrap().page, 0);
    assert_eq!(client.reset_composition().unwrap().page_count, 1);
    assert_eq!(client.commit_raw().unwrap(), "raw");
    assert_eq!(client.next_page().unwrap().input, "ni");
    assert_eq!(client.prev_page().unwrap().input, "ni");
    assert_eq!(client.select(0).unwrap(), "你");
    let (comp, committed) = client.select_prefix(0).unwrap();
    assert_eq!(comp.preedit, "ni");
    assert_eq!(committed, "你");
    let candidates = client.associate(&["ni".to_string()]).unwrap();
    assert_eq!(candidates.len(), 1);
    assert_eq!(candidates[0].text, "你好");
    assert_eq!(
        client
            .associate_select("你好", &["ni".to_string(), "hao".to_string()])
            .unwrap(),
        "你好"
    );
    assert_eq!(client.punct(",", false).unwrap(), "，");
    let stats = client.user_stats().unwrap();
    assert_eq!((stats.words, stats.phrases, stats.bigrams), (3, 2, 1));
    client.user_clear().unwrap();
    assert!(client.user_delete("你好", &[]).unwrap());
    assert_eq!(client.user_export(false).unwrap(), "# 导出\n");
    client.ui(true).unwrap();

    client.close();
    daemon.join();

    let log = log.lock().unwrap();
    let methods: Vec<&str> = log.iter().map(|(method, _)| method.as_str()).collect();
    assert_eq!(
        methods,
        vec![
            "ping",
            "input",
            "backspace",
            "backspaceWord",
            "reset",
            "resetComposition",
            "commitRaw",
            "nextPage",
            "prevPage",
            "select",
            "selectPrefix",
            "associate",
            "associateSelect",
            "punct",
            "userStats",
            "userClear",
            "userDelete",
            "userExport",
            "ui",
        ],
        "19 个客户端方法与 daemon 方法名必须一一对应"
    );
    // input 的 half 参数随每次请求下发（daemon 不保存壳的宽/半角状态）。
    let input_params = log
        .iter()
        .find(|(m, _)| m == "input")
        .unwrap()
        .1
        .as_ref()
        .unwrap();
    assert_eq!(input_params["half"], json!(false));
    // ui 的 settings 参数。
    let ui_params = log
        .iter()
        .find(|(m, _)| m == "ui")
        .unwrap()
        .1
        .as_ref()
        .unwrap();
    assert_eq!(ui_params["settings"], json!(true));
}
