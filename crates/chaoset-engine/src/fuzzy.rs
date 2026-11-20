//! 模糊音规则与音节变体生成。

use crate::syllable::is_syllable;

const FUZZY_PAIRS: &[(&str, &str)] = &[
    ("zh", "z"),
    ("z", "zh"),
    ("ch", "c"),
    ("c", "ch"),
    ("sh", "s"),
    ("s", "sh"),
    ("an", "ang"),
    ("ang", "an"),
    ("en", "eng"),
    ("eng", "en"),
    ("in", "ing"),
    ("ing", "in"),
    ("l", "n"),
    ("n", "l"),
    ("f", "h"),
    ("h", "f"),
];

/// 为单个标准音节生成所有合法模糊音变体
pub fn fuzzy_syllable_variants(syllable: &str) -> Vec<String> {
    let mut variants = vec![syllable.to_string()];

    for &(from, to) in FUZZY_PAIRS {
        // 声母替换（只在词头匹配）
        if matches!(from, "zh" | "z" | "ch" | "c" | "sh" | "s" | "l" | "n" | "f" | "h") {
            // 防止单字母声母（如 z/c/s）误伤双字母声母（zh/ch/sh）
            if (from == "z" && syllable.starts_with("zh"))
                || (from == "c" && syllable.starts_with("ch"))
                || (from == "s" && syllable.starts_with("sh"))
            {
                continue;
            }

            if syllable.starts_with(from) {
                let replaced = format!("{}{}", to, &syllable[from.len()..]);
                if is_syllable(&replaced) && !variants.contains(&replaced) {
                    variants.push(replaced);
                }
            }
        } else {
            // 韵尾替换（防止 an/en/in 误伤后鼻音 ang/eng/ing）
            if (from == "an" && syllable.ends_with("ang"))
                || (from == "en" && syllable.ends_with("eng"))
                || (from == "in" && syllable.ends_with("ing"))
            {
                continue;
            }

            if syllable.ends_with(from) {
                let replaced = format!("{}{}", &syllable[..syllable.len() - from.len()], to);
                if is_syllable(&replaced) && !variants.contains(&replaced) {
                    variants.push(replaced);
                }
            }
        }
    }

    variants
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_fuzzy_variants() {
        let vars = fuzzy_syllable_variants("zhang");
        assert!(vars.contains(&"zhang".to_string()));
        assert!(vars.contains(&"zang".to_string())); // zh -> z
        assert!(vars.contains(&"zhan".to_string())); // ang -> an

        let vars = fuzzy_syllable_variants("lan");
        assert!(vars.contains(&"nan".to_string())); // l -> n
        assert!(vars.contains(&"lang".to_string())); // an -> ang
    }
}
