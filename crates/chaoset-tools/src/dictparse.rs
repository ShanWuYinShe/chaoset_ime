//! 词库格式能力：TSV / Rime dict.yaml / CC-CEDICT 解析、拼音归一化与 TSV 导出。
//!
//! 语义逐条对齐 Go 工具链的 internal/dict（parse.go / pinyin.go / write.go）：
//! 导入器与词库生成共用同一套宽松解析与规范化规则，任何改进都必须两边同步。
//! 音节表不在这里复制，直接用引擎（crates/chaoset-engine）的单一真源。

use std::collections::HashMap;
use std::io::{self, Write};

use chaoset_engine::syllable::is_syllable;

/// 一条词库记录：词、音节序列（ü 写作 v）、词频。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Entry {
    pub word: String,
    pub syllables: Vec<String>,
    pub freq: i32,
}

impl Entry {
    /// 音节串（空格分隔），与 TSV 第二列同口径。
    pub fn syllables_joined(&self) -> String {
        self.syllables.join(" ")
    }
}

/// 宽松解析时写进警告的坏行样本上限（与 Go parseErrSamples 一致）。
pub const PARSE_ERR_SAMPLES: usize = 3;

/// 宽松解析统计：跳过的坏行数与样本（前 PARSE_ERR_SAMPLES 条）。
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ParseStats {
    pub skipped: usize,
    pub samples: Vec<String>,
}

impl ParseStats {
    fn skip(&mut self, line_no: usize, reason: &str) {
        self.skipped += 1;
        if self.samples.len() < PARSE_ERR_SAMPLES {
            self.samples.push(format!("第 {line_no} 行{reason}"));
        }
    }
}

/// CC-CEDICT 词条的合成词频：低于种子词库的常用词频，保证与自有词库合并时
/// 用户精选词条优先（与 Go cedictDefaultFreq 一致）。
pub const CEDICT_DEFAULT_FREQ: i32 = 30;

/// 导出文件首行：逐字保留 Go 的写法（注意列名之间是字面反斜杠 + t，不是制表符，
/// 与 Go fmt.Fprintln 输出的字节完全一致）。
pub const TSV_HEADER: &str = "# chaoset 词库（由 chaoset-import 生成，词\\t拼音\\t词频）\n";

/// 解析 TSV、Rime dict.yaml 或 CC-CEDICT 词库（自动识别）。
///
/// 面向导入场景：宽容模式——无法归一化为合法音节的行跳过并计数，不报错。
pub fn parse_auto(content: &str) -> (Vec<Entry>, ParseStats) {
    if looks_like_cccedict(content) {
        return parse_ccc_edict(content);
    }
    parse_tsv_lenient(&strip_yaml_header(content))
}

/// 依据首个非注释行判断内容是否为 CC-CEDICT u8 格式：
/// 繁体 简体 [拼音] /英文释义/（空格分隔，第三个词以 [ 开头）。
pub fn looks_like_cccedict(content: &str) -> bool {
    for line in content.split('\n') {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let tokens: Vec<&str> = line.split_whitespace().collect();
        return tokens.len() >= 3 && tokens[2].starts_with('[');
    }
    false
}

/// 若内容以 --- 开头（Rime dict.yaml），去掉到第二条 --- 或 ... 为止的 YAML 头。
pub fn strip_yaml_header(content: &str) -> String {
    let lines: Vec<&str> = content.split('\n').collect();
    let mut start = 0;
    while start < lines.len() {
        let trim = lines[start].trim();
        if trim.is_empty() || trim.starts_with('#') {
            start += 1;
            continue;
        }
        break;
    }
    if start >= lines.len() || lines[start].trim() != "---" {
        return content.to_string();
    }
    start += 1;
    while start < lines.len() {
        let trim = lines[start].trim();
        if trim == "---" || trim == "..." {
            return lines[start + 1..].join("\n");
        }
        start += 1;
    }
    String::new()
}

/// 宽松解析「词\t拼音\t词频」TSV：能按严格规则解析的行原样保留，否则尝试
/// 归一化（去声调、ü→v、连写切分），仍失败才跳过并计数。
///
/// 缺省词频 20，上限压缩到 99（用户学习权重远高于静态词频）；同一
/// 「词+音节」重复出现时取较大词频。
fn parse_tsv_lenient(content: &str) -> (Vec<Entry>, ParseStats) {
    let mut stats = ParseStats::default();
    let mut entries: Vec<Entry> = Vec::new();
    let mut seen: HashMap<String, usize> = HashMap::new();

    for (idx, raw) in content.split('\n').enumerate() {
        let line_no = idx + 1;
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let parts: Vec<&str> = line.split('\t').collect();
        if parts.len() < 2 {
            stats.skip(line_no, "缺少制表符分隔（应为「词\t拼音\t词频」）");
            continue;
        }
        let word = parts[0].trim();
        let mut sylls: Vec<String> = parts[1].split_whitespace().map(String::from).collect();
        let mut freq: i64 = 0;
        if parts.len() >= 3 {
            if let Ok(n) = parts[2].trim().parse::<i64>() {
                freq = n;
            }
        }

        let mut canonical = !word.is_empty() && !sylls.is_empty();
        if canonical {
            for syl in &sylls {
                if !is_syllable(syl) {
                    canonical = false;
                    break;
                }
            }
        }
        if !canonical {
            match normalize_pinyin(parts[1]) {
                Some(normalized) => sylls = normalized,
                None => {
                    stats.skip(line_no, "词或拼音无法解析为合法音节");
                    continue;
                }
            }
            if word.is_empty() || sylls.is_empty() {
                stats.skip(line_no, "词或拼音无法解析为合法音节");
                continue;
            }
        }

        let freq = if freq <= 0 {
            20
        } else if freq > 99 {
            99
        } else {
            freq as i32
        };
        let key = format!("{}\u{0}{}", word, sylls.join(" "));
        if let Some(&prev) = seen.get(&key) {
            if freq > entries[prev].freq {
                entries[prev].freq = freq;
            }
            continue;
        }
        seen.insert(key, entries.len());
        entries.push(Entry {
            word: word.to_string(),
            syllables: sylls,
            freq,
        });
    }

    (entries, stats)
}

/// 过滤非纯汉字词条，仅保留 CJK 汉字（含 〇 与兼容区），与 Go cedictWordOK 一致。
fn cedict_word_ok(word: &str) -> bool {
    if word.is_empty() {
        return false;
    }
    word.chars().all(|r| {
        let code = r as u32;
        (0x3400..=0x9FFF).contains(&code) || (0xF900..=0xFAFF).contains(&code) || code == 0x3007
    })
}

/// 解析 CC-CEDICT u8 格式（宽容）：取简体列、拼音剥离方括号与声调数字、
/// u: 归一为 v，词频统一 CEDICT_DEFAULT_FREQ。
fn parse_ccc_edict(content: &str) -> (Vec<Entry>, ParseStats) {
    let mut stats = ParseStats::default();
    let mut entries: Vec<Entry> = Vec::new();
    let mut seen: HashMap<String, usize> = HashMap::new();

    for raw in content.split('\n') {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let Some(bra) = line.find('[') else {
            stats.skipped += 1;
            continue;
        };
        let Some(ket) = line.find(']') else {
            stats.skipped += 1;
            continue;
        };
        if ket <= bra {
            stats.skipped += 1;
            continue;
        }
        let head: Vec<&str> = line[..bra].split_whitespace().collect();
        if head.len() < 2 {
            stats.skipped += 1;
            continue;
        }
        let word = head[1];
        if !cedict_word_ok(word) {
            stats.skipped += 1;
            continue;
        }
        let code = line[bra + 1..ket].replace("u:", "v");
        let Some(sylls) = normalize_pinyin(&code) else {
            stats.skipped += 1;
            continue;
        };
        let key = format!("{}\u{0}{}", word, sylls.join(" "));
        if let Some(&prev) = seen.get(&key) {
            if CEDICT_DEFAULT_FREQ > entries[prev].freq {
                entries[prev].freq = CEDICT_DEFAULT_FREQ;
            }
            continue;
        }
        seen.insert(key, entries.len());
        entries.push(Entry {
            word: word.to_string(),
            syllables: sylls,
            freq: CEDICT_DEFAULT_FREQ,
        });
    }

    (entries, stats)
}

/// 音调符号 → 基础字母（ü 系 → v），覆盖 Rime 词库常见的标调写法。
fn tone_base(c: char) -> Option<char> {
    Some(match c {
        'ā' | 'á' | 'ǎ' | 'à' => 'a',
        'ē' | 'é' | 'ě' | 'è' => 'e',
        'ī' | 'í' | 'ǐ' | 'ì' => 'i',
        'ō' | 'ó' | 'ǒ' | 'ò' => 'o',
        'ū' | 'ú' | 'ǔ' | 'ù' => 'u',
        'ǖ' | 'ǘ' | 'ǚ' | 'ǜ' | 'ü' => 'v',
        _ => return None,
    })
}

/// 处理 Rime 词库里 ü 写作 u 的常见音节（略/虐类）。
fn special_syllable(tok: &str) -> &str {
    match tok {
        "lue" => "lve",
        "nue" => "nve",
        other => other,
    }
}

/// 把拼音编码归一化为音节切片：全小写、去音调符号（ü 系→v）、去音调数字
/// （1-5，空格分隔与连写都要支持）；任一首节无法归一化时返回 None。
pub fn normalize_pinyin(code: &str) -> Option<Vec<String>> {
    let code = code.replace('\'', " ");
    let mut sylls: Vec<String> = Vec::new();
    for tok in code.split_whitespace() {
        let lower = tok.to_lowercase();
        let mut cleaned = String::with_capacity(lower.len());
        for c in lower.chars() {
            if let Some(base) = tone_base(c) {
                cleaned.push(base);
            } else if ('1'..='5').contains(&c) {
                continue;
            } else {
                cleaned.push(c);
            }
        }
        let cleaned = special_syllable(&cleaned);
        if is_syllable(cleaned) {
            sylls.push(cleaned.to_string());
            continue;
        }
        // 别人编辑/导出的词库常见「连写拼音」（hundun、shurufa）：整串能切成
        // 合法音节就自动切分，切不干净才按无效行跳过。
        let parts = segment_runon(cleaned)?;
        sylls.extend(parts);
    }
    Some(sylls)
}

/// 把连写拼音整串切成合法音节（hundunshurufa → hun dun shu ru fa）。
/// 仅当整串都能切完才返回，否则 None；切法取「最长首音节优先」。
fn segment_runon(s: &str) -> Option<Vec<String>> {
    const MAX_RUNON: usize = 64; // 异常长串直接放弃，避免 O(n²) 退化
    if s.is_empty() || s.len() > MAX_RUNON || !s.is_ascii() {
        return None;
    }
    let n = s.len();
    let mut dp: Vec<Option<Vec<String>>> = vec![None; n + 1];
    dp[0] = Some(Vec::new());
    for i in 1..=n {
        for j in 0..i {
            if dp[j].is_none() {
                continue;
            }
            if is_syllable(&s[j..i]) {
                let mut parts = dp[j].clone().unwrap_or_default();
                parts.push(s[j..i].to_string());
                dp[i] = Some(parts);
                break; // j 递增 → 首音节最长者优先
            }
        }
    }
    dp[n].take()
}

/// 按导出契约排序：词频降序 → 词 UTF-8 字节序升序 → 音节串字节序升序。
/// 第三级保证同词多音（多音字）也是全序，同样的输入在不同路径下产出逐字节一致。
fn sorted_entries(entries: &[Entry]) -> Vec<&Entry> {
    let mut sorted: Vec<&Entry> = entries.iter().collect();
    sorted.sort_by(|a, b| {
        b.freq
            .cmp(&a.freq)
            .then_with(|| a.word.as_bytes().cmp(b.word.as_bytes()))
            .then_with(|| {
                a.syllables_joined()
                    .as_bytes()
                    .cmp(b.syllables_joined().as_bytes())
            })
    });
    sorted
}

/// 把词条导出为 TSV 写入 writer（首行固定注释头）。
pub fn write_tsv<W: Write + ?Sized>(writer: &mut W, entries: &[Entry]) -> io::Result<()> {
    writer.write_all(TSV_HEADER.as_bytes())?;
    for entry in sorted_entries(entries) {
        writeln!(
            writer,
            "{}\t{}\t{}",
            entry.word,
            entry.syllables_joined(),
            entry.freq
        )?;
    }
    Ok(())
}

/// 把词条导出为 TSV 字符串（write_tsv 的无 I/O 版本，测试/金标用）。
pub fn format_tsv(entries: &[Entry]) -> String {
    let mut buf: Vec<u8> = Vec::new();
    write_tsv(&mut buf, entries).expect("写入 Vec 不会失败");
    String::from_utf8(buf).expect("TSV 内容应为 UTF-8")
}
