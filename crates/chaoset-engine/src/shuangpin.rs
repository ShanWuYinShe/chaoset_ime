//! 双拼方案支持：自然码（zrm）、微软双拼（mspy）与小鹤双拼（flypy）。

use std::collections::HashMap;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ShuangpinScheme {
    Zrm,
    Mspy,
    Flypy,
}

impl ShuangpinScheme {
    pub fn from_str(s: &str) -> Option<Self> {
        match s.to_ascii_lowercase().as_str() {
            "zrm" => Some(Self::Zrm),
            "mspy" => Some(Self::Mspy),
            "flypy" => Some(Self::Flypy),
            _ => None,
        }
    }

    pub fn name(&self) -> &'static str {
        match self {
            Self::Zrm => "zrm",
            Self::Mspy => "mspy",
            Self::Flypy => "flypy",
        }
    }
}

/// 双拼解码器：将 2 键码转换为拼音音节
pub struct ShuangpinDecoder {
    scheme: ShuangpinScheme,
    key_to_syllable: HashMap<String, String>,
}

impl ShuangpinDecoder {
    pub fn new(scheme: ShuangpinScheme) -> Self {
        let mut decoder = Self {
            scheme,
            key_to_syllable: HashMap::new(),
        };
        decoder.init_table();
        decoder
    }

    fn init_table(&mut self) {
        // 构建常见音节的双拼映射
        // zh -> v (zrm/mspy/flypy), ch -> i, sh -> u
        // 各方案韵母映射：
        let finals: &[(&str, char, char, char)] = &[
            // 韵母, zrm, mspy, flypy
            ("iu", 'q', 'q', 'q'),
            ("ei", 'w', 'w', 'w'),
            ("uan", 'r', 'r', 'r'),
            ("ue", 't', 't', 't'),
            ("ve", 't', 't', 't'),
            ("un", 'y', 'y', 'y'),
            ("vn", 'y', 'y', 'y'),
            ("sh", 'u', 'u', 'u'),
            ("ch", 'i', 'i', 'i'),
            ("uo", 'o', 'o', 'o'),
            ("ie", 'p', 'p', 'p'),
            ("ong", 's', 's', 's'),
            ("iong", 's', 's', 's'),
            ("ai", 'd', 'd', 'd'),
            ("en", 'f', 'f', 'f'),
            ("eng", 'g', 'g', 'g'),
            ("ang", 'h', 'h', 'h'),
            ("an", 'j', 'j', 'j'),
            ("uai", 'k', 'k', 'k'),
            ("ing", 'k', ';', 'k'),
            ("iang", 'l', 'l', 'l'),
            ("uang", 'l', 'l', 'l'),
            ("ou", 'z', 'z', 'z'),
            ("ia", 'x', 'x', 'x'),
            ("ua", 'x', 'x', 'x'),
            ("ao", 'c', 'c', 'c'),
            ("zh", 'v', 'v', 'v'),
            ("ui", 'v', 'v', 'v'),
            ("in", 'b', 'b', 'b'),
            ("iao", 'n', 'n', 'n'),
            ("ian", 'm', 'm', 'm'),
        ];

        for syll in crate::syllable::SYLLABLE_TABLE {
            if let Some(code) = self.encode_syllable(syll, finals) {
                self.key_to_syllable.insert(code, syll.to_string());
            }
        }
    }

    fn encode_syllable(&self, syll: &str, finals: &[(&str, char, char, char)]) -> Option<String> {
        // 零声母处理 (如 a, ai, an, ang, ao, e, en, eng, er, o, ou)
        if matches!(syll, "a" | "o" | "e") {
            return Some(format!("{}{}", syll, syll));
        }

        // 声母提取
        let (initial, final_part) = if syll.starts_with("zh") || syll.starts_with("ch") || syll.starts_with("sh") {
            let initial_char = match &syll[..2] {
                "zh" => 'v',
                "ch" => 'i',
                "sh" => 'u',
                _ => unreachable!(),
            };
            (initial_char, &syll[2..])
        } else if syll.len() >= 2 && matches!(syll.as_bytes()[0], b'b'..=b'z') && !matches!(syll.as_bytes()[0], b'a' | b'e' | b'o') {
            (syll.chars().next().unwrap(), &syll[1..])
        } else {
            // 零声母双写规则
            let c = syll.chars().next().unwrap();
            (c, &syll[1..])
        };

        if final_part.len() == 1 {
            return Some(format!("{}{}", initial, final_part));
        }

        for &(name, zrm_k, mspy_k, flypy_k) in finals {
            if final_part == name {
                let k = match self.scheme {
                    ShuangpinScheme::Zrm => zrm_k,
                    ShuangpinScheme::Mspy => mspy_k,
                    ShuangpinScheme::Flypy => flypy_k,
                };
                return Some(format!("{}{}", initial, k));
            }
        }

        None
    }

    /// 解码双键为音节
    pub fn decode(&self, two_keys: &str) -> Option<&str> {
        self.key_to_syllable.get(two_keys).map(|s| s.as_str())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_shuangpin_decode() {
        let decoder = ShuangpinDecoder::new(ShuangpinScheme::Zrm);
        assert_eq!(decoder.decode("ni"), Some("ni"));
        assert_eq!(decoder.decode("hc"), Some("hao")); // h + c(ao) = hao
        assert_eq!(decoder.decode("vs"), Some("zhong")); // v(zh) + s(ong) = zhong
    }
}
