//! 拼音音节解析、规范化、切分与拼写纠错。

use std::borrow::Cow;

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
    let slots = syllable_hash_slots();
    let bytes = s.as_bytes();
    let mask = slots.len() - 1;
    let mut idx = syllable_hash(bytes) & mask;
    loop {
        let slot = slots[idx];
        if slot == 0 {
            return false;
        }
        if SYLLABLE_TABLE[(slot - 1) as usize].as_bytes() == bytes {
            return true;
        }
        idx = (idx + 1) & mask;
    }
}

fn syllable_hash(bytes: &[u8]) -> usize {
    let mut h: u32 = 0x811c_9dc5;
    for &b in bytes {
        h ^= b as u32;
        h = h.wrapping_mul(0x0100_0193);
    }
    h as usize
}

fn syllable_hash_slots() -> &'static [u16] {
    static SLOTS: std::sync::OnceLock<Vec<u16>> = std::sync::OnceLock::new();
    SLOTS.get_or_init(|| {
        let mut size = 1024usize;
        while size < SYLLABLE_TABLE.len() * 4 {
            size *= 2;
        }
        let mut slots = vec![0u16; size];
        let mask = size - 1;
        for (i, s) in SYLLABLE_TABLE.iter().enumerate() {
            let mut idx = syllable_hash(s.as_bytes()) & mask;
            while slots[idx] != 0 {
                idx = (idx + 1) & mask;
            }
            slots[idx] = (i + 1) as u16;
        }
        slots
    })
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

// 两段式切分器常量（Go session.go）
const MAX_PATHS_PER_CHUNK: usize = 8;

/// 单段声母判定（Go isInitial）：zh/ch/sh 恒真；单字母 a-z 且不为 u/i
/// （v 是 ü 的键盘写法，保留可使 zxcv 这类串走常规回退拿声母单字候选）。
fn is_initial_token(s: &str) -> bool {
    if s == "zh" || s == "ch" || s == "sh" {
        return true;
    }
    if s.len() == 1 {
        let b = s.as_bytes()[0];
        return (b >= b'a' && b <= b'z') && b != b'u' && b != b'i';
    }
    false
}

/// 一段拼音的音节切分（Go tokenizeChunk 两段式镜像）：
///
/// 1. 优先寻找能完整覆盖片段的**纯全拼**音节序列（最长贪心 + dead-end
///    记忆化剪枝）；存在即只返回纯全拼路径——错拼输入（zhonguo）在纯全拼
///    阶段切不出完整路径，patterns 为空交给形态判定走纠错，不会像单段
///    DFS 那样混出 [z,o,n,guo] 碎片假路径。
/// 2. 仅当纯全拼切分不存在时（简拼 zg/bj/jsj、混拼 zhongg/womd/chifl），
///    启用声母简拼约束的混拼切分：完整音节 + zh/ch/sh 双字母 + 单字母
///    声母（同位置有 ≥2 字母音节可匹配时不出单字母碎片）。
///
/// 复杂度：failedFull/failedMixed dead-end 记忆化，杜绝指数回溯。
pub fn tokenize_chunk(input: &str) -> Vec<Vec<Token>> {
    if input.is_empty() {
        return Vec::new();
    }
    let chars: Vec<char> = input.chars().collect();
    let n = chars.len();

    // ── 阶段 1：纯全拼（所有 token 都是完整音节）──
    let mut full_paths: Vec<Vec<Token>> = Vec::new();
    let mut cur_full: Vec<Token> = Vec::new();
    let mut failed_full = vec![false; n + 1];
    dfs_full(&chars, 0, &mut cur_full, &mut full_paths, &mut failed_full);

    if !full_paths.is_empty() {
        // 单字母零声母（a/o/e）本身是独立音节（啊/哦/饿），亦作为该元音
        // 开头音节（an/ai/ao、ou、en/er）的简拼声母（如 x'a 匹配「西安」）。
        if n == 1 && (input == "a" || input == "o" || input == "e") {
            full_paths.push(vec![Token {
                text: input.to_string(),
                is_exact: false,
            }]);
        }
        return full_paths;
    }

    // ── 阶段 2：混拼（完整音节 + zh/ch/sh 双字母 + 单字母声母）──
    let mut mixed_paths: Vec<Vec<Token>> = Vec::new();
    let mut cur_mixed: Vec<Token> = Vec::new();
    let mut failed_mixed = vec![false; n + 1];
    dfs_mixed(
        &chars,
        0,
        &mut cur_mixed,
        &mut mixed_paths,
        &mut failed_mixed,
    );
    mixed_paths
}

fn dfs_full(
    chars: &[char],
    pos: usize,
    cur: &mut Vec<Token>,
    paths: &mut Vec<Vec<Token>>,
    failed: &mut [bool],
) -> bool {
    let n = chars.len();
    if pos == n {
        paths.push(cur.clone());
        return true;
    }
    if paths.len() >= MAX_PATHS_PER_CHUNK {
        return true;
    }
    if failed[pos] {
        return false;
    }
    let max_len = (n - pos).min(MAX_SYLLABLE_LEN);
    let mut any_success = false;
    for l in (1..=max_len).rev() {
        let sub: String = chars[pos..pos + l].iter().collect();
        let canon = canonical_syllable(&sub);
        if is_syllable(&canon) {
            cur.push(Token {
                text: canon.to_string(),
                is_exact: true,
            });
            if dfs_full(chars, pos + l, cur, paths, failed) {
                any_success = true;
            }
            cur.pop();
            if paths.len() >= MAX_PATHS_PER_CHUNK {
                return true;
            }
        }
    }
    if !any_success {
        failed[pos] = true;
    }
    any_success
}

fn dfs_mixed(
    chars: &[char],
    pos: usize,
    cur: &mut Vec<Token>,
    paths: &mut Vec<Vec<Token>>,
    failed: &mut [bool],
) -> bool {
    let n = chars.len();
    if pos == n {
        paths.push(cur.clone());
        return true;
    }
    if paths.len() >= MAX_PATHS_PER_CHUNK {
        return true;
    }
    if failed[pos] {
        return false;
    }
    let max_len = (n - pos).min(MAX_SYLLABLE_LEN);
    let mut any_success = false;
    let mut found_multi_syllable = false;
    for l in (1..=max_len).rev() {
        let sub: String = chars[pos..pos + l].iter().collect();
        if is_syllable(&sub) {
            if l >= 2 {
                found_multi_syllable = true;
            }
            cur.push(Token {
                text: sub,
                is_exact: true,
            });
            if dfs_mixed(chars, pos + l, cur, paths, failed) {
                any_success = true;
            }
            cur.pop();
            if paths.len() >= MAX_PATHS_PER_CHUNK {
                return true;
            }
        }
    }

    // 同位置已有 ≥2 字母音节可匹配时不出碎片（对齐 Go foundMultiSyllable 门控）
    if !found_multi_syllable {
        if pos + 2 <= n {
            let sub: String = chars[pos..pos + 2].iter().collect();
            if sub == "zh" || sub == "ch" || sub == "sh" {
                cur.push(Token {
                    text: sub,
                    is_exact: false,
                });
                if dfs_mixed(chars, pos + 2, cur, paths, failed) {
                    any_success = true;
                }
                cur.pop();
                if paths.len() >= MAX_PATHS_PER_CHUNK {
                    return true;
                }
            }
        }
        if pos + 1 <= n {
            let sub: String = chars[pos..pos + 1].iter().collect();
            if is_initial_token(&sub) {
                cur.push(Token {
                    text: sub,
                    is_exact: false,
                });
                if dfs_mixed(chars, pos + 1, cur, paths, failed) {
                    any_success = true;
                }
                cur.pop();
                if paths.len() >= MAX_PATHS_PER_CHUNK {
                    return true;
                }
            }
        }
    }

    if !any_success {
        failed[pos] = true;
    }
    any_success
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

/// 「完整音节切分」DP 用的音节前缀树：把所有能被 `canonical_syllable`
/// 规范化成合法音节的 1..=6 字母源串建成 26 叉树。
///
/// 为什么不直接逐个子串 `is_syllable(canonical_syllable(sub))`：错拼修正要为
/// 上千个单次编辑候选各做一遍整串 DP，每个位置都要试 1..=6 种子串；哈希查表
/// 仍要算哈希 + 比较，前缀树只走几个数组下标、失配立即停。30+ 字符的整句里
/// 这一项是 O(n²) 候选 × O(n) DP 的主要成本（issue #122）。
struct SyllableTrie {
    /// 每个节点的 26 个子节点下标；u16::MAX 表示没有该字母的子节点。
    next: Vec<[u16; 26]>,
    /// 到该节点为止的串是否能规范化成合法音节。
    terminal: Vec<bool>,
}

fn syllable_trie() -> &'static SyllableTrie {
    static TRIE: std::sync::OnceLock<SyllableTrie> = std::sync::OnceLock::new();
    TRIE.get_or_init(|| {
        let mut trie = SyllableTrie {
            next: vec![[u16::MAX; 26]],
            terminal: vec![false],
        };
        for src in syllable_sources() {
            let mut node = 0usize;
            for b in src.bytes() {
                let slot = (b - b'a') as usize;
                let child = trie.next[node][slot];
                node = if child == u16::MAX {
                    let idx = trie.next.len();
                    assert!(idx < u16::MAX as usize, "音节表过大，前缀树下标溢出");
                    trie.next.push([u16::MAX; 26]);
                    trie.terminal.push(false);
                    trie.next[node][slot] = idx as u16;
                    idx
                } else {
                    child as usize
                };
            }
            trie.terminal[node] = true;
        }
        trie
    })
}

/// 前缀树要收录的全部源串：音节表本身 + `canonical_syllable` 的三类别名
/// 写法（lue/nue/xion/jion/qion、省写韵母 uei/iou/uen、j/q/x/y 的 v 写法）。
/// 收录条件统一用 `is_syllable(canonical_syllable(src))` 复核，保证与
/// 「规范化后是合法音节」严格等价。
fn syllable_sources() -> Vec<String> {
    use std::collections::BTreeSet;

    let mut out: BTreeSet<String> = BTreeSet::new();
    for s in SYLLABLE_TABLE {
        out.insert((*s).to_string());
    }
    let mut add = |src: String| {
        if is_syllable(&canonical_syllable(&src)) {
            out.insert(src);
        }
    };
    // 1) canonical_syllable 的逐字别名
    for s in ["lue", "nue", "xion", "jion", "qion"] {
        add(s.to_string());
    }
    for s in SYLLABLE_TABLE {
        // 2) 省写韵母反向：ui→uei / iu→iou / un→uen（源串至少 4 字符）
        if s.len() >= 4 {
            for (short, long) in [("ui", "uei"), ("iu", "iou"), ("un", "uen")] {
                if let Some(head) = s.strip_suffix(short) {
                    add(format!("{head}{long}"));
                }
            }
        }
        // 3) ü 的 v 写法：j/q/x/y 开头、把任意非空子集的 u 换成 v
        if matches!(s.as_bytes()[0], b'j' | b'q' | b'x' | b'y') && s.contains('u') {
            let u_pos: Vec<usize> = s.match_indices('u').map(|(i, _)| i).collect();
            for mask in 1u32..(1u32 << u_pos.len()) {
                let mut bytes = s.as_bytes().to_vec();
                for (bit, pos) in u_pos.iter().enumerate() {
                    if mask & (1 << bit) != 0 {
                        bytes[*pos] = b'v';
                    }
                }
                if let Ok(src) = String::from_utf8(bytes) {
                    add(src);
                }
            }
        }
    }
    out.into_iter().collect()
}

/// 判断一个字符串能否完整切分为标准音节。
///
/// 语义与逐个子串 `is_syllable(canonical_syllable(sub))` 完全一致，只是改用
/// 前缀树走一遍：从每个可达位置出发沿树走，走到音节终点就标记对应位置可达。
pub fn has_clean_syllable_split(s: &str) -> bool {
    let bytes = s.as_bytes();
    let len = bytes.len();
    if len == 0 {
        return false;
    }
    let trie = syllable_trie();
    let mut dp = vec![false; len + 1];
    dp[0] = true;
    for i in 0..len {
        if !dp[i] {
            continue;
        }
        let mut node = 0usize;
        for (j, &b) in bytes[i..].iter().take(MAX_SYLLABLE_LEN).enumerate() {
            if !b.is_ascii_lowercase() {
                break;
            }
            let child = trie.next[node][(b - b'a') as usize];
            if child == u16::MAX {
                break;
            }
            node = child as usize;
            if trie.terminal[node] {
                dp[i + j + 1] = true;
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
///   - 切分路径为空（zhonguo 两段式纯全拼/混拼都切不出完整路径）→ 手误；
///   - 任一条切分路径里出现完整音节（≥2 字母）→ 信任为正常混拼；
///   - 短串（≤4 字母，如 zg/bj/jsj）一律信任。
pub fn misspelled_from_patterns(s: &str, patterns: &[Vec<Token>]) -> bool {
    if s.is_empty() {
        return false;
    }
    if all_initial_patterns(patterns) {
        return false;
    }
    if has_clean_full_split(patterns) {
        return false;
    }
    if has_common_mistake_cluster(s) {
        return true;
    }
    if patterns.is_empty() {
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

    /// 哈希查表版 `is_syllable` 必须与音节表（二分查找）逐值等价。
    #[test]
    fn test_is_syllable_hash_matches_table() {
        let brute = |s: &str| SYLLABLE_TABLE.binary_search(&s).is_ok();
        for s in SYLLABLE_TABLE {
            assert!(is_syllable(s), "{s} 应在音节表里");
            assert!(brute(s));
        }
        for a in b'a'..=b'z' {
            for b in b'a'..=b'z' {
                let s = String::from_utf8(vec![a, b]).unwrap();
                assert_eq!(is_syllable(&s), brute(&s), "两字母 {s}");
            }
        }
        // 三字母空间（26^3 = 17576）全量比对，覆盖大量非音节负例。
        for a in b'a'..=b'z' {
            for b in b'a'..=b'z' {
                for c in b'a'..=b'z' {
                    let s = String::from_utf8(vec![a, b, c]).unwrap();
                    assert_eq!(is_syllable(&s), brute(&s), "三字母 {s}");
                }
            }
        }
        assert!(!is_syllable(""));
        assert!(!is_syllable("NIHAO"));
        assert!(!is_syllable("zhonguo"));
    }

    /// 前缀树版 `has_clean_syllable_split` 必须与旧的逐子串规范判定完全等价。
    #[test]
    fn test_has_clean_syllable_split_matches_reference() {
        fn reference(s: &str) -> bool {
            let len = s.len();
            if len == 0 {
                return false;
            }
            let mut dp = vec![false; len + 1];
            dp[0] = true;
            for i in 1..=len {
                for l in 1..=MAX_SYLLABLE_LEN.min(i) {
                    if dp[i - l] && is_syllable(&canonical_syllable(&s[i - l..i])) {
                        dp[i] = true;
                        break;
                    }
                }
            }
            dp[len]
        }

        let mut cases: Vec<String> = Vec::new();
        for s in SYLLABLE_TABLE {
            cases.push((*s).to_string());
        }
        for s in syllable_sources() {
            cases.push(s);
        }
        // 1..=3 字母全量
        for a in b'a'..=b'z' {
            cases.push(String::from_utf8(vec![a]).unwrap());
            for b in b'a'..=b'z' {
                cases.push(String::from_utf8(vec![a, b]).unwrap());
                for c in b'a'..=b'z' {
                    cases.push(String::from_utf8(vec![a, b, c]).unwrap());
                }
            }
        }
        // 省写韵母的 4..=6 字母源串全量（canonical_syllable 的另一条特判路径）
        for suffix in ["uei", "iou", "uen"] {
            for a in b'a'..=b'z' {
                for b in b'a'..=b'z' {
                    cases.push(format!("{}{}{}", a as char, b as char, suffix));
                    for c in b'a'..=b'z' {
                        cases.push(format!("{}{}{}{}", a as char, b as char, c as char, suffix));
                    }
                }
            }
        }
        for s in &cases {
            assert_eq!(
                has_clean_syllable_split(s),
                reference(s),
                "前缀树判定与基准不一致: {s}"
            );
        }
        assert!(has_clean_syllable_split("shuei"));
        assert!(has_clean_syllable_split("jvan"));
        assert!(has_clean_syllable_split("zhuangtaizhuangtai"));
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
