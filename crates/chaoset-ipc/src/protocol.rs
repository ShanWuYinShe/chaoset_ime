//! 帧协议：4 字节大端长度 + JSON 载荷，上限 1 MiB。

use std::io::{self, Read, Write};

pub const MAX_MESSAGE_SIZE: usize = 1 << 20;

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct Request {
    pub id: u64,
    pub method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub params: Option<serde_json::Value>,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct Response {
    pub id: u64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<serde_json::Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// None = 对端在帧边界正常关闭；body 短读/长度非法 = Err。
pub fn read_frame<R: Read>(reader: &mut R) -> io::Result<Option<Vec<u8>>> {
    let mut header = [0u8; 4];
    match reader.read_exact(&mut header) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(e) => return Err(e),
    }
    let size = u32::from_be_bytes(header) as usize;
    if size == 0 || size > MAX_MESSAGE_SIZE {
        return Err(io::Error::new(io::ErrorKind::InvalidData, format!("消息长度 {size} 非法（上限 {MAX_MESSAGE_SIZE}）")));
    }
    let mut buf = vec![0u8; size];
    reader.read_exact(&mut buf)?;
    Ok(Some(buf))
}

pub fn write_frame<W: Write>(writer: &mut W, payload: &[u8]) -> io::Result<()> {
    if payload.is_empty() || payload.len() > MAX_MESSAGE_SIZE {
        return Err(io::Error::new(io::ErrorKind::InvalidInput, format!("消息长度 {} 非法（上限 {MAX_MESSAGE_SIZE}）", payload.len())));
    }
    writer.write_all(&(payload.len() as u32).to_be_bytes())?;
    writer.write_all(payload)?;
    writer.flush()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frame_roundtrip_is_big_endian() {
        let mut buf = Vec::new();
        write_frame(&mut buf, b"{}").unwrap();
        assert_eq!(&buf[..4], &(2u32).to_be_bytes());
        assert_eq!(&buf[4..], b"{}");
        assert_eq!(read_frame(&mut &buf[..]).unwrap().unwrap(), b"{}");
    }

    #[test]
    fn read_rejects_zero_and_oversize() {
        assert!(read_frame(&mut &[0u8, 0, 0, 0][..]).is_err());
        let mut oversize = ((MAX_MESSAGE_SIZE + 1) as u32).to_be_bytes().to_vec();
        oversize.push(0);
        assert!(read_frame(&mut &oversize[..]).is_err());
        // 恰好等于上限是合法的（上限含等号），先给满 body。
        let at_limit = vec![b'x'; MAX_MESSAGE_SIZE];
        let mut buf = (MAX_MESSAGE_SIZE as u32).to_be_bytes().to_vec();
        buf.extend_from_slice(&at_limit);
        assert_eq!(read_frame(&mut &buf[..]).unwrap().unwrap().len(), MAX_MESSAGE_SIZE);
    }

    #[test]
    fn read_eof_at_frame_boundary_is_none() {
        // 头阶段 0 字节即 EOF：对端在帧边界正常关闭。
        assert!(read_frame(&mut &[][..]).unwrap().is_none());
        // 头阶段短读（只读到半个长度头）同样按 EOF 处理（read_exact 的
        // UnexpectedEof 语义），不算 body 短读。
        assert!(read_frame(&mut &[0u8, 0][..]).unwrap().is_none());
        // 但 body 短读必须报错：不能把截断的载荷当成完整消息。
        let mut truncated = 4u32.to_be_bytes().to_vec();
        truncated.extend_from_slice(b"{\"");
        assert!(read_frame(&mut &truncated[..]).is_err());
    }

    #[test]
    fn write_oversize_rejects_without_writing_any_byte() {
        let mut out = Vec::new();
        let oversize = vec![0u8; MAX_MESSAGE_SIZE + 1];
        let err = write_frame(&mut out, &oversize).unwrap_err();
        // 长度校验在写头之前：超限时一个字节都不能写，否则对端会读到半个帧。
        assert!(out.is_empty(), "超限帧不得写出任何字节");
        assert!(err.to_string().contains("上限"));
        // 空载荷同样拒绝（读侧 size==0 非法，写出来就是坏帧）。
        let mut out = Vec::new();
        assert!(write_frame(&mut out, b"").is_err());
        assert!(out.is_empty());
    }
}
