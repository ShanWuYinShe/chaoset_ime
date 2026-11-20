//! 简繁体转换：基于 OpenCC 单字与词组词表的最长前缀匹配。

use std::collections::HashMap;
use std::sync::OnceLock;

static CHARS_DATA: &str = include_str!("../../../engine/dp_data/STCharacters.txt");
static PHRASES_DATA: &str = include_str!("../../../engine/dp_data/STPhrases.txt");

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
    let data = get_data();
    let chars: Vec<char> = text.chars().collect();
    let mut result = String::with_capacity(text.len());
    let mut i = 0;

    while i < chars.len() {
        let mut matched = false;
        let max_len = data.max_phrase_len.min(chars.len() - i);

        // 优先最长词组匹配
        for l in (2..=max_len).rev() {
            let sub: String = chars[i..i + l].iter().collect();
            if let Some(trad) = data.phrases.get(&sub) {
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
