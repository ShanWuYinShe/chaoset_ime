//! daemon 响应 DTO（字段全部 `#[serde(default)]`，兼容旧/新 daemon）。

#[derive(Debug, Clone, Default, serde::Deserialize)]
pub struct Candidate {
    #[serde(default)] pub text: String,
    #[serde(default)] pub syllables: String,
    #[serde(default)] pub score: i64,
    #[serde(default)] pub from_user: bool,
}

#[derive(Debug, Clone, Default, serde::Deserialize)]
pub struct Composition {
    #[serde(default)] pub input: String,
    #[serde(default)] pub preedit: String,
    #[serde(default)] pub candidates: Vec<Candidate>,
    #[serde(default)] pub page: u32,
    #[serde(default)] pub page_count: u32,
    #[serde(default)] pub committed: String,
    #[serde(default)] pub shuangpin: String,
    #[serde(default)] pub dp_pending: String,
}

#[derive(Debug, Clone, Default, serde::Deserialize)]
pub struct UserStats {
    #[serde(default)] pub words: u64,
    #[serde(default)] pub phrases: u64,
    #[serde(default)] pub bigrams: u64,
}
