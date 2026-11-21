//! 引擎差分回放器（engine parity replay）。
//!
//! 读取与 tools/engineparity 相同格式的场景 JSONL，逐场景在**全新 Session**
//! 上回放（不带用户词典，保证确定性），输出规范化响应 JSONL。输出由
//! tools/engineparity 的 compare 模式解析，与 Go 侧回放结果做语义对比。
//!
//! 场景行格式（字段缺省值与 Go 侧一致）：
//!   {"name":"basic-nihao","config":{"fuzzy":false,"traditional":false,"shuangpin":""},
//!    "steps":[{"op":"input","text":"nihao"},{"op":"select","index":0}]}
//!
//! 用法：replay --dict <tsv> --scenarios <jsonl>（结果写 stdout，日志走 stderr）

use chaoset_engine::{Composition, Candidate, Dictionary, Session, ShuangpinScheme};
use serde::Deserialize;
use serde_json::{json, Value};
use std::io::{BufWriter, Write};
use std::process::ExitCode;
use std::sync::Arc;

#[derive(Deserialize)]
struct Scenario {
    name: String,
    #[serde(default)]
    config: Config,
    steps: Vec<Step>,
}

#[derive(Deserialize, Default)]
struct Config {
    #[serde(default)]
    fuzzy: bool,
    #[serde(default)]
    traditional: bool,
    #[serde(default)]
    shuangpin: String,
}

#[derive(Deserialize)]
struct Step {
    op: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    half: bool,
    #[serde(default)]
    index: usize,
    #[serde(default)]
    key: String,
    #[serde(default)]
    word: String,
    #[serde(default)]
    sylls: Vec<String>,
}

/// 与 Go 侧 normComp 完全同构的规范化 Composition（字段恒在，空值显式输出）。
fn norm_comp(c: &Composition) -> Value {
    let cands: Vec<Value> = c.candidates.iter().map(norm_candidate).collect();
    json!({
        "input": c.input,
        "preedit": c.preedit,
        "page": c.page,
        "page_count": c.page_count,
        "candidates": cands,
        "committed": c.committed,
        "shuangpin": c.shuangpin,
    })
}

fn norm_candidate(cd: &Candidate) -> Value {
    json!({
        "text": cd.text,
        "syllables": cd.syllables,
        "score": cd.score,
        "from_user": cd.from_user,
    })
}

fn comp_ok(c: Composition) -> Value {
    json!({ "ok": true, "comp": norm_comp(&c) })
}

fn committed_ok(t: String) -> Value {
    json!({ "ok": true, "committed": t })
}

fn err_val(e: String) -> Value {
    json!({ "ok": false, "error": e })
}

fn main() -> ExitCode {
    let mut dict_path = String::new();
    let mut scen_path = String::new();
    let raw: Vec<String> = std::env::args().skip(1).collect();
    let mut i = 0;
    while i < raw.len() {
        match raw[i].as_str() {
            "--dict" => {
                i += 1;
                dict_path = raw.get(i).cloned().unwrap_or_default();
            }
            "--scenarios" => {
                i += 1;
                scen_path = raw.get(i).cloned().unwrap_or_default();
            }
            other => {
                eprintln!("replay: 未知参数 {other}");
                return ExitCode::from(2);
            }
        }
        i += 1;
    }
    if dict_path.is_empty() || scen_path.is_empty() {
        eprintln!("用法: replay --dict <tsv> --scenarios <jsonl>");
        return ExitCode::from(2);
    }

    let mut dict = Dictionary::new();
    match dict.load_tsv(&dict_path) {
        Ok(n) => eprintln!("replay: 已加载 {n} 条词条（{dict_path}）"),
        Err(e) => {
            eprintln!("replay: 加载词库失败: {e}");
            return ExitCode::from(1);
        }
    }
    let dict = Arc::new(dict);

    let content = match std::fs::read_to_string(&scen_path) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("replay: 读取场景文件失败: {e}");
            return ExitCode::from(1);
        }
    };

    let stdout = std::io::stdout();
    let mut out = BufWriter::new(stdout.lock());
    for line in content.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let scen: Scenario = match serde_json::from_str(line) {
            Ok(s) => s,
            Err(e) => {
                eprintln!("replay: 场景行解析失败: {e}");
                return ExitCode::from(1);
            }
        };
        let results = run_scenario(&dict, &scen);
        let rec = json!({ "name": scen.name, "steps": results });
        if writeln!(out, "{rec}").is_err() {
            return ExitCode::from(1);
        }
    }
    ExitCode::SUCCESS
}

fn run_scenario(dict: &Arc<Dictionary>, scen: &Scenario) -> Vec<Value> {
    let mut sess = Session::new(Arc::clone(dict), None);
    sess.set_fuzzy(scen.config.fuzzy);
    sess.set_traditional(scen.config.traditional);
    if !scen.config.shuangpin.is_empty() {
        let scheme = match scen.config.shuangpin.as_str() {
            "zrm" => Some(ShuangpinScheme::Zrm),
            "mspy" => Some(ShuangpinScheme::Mspy),
            "flypy" => Some(ShuangpinScheme::Flypy),
            _ => None,
        };
        sess.set_shuangpin(scheme);
    }
    scen.steps.iter().map(|step| run_step(&mut sess, step)).collect()
}

fn run_step(sess: &mut Session, step: &Step) -> Value {
    match step.op.as_str() {
        "input" => match sess.input(&step.text, step.half) {
            Ok(c) => comp_ok(c),
            Err(e) => err_val(e),
        },
        "backspace" => comp_ok(sess.backspace()),
        "backspaceWord" => comp_ok(sess.backspace_word()),
        "reset" => comp_ok(sess.reset()),
        "resetComposition" => comp_ok(sess.reset_composition()),
        "nextPage" => comp_ok(sess.next_page()),
        "prevPage" => comp_ok(sess.prev_page()),
        "select" => match sess.select(step.index) {
            Ok(t) => committed_ok(t),
            Err(e) => err_val(e),
        },
        "selectPrefix" => match sess.select_prefix(step.index) {
            Ok(c) => comp_ok(c),
            Err(e) => err_val(e),
        },
        "commitRaw" => match sess.commit_raw() {
            Ok(t) => committed_ok(t),
            Err(e) => err_val(e),
        },
        "punct" => match sess.punct(&step.key, step.half) {
            Ok(t) => committed_ok(t),
            Err(e) => err_val(e),
        },
        "associate" => {
            let cands: Vec<Value> = sess.associate(&step.sylls).iter().map(norm_candidate).collect();
            json!({ "ok": true, "candidates": cands })
        }
        "associateSelect" => match sess.associate_select(&step.word, &step.sylls) {
            Ok(t) => committed_ok(t),
            Err(e) => err_val(e),
        },
        other => err_val(format!("未知操作 {other}")),
    }
}
