//! 词库解析/写出契约（移植 Go internal/dict/parse_test.go 全清单 + 字节级金标）。

use chaoset_tools::dictparse::{
    format_tsv, looks_like_cccedict, normalize_pinyin, parse_auto, strip_yaml_header, write_tsv,
    Entry, CEDICT_DEFAULT_FREQ, TSV_HEADER,
};

/// 覆盖 Rime dict.yaml 常见写法：YAML 头、音调数字、声调符号、ü→v。
const RIME_YAML: &str = "\
# Rime dictionary
---
name: test
version: \"1\"
sort: original
...
你好\tni3 hao3\t50000
做\tzuò\t300
绿\tlǜ4\t100
略\tlüe4\t90
略\tlue4\t88
坏词\tzzz\t10
只有词
";

fn find<'a>(entries: &'a [Entry], word: &str) -> Option<&'a Entry> {
    entries.iter().find(|e| e.word == word)
}

#[test]
fn parse_auto_rime_yaml() {
    let (entries, stats) = parse_auto(RIME_YAML);
    assert_eq!(stats.skipped, 2, "应跳过 2 行无效词条: {stats:?}");
    assert_eq!(entries.len(), 4, "词条数应为 4: {entries:?}");

    let e = find(&entries, "你好").expect("缺少 你好");
    assert_eq!(e.syllables_joined(), "ni hao");
    assert_eq!(e.freq, 99, "词频应压缩到 99");
    assert_eq!(
        find(&entries, "做").unwrap().syllables_joined(),
        "zuo",
        "带声调符号应归一化"
    );
    assert_eq!(find(&entries, "绿").unwrap().syllables_joined(), "lv");
    let e = find(&entries, "略").unwrap();
    assert_eq!(e.syllables_joined(), "lve", "lüe4/lue4 应归一化为 lve");
    assert_eq!(e.freq, 90, "去重应取较大词频");
}

#[test]
fn parse_auto_two_column_tsv_defaults_freq_20() {
    let (entries, stats) = parse_auto("两列词\tliang lie\n");
    assert_eq!(stats.skipped, 0);
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0].freq, 20);
}

#[test]
fn parse_auto_runon_pinyin_is_segmented() {
    let (entries, stats) =
        parse_auto("混沌输入法\thundun shurufa\t88\n好朋友\thaopengyou\t77\n坏词\tzzz\t10\n");
    assert_eq!(stats.skipped, 1);
    assert_eq!(
        find(&entries, "混沌输入法").unwrap().syllables_joined(),
        "hun dun shu ru fa"
    );
    assert_eq!(
        find(&entries, "好朋友").unwrap().syllables_joined(),
        "hao peng you"
    );
}

#[test]
fn parse_auto_tsv_is_not_mistaken_for_cedict() {
    let (entries, stats) = parse_auto("你好\tni hao\t99\n");
    assert_eq!((entries.len(), stats.skipped), (1, 0));
    assert!(!looks_like_cccedict("你好\tni hao\t99\n"));
}

#[test]
fn freq_clamped_and_defaulted() {
    let (entries, _) = parse_auto("高频\ta\t500\n负频\te\t-3\n零频\tei\t0\n非数字\ten\tx\n\n");
    assert_eq!(find(&entries, "高频").unwrap().freq, 99);
    assert_eq!(find(&entries, "负频").unwrap().freq, 20);
    assert_eq!(find(&entries, "零频").unwrap().freq, 20);
    assert_eq!(find(&entries, "非数字").unwrap().freq, 20);
}

#[test]
fn duplicate_word_syllables_keep_larger_freq() {
    let (entries, _) = parse_auto("你好\tni hao\t10\n你好\tni hao\t30\n你好\tni hao\t20\n");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0].freq, 30);
}

#[test]
fn skip_samples_record_line_numbers_and_reasons() {
    let src = "坏行\n坏词\tzzz\t1\n也坏\tqqq\t2\n另一坏\twww\t3\n再坏\tyyy\t4\n";
    let (_, stats) = parse_auto(src);
    assert_eq!(stats.skipped, 5);
    assert_eq!(stats.samples.len(), 3, "样本上限 3 条");
    assert!(
        stats.samples[0].contains("第 1 行"),
        "样本带行号: {stats:?}"
    );
    assert!(stats.samples[0].contains("缺少制表符分隔"));
    assert!(stats.samples[1].contains("第 2 行"));
}

#[test]
fn parse_ccc_edict_sample() {
    let sample = "\
# CC-CEDICT 示例（许可证头注释会被跳过）
# License: CC-BY-SA 4.0
中國\t中国\t[Zhong1 guo2] /China/Middle Kingdom/
時間\t时间\t[shi2 jian1] /time/
女\t女\t[nv3] /female/
略\t略\t[lve4] /strategy/
 Liverpool\tLiverpool\t[Li1 wu2 pu3] /Liverpool/
OK\tOK\t[O K] /OK/
";
    let (entries, stats) = parse_auto(sample);
    assert_eq!(
        (entries.len(), stats.skipped),
        (4, 2),
        "entries={entries:?}"
    );
    let e = find(&entries, "中国").unwrap();
    assert_eq!(e.syllables_joined(), "zhong guo");
    assert_eq!(e.freq, CEDICT_DEFAULT_FREQ);
    assert_eq!(
        find(&entries, "时间").unwrap().syllables_joined(),
        "shi jian"
    );
    assert_eq!(find(&entries, "女").unwrap().syllables_joined(), "nv");
    assert_eq!(find(&entries, "略").unwrap().syllables_joined(), "lve");
    // 词必须全 CJK：Liverpool/OK 被过滤
    assert!(find(&entries, "Liverpool").is_none());
    assert!(find(&entries, "OK").is_none());
}

#[test]
fn cedict_u_colon_maps_to_v() {
    let (entries, _) = parse_auto("綠\t绿\t[lu:4] /green/\n");
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0].syllables_joined(), "lv");
}

#[test]
fn normalize_pinyin_cases() {
    assert_eq!(normalize_pinyin("ni3 hao3").unwrap(), vec!["ni", "hao"]);
    assert_eq!(normalize_pinyin("ni3hao3").unwrap(), vec!["ni", "hao"]);
    assert_eq!(normalize_pinyin("ni'hao").unwrap(), vec!["ni", "hao"]);
    assert_eq!(normalize_pinyin("zuò").unwrap(), vec!["zuo"]);
    assert_eq!(normalize_pinyin("lǜ4").unwrap(), vec!["lv"]);
    assert_eq!(normalize_pinyin("lüe4").unwrap(), vec!["lve"]);
    assert_eq!(normalize_pinyin("lue4").unwrap(), vec!["lve"]);
    assert_eq!(normalize_pinyin("nue4").unwrap(), vec!["nve"]);
    assert_eq!(
        normalize_pinyin("zhong1guo2").unwrap(),
        vec!["zhong", "guo"]
    );
    assert_eq!(normalize_pinyin("").unwrap(), Vec::<String>::new());
    assert!(normalize_pinyin("zzz").is_none());
    assert!(normalize_pinyin("ni好").is_none(), "非 ASCII 无法归一化");
}

#[test]
fn strip_yaml_header_cases() {
    let with_header = "# c\n---\nname: test\n...\n你好\tni hao\t1\n";
    assert_eq!(strip_yaml_header(with_header), "你好\tni hao\t1\n");

    let with_dashes = "---\nname: test\n---\n你好\tni hao\t1\n";
    assert_eq!(strip_yaml_header(with_dashes), "你好\tni hao\t1\n");

    let plain = "你好\tni hao\t1\n";
    assert_eq!(strip_yaml_header(plain), plain);

    // 只有开头 --- 没有结束标记：整个内容都算头（与 Go 一致）
    assert_eq!(strip_yaml_header("---\nname: test\n"), "");
}

#[test]
fn tsv_header_is_byte_exact() {
    // 首行是字面反斜杠 + t（不是制表符），逐字节与 Go 输出一致。
    assert_eq!(
        TSV_HEADER.as_bytes(),
        "# chaoset 词库（由 chaoset-import 生成，词\\t拼音\\t词频）\n".as_bytes()
    );
    assert_eq!(format_tsv(&[]), TSV_HEADER);
}

#[test]
fn tsv_sort_and_golden_bytes() {
    let entries = vec![
        Entry {
            word: "b".into(),
            syllables: vec!["bi".into()],
            freq: 2,
        },
        Entry {
            word: "a".into(),
            syllables: vec!["a".into()],
            freq: 2,
        },
        Entry {
            word: "a".into(),
            syllables: vec!["a".into(), "b".into()],
            freq: 2,
        },
        Entry {
            word: "c".into(),
            syllables: vec!["ci".into()],
            freq: 1,
        },
    ];
    let expected = format!("{TSV_HEADER}a\ta\t2\na\ta b\t2\nb\tbi\t2\nc\tci\t1\n");
    assert_eq!(format_tsv(&entries), expected);
}

#[test]
fn tsv_sorts_words_by_utf8_bytes() {
    let entries = vec![
        Entry {
            word: "中".into(),
            syllables: vec!["zhong".into()],
            freq: 5,
        },
        Entry {
            word: "a".into(),
            syllables: vec!["a".into()],
            freq: 5,
        },
        Entry {
            word: "Z".into(),
            syllables: vec!["zi".into()],
            freq: 5,
        },
        Entry {
            word: "é".into(),
            syllables: vec!["e".into()],
            freq: 5,
        },
    ];
    let out = format_tsv(&entries);
    let words: Vec<&str> = out
        .lines()
        .skip(1)
        .map(|line| line.split('\t').next().unwrap())
        .collect();
    assert_eq!(words, vec!["Z", "a", "é", "中"], "UTF-8 字节序");
}

#[test]
fn write_tsv_roundtrip() {
    let (entries, _) = parse_auto(RIME_YAML);
    let out = format_tsv(&entries);
    assert!(out.contains("你好\tni hao\t99"), "导出内容不符: {out}");
    let (back, stats) = parse_auto(&out);
    assert_eq!(stats.skipped, 0, "导出的 TSV 应能被重新解析");
    assert_eq!(back.len(), entries.len(), "往返词条数应一致: {back:?}");
    // 写入任意 io::Write 也不出错（Vec 缓冲路线已覆盖，这里锁一下 trait 形态）
    let mut sink: Vec<u8> = Vec::new();
    write_tsv(&mut sink, &entries).unwrap();
    assert_eq!(sink, out.as_bytes());
}
