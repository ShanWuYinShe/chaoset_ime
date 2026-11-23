//! 壳重连后的组词状态重建（issue #122）。
//!
//! Windows 壳的按键请求有 30ms 截止；超时后管道断开，daemon 会话随连接消失，
//! 而壳本地仍持有用户已输入的内容。壳重连后用 `input` 的 `base` 参数把
//! 「上次确认的输入串 + 此后按键」一次送回（`Session::input_with_base`），
//! 不必拆掉组词让用户重打。这里断言重建与逐键输入**逐条等价**。

use chaoset_engine::{Composition, Dictionary, Session, ShuangpinScheme};
use std::path::PathBuf;
use std::sync::Arc;

/// 加载内置词库（与 session.rs 单测的 common_dict 同源）。
fn common_dict() -> Arc<Dictionary> {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../data/dict_common.tsv");
    let mut dict = Dictionary::new();
    dict.load_tsv(&path)
        .unwrap_or_else(|e| panic!("加载内置词库失败 {}: {e}", path.display()));
    Arc::new(dict)
}

/// 组词结果的可比较视图：输入、预编辑、总页数与**全部分页**候选
/// （文本/音节/得分/纠错标记）。取完把会话翻回首页，便于继续比较后续操作。
type Snapshot = (String, String, usize, Vec<(String, String, i32, bool)>);

fn composition_snapshot(sess: &mut Session, mut comp: Composition) -> Snapshot {
    let rows = |c: &Composition| -> Vec<(String, String, i32, bool)> {
        c.candidates
            .iter()
            .map(|c| (c.text.clone(), c.syllables.clone(), c.score, c.typo_fix))
            .collect()
    };
    let mut all = rows(&comp);
    while comp.page + 1 < comp.page_count {
        comp = sess.next_page();
        all.extend(rows(&comp));
    }
    while comp.page > 0 {
        comp = sess.prev_page();
    }
    (comp.input, comp.preedit, comp.page_count, all)
}

/// 追加一段输入后取快照。
fn snapshot_input(sess: &mut Session, text: &str) -> Snapshot {
    let comp = sess.input(text, false).unwrap();
    composition_snapshot(sess, comp)
}

/// 退格一次后取快照。
fn snapshot_backspace(sess: &mut Session) -> Snapshot {
    let comp = sess.backspace();
    composition_snapshot(sess, comp)
}

/// `restore_input(整串)` 必须与逐键输入逐条等价：覆盖普通词、歧义切分、
/// 简拼、纠错形态与长整句，重建之后的退格也必须一致。
#[test]
fn restore_input_equals_typing_full_pinyin() {
    let dict = common_dict();
    for text in [
        "duankou",
        "chuangtiao",
        "xian",
        "nh",
        "jieliao",
        "yingwenzhuangtaiduankouyinggaishi",
    ] {
        let mut typed = Session::new(Arc::clone(&dict), None);
        let mut want = None;
        for c in text.chars() {
            want = Some(snapshot_input(&mut typed, &c.to_string()));
        }

        let mut restored = Session::new(Arc::clone(&dict), None);
        let comp = restored.restore_input(text);
        let got = composition_snapshot(&mut restored, comp);
        assert_eq!(want.unwrap(), got, "restore_input({text}) 必须等价于逐键输入");

        assert_eq!(
            snapshot_backspace(&mut typed),
            snapshot_backspace(&mut restored),
            "restore_input({text}) 后退格必须等价"
        );
    }
}

/// `input_with_base` = 先重建基底再追加：等价于「逐键敲出 base，再追加 text」；
/// base 为 None 时与 `input` 完全等价。
#[test]
fn input_with_base_equals_typing() {
    let dict = common_dict();
    let mut typed = Session::new(Arc::clone(&dict), None);
    snapshot_input(&mut typed, "duan");
    let want = snapshot_input(&mut typed, "kou");

    let mut fresh = Session::new(Arc::clone(&dict), None);
    let comp = fresh.input_with_base("kou", false, Some("duan")).unwrap();
    let got = composition_snapshot(&mut fresh, comp);
    assert_eq!(want, got, "input_with_base 必须等价于先敲 base 再追加 text");

    let mut plain = Session::new(Arc::clone(&dict), None);
    let comp = plain.input_with_base("duankou", false, None).unwrap();
    let got = composition_snapshot(&mut plain, comp);
    assert_eq!(want, got, "base=None 时 input_with_base 必须与 input 等价");
}

/// 双拼下的重建：`restore_input` 收的是已解码音节串，重建后候选与逐键解码
/// 一致，且退格仍按音节整段回滚（回滚粒度由首选切分还原）。
#[test]
fn restore_input_shuangpin() {
    let dict = common_dict();
    let mut typed = Session::new(Arc::clone(&dict), None);
    typed.set_shuangpin(Some(ShuangpinScheme::Zrm));
    for key in ["n", "i", "h", "k"] {
        typed.input(key, false).unwrap();
    }
    let want = snapshot_input(&mut typed, "");
    assert_eq!(want.0, "nihao", "逐键解码应得到 nihao");

    let mut restored = Session::new(Arc::clone(&dict), None);
    restored.set_shuangpin(Some(ShuangpinScheme::Zrm));
    let comp = restored.restore_input("nihao");
    let got = composition_snapshot(&mut restored, comp);
    assert_eq!(want, got, "双拼重建必须等价于逐键解码");

    // 退格：两边都整段回滚 hao，只剩 ni；随后继续解码也一致
    let want = snapshot_backspace(&mut typed);
    let got = snapshot_backspace(&mut restored);
    assert_eq!(want.0, "ni", "重建后的退格必须按音节整段回滚");
    assert_eq!(want, got);
    for key in ["h", "k"] {
        typed.input(key, false).unwrap();
        restored.input(key, false).unwrap();
    }
    assert_eq!(
        snapshot_input(&mut typed, ""),
        snapshot_input(&mut restored, "")
    );
}

/// 重建只认拼音字母与分隔符（与 input 的字符过滤同口径）。
#[test]
fn restore_input_filters_non_pinyin_chars() {
    let mut a = Session::new(common_dict(), None);
    let want = snapshot_input(&mut a, "duankou");
    let mut b = Session::new(common_dict(), None);
    let comp = b.restore_input(" du-an kou!");
    let got = composition_snapshot(&mut b, comp);
    assert_eq!(want, got, "非拼音字符必须被过滤");
}
