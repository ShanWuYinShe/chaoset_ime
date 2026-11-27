//! 简繁体转换：基于 OpenCC 单字与词组词表的最长前缀匹配。

use std::collections::HashMap;
use std::sync::OnceLock;

static CHARS_DATA: &str = include_str!("data/s2t/STCharacters.txt");
static PHRASES_DATA: &str = include_str!("data/s2t/STPhrases.txt");

struct S2tData {
    chars: HashMap<char, char>,
    phrases: HashMap<String, String>,
    max_phrase_len: usize,
}

static S2T_DATA: OnceLock<S2tData> = OnceLock::new();

fn get_data() -> &'static S2tData {
    S2T_DATA.get_or_init(|| {
        let mut chars = HashMap::with_capacity(4096);
        let mut phrases = HashMap::with_capacity(50000);
        let mut max_phrase_len = 0;

        for line in CHARS_DATA.lines() {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') {
                continue;
            }
            if let Some((k, v)) = line.split_once('\t') {
                if let Some(kc) = k.chars().next() {
                    let target = v.split_whitespace().next().unwrap_or(v);
                    if let Some(vc) = target.chars().next() {
                        chars.insert(kc, vc);
                    }
                }
            }
        }

        for line in PHRASES_DATA.lines() {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') {
                continue;
            }
            if let Some((k, v)) = line.split_once('\t') {
                let target = v.split_whitespace().next().unwrap_or(v);
                let char_len = k.chars().count();
                if char_len > max_phrase_len {
                    max_phrase_len = char_len;
                }
                phrases.insert(k.to_string(), target.to_string());
            }
        }

        S2tData {
            chars,
            phrases,
            max_phrase_len,
        }
    })
}

/// 将简体中文转换为繁体中文
pub fn to_traditional(text: &str) -> String {
    if text.is_empty() {
        return String::new();
    }
    let data = get_data();
    let mut it = text.chars();
    let first = it.next().unwrap();
    if it.next().is_none() {
        return match data.chars.get(&first) {
            Some(&tc) => String::from(tc),
            None => String::from(first),
        };
    }

    let mut starts: Vec<usize> = Vec::with_capacity(text.len().min(64) + 1);
    let mut chars: Vec<char> = Vec::with_capacity(text.len().min(64));
    for (byte_idx, c) in text.char_indices() {
        starts.push(byte_idx);
        chars.push(c);
    }
    starts.push(text.len());

    let mut result = String::with_capacity(text.len());
    let mut i = 0;

    while i < chars.len() {
        let mut matched = false;
        let max_len = data.max_phrase_len.min(chars.len() - i);

        // 优先最长词组匹配（零分配切片查表）
        for l in (2..=max_len).rev() {
            let sub = &text[starts[i]..starts[i + l]];
            if let Some(trad) = data.phrases.get(sub) {
                result.push_str(trad);
                i += l;
                matched = true;
                break;
            }
        }

        if !matched {
            let c = chars[i];
            if let Some(&tc) = data.chars.get(&c) {
                result.push(tc);
            } else {
                result.push(c);
            }
            i += 1;
        }
    }

    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_s2t_conversion() {
        assert_eq!(to_traditional("简体中文输入法"), "簡體中文輸入法");
        assert_eq!(to_traditional("头发"), "頭髮");
        assert_eq!(to_traditional("公里"), "公里"); // 保持公里的里
    }
}
