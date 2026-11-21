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
    "b", "p", "m", "f", "d", "t", "n", "l", "g", "k", "h", "j", "q", "x", "zh", "ch", "sh", "r",
    "z", "c", "s", "y", "w",
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

/// 返回「以 syl 为前缀、比 syl 更长的合法音节」中 syl 之后需要补敲的那一段
/// （如 gu → ["o","i","ang"…]）。空结果表示该音节无法再延长，没有续接
/// 预测价值。对齐 Go engine/session.go syllableContinuations。
pub fn syllable_continuations(syl: &str) -> Vec<String> {
    if syl.is_empty() {
        return Vec::new();
    }
    SYLLABLE_TABLE
        .iter()
        .filter(|s| s.len() > syl.len() && s.starts_with(syl))
        .map(|s| s[syl.len()..].to_string())
        .collect()
}

/// 把一段拼音贪心切成完整音节；有任何字符无法归入合法音节时返回 None。
/// 对齐 Go engine/session.go splitExactSyllables。
pub fn split_exact_syllables(chunk: &str) -> Option<Vec<String>> {
    let mut syls = Vec::new();
    let mut rest = chunk;
    while !rest.is_empty() {
        let max_l = rest.len().min(MAX_SYLLABLE_LEN);
        let mut matched = false;
        for l in (1..=max_l).rev() {
            if is_syllable(&rest[..l]) {
                syls.push(rest[..l].to_string());
                rest = &rest[l..];
                matched = true;
                break;
            }
        }
        if !matched {
            return None;
        }
    }
    Some(syls)
}

/// 末段残缺续接的拆分形态：（前面已完整的音节序列, 待补全串 frag）。
/// 与「末音节可延长」那条路径的区别：这里末段连完整音节都不是（quanmia
/// 的 mia / nchen 的 chen），必须整段作为 prefix 去续接，否则只会被切成
/// 碎片出「全米啊」这类假词。frag 从 2 字符起递增（1 字符属声母简拼领域，
/// 交给常规路径）；优先把最长的一段当前置完整音节（quanmia → quan + mia）；
/// frag 本身已是合法音节时停止（由上一条路径覆盖）。最多返回 2 条。
/// 对齐 Go engine/session.go partialTailSplits。
pub fn partial_tail_splits(input: &str) -> Vec<(Vec<String>, String)> {
    if input.is_empty() || input.ends_with('\'') {
        return Vec::new();
    }
    let last_chunk = match input.rfind('\'') {
        Some(i) => &input[i + 1..],
        None => input,
    };
    if last_chunk.is_empty() {
        return Vec::new();
    }
    let mut out = Vec::new();
    for fl in 2..=MAX_SYLLABLE_LEN {
        if out.len() >= 2 {
            break;
        }
        if fl >= last_chunk.len() {
            continue;
        }
        let frag = &last_chunk[last_chunk.len() - fl..];
        if is_syllable(frag) {
            break;
        }
        let head = &last_chunk[..last_chunk.len() - fl];
        let Some(syls) = split_exact_syllables(head) else {
            continue;
        };
        if syls.is_empty() {
            continue;
        }
        out.push((syls, frag.to_string()));
    }
    out
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

    fn dfs(chars: &[char], idx: usize, current: &mut Vec<Token>, results: &mut Vec<Vec<Token>>) {
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

// ── 反序双字母手误（对齐 Go engine/syllable.go，逐值移植）────────────
// 反序双字母手误分两档：
//   1. FORCED_MISTAKE_PAIRS：反序后在拼音里几乎不可能出现、也不属于合法
//      混拼形态的组合（gn/hz/hc/hs）——遇到即判定手误，强制进入纠错路径，
//      归位修正无条件提升到精确层。
//   2. SOFT_MISTAKE_PAIRS：反序形态在拼音音节里不出现，但可能与「合法混
//      拼」同形（ao/oa、ua/au、ue/eu、ve/ev）——只在纠错路径里作为最高
//      优先级修正，不强制触发、也不无条件提升。
pub const FORCED_MISTAKE_PAIRS: &[(&str, &str)] = &[
    ("gn", "ng"), // ng 韵尾打反：dign → ding
    ("hz", "zh"),
    ("hc", "ch"),
    ("hs", "sh"),
    ("ev", "ve"), // ü 韵母（lve/nve 打反）；v 不是合法声母/音节，ev 不可能是混拼
];

pub const SOFT_MISTAKE_PAIRS: &[(&str, &str)] = &[
    ("oa", "ao"), // hao → hoa
    ("au", "ua"), // hua → hau
    ("eu", "ue"), // xue → xeu
];

/// 两档合并后的完整修正表（生成修正串时用）。
pub const MISTAKE_PAIRS_ALL: &[(&str, &str)] = &[
    ("gn", "ng"),
    ("hz", "zh"),
    ("hc", "ch"),
    ("hs", "sh"),
    ("ev", "ve"),
    ("oa", "ao"),
    ("au", "ua"),
    ("eu", "ue"),
];

/// 报告输入是否含有「强制归位」的反序组合（Go hasCommonMistakeCluster）。
pub fn has_common_mistake_cluster(s: &str) -> bool {
    FORCED_MISTAKE_PAIRS.iter().any(|p| s.contains(p.0))
}

/// 报告是否存在「所有段都是完整音节」的切分路径（Go hasCleanFullSplit）。
pub fn has_clean_full_split(patterns: &[Vec<Token>]) -> bool {
    patterns
        .iter()
        .any(|pat| !pat.is_empty() && pat.iter().all(|t| t.is_exact))
}

/// 报告输入的所有切分路径是否都只由声母碎片组成（纯简拼，Go allInitialPatterns）。
pub fn all_initial_patterns(patterns: &[Vec<Token>]) -> bool {
    !patterns.is_empty() && patterns.iter().all(|pat| pat.iter().all(|t| !t.is_exact))
}

/// 生成把输入中反序组合翻回正常顺序的修正串：每个位置单独翻一次 + 所有
/// 位置一起翻（支持同一输入里多处连错）。对齐 Go commonMistakeFixes。
pub fn common_mistake_fixes(input: &str) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
    let bytes = input.as_bytes();
    for p in MISTAKE_PAIRS_ALL {
        let (from, to) = p;
        for i in 0..bytes.len().saturating_sub(1) {
            if &input[i..i + 2] != *from {
                continue;
            }
            let fixed = format!("{}{}{}", &input[..i], to, &input[i + 2..]);
            if !fixed.is_empty() && fixed != input && seen.insert(fixed.clone()) {
                out.push(fixed);
            }
        }
    }
    let mut all = input.to_string();
    for p in MISTAKE_PAIRS_ALL {
        all = all.replace(p.0, p.1);
    }
    if !all.is_empty() && all != input && seen.insert(all.clone()) {
        out.push(all);
    }
    out
}

/// 判断输入是否「像打错了」——错拼修正的预筛（Go misspelledFromPatterns，
/// 判据刻意保守）：
///   - 纯声母简拼（gn=gong+neng、zg=zhong+guo）合法，不判手误；
///   - 整串能切成完整音节时，反序组合可能跨音节边界（gongneng 的 gn），
///     不判手误；
///   - 含「反序双字母」组合（gn/hz/hc/hs）且无法完整切分 → 手误；
///   - 切分路径为空（zhonguo/jisuanjii/niihao 都切不动）→ 手误；
///   - 任一条切分路径里出现完整音节（≥2 字母）→ 信任为正常混拼；
///   - 短串（≤4 字母，如 zg/bj/jsj）一律信任。
pub fn misspelled_from_patterns(s: &str, patterns: &[Vec<Token>]) -> bool {
    if s.is_empty() {
        return false;
    }
    // Rust tokenize_chunk 对「无任何完整切分」的输入回退成单条原样碎片
    // token——语义上等价于 Go 的空切分路径（整串切不动）。Go 侧 patterns
    // 为空时该回退不存在；PR-B 移植两段式切分器后此桥接可移除。
    let effectively_empty = patterns.is_empty()
        || (patterns.len() == 1
            && patterns[0].len() == 1
            && !patterns[0][0].is_exact
            && patterns[0][0].text == s);
    if !effectively_empty && all_initial_patterns(patterns) {
        return false;
    }
    if has_clean_full_split(patterns) {
        return false;
    }
    if has_common_mistake_cluster(s) {
        return true;
    }
    if effectively_empty {
        return true;
    }
    for pat in patterns {
        for tok in pat {
            if tok.is_exact && tok.text.len() >= 2 {
                return false;
            }
        }
    }
    s.len() > 4
}

/// misspelled_from_patterns 的自切分包装（Go mayBeMisspelled）。
pub fn may_be_misspelled(s: &str) -> bool {
    if s.is_empty() {
        return false;
    }
    misspelled_from_patterns(s, &tokenize_chunk(s))
}

/// 报告任一切分路径里「非末位」出现单字母声母碎片（Go hasMidIsolatedInitial）。
pub fn has_mid_isolated_initial(patterns: &[Vec<Token>]) -> bool {
    for pat in patterns {
        for k in 0..pat.len().saturating_sub(1) {
            if !pat[k].is_exact && pat[k].text.chars().count() == 1 {
                return true;
            }
        }
    }
    false
}

/// 报告任一切分路径里「末位」出现单字母声母碎片（Go hasTailIsolatedInitial）。
pub fn has_tail_isolated_initial(patterns: &[Vec<Token>]) -> bool {
    patterns.iter().any(|pat| {
        pat.last()
            .map(|last| !last.is_exact && last.text.chars().count() == 1)
            .unwrap_or(false)
    })
}

/// 判断 a 与 b 是否为 QWERTY 物理相邻键（Go isQWERTYNeighbor）。
pub fn is_qwerty_neighbor(a: char, b: char) -> bool {
    qwerty_neighbors(a).contains(&b)
}

/// 生成 input 的「单次编辑」修正候选（只生成能完整切分成合法音节的那些
/// 串），按编辑类型与置信度稳定输出。对齐 Go syllable.go typoCorrections：
///
/// 覆盖六类最常见的手误（生成顺序 = 置信度顺序）：
///   0. 常见反序双字母归位（gn/hz/hc/hs/ev + 软纠错 ao/au/eu）：最高置信度
///   1. transpose：相邻两字母颠倒（zhogn → zhong）
///   2. delete：多敲了一个字母（zhonng / niihao → zhong / nihao）
///   3. stutter：连击/粘键折叠（niihaoo → nihao）
///   4. substitute-near：QWERTY 邻近键敲错（yishrng → yisheng）
///   5. insert：漏敲了一个字母（zhonguo → zhongguo；字母在外、位置在内交错，
///      避免位置 0 的 26 个插入吃光名额）
///   6. substitute-far：非邻近键替换兜底（同样字母交错）
pub fn typo_corrections(input: &str, max_results: usize) -> Vec<TypoCorrection> {
    if input.is_empty() {
        return Vec::new();
    }
    let max_results = if max_results == 0 { 32 } else { max_results };
    let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
    let mut out: Vec<TypoCorrection> = Vec::new();

    let mut add = |cand: String, is_near: bool, out: &mut Vec<TypoCorrection>| {
        if out.len() >= max_results || !seen.insert(cand.clone()) {
            return;
        }
        if cand == input || !has_clean_syllable_split(&cand) {
            return;
        }
        out.push(TypoCorrection {
            text: cand,
            is_near,
        });
    };

    // 0) 常见反序双字母（gn/hz/hc/hs → ng/zh/ch/sh）：最高置信度的手误，
    //    优先于相邻互换/删除/插入等通用编辑，保证 dign→定、hzi→之 这类
    //    修正排在纠错候选最前。
    for fixed in common_mistake_fixes(input) {
        add(fixed, true, &mut out);
    }
    let r = input.as_bytes();
    let n = r.len();

    // 1) 相邻互换：最优先，因为它是「同一个词、顺序打反」，语义最可能正确。
    for i in 0..n.saturating_sub(1) {
        if r[i] == r[i + 1] {
            continue;
        }
        let mut c = r.to_vec();
        c.swap(i, i + 1);
        add(String::from_utf8_lossy(&c).to_string(), true, &mut out);
    }
    // 2) 删除一个字母（多敲或连带误触）
    for i in 0..n {
        let mut d = r.to_vec();
        d.remove(i);
        add(String::from_utf8_lossy(&d).to_string(), true, &mut out);
    }
    // 3) 连击/按键粘连折叠（连续重复字母手误，如 niihaoo -> nihao）
    let has_dup = r.windows(2).any(|w| w[0] == w[1]);
    if has_dup {
        let collapsed: Vec<u8> = r
            .iter()
            .enumerate()
            .filter(|(i, _)| *i == 0 || r[*i] != r[*i - 1])
            .map(|(_, b)| *b)
            .collect();
        add(
            String::from_utf8_lossy(&collapsed).to_string(),
            true,
            &mut out,
        );
    }
    // 4) QWERTY 键盘邻近键替换：手指物理按偏。优先级必须高于全键盘任意
    //    插入/远端替换，避免有限配额被无关联字母耗尽。
    for i in 0..n {
        let orig = r[i] as char;
        if !orig.is_ascii_lowercase() {
            continue;
        }
        for nb in qwerty_neighbors(orig) {
            let mut c = r.to_vec();
            c[i] = *nb as u8;
            add(String::from_utf8_lossy(&c).to_string(), true, &mut out);
        }
    }
    // 5) 插入一个字母（漏敲）。字母在外、位置在内交错。
    for ch in b'a'..=b'z' {
        for i in 0..=n {
            let mut c = Vec::with_capacity(n + 1);
            c.extend_from_slice(&r[..i]);
            c.push(ch);
            c.extend_from_slice(&r[i..]);
            add(String::from_utf8_lossy(&c).to_string(), true, &mut out);
        }
    }
    // 6) 远端非邻近键替换（敲错远端键），同样按字母交错。
    for ci in 0..26u8 {
        let ch = b'a' + ci;
        for i in 0..n {
            let orig = r[i] as char;
            if r[i] == ch || is_qwerty_neighbor(orig, ch as char) {
                continue;
            }
            let mut c = r.to_vec();
            c[i] = ch;
            add(String::from_utf8_lossy(&c).to_string(), false, &mut out);
        }
    }
    out
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
    /// TestMisspelledPrefilterBoundary 镜像：混拼形态判「正常」，彻底切不动
    /// 的长串判「错拼」。
    #[test]
    fn test_misspelled_prefilter_boundary() {
        for input in ["zhogn", "zhonng", "zhonguo", "jisuanjii", "niihao"] {
            assert!(may_be_misspelled(input), "{} 应被判为错拼", input);
        }
        for input in [
            "zhong",
            "nihao",
            "jingchang",
            "zhongg",
            "chifanl",
            "zg",
            "bj",
            "jsj",
            "yiw",
            "shangxj",
        ] {
            assert!(
                !may_be_misspelled(input),
                "{} 不应被判为错拼（正常简拼/混拼输入）",
                input
            );
        }
    }

    /// TestCommonMistakeFixesMultiple 镜像：同一输入里多处反序组合一起翻回。
    #[test]
    fn test_common_mistake_fixes_multiple() {
        let fixes = common_mistake_fixes("dignhzi");
        let set: std::collections::HashSet<String> = fixes.iter().cloned().collect();
        for want in ["dinghzi", "dignzhi", "dingzhi"] {
            assert!(
                set.contains(want),
                "common_mistake_fixes(dignhzi) 缺少 {}，实际 {:?}",
                want,
                fixes
            );
        }
        assert!(
            common_mistake_fixes("zhongguo").is_empty(),
            "zhongguo 不应产生反序修正"
        );
    }

    /// TestCommonMistakeFixesContainsSoftPairs 镜像：软纠错组合进入修正串列表。
    #[test]
    fn test_common_mistake_fixes_contains_soft_pairs() {
        for (input, want) in [
            ("hoa", "hao"),
            ("hau", "hua"),
            ("xeu", "xue"),
            ("lev", "lve"),
        ] {
            let fixes = common_mistake_fixes(input);
            assert!(
                fixes.iter().any(|f| f == want),
                "common_mistake_fixes({}) 应包含 {}，实际 {:?}",
                input,
                want,
                fixes
            );
        }
    }

    /// 生成器顺序契约（Go typoCorrections 注释）：dign 的第一条修正应是
    /// 强制归位 ding；zhoonng 的折叠修正 zhong 必须在列表里；zhonguo 的
    /// 插入修正 zhonggu 必须在列表里；正常输入不产生修正。
    #[test]
    fn test_typo_generator_ordering_and_coverage() {
        let fixes = typo_corrections("dign", 32);
        assert_eq!(fixes[0].text, "ding", "强制归位应排最前: {:?}", fixes);
        assert!(fixes[0].is_near);

        let fixes = typo_corrections("zhoonng", 32);
        assert!(
            fixes.iter().any(|f| f.text == "zhong"),
            "连击折叠应产出 zhong: {:?}",
            fixes
        );

        let fixes = typo_corrections("zhonguo", 32);
        assert!(
            fixes.iter().any(|f| f.text == "zhongguo"),
            "漏敲修正应产出 zhongguo: {:?}",
            fixes
        );

        // 生成器永不把输入自身当作修正（Go add 同款排除；「正常输入的修正
        // 列表是否被管线采用」由 TypoSuspicious 门控决定，属 PR-B 管线语义）
        assert!(
            !typo_corrections("zhongguo", 32)
                .iter()
                .any(|f| f.text == "zhongguo"),
            "修正不得包含输入自身"
        );
    }
}
