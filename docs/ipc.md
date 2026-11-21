# IPC 协议参考

daemon 与壳之间的纯数据协议;帧格式与传输契约的现行摘要见 AGENTS.md「IPC 协议契约」。

帧格式：4 字节大端长度 + JSON 载荷（上限 1 MiB）。

- 请求：`{"id":1,"method":"input","params":{"text":"nihao","half":false}}`
  （`half` 可选：壳侧全/半角标点状态，**每次请求随参数下发，daemon 不保存**
  ——壳是该状态的唯一真源。省略时按全角处理（历史默认），不会回落到任何
  会话残留值。双拼 dpPending==0 的 `;` 走 Punct 语义，宽度必须用本次壳状态，
  否则会有 1 键瞬态；纯传参语义下壳与 daemon 的状态不可能不同步。）
- 响应：`{"id":1,"result":{...}}` 或 `{"id":1,"error":"..."}`

方法（19 个）：`ping` / `input` / `backspace` / `backspaceWord`（按音节回退） /
`reset`（清组词**与**学习上下文：Esc/失焦/新会话）/ `resetComposition`（只清组词、
**保留** prev/history：切中英、透传等「用户还会继续打字」的场景）/ `commitRaw`
（回车：返回原始拼音并清空组词）/ `select`（`{"index":0}`，
返回 `{"committed":"你好"}`）/ `selectPrefix`（`{"index":0}`，**渐进上屏**：只消耗
候选音节对应的 input 前缀，返回剩余组词状态 + `committed`，壳据此上屏前缀段并
用剩余 `preedit` 继续组词——三端选词走此路径）/ `nextPage` / `prevPage`（候选翻页，
返回当前组词状态，含 `page`/`page_count`）/ `punct`（`{"key":","}` 中文标点：
组词中返回 `{"committed":"你好，"}`，空组词返回 `{"committed":"，"}`）/
`associate`（`{"sylls":["xian","zai"]}`，返回与 Composition 同构的联想候选列表）/
`associateSelect`（`{"word":"现在时间","sylls":[...]}`，联想词上屏）/
`userStats` / `userClear`（清空全部学习记录，返回前 best-effort 落盘） /
`userDelete`（`{"word":"你好","sylls":["ni","hao"]}`，返回 `{"deleted":true}`；
`sylls` 为空时省略该键；返回前 best-effort 落盘） / `userExport`（用户词典导出：
`offset`/`limit` 按行分页，返回 `{"tsv":"...","next":N}`，`next=-1` 表示已导出完） /
`ui`（`{"settings":true}`，Windows 懒启动单例 UI 宿主；返回 `{"started":true}`）。
`Candidate` 新增可选 `segments`（仅整句式候选携带，引擎学习用的分段词文本；
壳无需渲染，解码时忽略即可），学习记录在 Words/Phrases 表内以 `segs` 持久化。
`Composition` 另有 `shuangpin`（双拼方案名，全拼为空；壳据此把双拼模式下的
`;` 键路由到 `input` 而不是 `punct`——mspy 的 ing 韵母键是 `;`）与顶层
`committed`（本次调用需壳写入文档的文本：渐进上屏段，或双拼 `;` 触发的
「首选词+标点」）。
IPC 是纯数据协议：daemon 只返回协议 DTO（Rust 客户端侧为
`crates/chaoset-ipc/src/dto.rs` 的 `Candidate` / `Composition` / `UserStats`，
字段全部 `serde(default)`，兼容旧/新 daemon），
候选窗由 mac/Windows 壳原生自绘（macOS 用 AppKit，见
`shims/macos/src/CandidatePanelView.swift`；Windows 用 GDI），Linux 不重绘面板、
用 ibus 原生 LookupTable/Preedit 呈现；daemon 不提供贴图渲染。

视觉参数（几何 + 浅/深配色）的基准期望值内联在 `shims/macos/tests/visual-parity.sh`（
**无运行时消费者**）；mac/Windows 两份壳实现按它手写。Go↔macOS 的布局常量
（5 项）+ macOS 参考常量（含 divider）+ 浅/深各 13 色由
`shims/macos/tests/visual-parity.sh` 逐项机器断言；Windows 侧目前只有 pt
口径约束与交叉编译/核心行为测试，无运行时视觉断言（见 docs/ui-parity.md
「机器检查覆盖」）。

传输形态：macOS 用 Unix socket；Linux 用 Unix socket（`$XDG_RUNTIME_DIR/chaoset/chaoset.sock`）；
Windows daemon 使用命名管道（`-pipe`，按当前用户 SID 生成、ACL 限当前用户）；
安装器 Run 项与 TSF 懒启动均用命名管道。没有网络端口，也没有回退传输。
命名管道的安全描述符除 DACL（当前用户 + SYSTEM + ALL APPLICATION PACKAGES）外
还带低完整性标签 `S:(ML;;NW;;;LW)`：Windows 11 的开始菜单/搜索框/UWP 是
AppContainer（低完整性）进程，缺这个标签时它们只能连、写不进，表现为「按了键、
daemon 也在、却没有候选」。AppContainer 宿主里壳不发起进程，只连已在运行的 daemon。

## 客户端（Rust `crates/chaoset-ipc`）

daemon 与 CLI/工具共用的唯一 Rust 客户端是 `crates/chaoset-ipc`（daemon 自身的帧读写也复用
同一份 `protocol` 实现，不再有第二份 Request/Response/上限常量）；入口 `client::Client`：

- 传输：Unix socket（macOS/Linux，`Client::connect_unix(path)` 可注入路径，默认路径由
  `chaoset_config::paths::socket_path()` 给出）与 Windows 命名管道
  （`Client::connect_pipe(name)`，名字由 `chaoset_config::paths::default_pipe_name()`
  按当前用户 SID 生成 `\\.\pipe\chaoset-<SID>`；`ERROR_PIPE_BUSY` 时每 10ms 重试、最多 2s，
  其它错误立即失败）。`Client::connect_default()` 取平台默认端点。
- 单连接串行：请求 id 从 1 自增，一次只有一个请求在途；需要并发就各自建连接。
- 每次调用 5s 截止（Unix 用 socket 读写超时；Windows 用 overlapped I/O +
  `WaitForSingleObject`，超时 `CancelIoEx` 回收请求），调用结束清除截止时间。
- 关闭连接规则：响应 id 与请求 id 不匹配（流已错位）→ 关连接并报
  `响应 ID 不匹配`；IO/解码/超时错误 → 关连接。**daemon 返回 `error` 不关连接**，
  错误原样包成 `daemon 返回错误: <e>`。连接关闭后再次调用直接报「连接已关闭」，
  需要重新连接。
- `user_export(phrases_only)` 在客户端循环拼接：`limit=2000`、`offset` 从 0 递增，
  `next<0` 结束；`next<=offset` 报 `导出分页 next=... 未前进`；单页超过 1 MiB 报协议
  上限；最多 1000 页保护。返回值是完整 TSV，调用方无需感知分页。
- daemon 的 `userClear` / `userDelete` 在 IPC 响应前 best-effort `save()` 落盘（失败只写
  stderr、不影响响应；内存已更新，2s 后台 flush 会重试），兑现「内存与磁盘同时生效」；
  后台 flush 与 IPC 落盘共用一把锁，避免并发写坏同一个 temp 文件。
- 响应 DTO 在 `dto` 模块：`Composition` / `Candidate` / `UserStats`，字段全部
  `#[serde(default)]`；客户端只解码协议 DTO，不依赖引擎类型。
