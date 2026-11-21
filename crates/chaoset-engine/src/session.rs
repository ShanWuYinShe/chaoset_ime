//! 输入法会话状态机：按键组词、候选词排序、分页、渐进选择与联想。

use std::sync::Arc;

use crate::dict::Dictionary;
use crate::fuzzy::fuzzy_syllable_variants;
use crate::punct::PunctState;
use crate::s2t::to_traditional;
use crate::shuangpin::{ShuangpinDecoder, ShuangpinScheme};
use crate::syllable::{tokenize_chunk, tokenize_pinyin, typo_corrections, Token};
use crate::userdict::UserDict;

pub const PAGE_SIZE: usize = 6;

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct Candidate {
    pub text: String,
    pub syllables: String,
    #[serde(default)]
    pub score: i32,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub from_user: bool,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct Composition {
    pub input: String,
    pub preedit: String,
    pub candidates: Vec<Candidate>,
    pub page: usize,
    pub page_count: usize,
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub committed: String,
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub shuangpin: String,
}

pub struct Session {
    dict: Arc<Dictionary>,
    user_dict: Option<Arc<UserDict>>,
    pub fuzzy: bool,
    pub traditional: bool,
    pub shuangpin: Option<ShuangpinScheme>,
    shuangpin_decoder: Option<ShuangpinDecoder>,
    punct_state: PunctState,

    input_raw: String,
    tokens: Vec<Token>,
    all_candidates: Vec<(Candidate, u32)>, // (Candidate, Score)
    page: usize,
    page_size: usize,

    last_committed: Option<String>,
}

impl Session {
    pub fn new(dict: Arc<Dictionary>, user_dict: Option<Arc<UserDict>>) -> Self {
        Self {
            dict,
            user_dict,
            fuzzy: false,
            traditional: false,
            shuangpin: None,
            shuangpin_decoder: None,
            punct_state: PunctState::default(),
            input_raw: String::new(),
            tokens: Vec::new(),
            all_candidates: Vec::new(),
            page: 0,
            page_size: PAGE_SIZE,
            last_committed: None,
        }
    }

    pub fn set_fuzzy(&mut self, fuzzy: bool) {
        self.fuzzy = fuzzy;
    }

    pub fn set_traditional(&mut self, traditional: bool) {
        self.traditional = traditional;
    }

    pub fn set_shuangpin(&mut self, scheme: Option<ShuangpinScheme>) {
        self.shuangpin = scheme;
        self.shuangpin_decoder = scheme.map(ShuangpinDecoder::new);
    }

    pub fn page_size(&self) -> usize {
        self.page_size
    }

    /// 核心组词函数
    pub fn compose(&mut self) -> Composition {
        if self.input_raw.is_empty() {
            self.tokens.clear();
            self.all_candidates.clear();
            self.page = 0;
            return Composition {
                shuangpin: self.shuangpin.map(|s| s.name().to_string()).unwrap_or_default(),
                ..Default::default()
            };
        }

        // 1. 双拼解码
        let pinyin_query = if let Some(ref decoder) = self.shuangpin_decoder {
            let mut decoded = String::new();
            let mut chars = self.input_raw.chars().peekable();
            while let Some(c1) = chars.next() {
                if let Some(&c2) = chars.peek() {
                    let pair = format!("{}{}", c1, c2);
                    if let Some(syll) = decoder.decode(&pair) {
                        chars.next();
                        if !decoded.is_empty() {
                            decoded.push('\'');
                        }
                        decoded.push_str(syll);
                        continue;
                    }
                }
                decoded.push(c1);
            }
            decoded
        } else {
            self.input_raw.clone()
        };

        // 2. 音节切分
        let patterns = tokenize_pinyin(&pinyin_query);
        let best_pattern = patterns.first().cloned().unwrap_or_default();
        self.tokens = best_pattern.clone();

        // 3. 构建音节序列与 preedit 显示
        let mut preedit_parts: Vec<String> = Vec::new();
        for tok in &best_pattern {
            preedit_parts.push(tok.text.clone());
        }

        let preedit = preedit_parts.join("'");

        // 4. 召回与打分
        let mut candidates_map: std::collections::HashMap<String, (Candidate, u32)> =
            std::collections::HashMap::new();

        // 4.1 对所有切分 pattern 召回候选
        for (pat_idx, pat) in patterns.iter().enumerate() {
            let syll_slices: Vec<&str> = pat.iter().map(|t| t.text.as_str()).collect();
            let pat_penalty = (pat_idx as u32) * 200;

            for l in (1..=syll_slices.len()).rev() {
                let sub_sylls = &syll_slices[..l];
                let is_full = l == syll_slices.len();
                let matched_entries = if is_full {
                    self.dict.query_prefix(sub_sylls, 64)
                } else {
                    self.dict.query_exact(sub_sylls)
                };

                for entry in matched_entries {
                    let (user_bonus, from_user) = if let Some(ref u) = self.user_dict {
                        u.score(&entry.word, self.last_committed.as_deref())
                    } else {
                        (0, false)
                    };

                    // 最长匹配加成（整词优先）
                    let match_bonus = if is_full && entry.syllables.len() == syll_slices.len() {
                        5000 // 完整精确词最高优先级
                    } else if is_full {
                        1000 // 完整前缀词 (如 nihao 输入匹配 你好吗)
                    } else {
                        entry.syllables.len() as u32 * 500 // 前缀子词 (如 nihao 输入匹配 你)
                    };
                    let score = (entry.freq + user_bonus + match_bonus).saturating_sub(pat_penalty);
                    let display_text = if self.traditional {
                        to_traditional(&entry.word)
                    } else {
                        entry.word.clone()
                    };

                    candidates_map.entry(display_text.clone()).or_insert_with(|| {
                        (
                            Candidate {
                                text: display_text,
                                syllables: entry.syllables.join("'"),
                                score: 0,
                                from_user,
                            },
                            score,
                        )
                    });
                }
            }
        }

        // 4.2 模糊音召回 (若启用且候选数量较少)
        let best_syll_slices: Vec<&str> = best_pattern.iter().map(|t| t.text.as_str()).collect();
        if self.fuzzy && !best_syll_slices.is_empty() {
            let first_syll = best_syll_slices[0];
            let fuzzy_vars = fuzzy_syllable_variants(first_syll);
            for var in fuzzy_vars {
                if var != first_syll {
                    let mut alt_sylls = best_syll_slices.clone();
                    alt_sylls[0] = &var;
                    for entry in self.dict.query_prefix(&alt_sylls, 16) {
                        let display_text = if self.traditional {
                            to_traditional(&entry.word)
                        } else {
                            entry.word.clone()
                        };
                        candidates_map.entry(display_text.clone()).or_insert_with(|| {
                            (
                                Candidate {
                                    text: display_text,
                                    syllables: entry.syllables.join("'"),
                                    score: 0,
                                    from_user: false,
                                },
                                entry.freq.saturating_sub(200), // 模糊候选稍降权
                            )
                        });
                    }
                }
            }
        }

        // 4.3 纠错召回 (若无精确候选)
        if candidates_map.is_empty() {
            let typos = typo_corrections(&self.input_raw, 3);
            for typo in typos {
                let typo_tokens = tokenize_chunk(&typo.text);
                if let Some(first_pat) = typo_tokens.first() {
                    let t_sylls: Vec<&str> = first_pat.iter().map(|t| t.text.as_str()).collect();
                    for entry in self.dict.query_prefix(&t_sylls, 8) {
                        let display_text = if self.traditional {
                            to_traditional(&entry.word)
                        } else {
                            entry.word.clone()
                        };
                        candidates_map.entry(display_text.clone()).or_insert_with(|| {
                            (
                                Candidate {
                                    text: display_text,
                                    syllables: entry.syllables.join("'"),
                                    score: 0,
                                    from_user: false,
                                },
                                entry.freq.saturating_sub(500),
                            )
                        });
                    }
                }
            }
        }

        // 5. 排序与排序收敛
        let mut sorted: Vec<(Candidate, u32)> = candidates_map.into_values().collect();
        sorted.sort_by(|a, b| b.1.cmp(&a.1));
        self.all_candidates = sorted;

        // 6. 构造分页 Composition
        let total = self.all_candidates.len();
        let page_count = if total == 0 {
            0
        } else {
            (total + self.page_size - 1) / self.page_size
        };

        if self.page >= page_count {
            self.page = 0;
        }

        let start = self.page * self.page_size;
        let end = (start + self.page_size).min(total);
        let page_cands = if start < total {
            self.all_candidates[start..end]
                .iter()
                .map(|(c, score)| {
                    let mut cand = c.clone();
                    cand.score = *score as i32;
                    cand
                })
                .collect()
        } else {
            Vec::new()
        };

        Composition {
            input: self.input_raw.clone(),
            preedit,
            candidates: page_cands,
            page: self.page,
            page_count,
            committed: String::new(),
            shuangpin: self.shuangpin.map(|s| s.name().to_string()).unwrap_or_default(),
        }
    }

    /// 用户键盘输入
    pub fn input(&mut self, text: &str, half_width: bool) -> Result<Composition, String> {
        // 如果包含标点符号键且已有组词内容，且不是撇号（撇号作为拼音分隔符），执行顶字上屏
        for c in text.chars() {
            if crate::punct::punct_map(c).is_some() && !self.input_raw.is_empty() && c != '\'' {
                // 上屏当前首选词
                let committed_word = self.select(0).unwrap_or_default();
                let punct_str = self.punct_state.process(c, half_width);
                let mut comp = self.compose();
                comp.committed = format!("{}{}", committed_word, punct_str);
                return Ok(comp);
            }
            self.input_raw.push(c);
        }

        Ok(self.compose())
    }

    /// 退格删除最后一个按键字符
    pub fn backspace(&mut self) -> Composition {
        self.input_raw.pop();
        self.compose()
    }

    /// 删除最后一个整词音节（按词退格）
    pub fn backspace_word(&mut self) -> Composition {
        if let Some(last_tok) = self.tokens.pop() {
            let remove_len = last_tok.text.len();
            for _ in 0..remove_len {
                self.input_raw.pop();
            }
        } else {
            self.input_raw.pop();
        }
        self.compose()
    }

    /// 选择指定候选词（0~page_size-1）直接全部上屏
    pub fn select(&mut self, index: usize) -> Result<String, String> {
        let global_idx = self.page * self.page_size + index;
        if global_idx >= self.all_candidates.len() {
            return Err("候选词索引越界".to_string());
        }

        let cand = self.all_candidates[global_idx].0.clone();

        // 记录用户调频与共现
        if let Some(ref u) = self.user_dict {
            u.bump_word(&cand.text, None);
            if let Some(ref prev) = self.last_committed {
                u.bump_bigram(prev, &cand.text);
            }
        }

        self.last_committed = Some(cand.text.clone());
        self.input_raw.clear();
        self.compose();

        Ok(cand.text)
    }

    /// 渐进上屏：只消耗匹配的前缀音节，剩余部分继续组词
    pub fn select_prefix(&mut self, index: usize) -> Result<Composition, String> {
        let global_idx = self.page * self.page_size + index;
        if global_idx >= self.all_candidates.len() {
            return Err("候选词索引越界".to_string());
        }

        let cand = self.all_candidates[global_idx].0.clone();
        let syll_count = cand.syllables.split('\'').count();

        // 消耗前面 syll_count 个 token 对应的字符
        let mut consumed_chars = 0;
        for tok in self.tokens.iter().take(syll_count) {
            consumed_chars += tok.text.len();
        }

        if consumed_chars >= self.input_raw.len() {
            self.input_raw.clear();
        } else {
            self.input_raw = self.input_raw[consumed_chars..].to_string();
        }

        // 记录调频
        if let Some(ref u) = self.user_dict {
            u.bump_word(&cand.text, None);
            if let Some(ref prev) = self.last_committed {
                u.bump_bigram(prev, &cand.text);
            }
        }
        self.last_committed = Some(cand.text.clone());

        let mut comp = self.compose();
        comp.committed = cand.text;
        Ok(comp)
    }

    /// 下一页
    pub fn next_page(&mut self) -> Composition {
        let total = self.all_candidates.len();
        let page_count = if total == 0 {
            0
        } else {
            (total + self.page_size - 1) / self.page_size
        };

        if page_count > 0 && self.page + 1 < page_count {
            self.page += 1;
        }
        self.compose()
    }

    /// 上一页
    pub fn prev_page(&mut self) -> Composition {
        if self.page > 0 {
            self.page -= 1;
        }
        self.compose()
    }

    /// 重置清空当前输入会话与上下文
    pub fn reset(&mut self) -> Composition {
        self.input_raw.clear();
        self.last_committed = None;
        self.punct_state.reset();
        self.compose()
    }

    /// 仅重置当前组词，保留历史上下文
    pub fn reset_composition(&mut self) -> Composition {
        self.input_raw.clear();
        self.compose()
    }

    /// 直接将未转化的原始拼音按键作为文本上屏
    pub fn commit_raw(&mut self) -> Result<String, String> {
        let text = self.input_raw.clone();
        self.input_raw.clear();
        self.compose();
        Ok(text)
    }

    /// 联想候选查询
    pub fn associate(&self, sylls: &[String]) -> Vec<Candidate> {
        let syll_slices: Vec<&str> = sylls.iter().map(|s| s.as_str()).collect();
        let matched = self.dict.query_prefix(&syll_slices, 16);

        matched
            .into_iter()
            .map(|e| {
                let text = if self.traditional {
                    to_traditional(&e.word)
                } else {
                    e.word.clone()
                };
                Candidate {
                    text,
                    syllables: e.syllables.join("'"),
                    score: 0,
                    from_user: false,
                }
            })
            .collect()
    }

    /// 联想词直接上屏
    pub fn associate_select(&mut self, word: &str, _sylls: &[String]) -> Result<String, String> {
        if let Some(ref u) = self.user_dict {
            u.bump_word(word, None);
            if let Some(ref prev) = self.last_committed {
                u.bump_bigram(prev, word);
            }
        }
        self.last_committed = Some(word.to_string());
        Ok(word.to_string())
    }

    /// 处理标点输入（组词中先上屏首选词再上屏标点）
    pub fn punct(&mut self, key: &str, half_width: bool) -> Result<String, String> {
        let c = key.chars().next().ok_or("标点键为空")?;
        if !crate::punct::is_punct_key(c) {
            return Err(format!("未知标点键 {:?}", key));
        }

        if !self.input_raw.is_empty() {
            if c == '\'' {
                return Err("组词中的 ' 是音节分隔符，不作为标点提交".to_string());
            }

            let committed = if !self.all_candidates.is_empty() {
                self.select(0)?
            } else {
                self.input_raw.clear();
                self.tokens.clear();
                self.all_candidates.clear();
                String::new()
            };

            let punct_str = self.punct_state.process(c, half_width);
            Ok(format!("{}{}", committed, punct_str))
        } else {
            let punct_str = self.punct_state.process(c, half_width);
            Ok(punct_str)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn create_test_session() -> Session {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "你好".to_string(),
            pinyin: "ni'hao".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 9000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "你".to_string(),
            pinyin: "ni".to_string(),
            syllables: vec!["ni".to_string()],
            freq: 5000,
        });
        dict.sort_nodes();

        Session::new(Arc::new(dict), None)
    }

    #[test]
    fn test_session_input_and_select() {
        let mut sess = create_test_session();
        let comp = sess.input("nihao", false).unwrap();
        assert_eq!(comp.preedit, "ni'hao");
        assert!(!comp.candidates.is_empty());
        assert_eq!(comp.candidates[0].text, "你好");

        let committed = sess.select(0).unwrap();
        assert_eq!(committed, "你好");

        let comp_after = sess.compose();
        assert!(comp_after.input.is_empty());
    }

    #[test]
    fn test_session_select_prefix() {
        let mut sess = create_test_session();
        sess.input("nihao", false).unwrap();

        // 假设选择第 2 项「你」（单音节），应消耗 "ni"，剩余 "hao"
        let comp = sess.select_prefix(1).unwrap();
        assert_eq!(comp.committed, "你");
        assert_eq!(comp.input, "hao");
    }

    #[test]
    fn test_session_backspace() {
        let mut sess = create_test_session();
        sess.input("ni", false).unwrap();
        let comp = sess.backspace();
        assert_eq!(comp.input, "n");
    }
}
