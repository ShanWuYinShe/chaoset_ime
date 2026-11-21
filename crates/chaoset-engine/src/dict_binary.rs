//! 发布用二进制词库（CSDB v1）的编译、加载与同目录缓存。
//!
//! 仓库里只保留 TSV 词库源（`data/dict_common.tsv`，由 `cmd/chaoset-dictgen`
//! 生成）；打包阶段用 `bin/dictc.rs` 把它编译成本模块定义的二进制文件，随发行包
//! 与 TSV 一起分发（导入、审阅、种子仍用 TSV）。
//!
//! # 文件格式（CSDB v1，整数全部小端）
//!
//! ```text
//! 偏移    类型      含义
//! 0       [u8; 4]   magic = b"CSDB"
//! 4       u8        格式版本，当前恒为 1
//! 5       u16 LE    音节表项数 S
//! 7       S × 项    每项：u8 字节长 + UTF-8 音节
//! ...     u32 LE    词条数 N
//! ...     N × 词条  每词条：
//!                     u8 word 字节长 + word UTF-8
//!                     u8 音节数 K
//!                     K × u16 LE 音节表下标
//!                     u16 LE 词频
//! ```
//!
//! # 设计取舍
//!
//! * **可复现**：音节表只收录词条实际用到的音节，按**首次出现顺序**编号
//!   （不排序、不依赖 HashMap 迭代顺序），同一份 TSV 两次编译逐字节一致，
//!   发布产物可复现、可对比。
//! * **单遍顺序**：写入是单遍顺序字节流（不 seek、不回填）；解析也是单遍顺序
//!   推进，且解析前按头部计数用 `Vec::with_capacity` 精确预分配（20 万条规模
//!   下不允许靠 Vec 倍增扩容产生大块拷贝）。
//! * **解析原子性**：词条先解析到暂存 Vec，magic/版本/截断/非法音节下标/尾部
//!   多余字节全部校验通过后才提交到 `Dictionary`。这样坏缓存不会留下半加载
//!   状态，`load_with_cache` 可以安全回退到源文件。
//! * **不静默降级**：词频字段是 u16，TSV 里若出现超出 `u16::MAX` 的词频，编译
//!   直接报 `InvalidInput` 而不是截断——发布产物必须可信；损坏的二进制词库
//!   一律 `InvalidData` 报错，绝不静默跳过。
//! * 本格式随发行包分发，**改动布局必须升 `FORMAT_VERSION`**，不得在原版本上
//!   改字段含义。
//!
//! # 加载策略
//!
//! `load_auto` 只读文件头 4 字节判断格式：`CSDB` 走 `load_binary`（整体一次性
//! 读入），其余交给原来的 `load_tsv` 流式解析，不会把同一个文件整读两遍。
//! 因此 daemon 的 `-dict` / `-seeddict` / 默认词库传 `.tsv` 或 `.bin` 都能用。
//!
//! # 二进制缓存（`load_with_cache`）
//!
//! 发布包同时带 TSV 源与预编译 `.bin`；daemon 启动参数保持不变（仍指向
//! `.tsv`），由 `load_with_cache` 优先使用「同目录同 stem 的 `.bin`」缓存：
//!
//! 1. 传入路径本身是 `.bin`（扩展名）或文件头是 `CSDB` → 等价 `load_auto`，
//!    不参与缓存语义；
//! 2. 否则在 `path.with_extension("bin")` 找缓存：缓存存在且 mtime ≥ 源
//!    （源不存在时视为可用）→ 直接加载缓存；
//! 3. 缓存缺失 / 过期 / 损坏 → 加载源文件，并**尽力**把本次新增词条写回缓存
//!    （同目录临时文件 + rename；目录只读、无权限、磁盘满只打警告，不影响
//!    加载结果）。
//!
//! 用户导入词库只会重写 TSV（mtime 变新），daemon 下次启动即自动重建 `.bin`；
//! 三端安装脚本 / LaunchAgent / Run 项 / 引擎参数都不必感知缓存的存在。

use crate::dict::{Dictionary, WordEntry};
use std::collections::HashMap;
use std::fs::File;
use std::io::{self, BufWriter, Read, Write};
use std::path::Path;

/// 二进制词库文件 magic（判定文件类型的唯一依据）。
pub const MAGIC: [u8; 4] = *b"CSDB";
/// 当前二进制词库格式版本。
pub const FORMAT_VERSION: u8 = 1;

impl Dictionary {
    /// 把当前 `entries` 编译成二进制词库写到 `path`（发布打包阶段用，见
    /// `bin/dictc.rs`）。
    ///
    /// 写出顺序即 `entries` 顺序，单遍顺序流、不做 seek/回填；音节表按首次
    /// 出现顺序去重编号。条目或字段超出 CSDB v1 的定长编码上限时报
    /// `InvalidInput`（不截断）。
    pub fn write_binary<P: AsRef<Path>>(&self, path: P) -> io::Result<()> {
        write_binary_entries(&self.entries, path.as_ref())
    }

    /// 从二进制词库（CSDB）加载词条。
    ///
    /// 语义与 `load_tsv` 一致：entries 顺序与 TSV 源相同，加载完成同样调用
    /// `sort_nodes()`，返回成功解析的词条数。二进制文件整体一次性读入后单遍
    /// 解析，entries 按头部词条数精确预分配；解析失败时词典保持原状。
    pub fn load_binary<P: AsRef<Path>>(&mut self, path: P) -> io::Result<usize> {
        let data = std::fs::read(path)?;
        self.parse_binary(&data)
    }

    /// 按文件头自动识别词库格式：`CSDB` 走 `load_binary`，其它走 `load_tsv`。
    ///
    /// 只读文件头 4 字节做判断：二进制路径由 `load_binary` 整体一次性读入，
    /// TSV 路径仍交给原 `load_tsv` 流式解析，不会把同一个文件整读两遍。
    pub fn load_auto<P: AsRef<Path>>(&mut self, path: P) -> io::Result<usize> {
        let path = path.as_ref();
        let mut head = [0u8; 4];
        let mut head_len = 0usize;
        {
            let mut file = File::open(path)?;
            while head_len < head.len() {
                match file.read(&mut head[head_len..])? {
                    0 => break,
                    n => head_len += n,
                }
            }
        }
        if head_len == head.len() && head == MAGIC {
            self.load_binary(path)
        } else {
            // 文件头不足 4 字节（空文件/极短文件）也走 TSV 路径，由原逻辑决定
            // 是 0 条词条还是读取错误——这里不额外加语义。
            self.load_tsv(path)
        }
    }

    /// 带「同目录同 stem `.bin` 缓存」的加载（daemon 启动路径用）。
    ///
    /// 目标：发行包同时带 `dict_common.tsv` 与预编译 `dict_common.bin`，三端
    /// 启动参数保持不变（仍指向 `.tsv`），但优先加载同目录 `dict_common.bin`；
    /// 用户导入词库只会重写 TSV，下次启动检测到 TSV 更新后自动重建缓存。
    ///
    /// 语义：
    /// 1. 传入路径本身是 `.bin`（扩展名）或文件头是 `CSDB` → 等价 `load_auto`；
    /// 2. 否则找 `path.with_extension("bin")`：缓存存在且 mtime ≥ 源（源不存在
    ///    时视为可用）→ 直接加载缓存；
    /// 3. 缓存缺失/过期/损坏 → 加载源文件，然后尽力把**本次加载新增**的词条写回
    ///    缓存。缓存写失败（目录只读、无权限等）只打警告，加载照常成功。
    ///
    /// 返回成功加载的词条数；只有源代码本身无法加载时才返回错误。缓存损坏会
    /// 自动回退源文件并重建，不会 panic、不会返回半加载的词典。
    pub fn load_with_cache<P: AsRef<Path>>(&mut self, path: P) -> io::Result<usize> {
        let path = path.as_ref();

        // 1) 本身就是二进制词库：直接加载，不参与缓存语义。
        if has_bin_extension(path) || path_has_csdb_magic(path) {
            return self.load_auto(path);
        }

        // 2) 同目录同 stem 的 .bin 缓存足够新 → 优先用它。
        let cache = path.with_extension("bin");
        if cache_is_fresh(path, &cache) {
            if path_has_csdb_magic(&cache) {
                match self.load_auto(&cache) {
                    Ok(count) => return Ok(count),
                    Err(e) => eprintln!(
                        "警告: 二进制词库缓存 {} 加载失败（{e}），回退源词库 {}",
                        cache.display(),
                        path.display()
                    ),
                }
            } else {
                eprintln!(
                    "警告: 二进制词库缓存 {} 不是 CSDB 格式（已损坏），回退源词库 {}",
                    cache.display(),
                    path.display()
                );
            }
        }

        // 3) 从源文件加载；缓存缺失/过期/损坏时重建。
        //    只把「本次路径新增的词条」写进缓存：daemon 支持多份 -dict，缓存文件
        //    必须只含对应源文件的词条，否则下次启动会重复加载。
        let start = self.entries.len();
        let count = self.load_auto(path)?;
        write_cache_best_effort(&cache, &self.entries[start..]);
        Ok(count)
    }

    /// 单遍解析已读入内存的二进制词库（`load_binary` 的实现；每个字段读取都
    /// 做边界校验，任何不符都返回 `InvalidData`）。
    ///
    /// 解析与提交分离：词条先进入暂存 Vec，全部校验通过后才写入 `self`。
    fn parse_binary(&mut self, data: &[u8]) -> io::Result<usize> {
        let mut rd = Reader::new(data);

        let magic = rd.take(4, "文件头 magic")?;
        if magic != MAGIC {
            return Err(invalid_data(format!(
                "magic 不匹配：期望 {:?}，实际 {:?}（文件不是 CSDB 二进制词库或已损坏）",
                String::from_utf8_lossy(&MAGIC),
                String::from_utf8_lossy(magic)
            )));
        }
        let version = rd.u8("格式版本号")?;
        if version != FORMAT_VERSION {
            return Err(invalid_data(format!(
                "不支持的词库格式版本 {version}（当前只支持 {FORMAT_VERSION}）"
            )));
        }

        // 音节表：数量已知，精确预分配。
        let table_len = rd.u16("音节表项数")? as usize;
        let mut syllable_table: Vec<&str> = Vec::with_capacity(table_len);
        for _ in 0..table_len {
            syllable_table.push(rd.len_prefixed_str("音节表项")?);
        }

        // 词条：数量已知，精确预分配（二进制路径整体读入，没有理由再靠扩容）。
        let count = rd.u32("词条数")? as usize;
        // 先按「每条词条的最小字节数」核对剩余长度，再预分配：损坏/篡改文件
        // 里的巨大词条数（如 u32::MAX）若直接 with_capacity 会申请几十 GB 并
        // 在分配失败时 abort——坏文件必须走 InvalidData 而不是让进程挂掉。
        const MIN_ENTRY_BYTES: usize = 4; // word 长度 1 + 音节数 1 + 词频 2
        if count > rd.remaining() / MIN_ENTRY_BYTES {
            return Err(invalid_data(format!(
                "词条数 {count} 与剩余 {} 字节不符（文件被截断或词条数被篡改）",
                rd.remaining()
            )));
        }
        let mut staged: Vec<WordEntry> = Vec::with_capacity(count);
        for _ in 0..count {
            let word = rd.len_prefixed_str("词条 word")?.to_string();
            let syll_count = rd.u8("词条音节数")? as usize;
            let mut syllables: Vec<String> = Vec::with_capacity(syll_count);
            for _ in 0..syll_count {
                let idx = rd.u16("词条音节下标")? as usize;
                let syll = syllable_table.get(idx).ok_or_else(|| {
                    invalid_data(format!(
                        "非法音节下标 {idx}（音节表共 {table_len} 项，合法下标 0..{}）",
                        table_len.saturating_sub(1)
                    ))
                })?;
                syllables.push(syll.to_string());
            }
            let freq = rd.u16("词条词频")? as u32;
            staged.push(WordEntry {
                word,
                syllables,
                freq,
            });
        }

        if rd.remaining() != 0 {
            return Err(invalid_data(format!(
                "词条数据之后还有 {} 字节多余数据（文件被拼接或损坏）",
                rd.remaining()
            )));
        }

        // 全部校验通过后才提交：坏缓存不能污染已有词典状态（load_with_cache
        // 依赖这一点安全回退源文件）。空词典直接接管暂存 Vec（保持精确容量），
        // 非空词典沿用逐条 insert 的追加语义。
        if self.entries.is_empty() {
            self.entries = staged;
            self.rebuild_trie();
        } else {
            self.entries.reserve_exact(staged.len());
            for entry in staged {
                self.insert(entry);
            }
        }
        self.sort_nodes();
        Ok(count)
    }
}

/// 把一组词条编译成二进制词库写到 `path`（`write_binary` 与缓存写回共用）。
///
/// 单遍顺序写出；音节表按首次出现顺序去重编号，保证同一输入逐字节可复现。
fn write_binary_entries(entries: &[WordEntry], path: &Path) -> io::Result<()> {
    let (syllable_table, syllable_index) = build_syllable_table(entries)?;

    let file = File::create(path)?;
    let mut out = BufWriter::new(file);

    out.write_all(&MAGIC)?;
    out.write_all(&[FORMAT_VERSION])?;

    // 音节表：数量与单项字节长都是定长编码，超限即报错。
    let table_len = u16::try_from(syllable_table.len()).map_err(|_| {
        invalid_input(format!(
            "音节表过大：{} 项，CSDB v1 上限 65535 项",
            syllable_table.len()
        ))
    })?;
    out.write_all(&table_len.to_le_bytes())?;
    for syll in &syllable_table {
        let bytes = syll.as_bytes();
        let len = u8::try_from(bytes.len()).map_err(|_| {
            invalid_input(format!(
                "音节 {syll:?} 的 UTF-8 长度为 {} 字节，CSDB v1 单项上限 255 字节",
                bytes.len()
            ))
        })?;
        out.write_all(&[len])?;
        out.write_all(bytes)?;
    }

    let count = u32::try_from(entries.len())
        .map_err(|_| invalid_input(format!("词条数 {} 超出 CSDB v1 的 u32 上限", entries.len())))?;
    out.write_all(&count.to_le_bytes())?;

    for entry in entries {
        let word = entry.word.as_bytes();
        let word_len = u8::try_from(word.len()).map_err(|_| {
            invalid_input(format!(
                "词 {:?} 的 UTF-8 长度为 {} 字节，CSDB v1 单个 word 上限 255 字节",
                entry.word,
                word.len()
            ))
        })?;
        out.write_all(&[word_len])?;
        out.write_all(word)?;

        let syll_count = u8::try_from(entry.syllables.len()).map_err(|_| {
            invalid_input(format!(
                "词 {:?} 有 {} 个音节，CSDB v1 单条上限 255 个",
                entry.word,
                entry.syllables.len()
            ))
        })?;
        out.write_all(&[syll_count])?;
        for syll in &entry.syllables {
            let idx = syllable_index
                .get(syll.as_str())
                .copied()
                .ok_or_else(|| invalid_input(format!("内部错误：音节表缺少音节 {syll:?}")))?;
            out.write_all(&idx.to_le_bytes())?;
        }

        let freq = u16::try_from(entry.freq).map_err(|_| {
            invalid_input(format!(
                "词 {:?} 的词频 {} 超出 CSDB v1 的 u16 范围（0..=65535）",
                entry.word, entry.freq
            ))
        })?;
        out.write_all(&freq.to_le_bytes())?;
    }

    out.flush()?;
    Ok(())
}

/// 从词条实际用到的音节按**首次出现顺序**去重生成音节表（返回表与反查表）。
///
/// 不排序、不依赖 HashMap 迭代顺序：同一份 entries 保证编译结果逐字节一致。
fn build_syllable_table(entries: &[WordEntry]) -> io::Result<(Vec<&str>, HashMap<&str, u16>)> {
    let mut table: Vec<&str> = Vec::new();
    let mut index: HashMap<&str, u16> = HashMap::new();
    for entry in entries {
        for syll in &entry.syllables {
            let key = syll.as_str();
            if index.contains_key(key) {
                continue;
            }
            if table.len() >= u16::MAX as usize {
                return Err(invalid_input(format!(
                    "音节表过大：已超过 CSDB v1 上限 65535 项（遇到新音节 {key:?}）"
                )));
            }
            index.insert(key, table.len() as u16);
            table.push(key);
        }
    }
    Ok((table, index))
}

/// 路径扩展名是否为 `bin`（大小写不敏感）。
fn has_bin_extension(path: &Path) -> bool {
    path.extension()
        .and_then(|ext| ext.to_str())
        .is_some_and(|ext| ext.eq_ignore_ascii_case("bin"))
}

/// 文件头 4 字节是否为 CSDB magic（打不开/不足 4 字节一律 false）。
fn path_has_csdb_magic(path: &Path) -> bool {
    let Ok(mut file) = File::open(path) else {
        return false;
    };
    let mut head = [0u8; 4];
    let mut read = 0usize;
    while read < head.len() {
        match file.read(&mut head[read..]) {
            Ok(0) => break,
            Ok(n) => read += n,
            Err(_) => return false,
        }
    }
    read == head.len() && head == MAGIC
}

/// 缓存是否可用：缓存是普通文件，且（源不存在，或缓存 mtime ≥ 源 mtime）。
///
/// mtime 拿不到时保守返回 false（宁可重建，也不拿无法判定新旧的缓存）。
fn cache_is_fresh(source: &Path, cache: &Path) -> bool {
    let Ok(cache_meta) = std::fs::metadata(cache) else {
        return false;
    };
    if !cache_meta.is_file() {
        return false;
    }
    let Ok(cache_time) = cache_meta.modified() else {
        return false;
    };
    match std::fs::metadata(source) {
        Ok(source_meta) => source_meta
            .modified()
            .map(|source_time| cache_time >= source_time)
            .unwrap_or(false),
        // 源不存在（发行包只带 .bin，或源被清理）时缓存仍可用。
        Err(_) => true,
    }
}

/// 尽力把词条写成 `.bin` 缓存：同目录临时文件 + rename；任何失败（只读目录、
/// 无权限、磁盘满）只打警告，不影响调用方的加载结果。
fn write_cache_best_effort(cache_path: &Path, entries: &[WordEntry]) {
    if entries.is_empty() {
        // 空词典没有缓存价值，也别留下一个 0 条目的 .bin 误导后续诊断。
        return;
    }
    // 临时文件名带 PID：两个 daemon 并发首启同一词库时不互相截断对方的临时
    // 文件（单实例锁在词库加载之后才拿，早期窗口理论上有并发）。
    let mut tmp = cache_path.to_path_buf();
    let name = cache_path
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| "dict.bin".to_string());
    tmp.set_file_name(format!("{name}.{}.tmp", std::process::id()));
    if let Err(e) = write_binary_entries(entries, &tmp) {
        eprintln!(
            "警告: 写二进制词库缓存 {} 失败（本次仍使用源词库）: {e}",
            tmp.display()
        );
        let _ = std::fs::remove_file(&tmp);
        return;
    }
    // Unix 上 rename 原子覆盖；Windows 上覆盖已存在目标可能失败，删旧后重试一次。
    if std::fs::rename(&tmp, cache_path).is_ok() {
        return;
    }
    let removed = std::fs::remove_file(cache_path).is_ok() || !cache_path.exists();
    if removed && std::fs::rename(&tmp, cache_path).is_ok() {
        return;
    }
    eprintln!(
        "警告: 更新二进制词库缓存 {} 失败（本次仍使用源词库）",
        cache_path.display()
    );
    let _ = std::fs::remove_file(&tmp);
}

/// 小端顺序字节流读取器：每次读取都带边界检查，越界统一报「截断」。
struct Reader<'a> {
    data: &'a [u8],
    pos: usize,
}

impl<'a> Reader<'a> {
    fn new(data: &'a [u8]) -> Self {
        Self { data, pos: 0 }
    }

    fn remaining(&self) -> usize {
        self.data.len().saturating_sub(self.pos)
    }

    fn take(&mut self, n: usize, what: &str) -> io::Result<&'a [u8]> {
        if self.remaining() < n {
            return Err(invalid_data(format!(
                "二进制词库被截断：读取{what}需要 {n} 字节，偏移 {} 之后只剩 {} 字节",
                self.pos,
                self.remaining()
            )));
        }
        let out = &self.data[self.pos..self.pos + n];
        self.pos += n;
        Ok(out)
    }

    fn u8(&mut self, what: &str) -> io::Result<u8> {
        Ok(self.take(1, what)?[0])
    }

    fn u16(&mut self, what: &str) -> io::Result<u16> {
        let b = self.take(2, what)?;
        Ok(u16::from_le_bytes([b[0], b[1]]))
    }

    fn u32(&mut self, what: &str) -> io::Result<u32> {
        let b = self.take(4, what)?;
        Ok(u32::from_le_bytes([b[0], b[1], b[2], b[3]]))
    }

    /// 读「u8 字节长 + UTF-8 内容」的字符串字段。
    fn len_prefixed_str(&mut self, what: &str) -> io::Result<&'a str> {
        let len = self.u8(what)? as usize;
        let offset = self.pos;
        let bytes = self.take(len, what)?;
        std::str::from_utf8(bytes)
            .map_err(|e| invalid_data(format!("偏移 {offset} 的{what}不是合法 UTF-8: {e}")))
    }
}

/// 二进制内容损坏（magic/版本/截断/非法下标/多余数据）。
fn invalid_data(msg: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, msg.into())
}

/// 待编译数据超出 CSDB v1 定长编码能力（词频/长度/数量超限）。
fn invalid_input(msg: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, msg.into())
}
