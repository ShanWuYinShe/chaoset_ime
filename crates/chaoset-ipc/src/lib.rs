//! daemon IPC 协议（4 字节大端长度 + JSON，1 MiB 上限）与客户端。

pub mod client;
pub mod dto;
pub mod protocol;
