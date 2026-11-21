//! daemon 热路径用：只关心 fuzzy/shuangpin，**永不失败**。
//!
//! 与 CLI 的 strict `store::load()` 分开：存储损坏/权限/平台差异都不能让
//! daemon 起不来（用户侧表现为打不出字）。读失败时按缺省（模糊音关、全拼）。

use crate::model::Shuangpin;
use crate::parse;
use crate::store;

#[derive(Default, Debug, Clone, PartialEq, Eq)]
pub struct InputBehavior {
    pub fuzzy: bool,
    /// "" = 全拼；zrm / mspy / flypy。
    pub shuangpin: String,
}

/// daemon 启动读取：任何错误 → 缺省值，绝不 panic/报错。
pub fn load() -> InputBehavior {
    let s = store::load().unwrap_or_default();
    InputBehavior { fuzzy: s.fuzzy, shuangpin: s.shuangpin.as_str().to_string() }
}

/// 归一化双拼方案名（与存储归一化同口径，保留给 daemon 日志/测试）。
pub fn normalize_shuangpin(raw: &str) -> String {
    parse::normalize_shuangpin(raw).as_str().to_string()
}

/// 旧 Go 名称保留：存储中的双拼字符串归一化结果。
pub fn parse_stored_shuangpin(raw: &str) -> Shuangpin {
    parse::normalize_shuangpin(raw)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_is_lenient() {
        assert_eq!(normalize_shuangpin(" ZRM "), "zrm");
        assert_eq!(normalize_shuangpin("bogus"), "");
    }

    #[test]
    fn load_never_fails() {
        let b = load();
        let _ = b.fuzzy;
        let _ = b.shuangpin;
    }
}
