//! 拼音音节解析、规范化、切分与拼写纠错。

use std::borrow::Cow;
use std::collections::HashSet;

/// 无声调标准普通话音节表（约 420 个，字母序排列，ü 统一以 v 记写：nv=女、lve=略）。
pub const SYLLABLE_TABLE: &[&str] = &[
    "a", "ai", "an", "ang", "ao", "ba", "bai", "ban", "bang", "bao", "bei", "ben", "beng", "bi",
    "bian", "biao", "bie", "bin", "bing", "bo", "bu", "ca", "cai", "can", "cang", "cao", "ce",
    "cen", "ceng", "cha", "chai", "chan", "chang", "chao", "che", "chen", "cheng", "chi", "chong",
    "chou", "chu", "chua", "chuai", "chuan", "chuang", "chui", "chun", "chuo", "ci", "cong", "cou",
    "cu", "cuan", "cui", "cun", "cuo", "da", "dai", "dan", "dang", "dao", "de", "dei", "den",
    "deng", "di", "dia", "dian", "diao", "die", "ding", "diu", "dong", "dou", "du", "duan", "dui",
    "dun", "duo", "e", "ei", "en", "eng", "er", "fa", "fan", "fang", "fei", "fen", "feng", "fo",
    "fou", "fu", "ga", "gai", "gan", "gang", "gao", "ge", "gei", "gen", "geng", "gong", "gou",
    "gu", "gua", "guai", "guan", "guang", "gui", "gun", "guo", "ha", "hai", "han", "hang", "hao",
    "he", "hei", "hen", "heng", "hong", "hou", "hu", "hua", "huai", "huan", "huang", "hui", "hun",
    "huo", "ji", "jia", "jian", "jiang", "jiao", "jie", "jin", "jing", "jiong", "jiu", "ju",
    "juan", "jue", "jun", "ka", "kai", "kan", "kang", "kao", "ke", "kei", "ken", "keng", "kong",
    "kou", "ku", "kua", "kuai", "kuan", "kuang", "kui", "kun", "kuo", "la", "lai", "lan", "lang",
    "lao", "le", "lei", "leng", "li", "lia", "lian", "liang", "liao", "lie", "lin", "ling", "liu",
    "lo", "long", "lou", "lu", "luan", "lun", "luo", "lv", "lve", "ma", "mai", "man", "mang",
    "mao", "me", "mei", "men", "meng", "mi", "mian", "miao", "mie", "min", "ming", "miu", "mo",
    "mou", "mu", "na", "nai", "nan", "nang", "nao", "ne", "nei", "nen", "neng", "ni", "nian",
    "niang", "niao", "nie", "nin", "ning", "niu", "nong", "nou", "nu", "nuan", "nuo", "nv", "nve",
    "o", "ou", "pa", "pai", "pan", "pang", "pao", "pei", "pen", "peng", "pi", "pian", "piao",
    "pie", "pin", "ping", "po", "pou", "pu", "qi", "qia", "qian", "qiang", "qiao", "qie", "qin",
    "qing", "qiong", "qiu", "qu", "quan", "que", "qun", "ran", "rang", "rao", "re", "ren", "reng",
    "ri", "rong", "rou", "ru", "rua", "ruan", "rui", "run", "ruo", "sa", "sai", "san", "sang",
    "sao", "se", "sen", "seng", "sha", "shai", "shan", "shang", "shao", "she", "shei", "shen",
    "sheng", "shi", "shou", "shu", "shua", "shuai", "shuan", "shuang", "shui", "shun", "shuo",
    "si", "song", "sou", "su", "suan", "sui", "sun", "suo", "ta", "tai", "tan", "tang", "tao",
    "te", "teng", "ti", "tian", "tiao", "tie", "ting", "tong", "tou", "tu", "tuan", "tui", "tun",
    "tuo", "wa", "wai", "wan", "wang", "wei", "wen", "weng", "wo", "wu", "xi", "xia", "xian",
    "xiang", "xiao", "xie", "xin", "xing", "xiong", "xiu", "xu", "xuan", "xue", "xun", "ya", "yan",
    "yang", "yao", "ye", "yi", "yin", "ying", "yo", "yong", "you", "yu", "yuan", "yue", "yun",
    "za", "zai", "zan", "zang", "zao", "ze", "zei", "zen", "zeng", "zha", "zhai", "zhan", "zhang",
    "zhao", "zhe", "zhen", "zheng", "zhi", "zhong", "zhou", "zhu", "zhua", "zhuai", "zhuan",
    "zhuang", "zhui", "zhun", "zhuo", "zi", "zong", "zou", "zu", "zuan", "zui", "zun", "zuo",
];

/// 声母列表（用于简拼判定与前缀切分）
pub const INITIALS: &[&str] = &[
    "b", "p", "m", "f", "d", "t", "n", "l", "g", "k", "h",
    "j", "q", "x", "zh", "ch", "sh", "r", "z", "c", "s", "y", "w",
];

pub const MAX_SYLLABLE_LEN: usize = 6; // 最长音节如 zhuang / shuang (6)

/// 判断是否是合法音节
pub fn is_syllable(s: &str) -> bool {
    SYLLABLE_TABLE.binary_search(&s).is_ok()
}

/// 返回所有以 frag 为前缀的音节
pub fn syllables_with_prefix(frag: &str) -> Vec<&'static str> {
    SYLLABLE_TABLE
        .iter()
        .copied()
        .filter(|s| s.starts_with(frag))
        .collect()
}

/// 音节别名映射与规范化（如 lue -> lve, nue -> nve, shuei -> shui 等）
pub fn canonical_syllable<'a>(s: &'a str) -> Cow<'a, str> {
    if is_syllable(s) {
        return Cow::Borrowed(s);
    }

    match s {
        "lue" => return Cow::Borrowed("lve"),
        "nue" => return Cow::Borrowed("nve"),
        "xion" => return Cow::Borrowed("xiong"),
        "jion" => return Cow::Borrowed("jiong"),
        "qion" => return Cow::Borrowed("qiong"),
        _ => {}
    }

    // 1. 省写韵母归一
    if s.len() >= 4 {
        if s.ends_with("uei") {
            let fixed = format!("{}ui", &s[..s.len() - 3]);
            if is_syllable(&fixed) {
                return Cow::Owned(fixed);
            }
        }
        if s.ends_with("iou") {
            let fixed = format!("{}iu", &s[..s.len() - 3]);
            if is_syllable(&fixed) {
                return Cow::Owned(fixed);
            }
        }
        if s.ends_with("uen") {
            let fixed = format!("{}un", &s[..s.len() - 3]);
            if is_syllable(&fixed) {
                return Cow::Owned(fixed);
            }
        }
    }

    // 2. 撮口呼字母 v 习惯 (jv -> ju, jve -> jue, etc.)
    if s.len() >= 2 && matches!(s.as_bytes()[0], b'j' | b'q' | b'x' | b'y') && s.contains('v') {
        let fixed = s.replace('v', "u");
        if is_syllable(&fixed) {
            return Cow::Owned(fixed);
        }
    }

    Cow::Borrowed(s)
}

/// 切分词元
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct Token {
    pub text: String,
    pub is_exact: bool,
}

/// 贪心最长匹配 + 声母碎片切分
pub fn tokenize_chunk(input: &str) -> Vec<Vec<Token>> {
    let mut results = Vec::new();
    let chars: Vec<char> = input.chars().collect();
    if chars.is_empty() {
        return results;
    }

    fn dfs(
        chars: &[char],
        idx: usize,
        current: &mut Vec<Token>,
        results: &mut Vec<Vec<Token>>,
    ) {
        if idx >= chars.len() {
            results.push(current.clone());
            return;
        }

        // 优先最长合法音节匹配
        let max_len = (chars.len() - idx).min(MAX_SYLLABLE_LEN);
        let mut matched_exact = false;

        for l in (1..=max_len).rev() {
            let sub: String = chars[idx..idx + l].iter().collect();
            let canon = canonical_syllable(&sub);
            if is_syllable(&canon) {
                matched_exact = true;
                current.push(Token {
                    text: canon.to_string(),
                    is_exact: true,
                });
                dfs(chars, idx + l, current, results);
                current.pop();
            }
        }

        // 如果没有完全精确匹配，尝试声母碎片（简拼）
        if !matched_exact {
            for l in (1..=2.min(chars.len() - idx)).rev() {
                let sub: String = chars[idx..idx + l].iter().collect();
                if INITIALS.contains(&sub.as_str()) {
                    current.push(Token {
                        text: sub,
                        is_exact: false,
                    });
                    dfs(chars, idx + l, current, results);
                    current.pop();
                }
            }
        }
    }

    let mut current = Vec::new();
    dfs(&chars, 0, &mut current, &mut results);

    // 如果无法切分，放入单一原样 Token
    if results.is_empty() {
        results.push(vec![Token {
            text: input.to_string(),
            is_exact: false,
        }]);
    }

    results
}

/// 支持 ' 显式分段的多段切分组合方案
pub fn tokenize_pinyin(input: &str) -> Vec<Vec<Token>> {
    let raw_chunks: Vec<&str> = input.split('\'').filter(|s| !s.is_empty()).collect();
    if raw_chunks.is_empty() {
        return Vec::new();
    }

    let mut patterns: Vec<Vec<Token>> = vec![vec![]];
    for chunk in raw_chunks {
        let chunk_paths = tokenize_chunk(chunk);
        if chunk_paths.is_empty() {
            return Vec::new();
        }
        let mut next = Vec::new();
        for base in &patterns {
            for cp in &chunk_paths {
                if next.len() >= 16 {
                    break;
                }
                let mut merged = base.clone();
                merged.extend_from_slice(cp);
                next.push(merged);
            }
        }
        patterns = next;
    }

    patterns
}

/// QWERTY 键盘物理相邻键矩阵
pub fn qwerty_neighbors(c: char) -> &'static [char] {
    match c {
        'a' => &['s', 'q', 'z', 'w'],
        'b' => &['n', 'v', 'g', 'h'],
        'c' => &['v', 'x', 'd', 'f'],
        'd' => &['f', 's', 'e', 'c', 'x', 'r'],
        'e' => &['r', 'w', 'd', 's'],
        'f' => &['d', 'g', 'r', 'c', 'v', 't'],
        'g' => &['f', 'h', 't', 'b', 'v', 'y'],
        'h' => &['g', 'j', 'y', 'b', 'n', 'u'],
        'i' => &['o', 'u', 'k', 'j'],
        'j' => &['h', 'k', 'u', 'm', 'n', 'i'],
        'k' => &['j', 'l', 'i', 'm', 'o'],
        'l' => &['k', 'o', 'p'],
        'm' => &['n', 'j', 'k'],
        'n' => &['b', 'm', 'h', 'j'],
        'o' => &['i', 'p', 'l', 'k'],
        'p' => &['o', 'l'],
        'q' => &['w', 'a'],
        'r' => &['e', 't', 'f', 'd'],
        's' => &['a', 'd', 'w', 'x', 'z', 'e'],
        't' => &['r', 'y', 'g', 'f'],
        'u' => &['i', 'y', 'j', 'h'],
        'v' => &['b', 'c', 'f', 'g'],
        'w' => &['e', 'q', 's', 'a'],
        'x' => &['c', 'z', 'd', 's'],
        'y' => &['t', 'u', 'h', 'g'],
        'z' => &['x', 'a', 's'],
        _ => &[],
    }
}

/// 判断一个字符串能否完整切分为标准音节
pub fn has_clean_syllable_split(s: &str) -> bool {
    let len = s.len();
    if len == 0 {
        return false;
    }
    let mut dp = vec![false; len + 1];
    dp[0] = true;

    for i in 1..=len {
        for l in 1..=MAX_SYLLABLE_LEN.min(i) {
            if dp[i - l] {
                let sub = &s[i - l..i];
                let canon = canonical_syllable(sub);
                if is_syllable(&canon) {
                    dp[i] = true;
                    break;
                }
            }
        }
    }
    dp[len]
}

/// 纠错项
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TypoCorrection {
    pub text: String,
    pub is_near: bool,
}

/// 生成单次编辑纠错候选
pub fn typo_corrections(input: &str, max_results: usize) -> Vec<TypoCorrection> {
    if input.is_empty() {
        return Vec::new();
    }
    let mut results = Vec::new();
    let mut seen = HashSet::new();

    let mut add = |cand: String, is_near: bool| {
        if cand == input || results.len() >= max_results || seen.contains(&cand) {
            return;
        }
        if has_clean_syllable_split(&cand) {
            seen.insert(cand.clone());
            results.push(TypoCorrection { text: cand, is_near });
        }
    };

    // 1. 经典反序纠错 (gn -> ng, hz -> zh, hc -> ch, hs -> sh, ev -> ve, etc.)
    const FORCED_PAIRS: &[(&str, &str)] = &[
        ("gn", "ng"),
        ("hz", "zh"),
        ("hc", "ch"),
        ("hs", "sh"),
        ("ev", "ve"),
        ("oa", "ao"),
        ("au", "ua"),
        ("eu", "ue"),
    ];

    for &(from, to) in FORCED_PAIRS {
        if input.contains(from) {
            add(input.replace(from, to), true);
        }
    }

    // 2. 相邻字母颠倒 (transpose)
    let bytes = input.as_bytes();
    for i in 0..bytes.len().saturating_sub(1) {
        if bytes[i] != bytes[i + 1] {
            let mut swapped = bytes.to_vec();
            swapped.swap(i, i + 1);
            if let Ok(s) = String::from_utf8(swapped) {
                add(s, true);
            }
        }
    }

    // 3. 删除连击/多敲字母 (delete)
    for i in 0..bytes.len() {
        let mut deleted = bytes.to_vec();
        deleted.remove(i);
        if let Ok(s) = String::from_utf8(deleted) {
            add(s, true);
        }
    }

    // 4. QWERTY 邻近替换 (substitute-near)
    for i in 0..bytes.len() {
        let c = bytes[i] as char;
        for &neighbor in qwerty_neighbors(c) {
            let mut subbed = bytes.to_vec();
            subbed[i] = neighbor as u8;
            if let Ok(s) = String::from_utf8(subbed) {
                add(s, true);
            }
        }
    }

    results
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_syllable() {
        assert!(is_syllable("ni"));
        assert!(is_syllable("hao"));
        assert!(is_syllable("zhong"));
        assert!(is_syllable("lve"));
        assert!(is_syllable("nv"));
        assert!(!is_syllable("xyz"));
        assert!(!is_syllable("lue")); // lue 通过 canonical_syllable 变为 lve
    }

    #[test]
    fn test_canonical_syllable() {
        assert_eq!(canonical_syllable("lue"), "lve");
        assert_eq!(canonical_syllable("nue"), "nve");
        assert_eq!(canonical_syllable("shuei"), "shui");
        assert_eq!(canonical_syllable("jiou"), "jiu");
        assert_eq!(canonical_syllable("chuen"), "chun");
        assert_eq!(canonical_syllable("jv"), "ju");
        assert_eq!(canonical_syllable("xion"), "xiong");
    }

    #[test]
    fn test_has_clean_syllable_split() {
        assert!(has_clean_syllable_split("nihao"));
        assert!(has_clean_syllable_split("zhongguo"));
        assert!(has_clean_syllable_split("hundunshurufa"));
        assert!(!has_clean_syllable_split("nihaox"));
    }

    #[test]
    fn test_typo_corrections() {
        let fixes = typo_corrections("dign", 5);
        assert!(fixes.iter().any(|f| f.text == "ding"));

        let fixes = typo_corrections("zhogn", 5);
        assert!(fixes.iter().any(|f| f.text == "zhong"));
    }
}
