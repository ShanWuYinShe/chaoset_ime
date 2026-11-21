//! `import` 子命令：委托 chaoset-tools 的词库合并实现。

use std::io::Write;

pub fn run(args: &[String], stdout: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    chaoset_tools::dictimport::run(args, stdout, stderr)
}
