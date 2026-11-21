//! 导入/导出共用的文件工具：本地时间戳、唯一临时文件、0644 权限与原子替换。
//!
//! 与 Go 工具链（internal/tools/dictimport、internal/tools/userdict）行为一致：
//! 写目标一律「同目录临时文件 + 权限设置 + rename」，失败时旧文件保持不变。

use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};

static TEMP_SEQ: AtomicU64 = AtomicU64::new(0);

fn current_unix_secs() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// UTC 时间戳 YYYYMMDD-HHMMSS（本地时区不可用时的兜底）。
///
/// 复用引擎导出的 RFC3339 格式化（引擎不引第三方时间库，这里同样不引）。
pub fn utc_stamp_now() -> String {
    let rfc = chaoset_engine::userdict::format_rfc3339_utc(current_unix_secs());
    // "YYYY-MM-DDTHH:MM:SSZ" → "YYYYMMDD-HHMMSS"
    format!(
        "{}{}{}-{}{}{}",
        &rfc[0..4],
        &rfc[5..7],
        &rfc[8..10],
        &rfc[11..13],
        &rfc[14..16],
        &rfc[17..19]
    )
}

/// 本地时间戳 YYYYMMDD-HHMMSS（备份文件名用；Windows/macOS/Linux 都取系统
/// 本地时区，与 Go 的 time.Now().Format("20060102-150405") 同口径）。
pub fn local_stamp() -> String {
    #[cfg(unix)]
    if let Some(fields) = unix_local_fields() {
        return format_fields(fields);
    }
    #[cfg(windows)]
    if let Some(fields) = windows_local_fields() {
        return format_fields(fields);
    }
    utc_stamp_now()
}

fn format_fields(
    (year, month, day, hour, minute, second): (i32, u32, u32, u32, u32, u32),
) -> String {
    format!(
        "{:04}{:02}{:02}-{:02}{:02}{:02}",
        year, month, day, hour, minute, second
    )
}

#[cfg(unix)]
fn unix_local_fields() -> Option<(i32, u32, u32, u32, u32, u32)> {
    // SAFETY: 标准 POSIX 调用；localtime_r 写入我们自己栈上的 tm 结构。
    unsafe {
        let secs = libc::time(std::ptr::null_mut());
        if secs < 0 {
            return None;
        }
        let mut tm: libc::tm = std::mem::zeroed();
        if libc::localtime_r(&secs, &mut tm).is_null() {
            return None;
        }
        Some((
            tm.tm_year + 1900,
            (tm.tm_mon + 1) as u32,
            tm.tm_mday as u32,
            tm.tm_hour as u32,
            tm.tm_min as u32,
            tm.tm_sec as u32,
        ))
    }
}

#[cfg(windows)]
fn windows_local_fields() -> Option<(i32, u32, u32, u32, u32, u32)> {
    use windows_sys::Win32::Foundation::SYSTEMTIME;
    use windows_sys::Win32::System::SystemInformation::GetLocalTime;
    // SAFETY: GetLocalTime 填充调用方提供的 SYSTEMTIME。
    unsafe {
        let mut st: SYSTEMTIME = std::mem::zeroed();
        GetLocalTime(&mut st);
        if st.wYear == 0 {
            return None;
        }
        Some((
            st.wYear as i32,
            st.wMonth as u32,
            st.wDay as u32,
            st.wHour as u32,
            st.wMinute as u32,
            st.wSecond as u32,
        ))
    }
}

/// 路径所在目录；裸文件名（无父目录）返回 "."（与 Go filepath.Dir 一致）。
pub fn parent_dir(path: &Path) -> &Path {
    match path.parent() {
        Some(p) if !p.as_os_str().is_empty() => p,
        _ => Path::new("."),
    }
}

/// 在 dir 下创建唯一临时文件（Go os.CreateTemp 语义：create_new 不覆盖已有文件）。
/// prefix/suffix 形如 ".chaoset-import-" 与 ".tmp"。
pub fn create_temp_file(dir: &Path, prefix: &str, suffix: &str) -> io::Result<(File, PathBuf)> {
    for _ in 0..100 {
        let seq = TEMP_SEQ.fetch_add(1, Ordering::Relaxed);
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.subsec_nanos())
            .unwrap_or(0);
        let name = format!("{prefix}{}-{seq}-{nanos}{suffix}", std::process::id());
        let path = dir.join(name);
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(file) => return Ok((file, path)),
            Err(err) if err.kind() == io::ErrorKind::AlreadyExists => continue,
            Err(err) => return Err(err),
        }
    }
    Err(io::Error::new(
        io::ErrorKind::AlreadyExists,
        "无法在目标目录创建唯一临时文件",
    ))
}

/// 把文件权限设为 0644（Unix；其他平台无操作）。CreateTemp 默认 0600，导出/词库
/// 文件需要可被同机其他工具读取，与旧实现保持一致。
pub fn set_mode_0644(path: &Path) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o644))
    }
    #[cfg(not(unix))]
    {
        let _ = path;
        Ok(())
    }
}

/// rename 原子替换目标。Windows 上 rename 覆盖可能因文件被占用/杀软扫描失败，
/// 失败时先删旧文件再重试一次（这是任务要求的兜底；Unix 不启用，保持纯原子语义）。
pub fn replace_file(tmp: &Path, target: &Path) -> io::Result<()> {
    match fs::rename(tmp, target) {
        Ok(()) => Ok(()),
        Err(first) => {
            if cfg!(windows) {
                if fs::remove_file(target).is_ok() {
                    if let Ok(()) = fs::rename(tmp, target) {
                        return Ok(());
                    }
                }
            }
            Err(first)
        }
    }
}

/// 同目录「临时文件 + 0644 + rename」原子写；不创建父目录。
/// 失败时清掉临时文件，旧目标保持不变。
pub fn write_atomic(path: &Path, content: &[u8]) -> io::Result<()> {
    let dir = parent_dir(path);
    let (mut file, tmp) = create_temp_file(dir, ".chaoset-write-", ".tmp")?;
    let cleanup = |tmp: &Path| {
        let _ = fs::remove_file(tmp);
    };
    if let Err(err) = file.write_all(content) {
        drop(file);
        cleanup(&tmp);
        return Err(err);
    }
    if let Err(err) = file.flush() {
        drop(file);
        cleanup(&tmp);
        return Err(err);
    }
    drop(file);
    if let Err(err) = set_mode_0644(&tmp) {
        cleanup(&tmp);
        return Err(err);
    }
    if let Err(err) = replace_file(&tmp, path) {
        cleanup(&tmp);
        return Err(err);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    fn assert_stamp_shape(stamp: &str) {
        assert_eq!(stamp.len(), 15, "时间戳应为 YYYYMMDD-HHMMSS: {stamp}");
        let bytes = stamp.as_bytes();
        assert_eq!(bytes[8], b'-', "第 9 位应是短横线: {stamp}");
        for (idx, byte) in bytes.iter().enumerate() {
            if idx == 8 {
                continue;
            }
            assert!(byte.is_ascii_digit(), "时间戳只含数字/短横线: {stamp}");
        }
    }

    #[test]
    fn local_stamp_shape() {
        assert_stamp_shape(&local_stamp());
    }

    #[test]
    fn utc_stamp_shape() {
        assert_stamp_shape(&utc_stamp_now());
    }

    #[test]
    fn parent_dir_handles_bare_name() {
        assert_eq!(parent_dir(Path::new("dict.tsv")), Path::new("."));
        assert_eq!(
            parent_dir(Path::new("/tmp/a/dict.tsv")),
            Path::new("/tmp/a")
        );
    }

    #[test]
    fn temp_files_are_unique() {
        let dir = tempdir().unwrap();
        let (file_a, path_a) = create_temp_file(dir.path(), ".chaoset-test-", ".tmp").unwrap();
        let (file_b, path_b) = create_temp_file(dir.path(), ".chaoset-test-", ".tmp").unwrap();
        drop(file_a);
        drop(file_b);
        assert_ne!(path_a, path_b);
        assert!(path_a.is_file() && path_b.is_file());
        assert!(path_a
            .file_name()
            .unwrap()
            .to_string_lossy()
            .starts_with(".chaoset-test-"));
    }

    #[test]
    fn write_atomic_replaces_target_and_sets_mode() {
        let dir = tempdir().unwrap();
        let target = dir.path().join("out.tsv");
        fs::write(&target, "旧内容").unwrap();
        write_atomic(&target, "新内容".as_bytes()).unwrap();
        assert_eq!(fs::read_to_string(&target).unwrap(), "新内容");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(&target).unwrap().permissions().mode() & 0o777;
            assert_eq!(mode, 0o644);
        }
        // 目录里不应残留临时文件
        let leftovers: Vec<_> = fs::read_dir(dir.path())
            .unwrap()
            .filter_map(|entry| entry.ok())
            .filter(|entry| {
                entry
                    .file_name()
                    .to_string_lossy()
                    .starts_with(".chaoset-write-")
            })
            .collect();
        assert!(leftovers.is_empty(), "残留临时文件: {leftovers:?}");
    }

    #[test]
    fn write_atomic_fails_when_parent_missing() {
        let dir = tempdir().unwrap();
        let target = dir.path().join("missing").join("out.tsv");
        assert!(write_atomic(&target, b"x").is_err());
        assert!(!dir.path().join("missing").exists());
    }
}
