//! Chaoset IME 纯 Rust 高性能、零 GC 输入法引擎核心库。

pub mod dict;
pub mod fuzzy;
pub mod punct;
pub mod s2t;
pub mod session;
pub mod shuangpin;
pub mod syllable;
pub mod typo;
pub mod userdict;

pub use dict::{Dictionary, WordEntry};
pub use session::{Candidate, Composition, Session, PAGE_SIZE};
pub use shuangpin::ShuangpinScheme;
pub use syllable::is_syllable;
pub use userdict::{UserDict, UserStats};
