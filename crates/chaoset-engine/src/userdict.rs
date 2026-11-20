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

    /// 用户选择词上屏，累加调频与上下文共现
    pub fn bump_word(&self, word: &str, segs: Option<Vec<String>>) {
        let mut data = self.data.write().unwrap();
        let now = current_timestamp();
        let entry = data.words.entry(word.to_string()).or_default();
        entry.count += 1;
        entry.last_used = now;
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
            let phrase_key_match = data.phrases.iter()
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
            .filter(|k| k.starts_with(&format!("{}->", word)) || k.ends_with(&format!("->{}", word)))
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
                out.push_str(&format!("{}\t{}\t{}\t{}\n", w, r.sylls, r.count, r.last_used));
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

        udict.bump_word("测试", None);
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
