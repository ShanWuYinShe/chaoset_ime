//! daemon IPC：帧读写（复用 chaoset_ipc::protocol）+ 方法分发 + 用户词典同步落盘。
//!
//! 帧格式/上限/Request/Response 由公共 crate crates/chaoset-ipc 定义，daemon 与
//! 客户端（chaosetctl 等）共用同一份实现；本模块只保留 daemon 侧的 serde 适配与
//! 19 个方法的 dispatch。

use chaoset_engine::{Session, UserDict};
use serde::de::DeserializeOwned;
use serde::Serialize;
use serde_json::{json, Value};
use std::io::{self, Read, Write};
use std::sync::{Arc, Mutex, MutexGuard};

pub use chaoset_ipc::protocol::{read_frame, write_frame, Request, Response};

/// 读一帧并反序列化为 T；Ok(None) = 对端在帧边界正常关闭。
pub fn read_message<R: Read, T: DeserializeOwned>(reader: &mut R) -> io::Result<Option<T>> {
    let Some(buf) = read_frame(reader)? else {
        return Ok(None);
    };
    serde_json::from_slice(&buf)
        .map(Some)
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

/// 序列化 T 成帧写出（超限拒绝且一个字节不写，见 write_frame）。
pub fn write_message<W: Write, T: Serialize>(writer: &mut W, msg: &T) -> io::Result<()> {
    let data =
        serde_json::to_vec(msg).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    write_frame(writer, &data)
}

/// 用户词典落盘串行化：IPC 的 userClear/userDelete 同步落盘与 2s 后台 flush
/// 线程共用一把锁，避免两者同时写同一个 temp 文件（交错写会产出坏 JSON）。
static USER_DICT_SAVE_LOCK: Mutex<()> = Mutex::new(());

fn save_lock() -> MutexGuard<'static, ()> {
    USER_DICT_SAVE_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// 返回响应前 best-effort 落盘（兑现 userClear/userDelete「内存与磁盘同时生效」）。
///
/// 失败只写 stderr 日志、不影响响应：内存已更新，dirty 保持 true，
/// 下一次 2s 后台 flush 会重试。
pub fn save_user_dict_now(user_dict: &UserDict) {
    let _guard = save_lock();
    if let Err(e) = user_dict.save() {
        eprintln!("用户词典落盘失败（内存已更新，后台 flush 会重试）: {e}");
    }
}

/// 后台 2s flush：与 IPC 同步落盘共用同一把锁，避免并发写 temp 文件。
pub fn flush_user_dict_if_dirty(user_dict: &UserDict) -> io::Result<bool> {
    let _guard = save_lock();
    user_dict.flush_if_dirty()
}

pub fn dispatch(
    sess: &mut Session,
    user_dict: &Option<Arc<UserDict>>,
    req: &Request,
) -> Result<Value, String> {
    match req.method.as_str() {
        "ping" => Ok(json!("pong")),

        "input" => {
            let params = req
                .params
                .as_ref()
                .ok_or_else(|| "解析 input 参数失败".to_string())?;
            let text = params.get("text").and_then(|v| v.as_str()).unwrap_or("");
            let half = params
                .get("half")
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            // base（可选）：壳重连后重建组词状态——先把引擎输入串整体置为 base，
            // 再按 input 语义追加 text（Session::input_with_base）。缺省时行为不变。
            let base = params.get("base").and_then(|v| v.as_str());
            let comp = sess.input_with_base(text, half, base)?;
            Ok(json!(comp))
        }

        "backspace" => {
            let comp = sess.backspace();
            Ok(json!(comp))
        }

        "backspaceWord" => {
            let comp = sess.backspace_word();
            Ok(json!(comp))
        }

        "reset" => {
            let comp = sess.reset();
            Ok(json!(comp))
        }

        "resetComposition" => {
            let comp = sess.reset_composition();
            Ok(json!(comp))
        }

        "commitRaw" => {
            let text = sess.commit_raw()?;
            Ok(json!({ "committed": text }))
        }

        "nextPage" => {
            let comp = sess.next_page();
            Ok(json!(comp))
        }

        "prevPage" => {
            let comp = sess.prev_page();
            Ok(json!(comp))
        }

        "select" => {
            let params = req
                .params
                .as_ref()
                .ok_or_else(|| "解析 select 参数失败".to_string())?;
            let index = params.get("index").and_then(|v| v.as_i64()).unwrap_or(0) as usize;
            let text = sess.select(index)?;
            Ok(json!({ "committed": text }))
        }

        "selectPrefix" => {
            let params = req
                .params
                .as_ref()
                .ok_or_else(|| "解析 selectPrefix 参数失败".to_string())?;
            let index = params.get("index").and_then(|v| v.as_i64()).unwrap_or(0) as usize;
            let comp = sess.select_prefix(index)?;
            Ok(json!(comp))
        }

        "associate" => {
            let empty_vec = vec![];
            let sylls: Vec<String> = req
                .params
                .as_ref()
                .and_then(|p| p.get("sylls"))
                .and_then(|v| v.as_array())
                .unwrap_or(&empty_vec)
                .iter()
                .filter_map(|s| s.as_str().map(|str_val| str_val.to_string()))
                .collect();
            let candidates = sess.associate(&sylls);
            Ok(json!({
                "input": "",
                "preedit": "",
                "page": 0,
                "page_count": 1,
                "candidates": candidates,
            }))
        }

        "associateSelect" => {
            let params = req
                .params
                .as_ref()
                .ok_or_else(|| "解析 associateSelect 参数失败".to_string())?;
            let word = params.get("word").and_then(|v| v.as_str()).unwrap_or("");
            let empty_vec = vec![];
            let sylls: Vec<String> = params
                .get("sylls")
                .and_then(|v| v.as_array())
                .unwrap_or(&empty_vec)
                .iter()
                .filter_map(|s| s.as_str().map(|str_val| str_val.to_string()))
                .collect();
            let text = sess.associate_select(word, &sylls)?;
            Ok(json!({ "committed": text }))
        }

        "punct" => {
            let params = req
                .params
                .as_ref()
                .ok_or_else(|| "解析 punct 参数失败".to_string())?;
            let key = params.get("key").and_then(|v| v.as_str()).unwrap_or("");
            let half = params
                .get("half")
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            let text = sess.punct(key, half)?;
            Ok(json!({ "committed": text }))
        }

        "userStats" => {
            let u = user_dict
                .as_ref()
                .ok_or_else(|| "未启用用户词典".to_string())?;
            Ok(json!(u.stats()))
        }

        "userClear" => {
            let u = user_dict
                .as_ref()
                .ok_or_else(|| "未启用用户词典".to_string())?;
            u.clear();
            // 「内存与磁盘同时生效」：返回前 best-effort 落盘（失败不影响响应）。
            save_user_dict_now(u);
            Ok(json!({ "cleared": true }))
        }

        "userDelete" => {
            let u = user_dict
                .as_ref()
                .ok_or_else(|| "未启用用户词典".to_string())?;
            let params = req
                .params
                .as_ref()
                .ok_or_else(|| "userDelete 缺少 word 参数".to_string())?;
            let word = params
                .get("word")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .trim();
            if word.is_empty() {
                return Err("userDelete 缺少 word 参数".to_string());
            }

            let mut sylls: Vec<String> = params
                .get("sylls")
                .and_then(|v| v.as_array())
                .map(|arr| {
                    arr.iter()
                        .filter_map(|s| s.as_str().map(|str_val| str_val.to_string()))
                        .collect()
                })
                .unwrap_or_default();

            if sylls.is_empty() {
                if let Some(s_str) = params.get("syllables").and_then(|v| v.as_str()) {
                    let cleaned = s_str.replace('\'', " ");
                    sylls = cleaned.split_whitespace().map(|s| s.to_string()).collect();
                }
            }

            let deleted = u.delete_word(word, &sylls);
            // 「内存与磁盘同时生效」：返回前 best-effort 落盘（失败不影响响应）。
            save_user_dict_now(u);
            Ok(json!({ "deleted": deleted }))
        }

        "ui" => {
            let settings = req
                .params
                .as_ref()
                .and_then(|p| p.get("settings"))
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            crate::ui::ensure_ui_host(settings).map_err(|e| e.to_string())?;
            Ok(json!({ "started": true }))
        }

        "userExport" => {
            let u = user_dict
                .as_ref()
                .ok_or_else(|| "未启用用户词典".to_string())?;
            let phrases_only = req
                .params
                .as_ref()
                .and_then(|p| p.get("phrases_only"))
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            let offset = req
                .params
                .as_ref()
                .and_then(|p| p.get("offset"))
                .and_then(|v| v.as_i64())
                .unwrap_or(0);
            let limit = req
                .params
                .as_ref()
                .and_then(|p| p.get("limit"))
                .and_then(|v| v.as_i64())
                .unwrap_or(0);

            let tsv_full = u.export_tsv(phrases_only);
            let (page, next) = paginate_tsv(&tsv_full, offset, limit);
            Ok(json!({ "tsv": page, "next": next }))
        }

        _ => Err(format!("未知方法 \"{}\"", req.method)),
    }
}

pub fn paginate_tsv(full: &str, offset: i64, limit: i64) -> (String, i64) {
    if limit <= 0 {
        return (full.to_string(), -1);
    }
    let off = if offset < 0 { 0 } else { offset as usize };
    let lim = limit as usize;

    let lines: Vec<&str> = full.split_inclusive('\n').collect();
    if off >= lines.len() {
        return (String::new(), -1);
    }

    let end = (off + lim).min(lines.len());
    let page = lines[off..end].concat();
    let next = if end >= lines.len() { -1 } else { end as i64 };
    (page, next)
}


#[cfg(test)]
mod tests {
    use super::*;
    use chaoset_engine::Dictionary;
    use std::sync::atomic::{AtomicUsize, Ordering};

    static TEMP_SEQ: AtomicUsize = AtomicUsize::new(0);

    fn temp_userdict_path(tag: &str) -> std::path::PathBuf {
        std::env::temp_dir().join(format!(
            "chaosetd-ipc-{}-{}-{}.json",
            std::process::id(),
            tag,
            TEMP_SEQ.fetch_add(1, Ordering::SeqCst)
        ))
    }

    fn make_session(ud: Arc<UserDict>) -> Session {
        Session::new(Arc::new(Dictionary::new()), Some(ud))
    }

    /// userClear 必须「内存与磁盘同时生效」：IPC 响应返回前落盘，重载文件看到 0 条。
    #[test]
    fn user_clear_persists_before_response() {
        let path = temp_userdict_path("clear");
        let ud = Arc::new(UserDict::new(Some(path.clone())));
        ud.bump_word("你好", Some("ni'hao"), None);
        assert_eq!(ud.stats().words, 1);

        let user_dict = Some(Arc::clone(&ud));
        let mut sess = make_session(Arc::clone(&ud));
        let req = Request {
            id: 1,
            method: "userClear".to_string(),
            params: None,
        };
        let result = dispatch(&mut sess, &user_dict, &req).expect("userClear 应成功");
        assert_eq!(result["cleared"], true);

        let reloaded = UserDict::new(Some(path.clone()));
        let stats = reloaded.stats();
        assert_eq!(
            (stats.words, stats.phrases, stats.bigrams),
            (0, 0, 0),
            "响应返回前必须已落盘（重载为 0 条）"
        );
        let _ = std::fs::remove_file(&path);
    }

    /// userDelete 同样在响应前落盘：删一条、留一条，重载后只剩保留的词。
    #[test]
    fn user_delete_persists_before_response() {
        let path = temp_userdict_path("delete");
        let ud = Arc::new(UserDict::new(Some(path.clone())));
        ud.bump_word("你好", Some("ni'hao"), None);
        ud.bump_word("世界", Some("shi'jie"), None);

        let user_dict = Some(Arc::clone(&ud));
        let mut sess = make_session(Arc::clone(&ud));
        let req = Request {
            id: 1,
            method: "userDelete".to_string(),
            params: Some(json!({ "word": "你好" })),
        };
        let result = dispatch(&mut sess, &user_dict, &req).expect("userDelete 应成功");
        assert_eq!(result["deleted"], true);

        let reloaded = UserDict::new(Some(path.clone()));
        assert_eq!(reloaded.stats().words, 1, "仅删除一条，且删除已落盘");
        assert!(reloaded.score("世界", None).1, "保留的词仍在磁盘上");
        let _ = std::fs::remove_file(&path);
    }

    /// `input` 的 `base` 参数（issue #122）：新会话上一次 `base + text` 的组词
    /// 结果，必须与逐键敲出整串完全一致——壳重连后据此重建组词状态。
    #[test]
    fn input_with_base_rebuilds_fresh_session() {
        let mut dict = Dictionary::new();
        dict.load_tsv_str("断口\tduan'kou\t900\n短\tduan\t800\n口\tkou\t700\n");
        let dict = Arc::new(dict);
        let make = || Session::new(Arc::clone(&dict), None);

        let mut typed = make();
        let mut want = None;
        for key in ["d", "u", "a", "n", "k", "o"] {
            want = dispatch(&mut typed, &None, &Request {
                id: 1,
                method: "input".to_string(),
                params: Some(json!({ "text": key, "half": false })),
            })
            .ok();
        }
        let want = want.expect("逐键 input 应成功");

        // 新会话（模拟壳重连）：base = 上次确认的输入串，text = 失步期间追加的键。
        let mut fresh = make();
        let got = dispatch(&mut fresh, &None, &Request {
            id: 2,
            method: "input".to_string(),
            params: Some(json!({ "text": "o", "half": false, "base": "duank" })),
        })
        .expect("带 base 的 input 应成功");
        assert_eq!(want, got, "带 base 的重建必须与逐键输入同构");
        assert_eq!(got["input"], "duanko");
        assert_eq!(got["candidates"][0]["text"], "断口");
    }

    /// 端到端：真实 daemon server（crate::server）+ 真实共享协议客户端
    /// （chaoset_ipc::client）走一遍 ping/input/select/userStats/userExport，
    /// 验证 daemon 与客户端确实共用同一份帧协议。
    #[cfg(unix)]
    #[test]
    fn client_and_daemon_server_interoperate() {
        use crate::server::{run_unix_server, ServerConfig};
        use std::os::unix::net::UnixListener;
        use std::sync::atomic::{AtomicBool, Ordering};
        use std::time::Duration;

        let socket_path = temp_userdict_path("e2e").with_extension("sock");
        let _ = std::fs::remove_file(&socket_path);
        let listener = UnixListener::bind(&socket_path).expect("bind test socket");

        let mut dict = Dictionary::new();
        dict.load_tsv_str("你好\tni'hao\t9999\n");
        let user_dict = Arc::new(UserDict::new::<&str>(None));
        let running = Arc::new(AtomicBool::new(true));
        let server_running = Arc::clone(&running);
        let config = ServerConfig {
            dict: Arc::new(dict),
            user_dict: Some(Arc::clone(&user_dict)),
            fuzzy: false,
            traditional: false,
            shuangpin: None,
        };
        let server = std::thread::spawn(move || {
            let _ = run_unix_server(listener, config, server_running);
        });
        // bind 在 connect 前已完成（listen backlog 兜底），无需等待 accept 线程。
        std::thread::sleep(Duration::from_millis(10));

        let mut client =
            chaoset_ipc::client::Client::connect_unix(&socket_path).expect("连接 daemon");
        client.ping().expect("ping");
        let comp = client.input("nihao", false).expect("input");
        assert_eq!(comp.preedit, "ni'hao");
        assert_eq!(
            comp.candidates.first().map(|c| c.text.as_str()),
            Some("你好"),
            "候选必须来自 daemon 加载的词库"
        );
        assert_eq!(client.select(0).expect("select"), "你好");
        assert!(client.user_stats().expect("userStats").words >= 1);
        client.user_clear().expect("userClear");
        assert!(
            !client.user_export(false).expect("userExport").contains("你好"),
            "清空后导出不应再有学习记录"
        );
        client.close();

        running.store(false, Ordering::SeqCst);
        let _ = server.join();
        let _ = std::fs::remove_file(&socket_path);
    }
}

