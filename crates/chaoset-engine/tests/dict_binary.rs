//! 发布用二进制词库（CSDB v1）的格式、等价性与发布证据测试。
//!
//! 覆盖四件事：
//! 1. TSV 加载与「TSV 编译出的 .bin」加载逐条一致（word/freq/syllables），
//!    load_auto 对这两种路径也一致；
//! 2. 行为等价：同一份 .bin 与 TSV 分别喂给 replay 回放 41 个冻结场景，
//!    输出必须逐字节相同且等于 parity_expected.jsonl；
//! 3. 损坏文件（坏 magic / 截断 / 坏版本 / 非法音节下标）必须报错而不是 panic；
//! 4. 打印体积与加载耗时（同进程顺序测量），作为「二进制更快更省」的证据。

use chaoset_engine::Dictionary;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Mutex, MutexGuard};
use std::time::Instant;

/// 测试进程内的串行锁：默认并行跑测试时，回放测试会起两个子进程各加载
/// 20 万条词条，与计时测试抢 CPU，打印出来的耗时不可信。三条测试统一先拿
/// 这把锁，保证「同进程顺序测量」在并行测试模式下也成立。
static HEAVY_LOCK: Mutex<()> = Mutex::new(());

fn heavy_lock() -> MutexGuard<'static, ()> {
    HEAVY_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn crate_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
}

fn repo_root() -> PathBuf {
    crate_dir().join("../..")
}

fn common_tsv() -> PathBuf {
    repo_root().join("data/dict_common.tsv")
}

/// 把内置 TSV 编译成 dir 下的 dict_common.bin，返回路径。
fn compile_common_bin(dir: &Path) -> PathBuf {
    let tsv = common_tsv();
    let bin = dir.join("dict_common.bin");
    let mut dict = Dictionary::new();
    let count = dict
        .load_tsv(&tsv)
        .unwrap_or_else(|e| panic!("加载 {} 失败: {e}", tsv.display()));
    assert_eq!(count, dict.entries.len());
    dict.write_binary(&bin)
        .unwrap_or_else(|e| panic!("写出 {} 失败: {e}", bin.display()));
    bin
}

/// 逐条比对两个词典的 word/freq/syllables（entries 顺序也必须一致）。
fn assert_entries_identical(left: &Dictionary, right: &Dictionary, context: &str) {
    assert_eq!(
        left.entries.len(),
        right.entries.len(),
        "{context}: 词条数不一致"
    );
    for (idx, (a, b)) in left.entries.iter().zip(right.entries.iter()).enumerate() {
        assert_eq!(a.word, b.word, "{context}: 第 {idx} 条 word 不一致");
        assert_eq!(
            a.freq, b.freq,
            "{context}: 第 {idx} 条（{}）freq 不一致",
            a.word
        );
        assert_eq!(
            a.syllables, b.syllables,
            "{context}: 第 {idx} 条（{}）syllables 不一致",
            a.word
        );
    }
}

#[test]
fn tsv_and_binary_load_identically() {
    let _guard = heavy_lock();
    let tsv = common_tsv();
    let tmp = tempfile::tempdir().expect("创建临时目录失败");

    let mut from_tsv = Dictionary::new();
    let tsv_count = from_tsv
        .load_tsv(&tsv)
        .unwrap_or_else(|e| panic!("加载 {} 失败: {e}", tsv.display()));
    assert!(tsv_count > 60_000, "内置词库规模异常: {tsv_count}");

    let bin = compile_common_bin(tmp.path());
    let mut from_bin = Dictionary::new();
    let bin_count = from_bin
        .load_binary(&bin)
        .unwrap_or_else(|e| panic!("加载 {} 失败: {e}", bin.display()));

    assert_eq!(tsv_count, bin_count, "二进制与 TSV 词条数不一致");
    assert_entries_identical(&from_tsv, &from_bin, "load_tsv vs load_binary");
    // 精确预分配：with_capacity(count) 后逐条 push，全程不触发扩容。
    assert_eq!(
        from_bin.entries.capacity(),
        bin_count,
        "load_binary 的 entries 未精确预分配"
    );

    // load_auto 按文件头分派：同一 TSV 路径与 .bin 路径都要得到相同结果。
    let mut auto_tsv = Dictionary::new();
    let auto_tsv_count = auto_tsv
        .load_auto(&tsv)
        .unwrap_or_else(|e| panic!("load_auto 加载 {} 失败: {e}", tsv.display()));
    assert_eq!(
        auto_tsv_count, tsv_count,
        "load_auto 走 TSV 分派时词条数不一致"
    );
    assert_entries_identical(&from_tsv, &auto_tsv, "load_tsv vs load_auto(tsv)");

    let mut auto_bin = Dictionary::new();
    let auto_bin_count = auto_bin
        .load_auto(&bin)
        .unwrap_or_else(|e| panic!("load_auto 加载 {} 失败: {e}", bin.display()));
    assert_eq!(
        auto_bin_count, bin_count,
        "load_auto 走二进制分派时词条数不一致"
    );
    assert_entries_identical(&from_bin, &auto_bin, "load_binary vs load_auto(bin)");

    // 发布证据：体积 + 同进程顺序测量的耗时（先 TSV 后二进制）。
    let tsv_bytes = std::fs::metadata(&tsv).map(|m| m.len()).unwrap_or(0);
    let bin_bytes = std::fs::metadata(&bin).map(|m| m.len()).unwrap_or(0);
    let profile = if cfg!(debug_assertions) {
        "debug"
    } else {
        "release"
    };
    println!(
        "[dict_binary] 构建配置: {profile}（debug_assertions={}）",
        cfg!(debug_assertions)
    );
    println!("[dict_binary] 词条数: {bin_count}");
    println!("[dict_binary] TSV 体积: {tsv_bytes} 字节");
    println!(
        "[dict_binary] BIN 体积: {bin_bytes} 字节（TSV 的 {:.1}%）",
        bin_bytes as f64 / tsv_bytes as f64 * 100.0
    );
    let tsv_times = time_loads(&tsv, false, 3);
    let bin_times = time_loads(&bin, true, 3);
    let tsv_best = *tsv_times.iter().min().expect("至少一轮");
    let bin_best = *bin_times.iter().min().expect("至少一轮");
    println!("[dict_binary] 加载耗时（同进程顺序测量，{profile}，各 3 轮）:");
    println!("[dict_binary]   load_tsv 每轮: {tsv_times:?}，最快 {tsv_best:?}");
    println!("[dict_binary]   load_binary 每轮: {bin_times:?}，最快 {bin_best:?}");
    println!(
        "[dict_binary]   最快一轮对比: 二进制比 TSV 快 {:.2}x（节省 {:.1}%）",
        tsv_best.as_secs_f64() / bin_best.as_secs_f64().max(f64::MIN_POSITIVE),
        (1.0 - bin_best.as_secs_f64() / tsv_best.as_secs_f64().max(f64::MIN_POSITIVE)) * 100.0
    );
}

/// 同一进程内顺序加载 rounds 轮，返回每轮耗时（含首轮预热，供观察抖动）。
fn time_loads(path: &Path, binary: bool, rounds: usize) -> Vec<std::time::Duration> {
    let mut out = Vec::with_capacity(rounds);
    for _ in 0..rounds {
        let mut dict = Dictionary::new();
        let start = Instant::now();
        if binary {
            dict.load_binary(path).expect("二进制词库加载失败");
        } else {
            dict.load_tsv(path).expect("TSV 词库加载失败");
        }
        out.push(start.elapsed());
        drop(dict);
    }
    out
}

/// 行为等价：把 TSV 编译出的 .bin 放临时目录，分别以 --dict <tsv> 与
/// --dict <bin> 运行 replay 回放同一批 41 个冻结场景，stdout 必须逐字节相同，
/// 且与 tests/data/parity_expected.jsonl 逐字节相同。
#[test]
fn replay_output_is_byte_identical_for_tsv_and_binary() {
    let _guard = heavy_lock();
    let tsv = common_tsv();
    let scenarios = crate_dir().join("tests/data/parity_scenarios.jsonl");
    let expected_path = crate_dir().join("tests/data/parity_expected.jsonl");
    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let bin = compile_common_bin(tmp.path());

    let run = |dict: &Path| -> Vec<u8> {
        let output = Command::new(env!("CARGO_BIN_EXE_replay"))
            .args([
                "--dict",
                &dict.to_string_lossy(),
                "--scenarios",
                &scenarios.to_string_lossy(),
            ])
            .output()
            .expect("启动 replay 失败");
        assert!(
            output.status.success(),
            "replay 失败（词库 {}）: {}",
            dict.display(),
            String::from_utf8_lossy(&output.stderr)
        );
        output.stdout
    };

    let from_tsv = run(&tsv);
    let from_bin = run(&bin);
    assert!(!from_tsv.is_empty(), "replay 输出为空");
    assert_eq!(
        from_tsv, from_bin,
        "TSV 与二进制词库的 41 场景回放输出不是逐字节相同"
    );
    let want = std::fs::read(&expected_path).expect("读取冻结快照失败");
    assert_eq!(
        from_tsv, want,
        "TSV 词库回放与冻结基线不一致（二进制加载不得改变候选/打分/顺序）"
    );
}

/// 损坏的二进制词库必须返回清晰错误，不能 panic、不能静默跳过或部分加载。
#[test]
fn corrupted_binary_is_rejected() {
    let _guard = heavy_lock();
    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let mut dict = Dictionary::new();
    dict.load_tsv_str("你\tni\t50\n你好\tni hao\t60\n");
    let good = tmp.path().join("good.bin");
    dict.write_binary(&good).expect("写出基准二进制失败");
    let bytes = std::fs::read(&good).expect("读回基准二进制失败");
    assert!(bytes.len() > 16, "基准二进制过短: {} 字节", bytes.len());

    let load = |name: &str, data: &[u8]| -> std::io::Result<usize> {
        let path = tmp.path().join(name);
        std::fs::write(&path, data).expect("写损坏样本失败");
        let mut probe = Dictionary::new();
        probe.load_binary(&path)
    };

    // 1) 坏 magic：任何非 CSDB 开头都必须报错，不能走宽松路径吞掉。
    let mut bad_magic = bytes.clone();
    bad_magic[0] = b'X';
    let err = load("bad_magic.bin", &bad_magic).expect_err("坏 magic 必须报错");
    assert_eq!(err.kind(), ErrorKind::InvalidData);
    assert!(err.to_string().contains("magic"), "错误信息不清晰: {err}");

    // 2) 坏版本：布局随版本变化，未知版本必须拒绝加载。
    let mut bad_version = bytes.clone();
    bad_version[4] = 99;
    let err = load("bad_version.bin", &bad_version).expect_err("未知版本必须报错");
    assert_eq!(err.kind(), ErrorKind::InvalidData);
    assert!(err.to_string().contains("版本"), "错误信息不清晰: {err}");

    // 3) 截断：一半长度必然在某个字段中途结束。
    let err = load("truncated.bin", &bytes[..bytes.len() / 2]).expect_err("截断文件必须报错");
    assert_eq!(err.kind(), ErrorKind::InvalidData);
    assert!(err.to_string().contains("截断"), "错误信息不清晰: {err}");

    // 4) 非法音节下标：最后一条词条的「音节下标」字段是倒数第 3、4 字节。
    let mut bad_index = bytes.clone();
    let len = bad_index.len();
    bad_index[len - 4..len - 2].copy_from_slice(&u16::MAX.to_le_bytes());
    let err = load("bad_index.bin", &bad_index).expect_err("非法音节下标必须报错");
    assert_eq!(err.kind(), ErrorKind::InvalidData);
    assert!(
        err.to_string().contains("音节下标"),
        "错误信息不清晰: {err}"
    );

    // 5) 空文件：不能 panic，报「截断」类 InvalidData。
    let err = load("empty.bin", &[]).expect_err("空文件必须报错");
    assert_eq!(err.kind(), ErrorKind::InvalidData);

    // 6) load_auto 对「有 magic 但损坏」的文件也必须暴露错误（不能回退当 TSV）。
    let auto_path = tmp.path().join("auto_truncated.bin");
    std::fs::write(&auto_path, &bytes[..4]).expect("写 auto 样本失败");
    let mut probe = Dictionary::new();
    let err = probe
        .load_auto(&auto_path)
        .expect_err("load_auto 必须暴露损坏");
    assert_eq!(err.kind(), ErrorKind::InvalidData);

    // 7) 头部词条数被篡改成 u32::MAX：必须先校验剩余长度再预分配，
    //    否则 with_capacity 会申请几十 GB、分配失败直接 abort（不是 Err）。
    let mut huge_count = Vec::new();
    huge_count.extend_from_slice(b"CSDB");
    huge_count.push(1); // 版本
    huge_count.extend_from_slice(&0u16.to_le_bytes()); // 音节表 0 项
    huge_count.extend_from_slice(&u32::MAX.to_le_bytes()); // 词条数
    let err = load("huge_count.bin", &huge_count).expect_err("异常大的词条数必须报错");
    assert_eq!(err.kind(), ErrorKind::InvalidData);
    assert!(err.to_string().contains("词条数"), "错误信息不清晰: {err}");
}

// ── 同目录 .bin 缓存（load_with_cache）────────────────────────────────────
//
// 目标场景：发行包同时带 TSV 源与预编译 .bin；三端启动参数仍指向 .tsv。
// 下面用小型测试词库（不依赖内置 20 万条大词库）覆盖：首启建缓存、二启命中、
// TSV 变新重建、坏缓存回退重建、只读目录降级、多 -dict 时缓存不串味。

/// 写一份小 TSV 词库（词\t拼音\t词频）。
fn write_tsv(path: &Path, rows: &[(&str, &str, u32)]) {
    let mut text = String::new();
    for (word, pinyin, freq) in rows {
        text.push_str(&format!("{word}\t{pinyin}\t{freq}\n"));
    }
    std::fs::write(path, text).expect("写测试词库失败");
}

fn load_tsv_dict(path: &Path) -> Dictionary {
    let mut dict = Dictionary::new();
    dict.load_tsv(path)
        .unwrap_or_else(|e| panic!("加载 TSV {} 失败: {e}", path.display()));
    dict
}

#[test]
fn cache_is_built_on_first_load_and_used_on_second() {
    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let tsv = tmp.path().join("dict.tsv");
    let cache = tmp.path().join("dict.bin");
    write_tsv(&tsv, &[("你好", "ni hao", 60), ("你", "ni", 50)]);

    let mut first = Dictionary::new();
    let first_count = first.load_with_cache(&tsv).expect("首启加载失败");
    assert_eq!(first_count, 2);
    assert!(cache.is_file(), "首启应生成同目录同 stem 的 .bin 缓存");
    assert_entries_identical(
        &load_tsv_dict(&tsv),
        &first,
        "首启 load_with_cache vs load_tsv",
    );
    let mut from_cache = Dictionary::new();
    from_cache
        .load_binary(&cache)
        .expect("生成的缓存应可独立加载");
    assert_entries_identical(&first, &from_cache, "首启结果 vs 缓存内容");

    // .bin 路径本身 → 直接加载（不参与缓存语义），也验证扩展名分派。
    let mut direct = Dictionary::new();
    assert_eq!(
        direct.load_with_cache(&cache).expect("直接加载 .bin 失败"),
        2
    );

    // 二启命中缓存：删掉 TSV 后仍能加载（源不存在 → 缓存视为可用），
    // 这是「发行包只带 .bin / 源被清理」场景的明确语义。
    std::fs::remove_file(&tsv).expect("删除 TSV 失败");
    let mut second = Dictionary::new();
    let second_count = second.load_with_cache(&tsv).expect("二启应命中 .bin 缓存");
    assert_eq!(second_count, first_count);
    assert_entries_identical(&first, &second, "二启缓存命中结果 vs 首启");
}

#[test]
fn newer_tsv_rebuilds_cache() {
    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let tsv = tmp.path().join("dict.tsv");
    let cache = tmp.path().join("dict.bin");
    write_tsv(&tsv, &[("你好", "ni hao", 60), ("你", "ni", 50)]);

    let mut first = Dictionary::new();
    first.load_with_cache(&tsv).expect("首启加载失败");
    assert!(cache.is_file());

    // 模拟用户导入：只重写 TSV（mtime 变新）。留出 10ms 保证文件系统
    // 时间戳分辨率较低（1s 级）时也能稳定判出「TSV 比缓存新」。
    std::thread::sleep(std::time::Duration::from_millis(10));
    write_tsv(
        &tsv,
        &[
            ("你好", "ni hao", 60),
            ("你", "ni", 50),
            ("数据", "shu ju", 80),
        ],
    );

    let mut second = Dictionary::new();
    let count = second.load_with_cache(&tsv).expect("TSV 变新后加载失败");
    assert_eq!(count, 3, "TSV 比缓存新时必须重新加载 TSV");
    assert!(second.entries.iter().any(|e| e.word == "数据"));

    let mut rebuilt = Dictionary::new();
    assert_eq!(
        rebuilt.load_binary(&cache).expect("重建后的缓存应可加载"),
        3,
        "缓存应已按新 TSV 重建"
    );
    assert!(rebuilt.entries.iter().any(|e| e.word == "数据"));
}

#[test]
fn corrupt_cache_falls_back_to_tsv_and_rebuilds() {
    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let tsv = tmp.path().join("dict.tsv");
    let cache = tmp.path().join("dict.bin");
    write_tsv(&tsv, &[("你好", "ni hao", 60), ("你", "ni", 50)]);

    let mut first = Dictionary::new();
    first.load_with_cache(&tsv).expect("首启加载失败");
    let good = std::fs::read(&cache).expect("读缓存失败");
    assert!(good.len() > 8, "缓存过短: {} 字节", good.len());

    // 1) 截断（magic 仍在、数据不完整）：mtime 比 TSV 新 → 新鲜但损坏。
    std::fs::write(&cache, &good[..good.len() / 2]).expect("写坏缓存失败");
    let mut dict = Dictionary::new();
    let count = dict
        .load_with_cache(&tsv)
        .expect("坏缓存应回退 TSV 而不是报错");
    assert_eq!(count, 2);
    assert_entries_identical(&load_tsv_dict(&tsv), &dict, "坏缓存回退结果");
    let mut rebuilt = Dictionary::new();
    assert_eq!(rebuilt.load_binary(&cache).expect("回退后缓存应已重建"), 2);

    // 2) 完全没有 magic 的内容也不能被当成 TSV 悄悄加载 0 条。
    std::fs::write(&cache, b"not a csdb file").expect("写坏缓存失败");
    let mut dict = Dictionary::new();
    assert_eq!(
        dict.load_with_cache(&tsv).expect("非 CSDB 缓存应回退 TSV"),
        2
    );
    let mut rebuilt = Dictionary::new();
    assert_eq!(rebuilt.load_binary(&cache).expect("回退后缓存应已重建"), 2);
}

#[test]
fn cache_contains_only_entries_from_its_own_source() {
    // daemon 支持多份 -dict：每份源文件的缓存只能含自己的词条，
    // 否则下次启动会重复加载（A 的缓存里混进 B 的词条）。
    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let tsv_a = tmp.path().join("a.tsv");
    let tsv_b = tmp.path().join("b.tsv");
    write_tsv(&tsv_a, &[("你好", "ni hao", 60)]);
    write_tsv(&tsv_b, &[("你", "ni", 50), ("数据", "shu ju", 80)]);

    let mut dict = Dictionary::new();
    assert_eq!(dict.load_with_cache(&tsv_a).expect("加载 a 失败"), 1);
    assert_eq!(dict.load_with_cache(&tsv_b).expect("加载 b 失败"), 2);
    assert_eq!(dict.entries.len(), 3, "同一 Dictionary 内两份词库应累加");

    let mut cache_a = Dictionary::new();
    assert_eq!(
        cache_a
            .load_binary(&tsv_a.with_extension("bin"))
            .expect("a 缓存应可加载"),
        1,
        "a 的缓存不得混入 b 的词条"
    );
    let mut cache_b = Dictionary::new();
    assert_eq!(
        cache_b
            .load_binary(&tsv_b.with_extension("bin"))
            .expect("b 缓存应可加载"),
        2,
        "b 的缓存不得混入 a 的词条"
    );
}

/// 只读目录：缓存写不进去时只降级为「本次从 TSV 加载」，不能报错或 panic。
#[cfg(unix)]
#[test]
fn read_only_dir_loads_without_cache() {
    use std::os::unix::fs::PermissionsExt;

    let tmp = tempfile::tempdir().expect("创建临时目录失败");
    let dir = tmp.path().join("ro");
    std::fs::create_dir(&dir).expect("创建只读目录失败");
    let tsv = dir.join("dict.tsv");
    write_tsv(&tsv, &[("你好", "ni hao", 60)]);
    std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o555))
        .expect("设置只读目录失败");

    // root 或特殊文件系统下 chmod 不生效时跳过断言（仍恢复权限让临时目录可删）。
    let probe = dir.join(".write-probe");
    let really_read_only = std::fs::write(&probe, b"x").is_err();
    if really_read_only {
        let mut dict = Dictionary::new();
        let count = dict
            .load_with_cache(&tsv)
            .expect("只读目录必须仍能从 TSV 加载成功");
        assert_eq!(count, 1);
        assert!(!dir.join("dict.bin").exists(), "只读目录不应生成缓存文件");
        assert_entries_identical(&load_tsv_dict(&tsv), &dict, "只读目录降级加载结果");
    } else {
        let _ = std::fs::remove_file(&probe);
        eprintln!("[dict_binary] 当前环境 chmod 0555 不影响写入（疑似 root），跳过只读目录断言");
    }

    std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o755))
        .expect("恢复目录权限失败");
}
