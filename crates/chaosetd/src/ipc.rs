use chaoset_engine::{Session, UserDict};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::io::{self, Read, Write};
use std::sync::Arc;

pub const MAX_MESSAGE_SIZE: usize = 1 << 20; // 1 MiB

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Request {
    pub id: u64,
    pub method: String,
    #[serde(default)]
    pub params: Option<Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Response {
    pub id: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

pub fn read_message<R: Read, T: serde::de::DeserializeOwned>(
    reader: &mut R,
) -> io::Result<Option<T>> {
    let mut header = [0u8; 4];
    match reader.read_exact(&mut header) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(e) => return Err(e),
    }

    let size = u32::from_be_bytes(header) as usize;
    if size > MAX_MESSAGE_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            format!("消息长度 {} 超过上限 {}", size, MAX_MESSAGE_SIZE),
        ));
    }

    let mut buf = vec![0u8; size];
    reader.read_exact(&mut buf)?;
    let msg: T =
        serde_json::from_slice(&buf).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    Ok(Some(msg))
}

pub fn write_message<W: Write, T: serde::Serialize>(writer: &mut W, msg: &T) -> io::Result<()> {
    let data =
        serde_json::to_vec(msg).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

    if data.len() > MAX_MESSAGE_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            format!("消息长度 {} 超过上限 {}", data.len(), MAX_MESSAGE_SIZE),
        ));
    }

    let header = (data.len() as u32).to_be_bytes();
    writer.write_all(&header)?;
    writer.write_all(&data)?;
    writer.flush()
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
            let comp = sess.input(text, half)?;
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
