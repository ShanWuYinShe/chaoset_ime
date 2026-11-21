//! 冻结的引擎行为基线（41 个场景）。
//!
//! 这 41 个场景来自已删除的 Go/Rust 差分工具 `tools/engineparity`：
//! 当时两种实现逐场景输出完全一致（含候选打分），Go 引擎删除后差分
//! 不再可行，于是把 Rust 侧当前输出冻结为快照——任何匹配/排序/学习
//! 行为的改动都会让它失败，迫使作者确认「这是有意的行为变化」并重新
//! 生成快照（重新生成方式见本文件末尾注释）。
//!
//! 快照放在 `tests/data/parity_expected.jsonl`，输入在
//! `tests/data/parity_scenarios.jsonl`；两者都随仓库提交。

use std::path::PathBuf;
use std::process::Command;

fn crate_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
}

#[test]
fn frozen_parity_scenarios_match_snapshot() {
    let root = crate_dir().join("../..");
    let dict = root.join("data/dict_common.tsv");
    let scenarios = crate_dir().join("tests/data/parity_scenarios.jsonl");
    let expected_path = crate_dir().join("tests/data/parity_expected.jsonl");

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
        "replay 失败: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let got = String::from_utf8(output.stdout).expect("replay 输出不是 UTF-8");
    let want = std::fs::read_to_string(&expected_path).expect("读取冻结快照失败");
    assert_eq!(
        got, want,
        "回放输出与冻结基线不一致：候选/打分/学习行为发生回归；若确为有意的行为改动，请重新生成 {} 并在 PR 中说明依据",
        expected_path.display()
    );
}

// 重新生成快照（确认行为改动有意后执行）：
//   cargo run --release --bin replay --manifest-path crates/Cargo.toml -- \
//     --dict data/dict_common.tsv \
//     --scenarios crates/chaoset-engine/tests/data/parity_scenarios.jsonl \
//     > crates/chaoset-engine/tests/data/parity_expected.jsonl
