//! 用户自学习词典、动态调频、上下文 Bigram 共现与词组进化。

use std::collections::HashMap;
use std::fs::{self, File};
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};
use std::sync::RwLock;
use std::time::{SystemTime, UNIX_EPOCH};

const USER_BOOST_PER_COUNT: f64 = 500.0;
const BIGRAM_BONUS_PER_COUNT: f64 = 250.0;
const BIGRAM_MAX_COUNT: i32 = 2;

fn current_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

fn decay(last_used: i64, now: i64) -> f64 {
    let diff_secs = now.saturating_sub(last_used);
    let days = diff_secs as f64 / 86400.0;
    if days < 1.0 {
        1.0
    } else if days < 7.0 {
        0.6
    } else if days < 30.0 {
        0.3
    } else {
        0.1
    }
}

/// Unix 秒 → UTC RFC3339（2026-09-24T12:00:00Z）。
///
/// 学习数据导出的时间列统一用 UTC（README：全量导出「按次数降序、UTC 时间，
/// 输出稳定可 diff」）。不引入 chrono/time 依赖，用 Howard Hinnant 的
/// civil_from_days 算法从 epoch 天数换算公历，负时间戳同样可表示。
pub fn format_rfc3339_utc(unix: i64) -> String {
    let (year, month, day, hour, minute, second) = utc_fields(unix);
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z",
        year, month, day, hour, minute, second
    )
}

/// Unix 秒 → UTC 文件名时间戳（YYYYMMDD-HHMMSS，损坏备份后缀用）。
fn format_stamp_utc(unix: i64) -> String {
    let (year, month, day, hour, minute, second) = utc_fields(unix);
    format!(
        "{:04}{:02}{:02}-{:02}{:02}{:02}",
        year, month, day, hour, minute, second
    )
}

fn utc_fields(unix: i64) -> (i64, u32, u32, u32, u32, u32) {
    let days = unix.div_euclid(86400);
    let secs_of_day = unix.rem_euclid(86400) as u32;
    let (year, month, day) = civil_from_days(days);
    (
        year,
        month,
        day,
        secs_of_day / 3600,
        (secs_of_day % 3600) / 60,
        secs_of_day % 60,
    )
}

/// Howard Hinnant 的 civil_from_days（proleptic Gregorian，1970-01-01 起）。
fn civil_from_days(days: i64) -> (i64, u32, u32) {
    let z = days + 719_468;
    let era = (if z >= 0 { z } else { z - 146_096 }) / 146_097;
    let doe = (z - era * 146_097) as u64; // [0, 146096]
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365; // [0, 399]
    let year = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100); // [0, 365]
    let mp = (5 * doy + 2) / 153; // [0, 11]
    let day = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let month = (if mp < 10 { mp + 3 } else { mp - 9 }) as u32;
    (if month <= 2 { year + 1 } else { year }, month, day)
}

/// 损坏用户词典的备份路径：<path>.corrupt-<UTC 时间戳>。带秒级时间戳，
/// 既避免覆盖现场，也便于按时间定位；同一秒内重复恢复会改名失败并降级为空词典。
fn corrupt_backup_path(path: &Path) -> PathBuf {
    let mut name = path.as_os_str().to_os_string();
    name.push(format!(
        ".corrupt-{}",
        format_stamp_utc(current_timestamp())
    ));
    PathBuf::from(name)
}

/// 解析用户词典 JSON；兼容旧格式，返回（数据, 是否发生迁移）。
///
/// 新格式（Rust canonical）：words/phrases/bigrams 三张表，words 以词为键、
/// 音节与整句分段在记录里。旧格式有两种，都迁移到 canonical：
///   1. 阶段 0 扁平格式：顶层就是「词\0音节」→ 记录；
///   2. Go daemon 的新格式：words/phrases 键是「词\0音节」、bigrams 键是
///      「前词\0后词」。
fn decode_user_file(data: &[u8]) -> Result<(UserFile, bool), String> {
    let value: serde_json::Value =
        serde_json::from_slice(data).map_err(|e| format!("JSON 解析失败: {e}"))?;
    let obj = value
        .as_object()
        .ok_or_else(|| "用户词典顶层不是 JSON 对象".to_string())?;
    if obj.contains_key("words") || obj.contains_key("phrases") || obj.contains_key("bigrams") {
        let mut file: UserFile =
            serde_json::from_value(value).map_err(|e| format!("格式不匹配: {e}"))?;
        let migrated = migrate_canonical_keys(&mut file);
        Ok((file, migrated))
    } else {
        let legacy: HashMap<String, UserRecord> =
            serde_json::from_value(value).map_err(|e| format!("格式不匹配: {e}"))?;
        let mut file = UserFile::default();
        for (key, mut rec) in legacy {
            let (word, sylls) = split_flat_key(&key);
            if word.is_empty() {
                continue;
            }
            if rec.sylls.is_empty() {
                rec.sylls = sylls;
            }
            merge_word_record(&mut file.words, word, rec);
        }
        Ok((file, true))
    }
}

/// 把「键=词\0空格音节」拆成（词, 空格分隔音节串）；无分隔符时整键为词。
fn split_flat_key(key: &str) -> (String, String) {
    match key.split_once('\u{0}') {
        Some((word, joined)) => (
            word.trim().to_string(),
            joined.split_whitespace().collect::<Vec<_>>().join(" "),
        ),
        None => (key.trim().to_string(), String::new()),
    }
}

/// 合并同词记录：旧格式同一词可能有多读音/多条记录，次数相加、最近使用取较新、
/// 保留非空的音节与分段——迁移不能把学习数据整条丢掉。
fn merge_word_record(map: &mut HashMap<String, UserRecord>, word: String, incoming: UserRecord) {
    match map.get_mut(&word) {
        Some(existing) => {
            existing.count = existing.count.saturating_add(incoming.count);
            if incoming.last_used > existing.last_used {
                existing.last_used = incoming.last_used;
            }
            if existing.sylls.is_empty() {
                existing.sylls = incoming.sylls;
            }
            if existing.segs.is_empty() {
                existing.segs = incoming.segs;
            }
        }
        None => {
            map.insert(word, incoming);
        }
    }
}

fn merge_phrase_record(map: &mut HashMap<String, PhraseRecord>, incoming: PhraseRecord) {
    let word = incoming.word.clone();
    match map.get_mut(&word) {
        Some(existing) => {
            existing.count = existing.count.saturating_add(incoming.count);
            if incoming.last_used > existing.last_used {
                existing.last_used = incoming.last_used;
            }
            if existing.sylls.is_empty() {
                existing.sylls = incoming.sylls;
            }
            if existing.segs.is_empty() {
                existing.segs = incoming.segs;
            }
        }
        None => {
            map.insert(word, incoming);
        }
    }
}

/// canonical 文件里也可能残留 Go daemon 写下的「词\0音节」键：逐表把键迁到
/// Rust canonical（words/phrases 以词为键、bigrams 以「前词->后词」为键）。
/// 返回是否改写过键（写过就要落盘升级）。
fn migrate_canonical_keys(file: &mut UserFile) -> bool {
    let mut migrated = false;

    let old_words = std::mem::take(&mut file.words);
    let mut words = HashMap::with_capacity(old_words.len());
    for (key, rec) in old_words {
        if key.contains('\u{0}') {
            migrated = true;
            let (word, sylls) = split_flat_key(&key);
            if word.is_empty() {
                continue;
            }
            let mut rec = rec;
            if rec.sylls.is_empty() {
                rec.sylls = sylls;
            }
            merge_word_record(&mut words, word, rec);
        } else {
            words.insert(key, rec);
        }
    }
    file.words = words;

    let old_phrases = std::mem::take(&mut file.phrases);
    let mut phrases = HashMap::with_capacity(old_phrases.len());
    for (key, mut rec) in old_phrases {
        if key.contains('\u{0}') {
            migrated = true;
            let (word, sylls) = split_flat_key(&key);
            if rec.word.is_empty() {
                rec.word = word;
            }
            if rec.sylls.is_empty() {
                rec.sylls = sylls;
            }
        }
        if rec.word.is_empty() {
            continue;
        }
        merge_phrase_record(&mut phrases, rec);
    }
    file.phrases = phrases;

    let old_bigrams = std::mem::take(&mut file.bigrams);
    let mut bigrams: HashMap<String, UserRecord> = HashMap::with_capacity(old_bigrams.len());
    for (key, rec) in old_bigrams {
        let key = match key.split_once('\u{0}') {
            Some((prev, next)) => {
                migrated = true;
                format!("{}->{}", prev, next)
            }
            None => key,
        };
        match bigrams.get_mut(&key) {
            Some(existing) => {
                existing.count = existing.count.saturating_add(rec.count);
                existing.last_used = existing.last_used.max(rec.last_used);
            }
            None => {
                bigrams.insert(key, rec);
            }
        }
    }
    file.bigrams = bigrams;

    migrated
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct UserRecord {
    pub count: i32,
    pub last_used: i64,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub segs: Vec<String>,
    /// 音节串（空格分隔）：联想按字前缀剥前缀时需要知道词条的音节切分。
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub sylls: String,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct PhraseRecord {
    pub word: String,
    pub sylls: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub segs: Vec<String>,
    pub count: i32,
    pub last_used: i64,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
struct UserFile {
    #[serde(default)]
    pub words: HashMap<String, UserRecord>,
    #[serde(default)]
    pub phrases: HashMap<String, PhraseRecord>,
    #[serde(default)]
    pub bigrams: HashMap<String, UserRecord>,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct UserStats {
    pub words: usize,
    pub phrases: usize,
    pub bigrams: usize,
}

/// 字前缀查询命中（联想续接用）：Go 的 *Entry + Segs 口径。
/// 词条音节串解析：用户记录可能来自 Go（空格分隔）或 Rust 学习路径
/// （' 连接），两种都按统一口径切开，联想剥前缀才能按音节数对齐。
fn split_sylls(s: &str) -> Vec<String> {
    s.split(|c: char| c == '\'' || c.is_whitespace())
        .filter(|x| !x.is_empty())
        .map(String::from)
        .collect()
}

/// 音节串导出成统一空格分隔（daemon 学习路径可能存 ' 连接；导出与再导入
/// 都按空格口径，chaoset-import 也能直接吃）。
fn format_sylls(s: &str) -> String {
    split_sylls(s).join(" ")
}

pub struct WordPrefixHit {
    pub word: String,
    pub syllables: Vec<String>,
    pub freq: i32,
    pub segs: Vec<String>,
}

impl WordPrefixHit {
    /// 与 Go 同序：调频分降序 → 词字典序 → 音节串（多读音同词时确定性）。
    fn sort(v: &mut [WordPrefixHit]) {
        v.sort_by(|a, b| {
            b.freq
                .cmp(&a.freq)
                .then(a.word.cmp(&b.word))
                .then(a.syllables.join("'").cmp(&b.syllables.join("'")))
        });
    }
}

pub struct UserDict {
    path: Option<PathBuf>,
    data: RwLock<UserFile>,
    dirty: RwLock<bool>,
}

impl UserDict {
    /// 用户词典文件路径（None = 内存态，不落盘）。
    pub fn path(&self) -> Option<&Path> {
        self.path.as_deref()
    }

    /// 宽松加载用户词典（daemon / CLI 本地模式的启动路径）。
    ///
    /// - 文件不存在：空词典，无警告；
    /// - 内容无法解析：改名 <path>.corrupt-<UTC 时间戳> 保留现场，以空词典继续，
    ///   并把警告文本交还调用方——学习数据是增量优化，绝不能因为它让输入法起不来；
    /// - 读取失败（权限不足、路径是目录等）：返回 Err——改名解决不了这类问题；
    /// - 旧格式（阶段 0 扁平表 / Go daemon 的「词\0音节」键）：迁移到 canonical，
    ///   迁移会标脏，下一次 save/flush 顺带把文件升级成新格式。
    pub fn open<P: AsRef<Path>>(path_opt: Option<P>) -> std::io::Result<(Self, Vec<String>)> {
        let path = path_opt.map(|p| p.as_ref().to_path_buf());
        let mut warnings = Vec::new();
        let mut file_data = UserFile::default();
        let mut dirty = false;
        if let Some(ref p) = path {
            match fs::read(p) {
                Ok(bytes) => match decode_user_file(&bytes) {
                    Ok((parsed, migrated)) => {
                        file_data = parsed;
                        dirty = migrated;
                    }
                    Err(err) => {
                        let backup = corrupt_backup_path(p);
                        let warning = match fs::rename(p, &backup) {
                            Ok(()) => format!(
                                "用户词典 {} 解析失败（{}），已备份为 {}；本次以空词典启动",
                                p.display(),
                                err,
                                backup.display()
                            ),
                            Err(rename_err) => format!(
                                "用户词典 {} 解析失败（{}）；备份改名也失败（{}），本次以空词典启动",
                                p.display(),
                                err,
                                rename_err
                            ),
                        };
                        warnings.push(warning);
                        file_data = UserFile::default();
                        dirty = false;
                    }
                },
                Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
                Err(err) => {
                    return Err(std::io::Error::new(
                        err.kind(),
                        format!("读取用户词典失败: {err}"),
                    ))
                }
            }
        }
        Ok((
            Self {
                path,
                data: RwLock::new(file_data),
                dirty: RwLock::new(dirty),
            },
            warnings,
        ))
    }

    /// 兼容入口（daemon 等常驻调用方）：加载失败一律降级为空词典，警告打印到
    /// stderr。需要区分「读取失败」的调用方用 UserDict::open。
    pub fn new<P: AsRef<Path>>(path_opt: Option<P>) -> Self {
        match Self::open(path_opt) {
            Ok((dict, warnings)) => {
                for warning in warnings {
                    eprintln!("警告: {warning}");
                }
                dict
            }
            Err(err) => {
                eprintln!("警告: {err}；本次以空词典启动");
                Self::in_memory()
            }
        }
    }

    /// 纯内存态（不落盘）。
    fn in_memory() -> Self {
        Self {
            path: None,
            data: RwLock::new(UserFile::default()),
            dirty: RwLock::new(false),
        }
    }

    /// 是否有尚未落盘的变更（迁移、删除与 bump 都会标脏）。
    pub fn is_dirty(&self) -> bool {
        *self.dirty.read().unwrap()
    }

    /// 计算候选词在当前上下文下的附加用户加分 (分值, 是否来自用户学习)
    pub fn score(&self, word: &str, prev_word: Option<&str>) -> (u32, bool) {
        let data = self.data.read().unwrap();
        let now = current_timestamp();
        let mut total_bonus = 0.0;
        let mut from_user = false;

        // 1. 静态词调频 / 用户词
        if let Some(rec) = data.words.get(word) {
            let factor = decay(rec.last_used, now);
            total_bonus += (rec.count as f64) * USER_BOOST_PER_COUNT * factor;
            from_user = true;
        }

        // 3. 上下文 Bigram 共现加分
        if let Some(prev) = prev_word {
            let bigram_key = format!("{}->{}", prev, word);
            if let Some(rec) = data.bigrams.get(&bigram_key) {
                let factor = decay(rec.last_used, now);
                let capped_count = rec.count.min(BIGRAM_MAX_COUNT);
                total_bonus += (capped_count as f64) * BIGRAM_BONUS_PER_COUNT * factor;
                from_user = true;
            }
        }

        (total_bonus.round() as u32, from_user)
    }

    /// 按「拼音连写 == 输入」匹配用户学习记录（Go UserDict.QueryJoinedPinyin）：
    /// Words 调频词与 Phrases 词组进化两张表都查，同一词去重后按调频分降序。
    pub fn query_joined_pinyin(&self, input: &str, max_results: usize) -> Vec<WordPrefixHit> {
        if input.is_empty() {
            return Vec::new();
        }
        let limit = if max_results == 0 { 20 } else { max_results };
        let data = self.data.read().unwrap();
        let now = current_timestamp();
        let mut out: Vec<WordPrefixHit> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        let mut add = |word: &str, sylls: Vec<String>, freq: i32, segs: Vec<String>| {
            if sylls.is_empty() || sylls.join("") != input {
                return;
            }
            let key = format!("{}|{}", word, sylls.join(" "));
            if !seen.insert(key) {
                return;
            }
            out.push(WordPrefixHit {
                word: word.to_string(),
                syllables: sylls,
                freq,
                segs,
            });
        };
        for (word, rec) in data.words.iter() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            add(word, split_sylls(&rec.sylls), freq, rec.segs.clone());
        }
        for rec in data.phrases.values() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            add(&rec.word, split_sylls(&rec.sylls), freq, rec.segs.clone());
        }
        WordPrefixHit::sort(&mut out);
        out.truncate(limit);
        out
    }

    /// 按**词字面**前缀查词组进化记录（Go QueryPhrasesByWordPrefix）。
    pub fn query_phrases_by_word_prefix(&self, prefix: &str) -> Vec<WordPrefixHit> {
        let data = self.data.read().unwrap();
        if prefix.is_empty() {
            return Vec::new();
        }
        let now = current_timestamp();
        let prefix_len = prefix.chars().count();
        let mut out: Vec<WordPrefixHit> = data
            .phrases
            .values()
            .filter(|rec| rec.word.chars().count() > prefix_len && rec.word.starts_with(prefix))
            .map(|rec| WordPrefixHit {
                word: rec.word.clone(),
                syllables: split_sylls(&rec.sylls),
                freq: ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now))
                    as i32,
                segs: rec.segs.clone(),
            })
            .collect();
        WordPrefixHit::sort(&mut out);
        out
    }

    /// 按**词字面**前缀查用户调频词条（Go QueryWordsByWordPrefix）：用户整句
    /// 上屏/选词的记录都在 Words 表，是「之前输入过的词」的主体——联想续接
    /// 按上屏词的字走时必须查它，否则用户打过的词不会出现在联想候选。
    pub fn query_words_by_word_prefix(&self, prefix: &str) -> Vec<WordPrefixHit> {
        let data = self.data.read().unwrap();
        if prefix.is_empty() {
            return Vec::new();
        }
        let now = current_timestamp();
        let prefix_len = prefix.chars().count();
        let mut out: Vec<WordPrefixHit> = data
            .words
            .iter()
            .filter(|(word, _)| word.chars().count() > prefix_len && word.starts_with(prefix))
            .map(|(word, rec)| WordPrefixHit {
                word: word.clone(),
                syllables: split_sylls(&rec.sylls),
                freq: ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now))
                    as i32,
                segs: rec.segs.clone(),
            })
            .collect();
        WordPrefixHit::sort(&mut out);
        out
    }

    /// 联想上下文加成（Go UserDict.Score）：只含 Words 调频 + Bigram 共现，
    /// **不含** Phrases——与组词打分用的 score() 口径不同，不要混用。
    pub fn score_full(&self, word: &str, prev: Option<&str>) -> i32 {
        let data = self.data.read().unwrap();
        let now = current_timestamp();
        let mut score = 0i32;
        if let Some(rec) = data.words.get(word) {
            score = ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
        }
        if let Some(prev) = prev {
            let key = format!("{}->{}", prev, word);
            if let Some(rec) = data.bigrams.get(&key) {
                let n = rec.count.min(BIGRAM_MAX_COUNT);
                score += n * BIGRAM_BONUS_PER_COUNT as i32;
            }
        }
        score
    }

    /// 用户选择词上屏，累加调频与上下文共现
    pub fn bump_word(&self, word: &str, sylls: Option<&str>, segs: Option<Vec<String>>) {
        let mut data = self.data.write().unwrap();
        let now = current_timestamp();
        let entry = data.words.entry(word.to_string()).or_default();
        entry.count += 1;
        entry.last_used = now;
        if let Some(s) = sylls {
            entry.sylls = s.to_string();
        }
        if let Some(s) = segs {
            entry.segs = s;
        }
        *self.dirty.write().unwrap() = true;
    }

    pub fn bump_bigram(&self, prev_word: &str, word: &str) {
        let mut data = self.data.write().unwrap();
        let now = current_timestamp();
        let key = format!("{}->{}", prev_word, word);
        let entry = data.bigrams.entry(key).or_default();
        entry.count += 1;
        entry.last_used = now;
        *self.dirty.write().unwrap() = true;
    }

    pub fn bump_phrase(&self, word: &str, sylls: &str, segs: Option<Vec<String>>) {
        let mut data = self.data.write().unwrap();
        let now = current_timestamp();
        let entry = data.phrases.entry(word.to_string()).or_default();
        entry.word = word.to_string();
        entry.sylls = sylls.to_string();
        entry.count += 1;
        entry.last_used = now;
        if let Some(s) = segs {
            entry.segs = s;
        }
        *self.dirty.write().unwrap() = true;
    }

    /// 删除一个词的学习记录（Words + Phrases），返回是否真的删到了记录。
    ///
    /// 匹配规则（与 Go 引擎同语义）：
    ///   - 给了音节：先按「词 + 音节」精确匹配 Words/Phrases；
    ///   - 精确匹配完全没命中：按「词文本」兜底删除同词的所有读音/分词记录
    ///     （CLI 不带音节、右键删除语义是「把这个词从学习数据里删掉」）；
    ///   - 什么都不匹配：返回 false，不产生任何变更。
    ///
    /// Bigrams（前后词共现）刻意保留：它是「前词→后词」的成对统计而不是单词
    /// 自身的学习记录，删单侧会扭曲其它词的上下文加成；保留最多让该词在特定
    /// 前文后仍有不超过 2 次共现的加分，下次使用会重新学习。
    pub fn delete_word(&self, word: &str, syllables: &[String]) -> bool {
        if word.is_empty() {
            return false;
        }
        let mut data = self.data.write().unwrap();
        let mut deleted = false;

        if !syllables.is_empty() {
            if let Some(rec) = data.words.get(word) {
                if split_sylls(&rec.sylls).as_slice() == syllables {
                    data.words.remove(word);
                    deleted = true;
                }
            }
            if let Some(rec) = data.phrases.get(word) {
                if split_sylls(&rec.sylls).as_slice() == syllables {
                    data.phrases.remove(word);
                    deleted = true;
                }
            }
        }

        if !deleted {
            if data.words.remove(word).is_some() {
                deleted = true;
            }
            if data.phrases.remove(word).is_some() {
                deleted = true;
            }
        }

        // 只有真删到记录才标脏：重复删除 / 未学习过不能触发落盘。
        if deleted {
            *self.dirty.write().unwrap() = true;
        }
        deleted
    }

    pub fn stats(&self) -> UserStats {
        let data = self.data.read().unwrap();
        UserStats {
            words: data.words.len(),
            phrases: data.phrases.len(),
            bigrams: data.bigrams.len(),
        }
    }

    pub fn clear(&self) {
        let mut data = self.data.write().unwrap();
        data.words.clear();
        data.phrases.clear();
        data.bigrams.clear();
        *self.dirty.write().unwrap() = true;
    }

    pub fn flush_if_dirty(&self) -> std::io::Result<bool> {
        let is_dirty = *self.dirty.read().unwrap();
        if !is_dirty {
            return Ok(false);
        }
        self.save()?;
        Ok(true)
    }

    /// 把学习记录导出为多段 TSV（# 开头为注释段头）：
    /// 词 / 用户词组 / 上下文共现；条目按次数降序、同次数按词排序，时间列用
    /// UTC RFC3339，输出稳定可 diff；空段省略。
    ///
    /// 拼音列取记录里的 syllables（不是整句分段 segs）。phrases_only 只导出
    /// 用户词组，且为无段头的「词\t拼音\t次数」三列，可直接作为
    /// chaoset-import 的输入词库。
    pub fn export_tsv(&self, phrases_only: bool) -> String {
        let data = self.data.read().unwrap();
        let mut out = String::new();

        if !phrases_only && !data.words.is_empty() {
            out.push_str("# 词\t拼音\t次数\t最近使用\n");
            let mut rows: Vec<(&String, &UserRecord)> = data.words.iter().collect();
            rows.sort_by(|a, b| b.1.count.cmp(&a.1.count).then_with(|| a.0.cmp(b.0)));
            for (word, rec) in rows {
                out.push_str(&format!(
                    "{}\t{}\t{}\t{}\n",
                    word,
                    format_sylls(&rec.sylls),
                    rec.count,
                    format_rfc3339_utc(rec.last_used)
                ));
            }
        }

        if !data.phrases.is_empty() {
            if !phrases_only {
                out.push_str("# 词组\t拼音\t次数\t最近使用\n");
            }
            let mut rows: Vec<&PhraseRecord> = data.phrases.values().collect();
            rows.sort_by(|a, b| b.count.cmp(&a.count).then_with(|| a.word.cmp(&b.word)));
            for rec in rows {
                if phrases_only {
                    out.push_str(&format!(
                        "{}\t{}\t{}\n",
                        rec.word,
                        format_sylls(&rec.sylls),
                        rec.count
                    ));
                } else {
                    out.push_str(&format!(
                        "{}\t{}\t{}\t{}\n",
                        rec.word,
                        format_sylls(&rec.sylls),
                        rec.count,
                        format_rfc3339_utc(rec.last_used)
                    ));
                }
            }
        }

        if !phrases_only && !data.bigrams.is_empty() {
            out.push_str("# 上下文\t前词\t后词\t次数\n");
            let mut rows: Vec<(&str, &str, i32)> = data
                .bigrams
                .iter()
                .filter_map(|(key, rec)| {
                    key.split_once("->")
                        .map(|(prev, next)| (prev, next, rec.count))
                })
                .collect();
            rows.sort_by(|a, b| {
                b.2.cmp(&a.2)
                    .then_with(|| a.0.as_bytes().cmp(b.0.as_bytes()))
                    .then_with(|| a.1.as_bytes().cmp(b.1.as_bytes()))
            });
            for (prev, next, count) in rows {
                out.push_str(&format!("{}\t{}\t{}\n", prev, next, count));
            }
        }

        out
    }

    pub fn save(&self) -> std::io::Result<()> {
        let path = match self.path {
            Some(ref p) => p,
            None => return Ok(()),
        };

        let is_dirty = *self.dirty.read().unwrap();
        if !is_dirty {
            return Ok(());
        }

        let snapshot = {
            let data = self.data.read().unwrap();
            data.clone()
        };

        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }

        let temp_path = path.with_extension("tmp");
        {
            let file = File::create(&temp_path)?;
            let mut writer = BufWriter::new(file);
            serde_json::to_writer_pretty(&mut writer, &snapshot)?;
            writer.flush()?;
        }

        fs::rename(temp_path, path)?;
        *self.dirty.write().unwrap() = false;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_userdict_score_and_decay() {
        let udict = UserDict::new::<&str>(None);
        let (initial_score, from_user) = udict.score("测试", None);
        assert_eq!(initial_score, 0);
        assert!(!from_user);

        udict.bump_word("测试", Some("ce'shi"), None);
        let (bumped_score, from_user) = udict.score("测试", None);
        assert!(bumped_score >= 500);
        assert!(from_user);

        // Bigram 上下文加分
        udict.bump_bigram("系统", "测试");
        let (bigram_score, from_user) = udict.score("测试", Some("系统"));
        assert!(bigram_score > bumped_score);
        assert!(from_user);

        // Export TSV
        let tsv = udict.export_tsv(false);
        assert!(tsv.contains("测试"));
        assert!(tsv.contains("系统\t测试"));

        // Delete word
        assert!(udict.delete_word("测试", &[]));
        let (after_del_score, from_user_after) = udict.score("测试", None);
        assert_eq!(after_del_score, 0);
        assert!(!from_user_after);
    }
}

/// 用户词库 token 查询命中（Go userdict *Entry 的 Rust 版）：带分段供整句
/// 安全判断与联想剥前缀使用。
#[derive(Debug, Clone)]
pub struct TokenHit {
    pub word: String,
    pub syllables: Vec<String>,
    pub freq: i32,
    pub segs: Vec<String>,
}

/// 单个音节与 token 的匹配（Go matchSyllable）：完整音节等值；碎片 token
/// 前缀匹配，且 z/c/s 兼容 zh/ch/sh。
fn token_matches(syl: &str, tok: &str, exact: bool) -> bool {
    if exact {
        return syl == tok;
    }
    if syl.starts_with(tok) {
        return true;
    }
    matches!((tok, syl), ("z", "zh") | ("c", "ch") | ("s", "sh"))
        || (tok == "z" && syl.starts_with("zh"))
        || (tok == "c" && syl.starts_with("ch"))
        || (tok == "s" && syl.starts_with("sh"))
}

/// 用户词库查询结果的统一排序（Go Query* 的 sortSlice）：词频降序 → 词长
/// 升序 → 词字典序 → 音节串；随后按词去重（保留排序后第一条）并截断。
fn sort_and_dedupe_hits(hits: &mut Vec<TokenHit>, limit: usize) {
    hits.sort_by(|a, b| {
        b.freq
            .cmp(&a.freq)
            .then(a.word.chars().count().cmp(&b.word.chars().count()))
            .then(a.word.cmp(&b.word))
            .then(a.syllables.join("'").cmp(&b.syllables.join("'")))
    });
    let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
    hits.retain(|h| seen.insert(h.word.clone()));
    hits.truncate(limit);
}

impl UserDict {
    /// 用户词库 token 精确查询（Go QueryTokensExact）：音节数恰好等于 tokens
    /// 且逐音节匹配；词组进化（Phrases）与调频词（Words）两张表都查。
    pub fn query_tokens_exact(&self, tokens: &[(&str, bool)], max_results: usize) -> Vec<TokenHit> {
        let limit = if max_results == 0 { 20 } else { max_results };
        let data = self.data.read().unwrap();
        let now = current_timestamp();
        let mut out: Vec<TokenHit> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        let consider = |word: &str,
                        sylls: Vec<String>,
                        freq: i32,
                        segs: Vec<String>,
                        out: &mut Vec<TokenHit>,
                        seen: &mut std::collections::HashSet<String>| {
            if sylls.len() != tokens.len() {
                return;
            }
            for (i, (tok, exact)) in tokens.iter().enumerate() {
                if !token_matches(&sylls[i], tok, *exact) {
                    return;
                }
            }
            let key = format!("{}\u{0}{}", word, sylls.join(" "));
            if !seen.insert(key) {
                return;
            }
            out.push(TokenHit {
                word: word.to_string(),
                syllables: sylls,
                freq,
                segs,
            });
        };
        for rec in data.phrases.values() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            consider(
                &rec.word,
                split_sylls(&rec.sylls),
                freq,
                rec.segs.clone(),
                &mut out,
                &mut seen,
            );
        }
        for (word, rec) in data.words.iter() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            consider(
                word,
                split_sylls(&rec.sylls),
                freq,
                rec.segs.clone(),
                &mut out,
                &mut seen,
            );
        }
        sort_and_dedupe_hits(&mut out, limit);
        out
    }

    /// 用户词库 token 查询（Go QueryTokens）：exact（音节数相等）与 prefix
    /// （以 tokens 为音节前缀、词条更长）两池，各自排序去重截断。
    pub fn query_tokens(
        &self,
        tokens: &[(&str, bool)],
        max_results: usize,
    ) -> (Vec<TokenHit>, Vec<TokenHit>) {
        let limit = if max_results == 0 { 20 } else { max_results };
        let data = self.data.read().unwrap();
        let now = current_timestamp();
        let mut exacts: Vec<TokenHit> = Vec::new();
        let mut prefixes: Vec<TokenHit> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        let mut consider = |word: &str, sylls: Vec<String>, freq: i32, segs: Vec<String>| {
            if sylls.len() < tokens.len() {
                return;
            }
            for (i, (tok, exact)) in tokens.iter().enumerate() {
                if !token_matches(&sylls[i], tok, *exact) {
                    return;
                }
            }
            let key = format!("{}\u{0}{}", word, sylls.join(" "));
            if !seen.insert(key) {
                return;
            }
            let hit = TokenHit {
                word: word.to_string(),
                syllables: sylls.clone(),
                freq,
                segs,
            };
            if sylls.len() == tokens.len() {
                exacts.push(hit);
            } else {
                prefixes.push(hit);
            }
        };
        for rec in data.phrases.values() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            consider(&rec.word, split_sylls(&rec.sylls), freq, rec.segs.clone());
        }
        for (word, rec) in data.words.iter() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            consider(word, split_sylls(&rec.sylls), freq, rec.segs.clone());
        }
        sort_and_dedupe_hits(&mut exacts, limit);
        sort_and_dedupe_hits(&mut prefixes, limit);
        (exacts, prefixes)
    }

    /// 用户词库末音节续接（Go QueryContinuation）：tokens 是前面已完整的音节
    /// 约束、frag 是末段尚未打完的串；命中「tokens + 以 frag 为前缀的音节」
    /// 开头的词条，返回 (词条, 需补全的剩余字符)。
    pub fn query_continuation(
        &self,
        tokens: &[(&str, bool)],
        frag: &str,
        max_results: usize,
    ) -> Vec<(TokenHit, String)> {
        if frag.is_empty() || tokens.is_empty() {
            return Vec::new();
        }
        let limit = if max_results == 0 { 8 } else { max_results };
        let data = self.data.read().unwrap();
        let now = current_timestamp();
        let mut out: Vec<(TokenHit, String)> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        let mut consider = |word: &str, sylls: Vec<String>, segs: Vec<String>, score: i32| {
            if sylls.len() < tokens.len() + 1 {
                return;
            }
            for (i, (tok, exact)) in tokens.iter().enumerate() {
                if !token_matches(&sylls[i], tok, *exact) {
                    return;
                }
            }
            let next = &sylls[tokens.len()];
            let Some(remain) = next.strip_prefix(frag) else {
                return;
            };
            let key = format!("{}\u{0}{}", word, sylls.join(" "));
            if !seen.insert(key) {
                return;
            }
            out.push((
                TokenHit {
                    word: word.to_string(),
                    syllables: sylls.clone(),
                    freq: score,
                    segs,
                },
                remain.to_string(),
            ));
        };
        for rec in data.phrases.values() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            consider(&rec.word, split_sylls(&rec.sylls), rec.segs.clone(), freq);
        }
        for (word, rec) in data.words.iter() {
            let freq =
                ((rec.count as f64 * USER_BOOST_PER_COUNT) * decay(rec.last_used, now)) as i32;
            consider(word, split_sylls(&rec.sylls), rec.segs.clone(), freq);
        }
        out.sort_by(|a, b| {
            a.1.len()
                .cmp(&b.1.len())
                .then(b.0.freq.cmp(&a.0.freq))
                .then(a.0.word.cmp(&b.0.word))
                .then(a.0.syllables.join("'").cmp(&b.0.syllables.join("'")))
        });
        out.truncate(limit);
        out
    }
}
