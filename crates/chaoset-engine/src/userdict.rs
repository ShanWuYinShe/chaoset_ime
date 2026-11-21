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
    pub fn new<P: AsRef<Path>>(path_opt: Option<P>) -> Self {
        let path = path_opt.map(|p| p.as_ref().to_path_buf());
        let mut file_data = UserFile::default();

        if let Some(ref p) = path {
            if p.exists() {
                if let Ok(content) = fs::read_to_string(p) {
                    if let Ok(parsed) = serde_json::from_str::<UserFile>(&content) {
                        file_data = parsed;
                    }
                }
            }
        }

        Self {
            path,
            data: RwLock::new(file_data),
            dirty: RwLock::new(false),
        }
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

        // 2. 合成词组加分
        if let Some(phrase) = data.phrases.get(word) {
            let factor = decay(phrase.last_used, now);
            total_bonus += (phrase.count as f64) * USER_BOOST_PER_COUNT * factor * 1.2;
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

    pub fn delete_word(&self, word: &str, syllables: &[String]) -> bool {
        let mut data = self.data.write().unwrap();
        let mut deleted = false;

        if !syllables.is_empty() {
            let sylls_joined = syllables.join(" ");
            let phrase_key_match = data
                .phrases
                .iter()
                .find(|(_, p)| p.word == word && p.sylls == sylls_joined)
                .map(|(k, _)| k.clone());
            if let Some(k) = phrase_key_match {
                data.phrases.remove(&k);
                deleted = true;
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

        // 同时清理相关的 bigram
        let keys_to_remove: Vec<String> = data
            .bigrams
            .keys()
            .filter(|k| {
                k.starts_with(&format!("{}->", word)) || k.ends_with(&format!("->{}", word))
            })
            .cloned()
            .collect();

        for k in keys_to_remove {
            data.bigrams.remove(&k);
        }

        if deleted {
            *self.dirty.write().unwrap() = true;
            true
        } else {
            false
        }
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

    pub fn export_tsv(&self, phrases_only: bool) -> String {
        let data = self.data.read().unwrap();
        let mut out = String::new();

        if !phrases_only && !data.words.is_empty() {
            out.push_str("# 词\t拼音\t次数\t最近使用\n");
            let mut word_rows: Vec<(&String, &UserRecord)> = data.words.iter().collect();
            word_rows.sort_by(|a, b| {
                if a.1.count != b.1.count {
                    b.1.count.cmp(&a.1.count)
                } else {
                    a.0.cmp(b.0)
                }
            });
            for (w, r) in word_rows {
                let segs = r.segs.join(" ");
                out.push_str(&format!("{}\t{}\t{}\t{}\n", w, segs, r.count, r.last_used));
            }
        }

        if !data.phrases.is_empty() {
            out.push_str("# 词组\t拼音\t次数\t最近使用\n");
            let mut phrase_rows: Vec<(&String, &PhraseRecord)> = data.phrases.iter().collect();
            phrase_rows.sort_by(|a, b| {
                if a.1.count != b.1.count {
                    b.1.count.cmp(&a.1.count)
                } else {
                    a.0.cmp(b.0)
                }
            });
            for (w, r) in phrase_rows {
                out.push_str(&format!(
                    "{}\t{}\t{}\t{}\n",
                    w, r.sylls, r.count, r.last_used
                ));
            }
        }

        if !phrases_only && !data.bigrams.is_empty() {
            out.push_str("# 上下文\t前词\t后词\t次数\n");
            let mut bigram_rows: Vec<(&String, &UserRecord)> = data.bigrams.iter().collect();
            bigram_rows.sort_by(|a, b| {
                if a.1.count != b.1.count {
                    b.1.count.cmp(&a.1.count)
                } else {
                    a.0.cmp(b.0)
                }
            });
            for (k, r) in bigram_rows {
                if let Some((prev, next)) = k.split_once("->") {
                    out.push_str(&format!("{}\t{}\t{}\n", prev, next, r.count));
                }
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
