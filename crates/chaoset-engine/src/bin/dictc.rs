//! 词库编译器：把 TSV 词库编译成发布用二进制词库（CSDB v1）。
//!
//! 仓库里只保留 TSV 源；打包阶段用本工具把 data/dict_common.tsv 编译成
//! .bin 随发行包分发，daemon 用 Dictionary::load_auto 按文件头自动识别。
//!
//! 用法：
//!   dictc <input.tsv> [output.bin]
//!
//! 省略输出路径时写到同目录同名 .bin（data/dict_common.tsv →
//! data/dict_common.bin）。格式定义见 chaoset_engine::dict_binary 模块文档。

use chaoset_engine::Dictionary;
use std::path::PathBuf;
use std::process::ExitCode;
use std::time::Instant;

const USAGE: &str = "用法: dictc <input.tsv> [output.bin]（省略输出 = 同目录同名 .bin）";

fn main() -> ExitCode {
    let raw: Vec<String> = std::env::args().skip(1).collect();

    if raw.first().is_some_and(|a| a == "-h" || a == "--help") {
        println!("{USAGE}");
        return ExitCode::SUCCESS;
    }
    if raw.is_empty() || raw.len() > 2 {
        eprintln!("{USAGE}");
        return ExitCode::from(2);
    }

    let input = PathBuf::from(&raw[0]);
    let output = match raw.get(1) {
        Some(path) => PathBuf::from(path),
        // 同目录同名：只替换扩展名（data/dict_common.tsv → data/dict_common.bin）。
        None => input.with_extension("bin"),
    };

    if input == output {
        eprintln!("dictc: 输出路径不能与输入相同（{output:?}），请换一个输出路径");
        return ExitCode::from(2);
    }

    // 先把 TSV 完整加载进内存再写出，避免「边读边覆盖源文件」。
    let mut dict = Dictionary::new();
    let load_start = Instant::now();
    let count = match dict.load_tsv(&input) {
        Ok(count) => count,
        Err(e) => {
            eprintln!("dictc: 加载 TSV 词库 {} 失败: {e}", input.display());
            return ExitCode::from(1);
        }
    };
    let load_elapsed = load_start.elapsed();

    if count == 0 {
        eprintln!(
            "dictc: 词库 {} 没有解析出任何词条，拒绝生成空词库",
            input.display()
        );
        return ExitCode::from(1);
    }

    let write_start = Instant::now();
    if let Err(e) = dict.write_binary(&output) {
        eprintln!("dictc: 写出二进制词库 {} 失败: {e}", output.display());
        return ExitCode::from(1);
    }
    let write_elapsed = write_start.elapsed();

    let input_bytes = std::fs::metadata(&input).map(|m| m.len()).unwrap_or(0);
    let output_bytes = std::fs::metadata(&output).map(|m| m.len()).unwrap_or(0);

    // stdout 只放持久化产物信息（count + 两个文件字节数），便于打包脚本解析；
    // 耗时属于运行时观测，走 stderr 不污染产物信息。
    println!("count: {count}");
    println!("input:  {} ({input_bytes} 字节)", input.display());
    println!("output: {} ({output_bytes} 字节)", output.display());
    eprintln!(
        "dictc: 加载 {:?}，写出 {:?}（{} → {} 字节）",
        load_elapsed, write_elapsed, input_bytes, output_bytes
    );

    ExitCode::SUCCESS
}
