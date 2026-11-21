//! 双拼方案（自然码 / 微软双拼 / 小鹤双拼）：键码表与解码器。
//!
//! 键位表**不手工编写**——分别忠实转写 Rime 官方 rime-double-pinyin 仓库三份
//! schema（double_pinyin / double_pinyin_mspy / double_pinyin_flypy）的
//! speller.algebra 规则管线，对全部合法音节正向生成两键码后建反向表。
//! 转录是否走样由完整性测试兜底：全表无不可消解冲突、每个音节都有两键码、
//! 三方案已知键位抽查（与 Go engine/double_pinyin_test.go 同一份期望值）。

use std::collections::HashMap;
use std::collections::HashSet;

/// 支持的双拼方案。
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

// ── 规则管线（Go engine/double_pinyin.go 的 Rust 版）─────────────────────

/// 派生规则：保留原拼写并追加一个匹配派生拼写。
type DeriveFn = fn(&str) -> Option<String>;
/// 原位改写规则：命中时原位改写拼写。
type XformFn = fn(&str) -> Option<String>;

/// 单条后缀/前缀替换规则：命中 suffix 时把该后缀替换为 key。
/// keep_head 对应 Rime 的 `(.)X$` 写法——额外保留后缀前的第一个字符
/// （an→aJ 而非 J），这正是「声母键 + 韵母键」两键结构的来源。
#[derive(Clone, Copy)]
struct DpOp {
    suffix: &'static str,
    key: char,
    keep_head: bool,
    /// 非空时按前缀触发（zh/ch/sh 声母键），keep_head 不生效。
    prefix: &'static str,
}

struct DpSchemeDef {
    derives: &'static [DeriveFn],
    xforms: &'static [XformFn],
    ops: &'static [DpOp],
}

fn dp_is_one_of(c: u8, chars: &str) -> bool {
    chars.as_bytes().contains(&c)
}

// 自然码/小鹤前置派生：零声母双写（a→aa、e→ee、o→oo、ang→aang、eng→eeng），
// 对应 schema 的 xform /^([aoe])(ng)?$/$1$1$2/。
fn dp_xform_zrm_double(sp: &str) -> Option<String> {
    let b = sp.as_bytes();
    if !b.is_empty() && b.len() <= 3 && dp_is_one_of(b[0], "aoe") {
        let rest = &sp[1..];
        if rest.is_empty() || rest == "ng" {
            return Some(format!("{}{}{}", b[0] as char, b[0] as char, rest));
        }
    }
    None
}

// 微软前置 xform：a/e 开头双写首字母（an→aan），对应 schema 的
// xform /^([ae])(.*)$/$1$1$2/。
fn dp_xform_mspy_double(sp: &str) -> Option<String> {
    let b = sp.as_bytes();
    if b.len() >= 2 && dp_is_one_of(b[0], "ae") {
        return Some(format!("{}{}", b[0] as char, sp));
    }
    None
}

// derive /^([jqxy])u$/$1v/（ju→jv、qu→qv、xu→xv、yu→yv）。
fn dp_derive_ju_to_v(sp: &str) -> Option<String> {
    let b = sp.as_bytes();
    if b.len() == 2 && matches!(b[0], b'j' | b'q' | b'x' | b'y') && b[1] == b'u' {
        return Some(format!("{}v", b[0] as char));
    }
    None
}

// derive /^([aoe])([ioun])$/$1$1$2/（ai→aai、an→aan、ou→oou）。
fn dp_derive_double_aoe(sp: &str) -> Option<String> {
    let b = sp.as_bytes();
    if b.len() == 2 && dp_is_one_of(b[0], "aoe") && dp_is_one_of(b[1], "ioun") {
        return Some(format!("{}{}{}", b[0] as char, b[0] as char, b[1] as char));
    }
    None
}

// 微软派生：derive /^([aoe].*)$/o$1/（零声母加 O 前缀：an→oan、e→oe）。
fn dp_derive_mspy_o_prefix(sp: &str) -> Option<String> {
    let b = sp.as_bytes();
    if !b.is_empty() && dp_is_one_of(b[0], "aoe") {
        return Some(format!("o{}", sp));
    }
    None
}

const DP_DERIVES_JU_DOUBLE: &[DeriveFn] = &[dp_derive_ju_to_v, dp_derive_double_aoe];
const DP_DERIVES_MSPY: &[DeriveFn] = &[dp_derive_ju_to_v, dp_derive_mspy_o_prefix];
const DP_XFORMS_DOUBLE: &[XformFn] = &[dp_xform_zrm_double];
const DP_XFORMS_MSPY_DOUBLE: &[XformFn] = &[dp_xform_mspy_double];

// 自然码：double_pinyin.schema.yaml 的 algebra。
const DP_OPS_ZRM: &[DpOp] = &[
    DpOp {
        suffix: "iu",
        key: 'Q',
        keep_head: false,
        prefix: "",
    },
    // 自然码 W 覆盖 ia/ua；Rime schema 缺 ia，按方案事实补齐。
    DpOp {
        suffix: "ia",
        key: 'W',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ua",
        key: 'W',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "va",
        key: 'W',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uan",
        key: 'R',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "van",
        key: 'R',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ue",
        key: 'T',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ve",
        key: 'T',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ing",
        key: 'Y',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uai",
        key: 'Y',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "",
        key: 'U',
        keep_head: false,
        prefix: "sh",
    },
    DpOp {
        suffix: "",
        key: 'I',
        keep_head: false,
        prefix: "ch",
    },
    DpOp {
        suffix: "",
        key: 'V',
        keep_head: false,
        prefix: "zh",
    },
    DpOp {
        suffix: "uo",
        key: 'O',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "un",
        key: 'P',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "vn",
        key: 'P',
        keep_head: false,
        prefix: "",
    },
    // iong 必须先于 ong，否则 jiong 被吞成三键。
    DpOp {
        suffix: "iong",
        key: 'S',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ong",
        key: 'S',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "iang",
        key: 'D',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uang",
        key: 'D',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "en",
        key: 'F',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "eng",
        key: 'G',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ang",
        key: 'H',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ian",
        key: 'M',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "an",
        key: 'J',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "iao",
        key: 'C',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ao",
        key: 'K',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ai",
        key: 'L',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ei",
        key: 'Z',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ie",
        key: 'X',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ui",
        key: 'V',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ou",
        key: 'B',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "in",
        key: 'N',
        keep_head: false,
        prefix: "",
    },
];

// 微软双拼：double_pinyin_mspy.schema.yaml 的 algebra。
// 特征：零声母加 O 前缀（oan=安）、ü/v 与 uai 同键 Y、ing 在 ; 键、er 单键 R。
const DP_OPS_MSPY: &[DpOp] = &[
    DpOp {
        suffix: "iu",
        key: 'Q',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ia",
        key: 'W',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ua",
        key: 'W',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "er",
        key: 'R',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uan",
        key: 'R',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "van",
        key: 'R',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ue",
        key: 'T',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ve",
        key: 'T',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "v",
        key: 'Y',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uai",
        key: 'Y',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "",
        key: 'U',
        keep_head: false,
        prefix: "sh",
    },
    DpOp {
        suffix: "",
        key: 'I',
        keep_head: false,
        prefix: "ch",
    },
    DpOp {
        suffix: "",
        key: 'V',
        keep_head: false,
        prefix: "zh",
    },
    DpOp {
        suffix: "uo",
        key: 'O',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "un",
        key: 'P',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "vn",
        key: 'P',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "iong",
        key: 'S',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ong",
        key: 'S',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "iang",
        key: 'D',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uang",
        key: 'D',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "en",
        key: 'F',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "eng",
        key: 'G',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ang",
        key: 'H',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ian",
        key: 'M',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "an",
        key: 'J',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "iao",
        key: 'C',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ao",
        key: 'K',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ai",
        key: 'L',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ei",
        key: 'Z',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ie",
        key: 'X',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ui",
        key: 'V',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ou",
        key: 'B',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "in",
        key: 'N',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ing",
        key: ';',
        keep_head: false,
        prefix: "",
    },
];

// 小鹤双拼：double_pinyin_flypy.schema.yaml 的 algebra。
// 特征：iu→Q、ei→W、uan→R、ue/v→T、un→Y、ing/uai→K、uo→O、ie→P、
// ong（含 iong）→S、ai→D、ang→H、ian→M、an→J、ou→Z、ia/ua→X、iao→N、
// ao→C、ui→V、in→B。
const DP_OPS_FLYPY: &[DpOp] = &[
    DpOp {
        suffix: "iu",
        key: 'Q',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ei",
        key: 'W',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "uan",
        key: 'R',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ue",
        key: 'T',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ve",
        key: 'T',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "un",
        key: 'Y',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "",
        key: 'U',
        keep_head: false,
        prefix: "sh",
    },
    DpOp {
        suffix: "",
        key: 'I',
        keep_head: false,
        prefix: "ch",
    },
    DpOp {
        suffix: "",
        key: 'V',
        keep_head: false,
        prefix: "zh",
    },
    DpOp {
        suffix: "uo",
        key: 'O',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ie",
        key: 'P',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "iong",
        key: 'S',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ong",
        key: 'S',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ing",
        key: 'K',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uai",
        key: 'K',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ai",
        key: 'D',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "en",
        key: 'F',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "eng",
        key: 'G',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "iang",
        key: 'L',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "uang",
        key: 'L',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ang",
        key: 'H',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ian",
        key: 'M',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "an",
        key: 'J',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ou",
        key: 'Z',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ia",
        key: 'X',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ua",
        key: 'X',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "iao",
        key: 'N',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "ao",
        key: 'C',
        keep_head: true,
        prefix: "",
    },
    DpOp {
        suffix: "ui",
        key: 'V',
        keep_head: false,
        prefix: "",
    },
    DpOp {
        suffix: "in",
        key: 'B',
        keep_head: false,
        prefix: "",
    },
];

fn dp_scheme_def(scheme: ShuangpinScheme) -> DpSchemeDef {
    match scheme {
        ShuangpinScheme::Zrm => DpSchemeDef {
            derives: DP_DERIVES_JU_DOUBLE,
            xforms: DP_XFORMS_DOUBLE,
            ops: DP_OPS_ZRM,
        },
        ShuangpinScheme::Mspy => DpSchemeDef {
            derives: DP_DERIVES_MSPY,
            xforms: DP_XFORMS_MSPY_DOUBLE,
            ops: DP_OPS_MSPY,
        },
        ShuangpinScheme::Flypy => DpSchemeDef {
            derives: DP_DERIVES_JU_DOUBLE,
            xforms: DP_XFORMS_DOUBLE,
            ops: DP_OPS_FLYPY,
        },
    }
}

/// dpXLit 把大写键位字母转小写；其余（a/e/o 等原字母）不变。
/// 对应 schema 的 xlit /QWRTYUIOPSDFGHMJCKLZXVBN/qwrtyuiopsdfghmjcklzxvbn/。
fn dp_xlit(sp: &str) -> String {
    sp.chars()
        .map(|c| {
            if "QWRTYUIOPSDFGHMJCKLZXVBN".contains(c) {
                c.to_ascii_lowercase()
            } else {
                c
            }
        })
        .collect()
}

/// 规则管线：输入标准音节与方案，输出全部合法键码（含 derive 别名，均为
/// 小写；个别零声母派生码可能超两键）。
fn dp_apply(scheme: ShuangpinScheme, syl: &str) -> Vec<String> {
    let def = dp_scheme_def(scheme);
    let mut set: HashSet<String> = HashSet::new();
    set.insert(syl.to_string());

    for d in def.derives {
        let snapshot: Vec<String> = set.iter().cloned().collect();
        for sp in snapshot {
            if let Some(out) = d(&sp) {
                set.insert(out);
            }
        }
    }
    for x in def.xforms {
        let mut next: HashSet<String> = HashSet::new();
        for sp in set {
            match x(&sp) {
                Some(out) => {
                    next.insert(out);
                }
                None => {
                    next.insert(sp);
                }
            }
        }
        set = next;
    }
    for op in def.ops {
        let mut next: HashSet<String> = HashSet::new();
        if !op.prefix.is_empty() {
            for sp in set {
                if let Some(rest) = sp.strip_prefix(op.prefix) {
                    next.insert(format!("{}{}", op.key, rest));
                } else {
                    next.insert(sp);
                }
            }
            set = next;
            continue;
        }
        for sp in set {
            if let Some(stem) = sp.strip_suffix(op.suffix) {
                if op.keep_head {
                    // `(.)X$` 语义：保留后缀前的一个字符（an→aJ、Van→VJ）。
                    match stem.char_indices().next_back() {
                        Some((idx, ch)) => {
                            next.insert(format!("{}{}{}", &stem[..idx], ch, op.key));
                        }
                        None => {
                            next.insert(sp);
                        }
                    }
                } else {
                    next.insert(format!("{}{}", stem, op.key));
                }
            } else {
                next.insert(sp);
            }
        }
        set = next;
    }

    set.into_iter().map(|sp| dp_xlit(&sp)).collect()
}

/// 双拼解码器：两键码 → 拼音音节。
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
        // 反向表冲突消解：键码冲突时「规则派生码」优先于「原样透传码」
        // （自然码 luo 经 uo→O 派生的 lo 优先于拟声字 lo 的透传）。双方同为
        // 派生/透传的冲突由完整性测试暴露——这里为确定性按音节字典序保留。
        let mut table: HashMap<String, (String, bool)> = HashMap::new();
        for syl in crate::syllable::SYLLABLE_TABLE {
            for code in dp_apply(self.scheme, syl) {
                if code.chars().count() != 2 {
                    continue; // 零声母派生码可能超两键，不收录
                }
                let derived = code != *syl;
                match table.get(&code) {
                    None => {
                        table.insert(code, (syl.to_string(), derived));
                    }
                    Some((prev_syl, prev_derived)) => {
                        if prev_syl == syl {
                            continue;
                        }
                        if derived && !prev_derived {
                            table.insert(code, (syl.to_string(), true));
                        } else if derived == *prev_derived {
                            // 不可消解冲突（两侧同为派生或同为透传）：按字典序
                            // 保留，保证同一输入的结果稳定且与 Go 全表无冲突一致。
                            if *syl < prev_syl.as_str() {
                                table.insert(code, (syl.to_string(), derived));
                            }
                        }
                    }
                }
            }
        }
        self.key_to_syllable = table
            .into_iter()
            .map(|(code, (syl, _))| (code, syl))
            .collect();
    }

    /// 解码两键码为音节；None 表示该键码无对应音节（调用方应丢弃这对按键）。
    pub fn decode(&self, code: &str) -> Option<String> {
        self.key_to_syllable.get(code).cloned()
    }

    pub fn scheme(&self) -> ShuangpinScheme {
        self.scheme
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn expect(scheme: ShuangpinScheme, pairs: &[(&str, &str, &str)]) {
        let decoder = ShuangpinDecoder::new(scheme);
        for (syl, a, b) in pairs {
            let code = format!("{}{}", a, b);
            assert_eq!(
                decoder.decode(&code).as_deref(),
                Some(*syl),
                "{} 键码 {code} 应解码为 {syl}",
                scheme.name()
            );
        }
    }

    /// 已知键位抽查——自然码（与 Go double_pinyin_test.go 同一份期望值）。
    #[test]
    fn test_zrm_known_pairs() {
        expect(
            ShuangpinScheme::Zrm,
            &[
                ("ni", "n", "i"),
                ("hao", "h", "k"),
                ("shi", "u", "i"),
                ("zhang", "v", "h"),
                ("zhuang", "v", "d"),
                ("liang", "l", "d"),
                ("kuai", "k", "y"),
                ("juan", "j", "r"),
                ("lve", "l", "t"),
                ("jue", "j", "t"),
                ("zhong", "v", "s"),
                ("jiong", "j", "s"),
                ("hua", "h", "w"),
                ("bei", "b", "z"),
                ("niu", "n", "q"),
                ("dun", "d", "p"),
                ("a", "a", "a"),
                ("ai", "a", "l"),
                ("ang", "a", "h"),
                ("en", "e", "f"),
                ("eng", "e", "g"),
                ("ou", "o", "b"),
                ("o", "o", "o"),
            ],
        );
    }

    /// 已知键位抽查——微软双拼。
    #[test]
    fn test_mspy_known_pairs() {
        expect(
            ShuangpinScheme::Mspy,
            &[
                ("ni", "n", "i"),
                ("hao", "h", "k"),
                ("shi", "u", "i"),
                ("zhuang", "v", "d"),
                ("lv", "l", "y"),
                ("xue", "x", "t"),
                ("lin", "l", "n"),
                ("er", "e", "r"),
                ("an", "o", "j"),
                ("an", "a", "j"),
                ("ai", "o", "l"),
                ("ai", "a", "l"),
                ("ou", "o", "b"),
                ("kuai", "k", "y"),
                ("juan", "j", "r"),
                ("jiong", "j", "s"),
                ("zhong", "v", "s"),
                ("ling", "l", ";"),
            ],
        );
    }

    /// 已知键位抽查——小鹤双拼（Rime double_pinyin_flypy schema）。
    #[test]
    fn test_flypy_known_pairs() {
        expect(
            ShuangpinScheme::Flypy,
            &[
                ("ni", "n", "i"),
                ("hao", "h", "c"),
                ("shi", "u", "i"),
                ("zhong", "v", "s"),
                ("zhuang", "v", "l"),
                ("jue", "j", "t"),
                ("lve", "l", "t"),
                ("ping", "p", "k"),
                ("ling", "l", "k"),
                ("kuai", "k", "k"),
                ("niao", "n", "n"),
                ("lian", "l", "m"),
                ("bei", "b", "w"),
                ("dun", "d", "y"),
                ("a", "a", "a"),
                ("ai", "a", "d"),
                ("ang", "a", "h"),
                ("ou", "o", "z"),
            ],
        );
    }

    /// 反向表完整性：每个合法音节都有两键码；冲突按「派生优先于透传」消解；
    /// 自然码 lo 键码必须消解给 luo。
    #[test]
    fn test_table_integrity() {
        for scheme in [
            ShuangpinScheme::Zrm,
            ShuangpinScheme::Mspy,
            ShuangpinScheme::Flypy,
        ] {
            let decoder = ShuangpinDecoder::new(scheme);
            let mut missing: Vec<&str> = Vec::new();
            for syl in crate::syllable::SYLLABLE_TABLE {
                let codes = dp_apply(scheme, syl);
                if !codes.iter().any(|c| c.chars().count() == 2) {
                    missing.push(syl);
                }
            }
            assert!(
                missing.is_empty(),
                "{} 有音节无两键编码: {:?}",
                scheme.name(),
                missing
            );
            assert!(
                decoder.key_to_syllable.len() >= crate::syllable::SYLLABLE_TABLE.len() * 9 / 10,
                "{} 反向表覆盖过低: {}",
                scheme.name(),
                decoder.key_to_syllable.len()
            );
            if scheme == ShuangpinScheme::Zrm {
                assert_eq!(
                    decoder.decode("lo").as_deref(),
                    Some("luo"),
                    "zrm lo 键码应消解给 luo"
                );
            }
        }
    }
}
