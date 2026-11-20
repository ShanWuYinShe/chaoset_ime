//! 中文标点映射、双引号/单引号成对匹配与半角回绕支持。

pub fn punct_map(c: char) -> Option<&'static str> {
    match c {
        ',' => Some("，"),
        '.' => Some("。"),
        ';' => Some("；"),
        ':' => Some("："),
        '?' => Some("？"),
        '!' => Some("！"),
        '(' => Some("（"),
        ')' => Some("）"),
        '[' => Some("【"),
        ']' => Some("】"),
        '<' => Some("《"),
        '>' => Some("》"),
        '\\' => Some("、"),
        '~' => Some("～"),
        '^' => Some("……"),
        _ => None,
    }
}

pub fn is_punct_key(c: char) -> bool {
    punct_map(c).is_some() || c == '"' || c == '\''
}

pub struct PunctState {
    pub double_quote_open: bool,
    pub single_quote_open: bool,
    pub last_punct: Option<char>,
    pub last_punct_count: usize,
}

impl Default for PunctState {
    fn default() -> Self {
        Self {
            double_quote_open: false,
            single_quote_open: false,
            last_punct: None,
            last_punct_count: 0,
        }
    }
}

impl PunctState {
    pub fn process(&mut self, key: char, half_width: bool) -> String {
        if half_width {
            return key.to_string();
        }

        match key {
            '"' => {
                let out = if self.double_quote_open { "”" } else { "“" };
                self.double_quote_open = !self.double_quote_open;
                self.last_punct = Some('"');
                self.last_punct_count = 1;
                out.to_string()
            }
            '\'' => {
                let out = if self.single_quote_open { "’" } else { "‘" };
                self.single_quote_open = !self.single_quote_open;
                self.last_punct = Some('\'');
                self.last_punct_count = 1;
                out.to_string()
            }
            '.' => {
                if self.last_punct == Some('.') {
                    self.last_punct_count += 1;
                    if self.last_punct_count % 2 == 0 {
                        ".".to_string()
                    } else {
                        "。".to_string()
                    }
                } else {
                    self.last_punct = Some('.');
                    self.last_punct_count = 1;
                    "。".to_string()
                }
            }
            ',' => {
                if self.last_punct == Some(',') {
                    self.last_punct_count += 1;
                    if self.last_punct_count % 2 == 0 {
                        ",".to_string()
                    } else {
                        "，".to_string()
                    }
                } else {
                    self.last_punct = Some(',');
                    self.last_punct_count = 1;
                    "，".to_string()
                }
            }
            other => {
                self.last_punct = Some(other);
                self.last_punct_count = 1;
                punct_map(other).unwrap_or("").to_string()
            }
        }
    }

    pub fn reset(&mut self) {
        self.double_quote_open = false;
        self.single_quote_open = false;
        self.last_punct = None;
        self.last_punct_count = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_punct_quotes_and_cycles() {
        let mut st = PunctState::default();
        assert_eq!(st.process('"', false), "“");
        assert_eq!(st.process('"', false), "”");
        assert_eq!(st.process('\'', false), "‘");
        assert_eq!(st.process('\'', false), "’");

        assert_eq!(st.process('.', false), "。");
        assert_eq!(st.process('.', false), ".");
        assert_eq!(st.process('.', false), "。");

        assert_eq!(st.process(',', false), "，");
        assert_eq!(st.process(',', false), ",");
    }
}
