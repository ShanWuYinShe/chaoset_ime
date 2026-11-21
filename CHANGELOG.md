# 更新日志

本文件记录各阶段的功能、修复与验收结论；README 只保留当前架构、安装与使用文档。

## 未发布

- **重构(引擎)：Rust 查询保真收尾——差分 34 场景**全部一致**（含 score）**：
  - `TrieNode.subtree_min` 由「exact_words 排序后第一个（=最高频词索引）」修正为**子树内最小词条下标**（Go `dictNode.subtreeMin`）——这是 prefix 逐层展开的确定性次序键，取错会让简拼展开集合偏差；
  - prefix 池的逐层 BFS 按 Go `queryTokens` 的真实结构重写：**ranked 跨起点累积、每处理一个起点就重排截断到 12 节点并收集其词条**（此前做成「全局只排一次取 12」，简拼 nh 的 prefix 池只有 13 条 vs Go 20 条，漏掉 7 条三音节扩展词）；
  - 找回「错拼输入不追加单字兜底」闸门（项目硬约束，此前重构中丢失）：`lianxiagn` 多出 18 个冷僻同音单字的问题消除；
  - 差分 34 场景用户可见行为**全部一致**（候选顺序/预编辑/页数/上屏文本/score 差异均为 0）；语料 5,655 场景首屏全同 full 99.9% / tail1 99.6% / tail2 99.6% / mixed 99.6% / initials 98.4%（无退化）；
  - 新增回归：`test_query_exact_and_prefix_fragment_bfs`（简拼 nh 双池条数与内容）、`test_typo_input_has_no_single_char_backfill`（错拼不出现只靠兜底层召回的冷僻同音字）。
- **重构(引擎)：Rust 双拼对齐 Go——Rime schema 规则管线建表 + 输入存解码音节**：
  - 键位表从手写简化表改为忠实移植 Go 的 Rime schema algebra 规则管线（derive / xform / op 三类规则 + keepHead、键位小写化、派生码优先于透传的冲突消解），修复自然码 k 键同时映射 uai/ing（`hk` 被误解成 huai）、ao 键位错成 c 等偏差；三方案已知键位与完整性断言与 Go 同一份期望值；
  - 双拼输入形态对齐 Go：`Input` 里两键成对解码后把**音节**写进输入串（不是键码），`;` 无暂存首键时走 punct、无效键码对整体丢弃；补 `dp_pending`/`dp_lens` 状态，退格按「按键事件」整段回滚、暂存首键的退格只清暂存、`BackspaceWord` 复用退格、重置换方案时清状态、`afterAppend`（超长截断 64 / 退格栈对账 / 回第一页 / 打断标点循环）；
  - 差分场景一致 30→32：`dp-zrm-nihk`、`dp-flypy-nihc` 转一致；语料 5,655 场景指标不变（全拼路径未受影响）；
  - 新增 41 项单测覆盖：三方案已知键位抽查、反向表完整性（每音节有两键码 / 自然码 lo→luo）、会话级双拼流式集成（逐键解码 + 退格粒度）。
- **重构(引擎)：Rust 召回层保真——连写匹配层、错拼闸门、模糊变体笛卡尔积、上限口径对齐**：
  - 新增「拼音连写 == 输入」匹配层（Go `addJoinedMatches`，rankExact）：不依赖切分枚举、只沿词库已有词条匹配，兜住枚举上限导致的漏匹配；主词典与用户词库各一份查询；错拼形态与含显式分隔符的输入不参与（Go 同闸门）；
  - 错拼形态闸门（Go `addTokenMatches`）：主查询换 `QueryTokensExact`（不展开 prefix 子树）、不做逐级回退、不追加单字兜底——项目硬约束「错拼输入不追加单字兜底」补齐；
  - 模糊音改在 token 主循环内做**变体笛卡尔积**（Go `expandTokenVariants`，封顶 8 条路径），删除原先只模糊首音节的简化层——模糊候选与 Go 一样进入正常层级而非固定降权；
  - 召回上限分配顺序对齐 Go（单段 300 → 多段简拼 120 → 常规 20）：单段声母残留输入（渐进上屏后的 `x`）此前被当成简拼只取 120，逐字挑选不可达；
  - 差分场景一致 28→30（`fuzzy-shishi`、`select-prefix-progressive` 转一致）；语料 5,655 场景首屏全同 mixed 99.5%→99.6%，其余持平（无退化）；
  - 新增 `test_expand_token_variants`（关模糊一条原路径、开模糊首条为原路径且封顶 8）。
- **重构(引擎)：Rust 联想（Associate）迁移——按上屏词的字续接 + 上下文共现排序**：
  - 主词典新增 `query_word_prefix`（Go `QueryWordPrefix`）：只续接「以刚上屏词开头且更长」的词条，按词频降序；
  - 用户词库新增 `QueryPhrasesByWordPrefix` / `QueryWordsByWordPrefix`（词组进化 + 调频词两张表）与 `score_full`（Words 调频 + Bigram 共现，与组词打分的 `score()` 口径分开）；`UserRecord` 补 `sylls` 字段（serde 默认，向后兼容）；
  - 会话侧补 `prev_sylls`（Go `s.prevSylls`）与 `infer_segs` / `entry_segs` / `strip_at_segment_boundary`：**整句式学习记录只在用户确认过的分段边界剥前缀**（项目红线：上屏「今天」给「星期一」，上屏「今」绝不给「天星期一」）；
  - 排序与去重对齐 Go：字前缀层优先、层内按加成后得分降序、同分按文本稳定排序，上限 30；
  - 差分场景一致 27→28（`associate-basic` 由「错误地按拼音前缀返回」修正为与 Go 相同的空结果）；语料 5,655 场景指标与本轮前完全一致（组词路径未受影响）；
  - 新增双向回归：`test_associate_word_prefix`（同字续接、不混入同音其它字、词频降序）与 `test_associate_sentence_only_at_segment_boundary`（边界给续接、非边界不给整词尾巴）。
- **修复(输入体验)：中文模式下大写字母直接入组词，回车原样上屏英文——不必为大写切输入法**：
  - 三端壳此前都先丢大写（macOS `isCompositionChar` 只认 a-z、Windows Shift+字母直接放行给宿主、Linux 只转发 a-z），引擎再 `ToLower` 后过滤一次，用户想打一段大写英文只能来回切中英；
  - 现在三端壳把 a-z/A-Z 都转发给引擎（Windows 按 Shift⊕CapsLock 还原正确大小写），引擎把字母**原样保留**在组词串里（`s.input`），回车/`CommitRaw` 原样上屏；拼音切分、查词、纠错、联想一律走 `matchInput()` 小写视图，`NIHAO` 与 `nihao` 候选一致；
  - 预编辑按切分边界还原原始大小写（`NI'HAO`），所见即回车所得；逐字符退格保留剩余大小写；
  - 契约由 `make check-uiparity`「字母入组词大小写」组锁死（引擎 + 三端壳任一处回退即红），按键矩阵同步更新 `docs/ui-parity.md`；
  - 回归：`TestUppercaseInputPreserved` / `TestCommitRawKeepsCase` / 更新的 `TestInputFiltersNonLetters`（大写保留、非字母丢弃）。

- **测试(重构)：新增 Go ↔ Rust 引擎差分对比工具（`tools/engineparity` + `make engineparity`），建立 rust 迁移量化基线**：
  - 同一份场景语料（`tools/engineparity/scenarios.jsonl`：切分/简拼/翻页/退格/渐进上屏/标点/联想/纠错/模糊音/繁体/双拼共 30 场景）分别驱动 Go `engine.Session` 与 Rust `chaoset-engine::Session`（后者经 `crates/chaoset-engine` 的 `replay` bin），逐场景逐步骤对比用户可见行为（候选序列、preedit、页码、committed）；候选打分 score 只做信息性统计；
  - 首次基线（`make engineparity` 取数）：30 场景一致 4 个、差异 26 个；主要缺口为排序键、召回范围（page_count）、非小写字符过滤、无候选兜底、简拼展开与退格后组词保留，后续迁移按此清单逐项推进并以该工具复验；
  - 修复 Rust 候选音节分隔符协议漂移：`Candidate.syllables` 由空格连接改为撇号连接（`ni hao` → `ni'hao`），与 Go `compose_state.go` 及三端壳消费口径一致。
- **重构(engine)：Rust 召回分层体系对齐 Go（`compose_query`/`compose_rank` 核心迁移），末段残缺命中率 32%→89%**：
  - `dict.rs`：新增 `query_exact_and_prefix` 双池查询（exact=音节路径词条同音字全量、prefix=子孙词组，独立上限独立截断）与 `query_continuation` 末段续接查询（head 精确下钻 + frag 前缀展开两层）；
  - `session.rs`：引入匹配层级（rank）——层级是第一排序维度，词频打分只在同层内起作用，替换旧的「词频 × 5000/1000 加成」打分模拟方案；新增末段残缺续接双路径（末音节可延长 zhonggu→中国、末段整体非音节 quanmia→全面，补全按字符数折价 `continuationPenalty=30`）、逐级回退（drop≤6、≥4 段首音节放开同音字全量、nihaox 置顶提升特例）、首段全量单字收尾（rankSingleBackfill）、单段输入只出单字硬约束（xian 只出单字，西安需显式 xi'an；zh/ch/sh 双字母简拼仍出词）、超段仲裁（仅全拼输入，完全消耗优先于音节超段）；
  - `syllable.rs`：新增 `syllable_continuations`/`split_exact_syllables`/`partial_tail_splits` 续接辅助；
  - 量化对比（`tools/engineparity` + skill 口径确定性语料 1,131 词 × 5 形态）：tail1（打一半，最高频形态）命中率 31.9%→**89.0%**（Go 88.8%）、首选率 13.8%→**45.3%**（Go 45.4%），tail2 命中率 42.8%→**59.9%**（Go 58.5%），full/initials/mixed 持平或略优，首屏全同 88.9%–98.5%；30 场景差分一致 4→7、score 信息性差异 186→21；
  - 新增 5 项双向回归测试（续接压过巧合词、残缺末段领跑、单段只出单字、回退置顶+单字收尾、超段仲裁），合计 22 项单测全过；
  - 未迁移项（后续清单）：组合候选层（rankComposition 的分词 DP）、DP 纠错（typo_dp）与错拼固定第 3 位、TypoSuspicious 形态的纠错主路径。
- **重构(engine)：Rust 声母碎片导航与输入过滤对齐 Go 契约（基线缺口「字符过滤/简拼展开/无候选兜底」三项关闭）**：
  - `dict.rs`：`query_prefix`/`query_exact` 统一走碎片感知 walk——单/双字母声母 token（z/x/n/zh…）按「音节以该声母开头」展开子树（此前 Trie 只认整音节键，`nh`/`zzz` 一类碎片输入召回为空）；
  - `session.rs` compose：两阶段切分仲裁——纯音节 pattern 优先召回，全部空手而归才回退含碎片的 pattern（`xian` 不再混入 `xia'n` 切分的瞎闹/下奶类候选；碎片只在整段无音节解时兜底，对齐 Go 观察行为）；
  - `session.rs` input：对齐 Go 契约——整体小写、只收 `[a-z']` 静默丢弃其余字符（标点唯一真源是壳，组词中标点走 `punct()`），双拼模式放行 `;`（mspy 的 ing 韵母键）；
  - 新增 4 项单元测试锁定行为：碎片导航（dict）、输入过滤、纯音节优先不反噬、简拼/兜底召回；差分对比（`make engineparity`）：`NI1hao!?` 过滤、`nh` 简拼、`zzz` 兜底、`xian` 纯音节四类场景首候选与 Go 一致，30 场景首候选一致 27 个；剩余差异集中于排序键体系与 DP 纠错（`typo_dp` 未迁移），后续按清单推进。

## v0.3.40（2026-11-21）

本版本为**任务栏图标 100% 满幅撑满（0 留白消除任务栏偏小问题）、全套可执行程序与窗口注入专属应用图标与开始菜单设置快捷方式补齐**的 patch 发版（重构完成前 minor 保持 0.3 不变）：

- **优化(视觉资产)：全分辨率图标 100% 满幅撑满（0 留白），彻底解决任务栏图标偏小问题**：
  - 查明原大尺寸（`sz > 48`）配置了 4% 的外圈留白 `pad`，导致系统截取或高 DPI 缩放渲染到任务栏时边缘自带一圈透明外框、视觉严重缩小；
  - 全面重构全分辨率（16 到 512）矢量生成算法，所有尺寸 100% 满幅撑满（`x=0 y=0 width=sz height=sz`，严格 0 留白），四角紧凑微倒角避免削切，大字母「C」与四芒星标「✦」占满视觉主体的 85% 以上，份量与方块汉字完全对齐；
  - 重新生成跨平台全套高精资产：Windows `chaoset.ico`（9 组分辨率层，小尺寸原生 32bpp DIB 封装）、Linux hicolor PNG 各级图标与 scalable SVG。
- **特性(应用生态)：全套可执行程序与窗口全面注入专属应用图标与版本信息**：
  - 为 Slint 跨平台设置中心 `chaoset-settings.exe` 注入 Windows PE 图标资源（`build.rs`），并在 `ui/app.slint` 声明窗口 `icon`，确保启动后窗口标题栏与任务栏窗口卡片清晰呈现品牌图标；
  - 为 Rust 守护进程 `crates/chaosetd` 注入 Windows 图标资源（`build.rs`）；
  - Windows 安装程序（NSIS）在开始菜单中自动生成带图标的「Chaoset 设置.lnk」快捷方式。

## v0.3.39（2026-11-20）

本版本为**核心输入引擎与守护进程向 Rust 全量迁移（亚毫秒响应、内存降至 <10MB、零 GC 停顿）、Windows 任务栏中英指示器乱码源码级根治、全尺寸品牌星标图标还原精修与设置中心黑框弹窗消除**的重量级 patch 发版（重构完成前 minor 保持 0.3 不变）：

- **重构(核心引擎)：核心拼音算法与状态机全面迁移至 Rust (`crates/chaoset-engine`)**：
  - 彻底摆脱 Go 垃圾回收（GC）在输入热路径上可能引起的潜在微停顿，实现确定性的亚毫秒级低延迟响应与内存极致压缩（守护进程驻留内存由 100~150MB 骤降至 <10MB，二进制体积仅 2.1MB）；
  - 全面重写拼音处理流水线：覆盖 420 个标准普通话音节全集、Trie 树前缀与精确双重检索字典、自然码/微软双拼/小鹤双拼方案、简繁体转换、智能错拼纠错与模糊音展开；
  - 完整实现 16 种输入状态机交互与用户自学习模型：支持组词中按标点自动上屏首选并追加中文标点、精确音节回退、多音节前缀级联、时间衰减调频与 Bigram 上下文共现自学习，100% 保持与既有薄壳的 IPC 协议与行为兼容。
- **重构(守护进程)：跨平台高性能 IPC 守护进程迁移至 Rust (`crates/chaosetd`)**：
  - 基于 Rust 实现原生 IPC 服务端与 19 项方法分发，严格遵守 4 字节大端长度 + JSON 载荷帧协议；
  - 平台原生支持：macOS / Linux Unix Domain Socket（异步连接接入与 flock 互斥锁）与 Windows 命名管道（低完整性 SDDL 安全描述符 `S:(ML;;NW;;;LW)`，AppContainer 宿主全面读写可用，会话级互斥体防重）；
  - 运行 C++ 原生契约测试（`shims/windows/tests/core_test.cpp`），**42 项原生输入行为与边界契约 100% 验证通过（PASS）**。
- **修复(Windows 壳)：源码层彻底根治任务栏中英状态指示器乱码（`è<±` → `英`）**：
  - 深入剖析 MSVC 在英文 Runner 环境下编译 UTF-8 无 BOM 源码时将非 ASCII 字符解析为本地代码页（Windows-1252）的缺陷；
  - 在 `ui_host.cpp`、`tip_keys.cpp`、`tip_registration.cpp` 中将任务栏中英状态字面量、提示文本与系统语言 Profile 描述全面转换为标准 Unicode 转义序列（`L"\u82f1"`、`L"\u4e2d"` 等），从源码根本上消除任何操作系统代码页或编译开关差异带来的字符集污染。
- **优化(视觉资产)：完整还原全尺寸品牌星标（✦）与优雅视觉质感**：
  - 响应用户反馈，彻底纠正过度简化的问题，完整恢复并强化 Chaoset 品牌经典核心标识——饱满挺拔的「C」+ 右上角璀璨闪耀的四芒星标「✦」；
  - 针对任务栏小尺寸（16~32px）引入数学内凹贝塞尔曲线算法，在有限像素内绘制极具张力与锐利度的四芒星，与加粗字母 C 形成视觉黄金分割，饱满撑满按键且低分辨率屏幕下绝不模糊；
  - 重新生成跨平台全套高精资产：Windows `chaoset.ico`（9 组分辨率层，小尺寸原生 32bpp DIB 封装）、Linux hicolor PNG 各级图标与 scalable SVG。
- **修复(设置中心)：彻底消除 Windows 设置中心子进程短暂黑框闪烁 (Close #60)**：
  - 在 Rust Slint 设置中心 `shims/settings/src/bridge.rs` 中，为所有 Windows 平台调用 `chaosetctl` 统一注入 Win32 创建标志 `CREATE_NO_WINDOW (0x08000000)`；在 `lib.rs` 中查询系统主题的 `reg.exe` 进程同样补齐该标志，彻底消除开关联想输入与操作设置时的控制台黑框闪现。

## v0.3.38（2026-11-20）

本版本为**Windows 任务栏中英指示器 UTF-8 乱码彻底根治、超大满幅撑满图标重构与 Windows SDK 原生 GUID 规范化**的 patch 发版（重构完成前 minor 保持 0.3 不变）：

- **修复(Windows 壳)：彻底根治任务栏中英指示器乱码缺陷（`è<±` → `英`）**：
  查明 MSVC `cl.exe` 默认按系统 ANSI 代码页（Windows Server 英文 runner 上为 CP1252）解析无 BOM 的 UTF-8 源码、导致宽字符字面量 `L"英"`（`\xE8\x8B\xB1`）被转译为 3 字节乱码 `è<±` 的根因；在 `package_msvc.sh` 的 64 位 DLL、32 位 DLL 与单例 UI 宿主 `chaoset-ui.exe` 的全部 MSVC 编译指令中强制加入 `/utf-8` 编译开关（相当于 `/source-charset:utf-8 /execution-charset:utf-8`），彻底根治多语言 CI Runner 环境下的文本编码问题。
- **优化(视觉资产)：重塑 Windows 任务栏输入法图标（`chaoset.ico`），彻底做大撑满与方块汉字平衡**：
  针对用户反馈的“任务栏图标并未真正做大、只是简化了”的问题进行二次彻底重构：将小尺寸 SVG 圆角半径由 `rx=3.6`（椭圆直径 7.2px）收敛至 `rx=1.5`（与 GDI 绘制中英状态的 `cr=3` 椭圆直径严格一致），消除四个角的大面积削切与内缩；按键方块 100% 满幅撑满（0 到 sz），去除深色描边；将品牌字母「C」字号拉大至满格的 102%，笔画加粗至 14x15 像素（占据方块 75% 以上高度），视觉面积与饱满度彻底与任务栏「中/英」达到平衡。
- **规范化(Windows TSF)：全面接入 Windows SDK 原生 GUID 与最佳实践**：
  彻底清理 `shims/windows/src/tip_ids.hpp` 中历史遗留的自写 `k...` GUID 常量与手写接口声明，全面继承 Windows SDK `<msctf.h>` 标准接口与符号（`CLSID_TF_CategoryMgr`、`CLSID_TF_InputProcessorProfiles`、`GUID_TFCAT_TIP_KEYBOARD`、`ITfTextInputProcessorEx` 等）；在 `tip_registration.cpp` 中通过 `<initguid.h>` 规范化实例化 TSF 原生 GUID，并从 MSVC 链接指令中清理不存在的 `msctf.lib`。

## v0.3.37（2026-11-20）

本版本为**Windows 构建流水线原生 MSVC 重构（6~8 倍提速）与任务栏输入法图标对齐放大**的 patch 发版（重构完成前 minor 保持 0.3 不变）：

- **重构(流水线 & CI/CD)：Windows 构建与打包全面迁移至 windows-latest 原生 MSVC + NSIS**：
  彻底终结在 Ubuntu runner 上通过 apt 下载超 1GB mingw-w64 交叉依赖时反复遭遇的网络超时、重试与卡顿瓶颈（构建总耗时从 30~40+ 分钟骤降至 5 分钟左右，提速达 6~8 倍）；全面启用 Windows 预装的 Visual Studio 2022 Enterprise + Windows SDK 原生工具链（`cl.exe` + `link.exe` + `rc.exe` + NSIS），通过 `shims/windows/package_msvc.sh` 完成 64 位 `ChaosetIME.dll`、32 位 WOW64 `ChaosetIME32.dll`、单例 UI 宿主 `chaoset-ui.exe`、Go `chaosetctl.exe` 与 Rust Slint `chaoset-settings.exe` 的极速原生构建、COM 导出符号校验与 NSIS/ZIP 打包。
- **优化(视觉资产)：重塑 Windows 任务栏输入法图标（`chaoset.ico`），彻底根治偏小与噪点**：
  针对系统任务栏输入指示器图标明显小于左侧「中/英」状态指示器、且缩放后出现杂散噪点的问题进行彻底重构：移除破坏居中与视觉面积的右上角星标，全分辨率统一采用满幅实心圆角键帽（Full-bleed Keycap，无多余留白），外形几何轮廓与 `UiTray` 中英文指示器 1:1 对齐；将品牌字母「C」字号拉大至约 82% 满格高度（字重 900），在视觉份量、按键轮廓与字体高度上与方块汉字「中/英」达到饱满平衡；Windows `.ico` 资源的小尺寸（≤64px）采用标准未压缩 32bpp DIB（BMP）格式封装，彻底根治 Windows GDI/TSF 在高 DPI（如 125%/150%）下因 PNG 兼容性缺陷导致的回退到 16px 或模糊缩放。

## v0.3.36（2026-11-20）

本版本为**统一全能命令行工具 chaosetctl 瘦身升级、任务栏图标尺寸重构、右键删词保护与独立设置中心脱钩**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版重点清理安装包冗余文件、重塑任务栏输入法图标并修复候选窗交互细节：

- **重构(CLI & 打包)：正式定名与整合全能控制工具 `chaosetctl`，彻底消除安装包冗余**：
  将原本分散编译的多个独立 Go 工具（`chaoset-cli`、`chaoset-import`、`chaoset-settingsctl`、`chaoset-userdict` 等）彻底合并为单一的 `chaosetctl`（Windows 为 `chaosetctl.exe`），支持子命令（`cli`/`import`/`userdict`/`config`/`get`/`set`）与 argv[0] Multicall 多调用模式；Windows NSIS 安装脚本只打包写入 `chaosetctl.exe`，并在升级时自动清理旧版遗留的 5 个冗余 exe，安装包与安装目录直接瘦身近 15 MB；Linux 打包同步去冗余，统一只构建一份 `chaosetctl`（配合符号链接兼容旧脚本）。
- **优化(视觉资产)：任务栏输入法图标（`chaoset.ico` 与 hicolor）放大与重构，对齐中英文指示器**：
  针对 Windows 任务栏输入法指示器图标明显偏小的问题进行彻底重构：在任务栏小尺寸（16~48px）下去除多余留白（Inset 归零），采用满格实心圆角键帽，几何轮廓与 `UiTray` 中英文指示器 1:1 绝对对齐；将中心瓷白「C」字号放大至按键高度的 75% 并严格水平垂直居中，加粗字重，消除在任务栏中的弱化感；Windows `chaoset.ico` 扩展补齐 9 个分辨率层（含 20x20 125% DPI 与 28x28 175% DPI），杜绝缩放模糊；同步更新 Linux 各级 hicolor 图标与 SVG。
- **修复(Windows)：候选窗右键删除候选词防误杀与即时生效**：
  修复 Windows 下在候选窗上右击候选词时导致候选窗瞬间消失的缺陷：引入上下文菜单追踪保护状态（`contextMenuTracking_`），在右键菜单弹出期间屏蔽系统的伪失焦、前台切换与合成终止事件；菜单点击“删除该词”后，向 daemon 发送 IPC `userDelete` 请求并立即重新组词刷新候选窗，词条删除即时生效且候选窗平滑保持显示。
- **重构(设置中心)：三端设置窗口统一由 Slint 实现并独立脱钩，彻底根治 Windows GDI 噪点**：
  候选窗齿轮与托盘菜单统一脱钩拉起独立运行的 `chaoset-settings`（Windows `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS`），关闭句柄，不隶属于触发它的宿主应用，宿主退出时设置窗口独立存活直至用户手动关闭；支持已有实例检测并置顶，避免重复创建多个设置窗口；全端发布包全部应用 Release 最高级优化与深度 strip。

## v0.3.35（2026-11-19）

本版本为**Slint 跨平台统一设置中心、无头 UI 单元测试体系与多功能 CLI 整合**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.34` 之后 5 个提交：全新引入基于 Slint 声明式 UI 的跨平台统一设置中心（`shims/settings`），像素级 100% 对齐 21 项色彩与 10 项几何令牌；创新性地将 UI 一致性检查升级为基于 Slint 强类型编译与 `i-slint-backend-testing` 的内存无头单元测试体系；全端 CI/Release 流水线完整接入 Rust 检查、单测与交叉编译并随包分发；同时将各命令行工具收敛合并为单一的 `chaoset` 多功能命令（支持 Multicall 与子命令调用），并对原生 Windows 设置窗进行了焦点重影与边缘噪点修复。

- **特性(Slint UI)：基于 Slint 实现跨平台统一设置中心 (`shims/settings`)**：
  引入 Slint 现代化声明式 UI 框架构建跨平台独立设置中心，生成单一原生可执行文件（Linux ELF / Windows PE32+ GUI / macOS Mach-O），运行时无额外动态库依赖。界面 100% 对齐 `docs/ui-parity.md` 规范与 21 项色彩令牌（涵盖卡片、开关、单选胶囊与文字等浅深双色系）；提供左侧三页导航（⚡ 拼音输入、📥 词库与数据、ℹ️ 关于与更新）、卡片容器、平滑过渡动画开关、单选软胶囊与 1:1 实时联动候选窗微缩展台；通过 `src/bridge.rs` 桥接 Go CLI 命令完成配置持久化与词库导入，并集成 `rfd` 原生系统文件选择对话框。
- **测试(UI 一致性)：基于 i-slint-backend-testing 的无头 UI 单元测试体系**：
  改进传统基于 tools 字符串匹配的 UI 校验机制，升级为 Slint 编译器强类型保障与内存无头单元测试：新增 `shims/settings/tests/ui_test.rs`，在无需显示服务（Headless）的环境下自动化遍历与断言 Light/Dark 浅深主题下的 21 项颜色令牌、10 项几何令牌、页面切换、主题切换与事件回调；`shims/settings/src/lib.rs` 规范模块化导出并严守行数预算（≤150行）；`src/bridge.rs` 补充配置反序列化、缺省容错与词库路径解析等核心逻辑单测，形成高置信度自动化防护网。
- **流水线(CI & Release)：全平台 CI 门禁、自动化无头测试与 Release 安装包集成**：
  在 `.github/workflows/ci.yml` 中全面接入 Slint 设置中心检查：`lint` 作业执行 `cargo check`，`test` 作业在预置 `libfontconfig1-dev` 环境下运行 `cargo test` 无头测试，`linux-e2e` / `macos-build` / `windows-build` 分别接入本地与 MinGW 交叉编译验证；在 `.github/workflows/release.yml` 中补齐 fontconfig 依赖与跨端编译打包流程，Windows 安装器（NSIS）正式捆绑并维护 `chaoset-settings.exe` 与 `chaoset.exe`，确保全平台发布产物完整可用。
- **重构(CLI)：合并命令行工具为统一的 chaoset 多功能命令**：
  抽离 `internal/tools/` 基础能力，新增统一二进制入口 `cmd/chaoset`。支持单一二进制运行所有管理命令（`import`、`userdict`、`config`/`settings`、`client`/`cli`、`version`）以及 busybox 风格的 Multicall (`argv[0]`) 多调用模式；保留原 `cmd/chaoset-*` 为薄转发层以 100% 保持向后兼容并满足既有工具契约。
- **修复(Windows)：消除原生设置窗控件周边光栅化噪点与焦点环重影**：
  针对 Windows GDI 原生设置窗在特定缩放与交互场景下的像素级细节进行修复：改进自绘控件的背景擦除与绘制逻辑，消除鼠标悬停及交互时光标切换带来的边缘光栅化毛边，修复按钮与单选胶囊控件获得点击焦点时的残留双层重影。

## v0.3.34（2026-11-18）

本版本为**Windows 设置窗质感重塑与 Fluent 现代视觉升级**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.33` 之后 3 个提交：Windows 设置窗告别模糊与粗糙，全面对齐现代 Windows 11 Fluent 审美——原生嵌入 PerMonitorV2 清单动态启用 PerMonitorAwareV2 彻底根治高分屏 DWM 位图缩放模糊、现代 YaHei UI 统一字族紧凑垂直居中、4 层透明度漫反射阴影消除生硬边界、DWM 系统圆角与双缓冲抗闪烁；控件子类化数组扩容至 16 并规范化销毁生命周期复位；引入 Fluent 垂直高亮指示胶囊、行级与卡片图标微锚点（⚡/🌐/🎨/📊/📥）、并在外观主题卡内自绘 1:1 实时联动候选窗微缩展台（随主题切换动态重绘 Preedit、高亮选中胶囊与微齿轮）。

- **重构(Windows)：设置窗 Fluent 质感提升、导航指示器、图标微锚点与候选窗实时微预览**：
  针对 Windows 设置窗质感欠佳、排版局促与视觉单调等痛点进行现代 Fluent 升级：侧边栏选中项引入 Win11 标志性垂直圆角高亮指示胶囊；关键配置卡片引入微图标锚点（⚡ 拼音输入、🌐 模式记忆、🎨 外观主题、📊 词库统计、📥 数据导入），建立清晰视觉扫描焦点；外观卡片右上角内嵌 1:1 候选窗微缩模型，跟随主题切换即时重绘联动；行项主标签改用 `g_settingsFontBold` YaHei UI，文字清晰度与层次显著提升；修复 MinGW 编译警告，行数严格控制在 1598 行（预算 ≤ 1600 行）。
- **修复(Windows)：设置窗控件子类化数组扩容与关闭生命周期状态复位**：
  解决 Windows 设置窗在多控件交互与二次打开时的悬停与手型光标失效问题：控件子类化数组 `g_ctlProcs` 容量扩容至 16，彻底覆盖全部 9 个自绘控件（含 133「深色」主题胶囊）；`WM_DESTROY` 时显式复位子类化计数与全部悬停/跟踪状态，杜绝窗口关闭后二次打开因计数未清空导致早退、控件丢失交互反馈的缺陷。
- **视觉(Windows)：高分屏 DPI 意识清单嵌入、YaHei UI 字族、卡片漫反射阴影与双缓冲**：
  根治 Windows 设置窗高分屏模糊与质感粗糙问题：`chaoset-ui.exe` 嵌入 PerMonitorV2 清单并在入口动态初始化 `SetProcessDpiAwarenessContext(PER_MONITOR_AWARE_V2)`，安装器启用 `ManifestDPIAware`，根治 DWM 位图缩放造成的糊感；统一升级为 Microsoft YaHei UI 现代字族；启用 `DWMWCP_ROUND` 现代化系统级圆角；卡片投影采用 4 层透明度渐变圆角漫反射；双缓冲彻底消除重绘刷新闪烁。

## v0.3.33（2026-11-18）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计
`v0.3.32` 之后 8 个提交：三端浮窗视觉体系统一收官——深色表面（候选窗/Toast）
统一为深海军蓝家族、Windows 浮窗在 Win11 改用 DWM 抗锯齿圆角（消除 region
锯齿债）、浅色 Toast 与托盘「英」键帽并入蓝白浅色族，并为 Toast/托盘配色
补齐机器断言（`make check-uiparity` 增至 53 组）；另同步修正 AGENTS.md 指引、
文档预写版本号与 CHANGELOG 自引用。

- **视觉(Windows)：托盘「英」键帽并入蓝白浮窗浅色族 + 托盘配色纳入机器断言**：
  任务栏托盘「中/英」自绘键帽的英文模式仍是暖陶配色残留（`#F6F3EE` 象牙白底 +
  `#C8C3B9` 暖灰边 + `#1E1B18` 暖黑字，v0.3.32 只改了中文蓝底），与蓝白体系
  割裂。本次英文模式并入候选窗/Toast 浅色面板族（底 `#F6F6F8` + 描边
  `#DDDDDF` + 文字 `#262628`）；中文模式 `#007AFF` 蓝底与设置窗 accent 同值
  不变；顶边高光为键帽造型语义保留。`tools/checkuiparity` 新增「托盘配色」组
  以 ui_host.cpp 整行三元片段锁住底/边框/文字三值；`docs/ui-parity.md` 新增
  「托盘键帽配色」表（macOS 不适用——中英指示由系统菜单栏图标承担，理由已注明）。
  改配色不更新文档表都会 FAIL。
- **加固(三端)：Toast 配色纳入机器断言，防止刚收敛的值再漂移**：上条把浅色
  Toast 收敛到候选窗面板族后，配色此前只有文档表没有机器守卫。本次 macOS 侧把
  `ModeToastView` 的配色抽成 `palette(isDark:)` 单一出口（draw 与度量工具共用），
  `panel_metrics` 导出 toastLight/toastDark，`visual-parity.sh` 新增 F 段对
  浅/深各 3 值逐色断言（编译集相应加入 CandidatePanel + CandidatePanelWindow）；
  Windows 侧 `check-uiparity` 新增「Toast 配色」组，以 mode_toast.cpp 的整行
  三元片段锁住 6 个预混合常量（单色值与候选窗 theme.hpp 共享、无法唯一定位）。
  两端任一改配色不更新 `docs/ui-parity.md`「Toast 配色」表都会 FAIL。
- **视觉(三端浅色)：模式 Toast 浅色配色两端收敛到候选窗浅色面板族**：macOS 浅色
  Toast 此前是 `white 0.97` 底 + `calibratedWhite 0.15` 文字（calibrated 灰阶的
  sRGB 实测有效值 #333333），与 Windows 的 #F6F6F8 底 / #262628 文字存在可见色差
  （文字一灰一黑）；描边两端深浅也不一致（mac 黑 0.15 vs Windows #C8C8CC）。本次
  macOS 浅色改为 #F6F6F8 底（alpha 0.95，半透明为平台差异）+ #262628 文字 + 黑
  alpha 1A 描边（有效 #DDDDDF），Windows 浅色描边 #C8C8CC → #DDDDDF，两端浅色
  Toast 与候选窗浅色面板完全同族；深色已是海军蓝家族（见本段「深色表面统一为
  深海军蓝家族」条）不变。
  `docs/ui-parity.md` 新增「Toast 配色」表作为两端单一事实源（实测值来自
  NSColor sRGB 转换探针，非目测）。
- **视觉(Windows)：候选窗与模式 Toast 在 Win11 改用 DWM 抗锯齿圆角，消除 GDI
  region 锯齿轮廓**：此前浮窗圆角靠 `SetWindowRgn(CreateRoundRectRgn)` 裁剪，
  region 边缘无抗锯齿，任何缩放下都是硬锯齿（docs/ui-parity.md 面板质感行记载的
  「圆角有锯齿」）。现在窗口创建时经 dwmapi 动态加载（零新静态导入，复用设置窗
  的 DWM 写入模式）申请 `DWMWA_WINDOW_CORNER_PREFERENCE = DWMWCP_ROUND`：
  成功（Win11+）则布局跳过 region，圆角与系统阴影由 DWM 呈现、边缘抗锯齿；
  失败（Win10/服务器核心）静默保持 SetWindowRgn 旧路径，老系统观感不变。
  自绘圆角半径（候选窗 pt(8)、Toast pt(10)）≥ DWM 系统圆角半径，裁剪区 ⊆ 绘制
  区，不会出现透明缺口；代价是自绘 1pt 描边的四角弧段被 DWM 裁掉，轮廓由 DWM
  抗锯齿边缘呈现（Win11 应用常态观感）。CS_DROPSHADOW 与 DWM 阴影是否叠加
  为**真机验收项**。候选窗与模式 Toast 两处浮窗同处理；公共助手
  `winutil::setDwmRoundedCorners` 供两个叶子模块共用。
- **视觉(三端深色)：候选窗与模式 Toast 深色表面统一为深海军蓝家族**：设置窗深色在
  v0.3.32 已是深海军蓝，而候选窗/Toast 深色仍是中性灰（#2C2C2E 面板 + #5A5A5E 键帽），
  同一产品的深色表面存在两种色相。本次把深色面板底统一为设置窗 cardBg `#1A2740`、
  键帽=controlBg `#223353`、正文/键帽数字=textPrimary `#F2F6FC`、页码/翻页箭头=
  textTertiary `#7E90AC`（macOS 基准值，带 alpha 的 NSColor），描边/分隔线沿用白
  alpha 随新底色自动带蓝调；Windows 按 round(bg*(1-a)+fg*a) 预混合固化对应有效值
  （panelBorder `#3A455B` / chipBorder `#59667E` / page `#62738E` / divider
  `#354056`）。macOS/Windows Toast 深色同步改海军蓝底 + 白 alpha 描边（与候选窗
  同款描边）。高亮蓝 #0066EB、学习标记绿 #30D158 为语义色不变；浅色板完全不变。
  对比度：正文 13.8:1、键帽数字 11.6:1、装饰页码 3.1:1（≥3:1）。基准期望值同步
  `shims/macos/tests/visual-parity.sh` 与 `docs/ui-parity.md`。

## v0.3.32（2026-11-17）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计
`v0.3.31` 之后 3 个提交：三端设置窗与图标由「暖陶纸感」改回蓝白配色（对齐候选窗
`#007AFF`/`#0066EB` 高亮蓝），并新增用户可见的外观主题设置（跟随系统/浅色/深色），
切换即时生效、无需改代码重编译。

- **重构(三端主题)：设置窗改回蓝白配色并支持主题切换**：用户反馈暖陶配色不好看，
  更偏好与候选窗一致的蓝白色系。`docs/ui-parity.md` 的 21 个设置窗视觉令牌（单一
  事实源）由暖陶色改为蓝白体系（浅色蓝白面板 + `#007AFF` 强调蓝、深色深海军蓝 +
  `#0066EB`），三端逐值同步并由 `checkuiparity`「设置窗视觉令牌」组逐值断言；
  应用图标（SVG/PNG/ICO/icns 生成源）改为蓝白渐变、Windows 托盘「中/英」改为蓝底
  白字；macOS 输入源模板图标按项目硬约束不动。
- **新增(设置)：外观主题三档（跟随系统/浅色/深色）**：Go `internal/settings` 新增
  `theme`（默认 `system`），三端分别落到注册表 `Theme`、UserDefaults `ChaosetTheme`、
  config.json `theme`，可用 `chaoset-settingsctl get/set theme` 查看/修改；三端设置窗
  「输入」页新增选择器，macOS/Windows 候选窗与设置窗即时重绘、Linux GTK3 设置窗即时
  生效；Windows 深色判定统一收口 `uiEffectiveDark()`。
- **修复(macOS)：候选窗主题解析自包含**：把主题解析放在 `CandidateTheme.swift`，
  避免 `visual-parity.sh` 受限编译集引用设置窗类型导致编译失败。

## v0.3.31（2026-11-16）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计
`v0.3.30` 之后 3 个提交：三端设置窗统一为「暖陶纸感」视觉系统（同构卡片、自绘控件、
21 个颜色令牌的单一事实源），并连续两轮修复 Linux 设置窗与另两端的渲染差异——字号
口径（pt→px）、暖色自绘标题栏、导航列/卡片布局宽度，以及控件圆角与高度
（GTK `min-height` 内容盒口径差、控件圆角 9→12px）。

- **修复(Linux 设置窗)：控件圆角与高度对齐另两端（不再显得方）**：用户反馈
  「Windows/mac 的按钮更圆润，Linux 像个矩形」。根因两条：①GTK 的 `min-height` 是
  内容盒，choice 写 30px、按钮写 32px 时再加上 1px 描边，实际渲染成 32/34px，比另两端
  高 2px，同一圆角下比例更方；②圆角沿用 9px，而 Windows 的 9pt 在 96dpi 等于 12px。
  修正：choice/按钮 `min-height` 改 28/30px（含描边恰为 30/32px），控件圆角 9→12px
  （导航项/品牌标同步 12px），与 Windows 逐像素一致、观感贴近 mac 截图；布局与文案不变。

- **修复(Linux 设置窗)：字号口径/标题栏/布局宽度与另两端对齐**：三端视觉系统合入后
  用户反馈「Linux 看起来明显比另两端丑」。逐帧对比定位并修复 Linux 侧四个问题：
  ①字号用 pt 被 GTK 按 96dpi 放大 33%，文字比另两端大、说明文字多折行——改 px 后
  视觉字号与 mac 一致；②窗口用系统服务端标题栏（GNOME 灰白标题栏 / Wayland 强制 CSD
  回退），与 macOS 透明标题栏、Windows DWM 着色标题栏割裂——改为自绘暖色
  GtkHeaderBar（跟随窗口底色、窗口按钮中性化、非 resizable 固定 640×560）；③横向
  GtkBox 会把多余宽度平分给导航列（148 被撑到 192，卡片区被挤窄 44pt）——改用 GtkGrid
  固定导航列宽、右侧列吃满，卡片 444/内容 408 与另两端一致；④关于页在未安装 hicolor
  图标时缺图标——加 `.brand-icon` 品牌标降级。另修深色开关滑钮被主题渐变覆盖、
  卡片背景（GtkBox 才画 CSS 底）、内容起点/页脚位置对齐。文案与设置行为不变。

- **体验(三端设置窗)：暖陶纸感视觉系统——三端同构卡片、自绘控件与令牌单一事实源**：
  用户反馈「设置窗要更温暖柔和、三端一致、没有突兀感」。此前三端各写一套：macOS
  是系统灰底 + 系统蓝开关/单选 + 实心橙导航胶囊；Windows 暖底但控件四套色板、无
  hover/软阴影；Linux 只有 4 行 CSS（两条选择器失效），仍用系统灰底与系统字体。
  本次统一为「暖陶纸感」设计系统：米白纸底 + 白瓷卡片 + 赤陶强调色，深色为暖炭灰；
  **21 个色值 × 浅/深 + 几何 + 控件形态写进 `docs/ui-parity.md`「设置窗视觉令牌」
  作为单一事实源**，三端逐值实现（macOS `SettingsTheme.swift` 自绘视图/控件、
  Windows 自绘圆角卡片与暖陶控件、Linux GTK CSS 主题），由 `checkuiparity`
  「设置窗视觉令牌」组逐值断言（含禁止色：系统蓝/冷灰/旧橙一律不得出现）。
  统一形态：白瓷卡片（圆角 14、内边距 18、间距 16、标题 13 semibold/内容 50pt 起）；
  开关（38×22 轨道 + 16 滑钮，开 accent/关 controlBg）、中英状态双软胶囊
  （accentSoft + accentText）、主/次/危险按钮、accentSoft 导航选中态（不再大面积
  实心橙）、accent 焦点环（替代系统虚线/系统蓝）；关于页加应用图标 + 名称/平台/
  许可证/仓库块；页脚版本号保留。macOS 窗口改透明标题栏融入暖色底，Windows
  Win11 标题栏颜色跟随（失败静默），Linux headerbar 跟随导航色。文案与设置行为
  零改动（三端共享文案、方案 A 冷路径、单例语义不变）。

## v0.3.30（2026-11-15）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.29` 之后 1 个提交：游戏内候选窗与拼音组字窗改锚到游戏输入框——UILess 宿主（SDL2/Dota 2）经 IMM32 兼容层把聊天输入框位置写进窗口输入上下文，旧实现丢掉了这份宿主自己给出的真实坐标，导致拼音组字窗固定贴屏幕左上角、候选窗跟着被移走的鼠标跑；现读该坐标作为锚点并回写组字窗位置，拿不到时退回旧兜底链。

- **修复(Windows壳/游戏输入)：游戏内候选窗与拼音组字窗改锚到游戏输入框（Dota 2 实测）**：
  v0.3.29 真机反馈两个问题——①拼音组字窗固定贴屏幕左上角（聊天框里不显示拼音）；
  ②候选窗出现在鼠标附近而不是输入框附近。根因：UILess 宿主（SDL2/Dota 2）以 IMM32
  兼容方式承载 TSF，游戏通过 ImmSetCompositionWindow / ImmSetCandidateWindow 把
  聊天输入框位置写进窗口输入上下文（HIMC）——那是宿主自己给出的真实输入区坐标，
  旧实现只信 GetTextExt/GDI 光标/鼠标，白白丢掉了它；系统默认组字窗也因宿主未设
  组字窗坐标而固定在 (0,0)。修复：UILess 宿主锚点优先读 HIMC 的组字窗/候选窗坐标
  （客户端 → 屏幕坐标，取输入行底边），宿主只设候选窗坐标时用同值补写
  ImmSetCompositionWindow，把系统组字窗挪到输入区；宿主没给坐标时保留上一轮锚点并
  退回 GDI 光标 → 鼠标旧兜底链。新增真机排障日志（锚点来源/宿主原始坐标/
  GetTextExt 原始值各一条，每条组词重新采集）。checkuiparity「UILess 能力」组断言
  同步收紧（IMM32 锚点实现与日志取证缺席即红）。

## v0.3.29（2026-11-15）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.28` 之后 2 个提交：游戏内候选窗锚点**定位一次后保持**——首键由鼠标兜底定位（点开聊天框时鼠标就在输入框附近），整条组词稳定在该位置不跳变（v0.3.28 实测：游戏中用户会把鼠标移走，鼠标锚点随之漂移）；组词结束/退场才重置。含一次探测恢复遗漏的补交。

- **修复(Windows壳/游戏输入)：游戏内候选窗锚点定位一次后保持，不随鼠标漂移**：
  v0.3.28 真机实测：游戏里点开聊天框后用户会把鼠标移走，鼠标锚点随之漂移。
  UILess 宿主（游戏/全屏）锚点改为**定位一次后保持**：首键由鼠标兜底定位
  （点开聊天框时鼠标就在输入框附近），整条组词稳定在该位置不跳变，组词结束/
  退场才重置；非 UILess 宿主照旧每轮重新探测（零变化）。render 锚点重置改为
  条件化（if (!uilessThread_)）。checkuiparity「UILess 能力」组断言锁条件
  重置形态（改坏探测必红）。

- **修复(Windows壳/游戏输入)：游戏内候选窗不再跟随鼠标漂移——锚点定位一次后保持**：
  v0.3.28 实测：游戏里点开聊天框后用户会把鼠标移走，鼠标锚点随之漂移。UILess 宿主
  （游戏/全屏）锚点改为**定位一次后保持**：首键由鼠标兜底定位（点开聊天框时鼠标就在
  输入框附近），整条组词稳定在该位置不跳变，组词结束/退场才重置；非 UILess 宿主照旧
  每轮重新探测。render 锚点重置改为条件化（if (!uilessThread_)）。
  checkuiparity「UILess 能力」组同步断言。

## v0.3.28（2026-11-15）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.27` 之后 1 个提交：修复 v0.3.27 实测发现的打字过程中候选窗飞左上角问题——UILess 宿主一律不采信 GetTextExt（返回宿主内部坐标系垃圾值），锚点恒走 GDI 光标/鼠标兜底链。

- **修复(Windows壳/游戏输入)：打字过程中候选窗不再飞到屏幕左上角（v0.3.27 后续）**：
  v0.3.27 首键候选窗在输入行附近（鼠标锚点生效），但合成建立后 applyPreedit 的
  GetTextExt 开始返回宿主内部坐标系的非零垃圾值（绕过 32px 退化矩形防线并置
  caretFound_），候选窗随之漂移到左上角。修复：UILess 宿主一律不采信 GetTextExt
  （updateCaretFromRange 入口守卫），锚点恒走 GDI 光标/鼠标兜底链。checkuiparity
  「UILess 能力」组同步断言入口守卫。

## v0.3.27（2026-11-15）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.26` 之后 1 个提交：UILess 候选锚点通用化——删除游戏宿主名单，一律 GDI 自绘（锚点三级兜底 + 退化矩形防线，通用无特判）；官方 ITfUIElementMgr 协商保留为显式开关 `UiElementRender`（缺省 0）。

- **重构(Windows壳/游戏输入)：UILess 候选锚点通用化——删除游戏宿主名单，一律 GDI 自绘**：
  v0.3.26 用「已知游戏宿主名单（进程 + 模块特征）跳过协商/强制鼠标锚点」修候选窗飞
  左上角，名单式特判不可持续（每遇新宿主都要加一条）。通用化为一致规则：UILess 宿主
  候选**默认 GDI 自绘**（锚点三级兜底 GetTextExt → GDI 光标 → 鼠标，加通用防线——
  GetTextExt 贴主屏左上角 32px 内的退化矩形一律不采信，正常宿主几乎不可能在该区域
  返回合成矩形），不区分宿主类型；官方 ITfUIElementMgr 协商保留为显式开关
  `HKCU\Software\Chaoset\UiElementRender`（DWORD，缺省 0——实测 Dota 2 等宿主的
  协商渲染位置不跟随输入框），供未来会正确渲染候选的 UILess 宿主使用。删除
  `isKnownGameUiLessHost` 名单检测；checkuiparity「UILess 能力」组断言改为锁
  自绘优先 + 开关调用形态（改坏探测必红）。

## v0.3.26（2026-11-15）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.25` 之后 1 个提交：修复 v0.3.25 实测发现的 Dota 2 内候选窗贴屏幕左上角问题——已知游戏宿主（dota2/cs2/deadlock 及 Source 2 / SDL2 特征）跳过宿主渲染协商直接 GDI 自绘并强制鼠标锚点兜底，通用防线上 GetTextExt 贴主屏左上角的退化矩形一律不采信。

- **修复(Windows壳/游戏输入)：游戏内候选窗不再飞到屏幕左上角，跟随输入框（鼠标）**：
  v0.3.25 实测 Dota 2 能打字后候选窗贴屏幕左上角——游戏 UILess 宿主的 GetTextExt
  返回贴左上角的退化矩形（且 BeginUIElement 宿主表态接管后渲染位置也不跟随输入框）。
  已知游戏宿主（dota2/cs2/deadlock 进程 + Source 2 `engine2.dll+imemanager.dll`、
  `SDL2.dll` 模块特征）跳过宿主渲染协商直接 GDI 自绘，并强制鼠标锚点兜底；通用防线
  上 GetTextExt 贴主屏左上角 32px 内的退化矩形一律不采信（正常宿主几乎不可能在该
  区域返回合成矩形）。checkuiparity「UILess 能力」组同步断言游戏宿主检测。

## v0.3.25（2026-11-15）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.24` 之后 6 个提交：修复 Windows 壳在 UILess 宿主（Dota 2 / CS2 / Deadlock 等 Source 2 与 SDL2 游戏及全屏应用）完全无法输入中文的问题——UILess 线程照常激活并按微软《UI less mode》官方协议实现 ITfUIElementMgr 候选协商（宿主可画时交宿主渲染，不画时按官方语义回退 GDI 自绘）；同步收紧 UILess 机器断言（改坏探测必红）并外置 README 维护者长文。

- **重构(Windows壳/游戏输入)：UILess 候选按微软《UI less mode》官方协议重做（ITfUIElementMgr / ITfCandidateListUIElement）**：
  在「UILess 线程照常激活」的基础上补齐官方协商路径——新增 `ui_element` 叶子模块实现
  ITfCandidateListUIElement（mingw 缺声明自补，IID/虚表顺序经 SDL 头、Windows SDK 元数据
  绑定、官方示例 SampleIME CCandidateListUIPresenter 三源交叉验证），render 经
  BeginUIElement 提交候选：宿主 pbShow=TRUE 由宿主渲染（不画 GDI 浮窗），pbShow=FALSE
  按官方语义回退 GDI 自绘（Dota 2/CS2 等 SDL2 宿主即此路径），协商失败一律自绘兜底、
  绝不丢候选；候选关闭/组词结束/退场统一 EndUIElement，数据变化按 TF_CLUIE_* 标志
  UpdateUIElement。注册 TFCAT_TIPCAP_UIELEMENTENABLED 类别（GUID 值三源验证；声明与
  实现一致，消除 2026-10-30「只声明不实现」的事故根因）；checkuiparity「UILess 能力」
  组断言同步扩展（协商四接口 + 类别注册 + 声明一致前提）。DLL 链接新增 oleaut32
  （BSTR 分配所需）；候选元素接口按微软 SDK 惯例加保护析构、实现类 final。

- **修复(Windows壳/游戏输入)：UILess 宿主（Dota 2/CS2 等游戏）照常激活输入法，游戏里可以打中文**：
  此前 `ActivateEx` 在 `TF_TMAE_UIELEMENTENABLEDONLY` 线程直接不激活（本壳不实现
  ITfUIElementMgr，选择在 UILess 线程隐身），而 Dota 2/CS2/Deadlock 等 Source 2 与
  SDL2 宿主正是以 UILess 方式激活 TSF——输入法在游戏里收不到任何按键，表现为
  「只能输入字母」（2026-11-15 用户反馈）。现在 UILess 线程照常完成激活，候选窗
  仍由本壳 GDI 自绘浮窗呈现（不依赖宿主代绘，也继续不注册 UIELEMENTENABLED 类别）。
  Valve 游戏白名单只挡宿主代绘候选（SDL2 对 BeginUIElement 一律 pbShow=FALSE），
  不挡按键路由；同行实现（水杉 msime PR #5983、清风论坛实测）已验证该路径可行。
  `HKCU\Software\Chaoset\UILessActivate`（DWORD，缺省 1）设 0 可回退为不激活。
  已知限制：CS2 Trusted Mode 只加载 `System32\IME\` 下带有效签名的 TIP DLL，未签名
  安装包在 CS2 仍不可用（Dota 2 无此限制）；游戏内 GetTextExt 常不可用，候选窗锚点
  退回鼠标位置；D3D 独占全屏下候选窗仍按全屏免打扰抑制（无边框全屏不抑制）。
  checkuiparity「UILess 能力」组同步断言激活与回退开关——锁 ActivateEx 调用
  形态与决策理由注释（函数名级断言在调用被删后仍绿，改坏探测实证后收紧）。
  真机验收待办：Dota 2 无边框全屏聊天框输入中文（CI 只能静态/编译验证）。

## v0.3.24（2026-11-14）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.23` 之后 2 个提交：全平台同构落地日式生活艺术风 UI 质感与微实体键帽升级，以及中英文状态指示字形清晰度优化。

- **全平台同构落地日式生活艺术风 UI 资产与质感升级**：
  - **App 图标跨端全量重构**：将旧版简陋蓝白图标全面升级为温润优雅的暖陶赤陶色底与白瓷微浮雕「C」字徽章，跨平台生成 macOS `ChaosetIME.icns`（16~1024 Retina 多尺寸）、Windows `chaoset.ico`（16~256 多尺寸）、Linux 原生矢量 `chaoset.svg` 与 hicolor 22/24/32/48/64/128/256/512 各级 PNG 资产（`make-icon.swift --sync-all`），并更新 `equivalence.sh` 视觉断言；
  - **Windows 任务栏中英状态托盘图标 3D 拟物微实体化**：重构 `ui_host.cpp` 中的 `makeGlyphIcon`，摒弃生硬平铺色块，采用 3D 实体键帽建模与微阴影倒角——中文模式为晨曦焦糖琉璃键帽，英文模式为象牙白瓷微雕键帽，顶沿辅以 1px 微光高光线；
  - **三端原生设置中心统一注入生活艺术风与琉璃胶囊**：Windows 原生 Win32（`settings_window.cpp`）、macOS AppKit 原生 Swift（`SettingsWindowTargets.swift`）与 Linux 原生 GTK3 C（`chaoset-setup.c`）三端设置窗统一升级为柔和温润的象牙米白/炭灰深色质感基底，侧边导航选中态统一采用焦糖琉璃圆角胶囊与雅致微高光。
- **中英文状态指示字形清晰度与协调度优化**：
  - **Windows 托盘键帽字形优化**：字号比例由 85% 提升至 96% 黄金协调比例（高度放大 15%~18%），字形挺拔饱满、清晰易辨，同时四周保留 1.5px~2px 的自然呼吸留白，与 3D 键帽边缘及高光线和谐相融；
  - **模式切换 Toast 浮窗字形升级**：Windows（`mode_toast.cpp`）与 macOS（`CandidatePanel.swift`）模式切换悬浮胶囊字号统一由 13pt 中黑升级至 15pt 半粗体，气泡尺寸自适应微拓展，中英切换时视觉指示更加直观清晰。

## v0.3.23（2026-11-14）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.22` 之后 2 个提交：内置词库扩充至 20 万条，并新增发布期紧凑二进制词库格式（CDB1，双模透明加载）与中文互联网 8 大分类词库；macOS 壳把 Cmd/Option/Ctrl/CapsLock/Fn 的非 Shift 修饰键改为零捕获，修远程桌面（RDP/ToDesk）下按 Ctrl+↑ 切 app 后远端 Ctrl 卡键的输入法侧根因。

- **macOS 非 Shift 修饰键零捕获：修远程桌面下 Ctrl 卡键的输入法侧根因**：macOS 全局
  快捷键（如 Ctrl+↑ 进 Mission Control）与远程桌面客户端（RDP/ToDesk）叠加时，输入法
  原先会把非 Shift 修饰键的 `flagsChanged` 当普通按键继续处理：① 会访问
  `charactersIgnoringModifiers`（AppKit 只允许在 keyDown/keyUp 上访问，对 flagsChanged
  访问抛 `NSInternalInconsistencyException`——本次事件处理被中断，远程客户端可能收不到
  Ctrl keyUp，远端 Windows 表现为 Ctrl 一直被按下）；② 主流程 `passThrough` 会同步
  daemon IPC 并清掉进行中的组词，延迟修饰键事件投递。英文模式会提前 `passThrough`、
  中文模式才会走到异常路径，所以现象时有时无。修复：Cmd/Option/Ctrl/CapsLock/Fn 的
  `flagsChanged`/`keyDown` 在 `KeyRouter.handle` 入口直接 `return false`——零消费、
  零副作用、零 IPC，与 Windows 壳「修饰键永不消费」契约对齐。远程桌面下 macOS 全局
  快捷键在客户端之前吞掉 keyup 属系统/客户端按键通道问题，输入法不得注入按键；把
  Mission Control 快捷键改掉或改用 Cmd+Tab 可规避。新增 key_routing 8 条断言（50 项）
  与 checkuiparity「修饰键映射」组机器断言（含「修饰键早退必须先于
  charactersIgnoringModifiers」的顺序检查）。

- **词库规模全景扩充至 200,000 条，全面覆盖现代日常生活与工作语境**：
  - **真实现代语境与全领域覆盖**：摒弃机械照搬陈旧繁简转换库，基于真实中文互联网环境与现代高频使用场景，构建涵盖现代职场工作（敏捷开发、工作流、代码审查、社保卡、不动产权、营业执照、劳动争议等）、数码科技与AI（大语言模型、智能体、提示词、向量数据库、折叠屏、潜望长焦、空间音频等）、现代消费与餐饮（美式咖啡、生椰拿铁、燕麦奶、杨枝甘露、空气炸锅、洗地机等）、医疗常识与药物（阿莫西林、布洛芬、左氧氟沙星、核磁共振等）、电竞游戏（王者荣耀、和平精英、英雄联盟、原神、星穹铁道、无畏契约、黑神话等）、政务公文与常用成语俗语等宏大词库体系。
  - **词法规范与前缀树防抢断保护**：根据汉语词汇学严格区分词汇与临时句式，过滤代词谓语短句、动宾/助词临时拼合碎片，保证输入法动态造词前缀与回退单字链路完全顺畅。
- **支持发布期紧凑二进制词库格式（CDB1）与双模透明加载**：
  - **CDB1 格式规范**：采用 409 独立普通话音节 ID 化与连续词条打包设计，20 万条词库体积从 4.6MB TSV 压缩至 3.3MB（缩减近 30%），反序列化加载速度提升近 30%；
  - **透明无缝兼容**：`engine.LoadDictFiles` 与 `LoadDictFilesLenient` 自动探测 `CDB1` 文件头魔数，无缝双模支持 `.bin` 与 `.tsv`；`cmd/chaoset-dictgen` 新增 `-bin` 与 `-compile` 参数支持一键编译；日常维护保持 TSV 纯文本以利版本控制审查，发布分发无缝提供二进制极速格式。

- **新增中文互联网 8 大分类词库与 1,725 条流行语/网络用语/特有字扩充**：针对现代中文互联网高频用语体量不足的问题，建立 `data/categories/` 分类词库体系并合并至内置词库（规模从 65,051 条增至 66,442 条）。
  - **8 大分类覆盖**：包含网络流行语与热梗（312 条）、社交互动与评论弹幕（276 条）、职场打工人与商业互联网（212 条）、游戏电竞与二次元 ACG（270 条）、情绪心理与人设标签（128 条）、现代科技与数码 AI（220 条）、网络特有字与语气拟声（144 条）、现代生活与网红消费（163 条）。
  - **规范与守卫**：全量词条经 `engine.IsSyllable` 严格校验，`ü` 统一为 `v`（如 `nv`, `lve`, `nve`），无跨分类重复；`engine/data_contract_test.go` 新增 `TestCategoriesValidAndMerged` 保证分类词表格式合法性且 100% 合并至内置词库；`cmd/chaoset-dictgen` 增强手工增量合并与全序排版能力。

## v0.3.22（2026-11-13）

本版本为**模块重构进行中**的 patch 发版（重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.21` 之后 2 个提交：修复 Windows 切走输入法后任务栏「中/英」托盘图标不消失——此前隐藏只依赖 `ITfActiveLanguageProfileNotifySink`，但该 sink 不覆盖「切到键盘布局」（美式键盘等），托盘永久残留；补挂 `ITfInputProcessorProfileActivationSink`（微软文档明确覆盖键盘布局与输入处理器两类）与 `Deactivate` 档案复核，TSF 信号 sink 按职责拆出新模块 `tip_profiles.cpp`。另将按键热路径收敛为「状态迁移只用 TSF 档案信号」：移除每键激活档案查询，热路径零 COM 查询。

- **修复 Windows 切走输入法后任务栏「中/英」托盘图标不消失（托盘常显）**：此前隐藏
  只依赖 `ITfActiveLanguageProfileNotifySink` 失活通知与按键复核兜底，但该 sink
  **不覆盖「切到键盘布局」**（美式键盘等，`TF_PROFILETYPE_KEYBOARDLAYOUT`），且多数
  宿主切走后按键也不再下沉给本 TIP——两条隐藏路径同时失效，托盘永久残留。修复：
  ①新增挂载 `ITfInputProcessorProfileActivationSink`（与既有 sink 同一 `ITfSource`，
  微软文档明确覆盖键盘布局与输入处理器两类、`TF_IPSINK_FLAG_ACTIVE` 区分激活/失活；
  其它输入法/布局激活以「本进程是前台 + 档案复核已不是本输入法」为准，防逐应用/语音
  手写误隐藏）；②`Deactivate` 档案复核：宿主切换输入法时也会调用 Deactivate，此时
  激活档案已换人即补报 `active 0`（档案仍是本输入法＝宿主关闭/TSF 卸载，只退场，
  保持 issue#3「关掉应用不代表切走」语义）；③失活通知收口统一到
  `switchToInactiveProfile`。sink 组按职责拆出到新模块 `tip_profiles.cpp`（模块地图
  与 checker 预算同步登记），tsf.log 增加 sink 挂载与首个档案事件的一次性日志便于
  真机排障；`checkuiparity` 托盘/零阻塞契约同步更新断言。
- **按键热路径去掉每键激活档案查询（用户决策：状态迁移只用对的通知）**：`bailOutIfInactive`
  不再每键 `GetActiveProfile`，只读本地 `inputActive_` 闸门（非激活整键放行、零动作）；
  `inputActive_` 的迁移完全由 TSF 档案信号驱动——失活通知 / `ITfInputProcessorProfileActivationSink`
  （键盘布局切换只有它通知）/ `Deactivate` 档案复核。通知齐备后每键复核是通知出现前的旧兜底
  （a95f2c6），已由 0273303 起的通知驱动状态迁移取代；checker 同步改为禁止按键热路径查询档案。

## v0.3.21（2026-11-13）

本版本为**模块重构进行中**的 patch 发版（macOS/Linux 设置窗接 `chaoset-settingsctl` 仍在进行，重构完成前 minor 保持 0.3 不变）。本版累计 `v0.3.20` 之后 8 个提交：修复三个用户反馈 issue——Windows 删除候选词后中英状态/数字选词异常（issue #1）、候选窗方向键改上/下翻页与左/右预选 + 上下双翻页箭头（issue #2，三端）、托盘中英图标「系统选中本输入法即显示」与输入焦点解耦（issue #3）；macOS 输入源登记幂等（重复安装/升级不再累积重复 Chaoset 条目）；右键「删除该词」成功改为静默刷新。另含 Linux ibus E2E 稳定性修复（dbus-monitor 启动竞态）与 CI 门禁/文档口径修正。

- **修复 macOS 重复安装/升级在系统设置里累积多行「Chaoset」输入源**：根因是 TIS 没有
  反注册 API——同一 bundle 每次调用 `TISRegisterInputSource` 都会在**当前登录会话**的
  输入源表追加一条记录，只有注销重登才能重建表；而 GUI 安装器（Setup.app）每次
  「一键安装/更新」都无条件注册、`tis register` 也无幂等保护，CLI 安装器用 `tis list`
  预判在受限进程里读不到权威表会误判成未登记而重复注册。修复：`tis register` 先按
  bundle id 查 TIS 表、已登记则跳过；CLI 安装器/GUI 安装器/reregister 统一走随包
  `tis register`，删除 `tis list | grep` 预判；README 记录契约，`checkuiparity` 新增
  「macOS 输入源登记幂等」组机器断言（注册调用必须在判重之后、安装器不得无条件注册）。
- **修复 Windows 删除候选词后中英状态/数字选词异常（issue #1）**：根因是候选窗右键
  「删除该词」菜单前对 `WS_EX_NOACTIVATE` 候选窗调用 `SetForegroundWindow`——微软文档
  明确该调用仍会**程序化激活** NOACTIVATE 窗口，候选窗与宿主文档同进程同线程，宿主文档
  因此失去 TSF 键盘焦点：`OnSetFocus(FALSE)` 迁移把 `inputActive_` 闸门关闭（后续按键
  全部直通 → 打字变英文、Shift 分支不可达），合成被 TSF 外部终止时未上屏拼音留在文档
  （数字键选不中、拼音被原样确认），只有重新聚焦（点托盘/切窗口）才恢复。修复：
  菜单 owner 改 TIP 返回的**本进程宿主窗口**（前台窗口优先，TSF 焦点上下文宿主窗口/
  合成缓存窗口兜底，pid 必须本进程；取不到 owner 时不弹菜单），删除对候选窗的
  `SetForegroundWindow`；菜单返回后重校验候选窗/TIP 状态与序号再删除；前台钩子忽略
  本进程自身候选窗/提示条窗口；`OnCompositionTerminated` 在「文档合成文本 == 最后写入的
  `confirmedPreedit_ + comp_.preedit`」时把残留拼音改写成已确认段（保留已确认段、清掉未
  上屏拼音，任何失败只记日志）；删除刷新在合成已外部终止时只清本地/引擎会话，刷新后
  校验预编辑一致性；焦点事件（`OnSetFocus(TRUE)` 等）参与激活恢复，修 `IsThreadFocus`
  更新滞后导致切回应用仍英文直通。macOS 静态排查无同类焦点破坏（NSMenu + nonactivating
  panel），Linux 无面板右键菜单（替代入口 `chaoset-userdict delete`）。
- **候选窗方向键：上/下翻页、左/右页内预选 + 空格确认 + 上下双翻页箭头（issue #2 + 用户反馈，三端）**：
  组词/联想有候选时 **`↑`/`↓` = 上一页/下一页**（翻页后预选归零；边界页保持不动且仍消费）、
  **`←`/`→` = 页内前一项/后一项**（页边界保持不动且仍消费）；空格确认**页内预选项**
  （数字键仍按序号直选，不移动预选）；无组词/无候选时方向键放行宿主；新输入/翻页/选词后
  预选归零。候选窗蓝色高亮胶囊从「固定第一项」改为跟随页内预选项。翻页箭头改为上下两枚：
  首页只有下箭头、末页只有上箭头、中间页两枚都有、单页两枚置灰不可点，两枚命中区上下分开
  （仍遵守 mouseup 触发契约），macOS/Windows 同几何（6pt 箭头块 + 页码垂直居中）。Linux ibus
  用 lookup cursor 实现左/右预选与上/下翻页，空格确认 cursor 候选（箭头显隐由 ibus 面板自绘、
  壳不可控，README 记录）。
- **候选窗右键「删除该词」成功不再弹提示（用户反馈）**：删除成功只静默刷新候选窗，不再弹
  「已删除该词」；失败仍提示「未找到该词的学习记录」「删除失败：daemon 不可达」。三端文案
  契约与 checker 同步（成功提示改为反向断言，防止回退）。
- **Windows 托盘中英图标改为「系统当前选中本输入法即显示」（issue #3）**：托盘可见性与
  输入焦点解耦——`OnSetFocus(FALSE)`/`OnKillThreadFocus`/前台离开本进程/`Deactivate` 只
  退场（关按键/候选窗闸门、销毁浮窗、撤钩子）不再隐藏托盘；显示由 `Activate`、
  `ITfActiveLanguageProfileNotifySink` 激活通知、焦点获得与前台回到本进程驱动（条件 =
  本线程激活档案是本输入法 + 本进程是前台进程，上报 `active 1`）；只有切到其它输入法
  （失活通知，或按键复核发现激活档案变化）才上报 `active 0`，UI 宿主 300ms 防抖后隐藏；
  owner 进程退出只清 owner/句柄、不隐藏托盘；无周期定时器/心跳不变。切到没有输入焦点的
  桌面/任务栏/无文本框窗口时托盘保持显示。
- 验证：`gofmt`、`go build ./...`、`go test ./tools/checkuiparity/`、`make vet`（50 组
  check-uiparity 含新增「候选窗翻页箭头」「候选窗方向键预选」组、托盘新契约，以及
  独立 review 后补的菜单 owner 优先级/合成终止清理/箭头可用性/方向键隐藏时放行等
  函数体级断言）、
  `./shims/windows/build.sh`（core_test + mingw 交叉编译）、`./shims/macos/build.sh` +
  `visual-parity.sh`（新增 E 段双箭头/预选契约）+ `key_routing`（42 项：上/下翻页、左/右
  预选、空格确认、联想断言）、Linux ibus 引擎容器内编译。**Windows 像素与 TSF 运行期行为仍
  需用户真机验收**（菜单 owner 不抢焦点、删除后状态、托盘常显/切走隐藏、方向键两阶段
  投递、DPI 下箭头/pill 几何、UWP/开始菜单边界）。

## v0.3.20（2026-11-12）

本版本为**模块重构进行中**的 patch 发版：按 `docs/architecture.md` 拆分三端壳大文件的重构尚未全部完成（macOS/Linux 设置窗接 `chaoset-settingsctl` 仍在进行），因此**重构完成前只递增 patch 版本号、minor 保持 0.3 不变**，不发布 `v0.4.x`。本版累计 `v0.3.19` 之后 4 个提交：Windows 壳拆分 wave1 续与 wave2（`ChaosetTIP` 类拆分完成）、中英状态作用域简化为两档并改为 TSF 事件驱动的托盘同步，以及配套的 checker/文档同步。

- **中英状态作用域简化为两档（system/process）+ 托盘中英状态按作用域同步（用户反馈「托盘状态不反映当前程序」）**：
  - 设置项 `mode_scope` 只保留两档：`system`（全局共享，默认）/ `process`（每进程独立）。
    存量迁移（三端一致，大小写/首尾空白容忍）：system→system、process→process；
    旧三档 window→process（「每窗口」并入「每进程」）；未知/空→system。新写入只写
    system/process（CLI `set mode-scope` 接受旧值 window 并归一化）。Go 单一真源
    `internal/settings` 与 `chaoset-settingsctl`、macOS `CandidateSettings.swift`
    （UserDefaults 旧值读取归一化）、Linux `setup_ctl.c`（`valid_mode_scope` 两档 +
    旧值读取归一化）与三端设置窗文案/单测同步；JSON 契约字段名与顺序不变，缺省值仍为 system。
  - **设置窗中英状态从三选一变二选一（用户可见）**：Windows `settings_window.cpp` 的
    单选按钮由「系统共享 / 每程序 / 每窗口」改为「全局共享 / 每进程独立」（控件 ID
    121/122 映射 system/process），macOS `SettingsWindowPages.swift` 与 Linux
    `setup_page_input.c` 同步为同一组逐字文案；原来的「每窗口」选项消失，存量值按上面
    的迁移规则并入「每进程独立」。checkuiparity 锁住三端这两条文案逐字一致。
  - **撤销上一版「每窗口独立」的实现**：`windowModes`/`focusWindowKey`/
    `ITfThreadMgrEventSink`（GetFocus→GetActiveView→GetWnd 取前台窗口）已在全仓
    无引用；`TipThreadSink` 的 `onActiveWatchTimer()`/`tipInputActive()` 两个
    窗口级方法与 `kActiveWatchTimerId`（`WM_TIMER` 500ms 复核激活档案）一并删除。
    中英状态改为进程粒度（同进程多 TIP 实例共享），托盘可见性改由 TSF 信号事件驱动
    （见下条），不再靠周期定时器兜底。
  - 托盘状态 = 当前输入上下文的实际状态，按作用域决定同步方向：`system` 下托盘是权威
    （TIP 获得焦点时从托盘同步，托盘左键切换全局中英，状态在所有程序间共享）；`process`
    下托盘是镜像（每个程序各自记住中英状态，同一程序的多个 TIP 实例共享；TIP 激活/切进程
    时把本进程状态上报到托盘，托盘左键只显示、不切换）。中英状态由输入法自己维护，TSF 只
    提供线程隔间与焦点/激活原语；不实现 `GUID_COMPARTMENT_KEYBOARD_INPUTMODE_CONVERSION`
    写入（mingw 头缺该 GUID），保留 `GUID_COMPARTMENT_KEYBOARD_OPENCLOSE`。
  - 托盘可见性改为**TSF 信号事件驱动（无周期定时器/心跳）**：激活/失活完全由
    `Activate`/`Deactivate`、`ITfThreadFocusSink`、`ITfActiveLanguageProfileNotifySink`
    驱动，TIP 在状态变化时发 `active 1 <0|1> <pid>`（显示托盘、把该进程设为 owner、更新
    图标）或 `active 0 <pid>`（仅当 pid 是当前 owner 时隐藏）；`mode <0|1> <pid>` 只更新
    图标。UI 宿主不轮询：用 `OpenProcess`+`RegisterWaitForSingleObject` 监听 owner 进程
    退出并清理，隐藏保留 300ms 防抖，没有输入焦点（桌面/任务栏）或切到其它输入法即隐
    藏。**UI 宿主被杀/未启动时能自动回来（用户可感知）**：worker 发命令失败且允许拉起
    时懒启动 `chaoset-ui.exe`（带当前 `--mode`）并短重试本次事件（非 AppContainer
    宿主 40×25ms≈1s；AppContainer 宿主不能 `CreateProcessW`，改由 daemon
    `ensureUiHostByDaemonWorker` 代拉起）；下一次 TSF 事件也会重发，因此不需要定时器/
    心跳。System 档的按键热路径另有两个时序窗：权威值 TTL 复读 300ms（超时才补一次
    HKCU `UiEnglish` 镜像读，不跨进程），本地 Shift 切换后 500ms 防回抖（不被尚未
    异步写回的旧权威值翻回去）。
  - 输入法切换链路：用户切换输入法（切走/切回）由系统/TSF 触发，TIP 在 `Activate`/`Deactivate`
    与 `ITfActiveLanguageProfileNotifySink` 中响应，据此更新激活态与托盘可见性。去掉 worker 中
    `workerWaitForegroundSelf(150)` 的前台 pid 门限——UWP/ApplicationFrameHost 宿主的前台
    窗口 pid ≠ TIP 进程 pid，上报曾被静默丢弃，这是「托盘有时不显示」的根因；上报可信度改由
    TIP 的激活档案 + `isActiveTip()` 决定。
  - 验证：`go test ./internal/settings ./cmd/chaoset-settingsctl`、`go build ./...`、
    `gofmt`、macOS `swiftc -typecheck` 通过；`go run ./tools/checkuiparity .`（47 组）、
    `go test ./tools/checkuiparity/ ./tools/checkgdi/`、Windows 双架构 mingw
    `-fsyntax-only` 与 `shims/windows/build.sh` 由 A/B 合并后的提交门禁统一复核。Linux
    GTK3 构建与 `test_setup_ctl` 由 CI linux-e2e 复核。Windows 真机（托盘事件驱动
    可见性/owner 退出清理、system/process 同步方向、UWP/ApplicationFrameHost 宿主、
    输入法切换链路、每进程独立状态）待用户验收。

- **Windows 壳拆分 wave2（审计 P1-1 收尾，TIP 类拆分完成，≤2000 目标达成）**：
  win32.cpp 2994→129 行，把 `class ChaosetTIP`（类体 2564 行，第 232–2795 行）按职责拆成
  `tip.hpp`（完整类声明 + kVk*/kPageSize 常量 + mingw 缺失的 ITfTextInputProcessorEx
  补声明）、`tip_core.cpp`（生命周期/激活状态机/中英模式/前台钩子/worker
  回投入口）、`tip_keys.cpp`（ITfKeyEventSink 五入口/Shift 状态机/键位映射/
  wouldHandle/handleKey）、`tip_composition.cpp`（EditSession/光标/预编辑/
  提交/结束合成/双拼会话收尾）、`tip_candidates.cpp`（候选点选/删除/翻页/
  showToast/设置窗入口/联想面板）。拆分当次行数：tip.hpp 396、tip_core.cpp 830、
  tip_keys.cpp 913、tip_composition.cpp 587、tip_candidates.cpp 278；本版末
  （拆分后本批又补了两档作用域/事件驱动托盘）为 tip.hpp 416、tip_core.cpp 955，
  tip_keys/composition/candidates 不变。`tip_thread.{hpp,cpp}`（hpp 36→68 行、
  cpp 68→189 行，本版末 61/181 行）迁入
  TIP 线程 message-only 窗口过程/创建销毁/按线程 TIP 注册表/前台钩子表；窗口过程对 TIP
  的分发改为 `TipThreadSink` 抽象接口（tip_thread.cpp 不 include tip.hpp，依赖方向仍
  单向）。win32.cpp 只留类厂/ChaosetRunUi/DllMain/DllGetClassObject/DllCanUnloadNow 与
  g_liveObjects；DllMain/DllCanUnloadNow 函数体逐字保留，后者计数来源只改为跨 TU extern
  （`g_liveTipMsgWindows`/shellWorkerAlive()/shellWorkerStop() 字面仍在体内）。
  方法体逐字迁移（脚本生成 + 连续行审计 0 失配；唯一接口化调整是 refreshActiveWatchTimer
  改经 `tipInputActive()` 查询、线程注册表指针类型 `ChaosetTIP*→TipThreadSink*`）。
  checker 同步（同一 commit）：预算 win32.cpp 3200→1200、新增 5 个 TIP 文件预算、
  tip_thread.cpp 150→350；checks_tsf 的 23 个不同函数体/入口签名 needle（34 处断言匹配点）
  全部更新为类外定义的完整签名（如 `HRESULT STDMETHODCALLTYPE ChaosetTIP::Activate(ITfThreadMgr* ptim,
  TfClientId tid)`、`void CALLBACK ChaosetTIP::fgHookProc(...)`、
  `bool ChaosetTIP::bailOutIfInactive()`），未删除/放宽任何断言；main_test.go 变异锚点
  （`g_lastOp = kOpActivate;` / `PostMessageW(g_tlTipMsgWnd, kTipForegroundMsg,` /
  `g_liveTipMsgWindows` / DllMain 的 removeCrashHandler+shellWorkerStop 组合 /
  `gearPressed_ = true;`）未改且仍能抓回退。docs/architecture.md §3.1、AGENTS.md
  架构地图、build.sh 注释、shims/windows/README 同步；顺带清理叶子模块注释里
  「ShellWorker/worker 编排在 win32.cpp」的陈旧表述（改指 shell_worker.cpp），并修正
  wave1 条目 needle 计数措辞（4 条字面量/3 种签名）。验证：双架构（x86_64/i686）
  `-fsyntax-only -Wall -Wextra` 零警告、`go run ./tools/checkuiparity .`（47 组）、
  `go test ./tools/checkuiparity/ ./tools/checkgdi/`、`sh shims/windows/build.sh`
  （core_test + DLL 导出表 + UIA 动态加载 + UI 宿主）、`make vet` 通过。

- **Windows 壳拆分 wave1 续（审计 P1-1 第二步）**：win32.cpp 4701→2994 行，把候选窗、
  后台 worker、COM 注册与 CLSID/GUID 常量拆成独立 TU：
  `candidate_window.{hpp,cpp}`（候选窗 GDI 渲染/DPI/鼠标与右键菜单/UIA 宿主 provider/
  齿轮圆头笔/防卸载计数 + 交互 handler，类体 872 行逐字迁出）、
  `shell_worker.{hpp,cpp}`（每进程后台 worker 任务队列、post* 投递、daemon 懒启动/
  连接、设置窗拉起、tsf.log 异步落盘）、`tip_registration.{hpp,cpp}`（COM/TSF 注册与
  反注册冷路径）、`tip_ids.hpp`（CLSID/GUID/TSF 接口与类别常量，C++17 inline const
  GUID）、`tip_thread.{hpp,cpp}`（kTip* 消息常量与 worker→TIP 线程 message-only 窗口
  注册表；窗口过程仍留 win32.cpp 待 wave2 随 tip.hpp 迁移）。
  候选窗对 TIP 的唯一接触收敛为 `CandidateWindowHost` 抽象接口（7 个方法，
  showSettingsWindow/selectCandidateByIndex/deleteCandidateByIndex/flipPage/
  flipAssociateFromPanel/isAssociatePanel/clearAssociateFromSettings），`ChaosetTIP`
  继承实现（同 DLL 内部类型，不影响 COM QI 返回的接口指针）；DllCanUnloadNow 的候选窗
  计数改为跨 TU extern。checker 预算 win32.cpp 4800→3200 并登记 9 个新文件（+10 条预算：
  9 个新文件 + win32.cpp 改值）；4 条函数体签名 needle
  （3 种签名，CandidateWindow::show 出现两处）同步到新定义处（show 的
  CandidateWindowHost 参数、去 static 的 ensureDaemonFast/reportModeToUi），
  未删除/放宽任何断言，变异测试仍能抓住回退。
  docs/architecture.md 模块地图、docs/ui-parity.md、README、shims/windows/README、
  build.sh 注释与 AGENTS.md 同步。验证：双架构（x86_64/i686）mingw
  `-fsyntax-only -Wall -Wextra` 零警告、`go run ./tools/checkuiparity .`、
  `go test ./tools/checkuiparity/ ./tools/checkgdi/`、`sh shims/windows/build.sh`
  完整构建（DLL 导出表/UIA 动态加载断言/UI 宿主/原生 core_test）与 `make vet` 通过。
  注：TIP 类本身（~2.5k 行）仍未拆分，是 ≤2000 目标的剩余前置。

## v0.3.19（2026-11-11）

本版本为**模块重构进行中**的 patch 发版：按 `docs/architecture.md` 拆分三端壳大文件的重构尚未完成（Windows `win32.cpp` 拆 TIP 类、macOS/Linux 设置窗接 `chaoset-settingsctl` 等仍在进行），因此**重构完成前只递增 patch 版本号、minor 保持 0.3 不变**，不发布 `v0.4.x`。本版累计 `v0.3.18` 之后 14 个提交的代码质量重构与缺陷修复，具体条目如下。

- **架构/代码质量：拆分 engine 组词核心与 checkuiparity 检查器（代码质量专项）**：
  - `engine/session.go` 2188→1193 行：`compose()` 1009 行按职责拆为
    `compose_state/shape/continuation/typo/query/rank.go` + 142 行编排 `compose.go`；
    调用顺序、rank 常量值、`SliceStable`、typoFix 标记、分页与 compose memo 副作用逐条保留。
  - 新增 `engine/compose_golden_test.go` + `engine/testdata/`（323 条语料 × 5 模式 = 1620 条记录，
    跨全部页的完整 Candidates/Preedit/PageCount 断言，gzip 约 320KB）：纯重构的逐字节等价门禁；
    `-update` 刷新、`-detect-unstable` 复核非确定性输入。
  - 性能无回归（synthetic 100k 词库，-count=3）：Compose100k 40.8–42.7µs→40.9–41.3µs，
    ComposeInitials ~292–299µs→~289–298µs；+10 allocs/op、B/op +3.5%。
  - `tools/checkuiparity` main.go 2553 行→10 个文件：组名唯一表（`groups.go`）+ 注册表
    （`run.go`）驱动执行，成功输出的组清单由注册表生成（旧硬编码 32 项漏了 11 个实际运行的组）；
    新增「候选每页数量」四端一致断言；Linux 原生引擎 `shims/linux/engine/*` 纳入模块预算（此前盲区）。

- **修复（engine）**：Compose 缓存失效不完整——`SetFuzzy`/`SetTraditional` 切换后连续
  `Compose()` 可能返回旧结果；新增 `invalidateComposeMemo` 并接入 4 个配置入口（另有单测）。

- **修复（engine 组词确定性）**：词典前缀 BFS 曾依赖 map 遍历序 + 非全序比较器，极短简拼/混拼
  输入（zh/aq/bh…）的候选总数会随运行抖动。修复：Trie 节点记录子树最小词条下标
  （`dictNode.subtreeMin`）作为前缀 BFS 的全序决胜键，各查询 sort 补齐 Word/音节决胜键
  （多音字「同词同频」不再依赖输入顺序）；用户词库查询把「按词去重」从收集阶段移到
  全序排序之后（多音字同词保留哪个读音不再由 map 遍历顺序决定）；该循环改用
  `slices.SortFunc` + 16 字节紧凑结构，
  顺带把热路径跑快：Compose100k 40.7–42.4µs→32.2–32.6µs（约 -20%）、ComposeInitials
  282–293µs→266–275µs（约 -5~7%），alloc 97→92 / 621→608。
  验证：2660 条语料 × 两种模式（词典/预热用户词库）各 8 轮重复组词 0 抖动；修复前后快照仅
  7/9696 条记录有差异且都发生在首页之后（首选不变）；新增 `TestComposeDeterministic` /
  `TestUserDictQueryDeterministic` 回归，原非确定性输入已纳入 golden 语料。

- **修复（IPC 健壮性）**：`selectPrefix` 响应直接回传 Composition（删除手工拼 map 的协议漂移点）；
  帧上限增加写侧校验，服务端对超限响应返回错误帧而不是写半帧（避免对端读帧失败后连接失步）；
  Client 校验响应 ID，错位即关闭连接（与 Ping 超时同语义）。

- **修复（Windows 壳生命周期）**：`~ChaosetTIP` 补幂等 `unregisterTipFromThread`——宿主未调
  Deactivate 直接 Release 时 `g_tlFgTips` 会留悬垂指针（500ms 定时器/前台消息解引用 → 宿主崩溃），
  message-only 窗口计数不归零导致 `DllCanUnloadNow` 恒 `S_FALSE`；checkuiparity「DLL 卸载安全」
  组新增析构断言。同时删除死消息 `kTipDaemonReadyMsg`、6 个仅注释引用的系统头、重复 `<map>`，
  修掉 mingw `-Wall` 的 cast-function-type 警告（win32.cpp / ui_main.cpp）与 macOS 设置窗未使用变量。

- **质量/工具链**：staticcheck 报出的 5 条静态缺陷修复（`qwertyNeighbors` 自减键、s2t 单测未检查
  error、appdirs 错误串首字母大写等）；`make vet` 新增 `check-fmt`（与 CI gofmt 同口径），
  `make fmt` 只格式化 git 跟踪的 `.go`（不再扫 `.workwork/` 临时文件）；`lint-workflows` 对
  ci.yml 断言全部 6 个 job（此前只断言 test，lint job 被删后 make vet 仍绿）。

- **修复（Linux 设置窗）**：`setup_config_save_mode_scope` 此前读取 associate 后**重建最小 JSON**，
  切换一次「中英状态作用域」就会静默清掉 Go 侧（chaoset-settingsctl）写入的
  fuzzy/traditional/shuangpin（审计 GO-03）。改为读-改-写：通用 `replace_json_value` 只替换
  目标键并保留其它键；作用域写入校验 system|process|window，读取时未知/损坏值回退 system
  （与 Go store 的宽容归一化一致）。`test_setup_ctl.c` 新增「保留未知键/缺省创建/损坏回退/
  非法值拒绝」用例；本机 Docker 内 GTK3 编译 + C 单测通过，CI linux-e2e 同命令复查。

- **测试（三端设置存储）**：三端设置单一真源此前只有 Linux store 有测试，Windows 注册表与
  macOS defaults 的解析/写值路径从未被执行（CI 的 macos/windows job 也不跑这些 Go 测试）。
  新增 `store_darwin_test.go`（`defaultsRunner` 注入点，覆盖解析/缺省回退/损坏值报错/写入参数，
  不碰真实用户偏好）与 `store_windows_test.go`（真实 HKCU 往返、未知 ModeScope 回退 system、
  值类型被改坏时回退缺省，测试后恢复原值）；CI 增加 windows-latest「Windows 设置存储单测」job，
  并在 macOS job 内跑 `go test ./internal/settings`（7 个并行 job）。

- **重构（IPC/cmd 去重，审计 GO-07/GO-11）**：`ipc.Server.dispatch` 里 12 处重复的匿名参数
  结构 + `json.Unmarshal` 错误包装收敛为具名参数结构体 + 泛型
  `decodeParams`/`decodeOptionalParams`（错误文案仍由 method 统一生成，协议与错误文本不变）；
  `repeatFlag` 从 chaosetd / chaoset-cli 两份逐字复制收敛到 `internal/cliutil`（含单测）。

- **修复（IPC 关闭生命周期/超时，审计 GO-09）**：`Server.Close` 由「只关 listener 就 Save」
  改为「停止接受 → 关闭活跃连接 → 等待 handler 退出 → Save」，消除 daemon 退出时最后一批
  学习数据落在 Save 之后的丢失窗口；连接登记与 `WaitGroup.Add` 在同一把锁内完成，避免
  Accept/Close 竞争漏等；Close 幂等。`Client` 普通调用增加 5s 默认截止时间（daemon 卡死时
  CLI 不再无限阻塞；Ping 仍保持自己的 250ms 语义）。新增 `ipc/shutdown_test.go` 覆盖超时、
  活跃连接关闭与 Close 幂等，`go test -race ./ipc ./cmd/chaosetd` 通过。

- **Windows 壳拆分 wave1（审计 P1-1 第一步）**：模式切换提示 `ModeToast`（239 行）从
  win32.cpp 拆到 `mode_toast.{hpp,cpp}` 独立编译；浮窗 owner 判定
  （`popupOwner`/`shellHostNeedsOwner`）上移到叶子模块 winutil（候选窗/提示条共用，
  避免叶子 TU 反向依赖 TIP TU）。win32.cpp 4974→4701 行，checker 预算从 5300 收紧到
  4800 锁定进度；新文件登记 checker 与 architecture.md 模块地图。双架构 mingw 语法
  编译零警告、`build.sh` 完整构建（DLL/导出表/资源/UI 宿主/core_test）与 `make vet` 通过。
  注：TIP 类本身（~2557 行）仍未拆分，是 ≤2000 目标的剩余前置。

- **发布一致性（审计 P1-4）**：release.yml 的 macOS 打包改为复用 Makefile `package-macos`——
  与日常 CI 的 macos-build 走同一条链（主壳/安装器/App 内置工具全量 universal 自检、app 资源
  清单、zip 生成），删除此前手写的更弱复制品（只 lipo 两个主二进制）；`Info.plist` 版本号与
  `scripts/version.sh` 的一致性断言下沉到 Makefile，日常 CI 与发版共用；release 仍保留
  equivalence / visual-parity 回归。

- **发布/CI 安全**：release.yml 的 `release` job 增加「仅 push tag」守卫——此前
  workflow_dispatch 即使选 tag ref 也会用「日期-hash」版本创建 Release（审计 P1-1）；移除
  release 测试 job 中 renderer 删除后已无用的 CJK 字体安装；`ci.yml` 删除与 `make vet`
  重复的 workflow 结构校验步骤，release.yml 的 checkworkflows 期望 job 列表补齐到 7 个。

- **文档**：architecture.md 修正方案 A 阶段状态（A1/A3 已完成、A4 明确不做）、补 Linux 引擎模块
  与预算、预算口径统一为逐文件、Swift 本地可 typecheck；AGENTS.md 修正 macOS 设置窗路径与
  「依赖方向已机器断言」的表述；README 开发命令与「验证交给 CI」对齐。

- **已知问题（本轮未修，已留证）**：win32.cpp 约 4980 行，拆到 ≤2000 需先拆 TIP 类
  （audit 已给出 13 文件方案与 checker 断言改法），是剩余唯一的架构级大项；其余为
  checkuiparity 部分检查组缺变异测试、macOS/Linux 设置窗接 chaoset-settingsctl（方案 A2/A3 收尾）。

## v0.3.18（2026-11-07）

- **根除 RDP 修饰键卡键 + 输入热路径零捕获零干扰（Windows 壳，2026-11-07 用户反馈）**：
  - 彻底根治远程桌面（如 macOS RDP 连接 Windows）下使用 `Ctrl+向上箭头` 切换应用切回后 Windows 出现 Ctrl 卡键状态的恶性 bug。
  - 严格贯彻输入法「被动响应、零捕获、零副作用」底线：所有修饰键（Ctrl/Alt/Win/Shift）在 `OnTestKeyDown`/`OnKeyDown` 中一律透明放行（`pfEaten=FALSE`），绝不调用 `handleKey`，绝不进入管道探测链路；彻底移除对 Ctrl/Alt/Win 释放事件的主动嗅探与写盘日志，只有 Shift 释放参与中英切换判定。
  - 非组词键（`!wouldHandle`）或英文模式下，若存在未决预编辑（`isComposing` / `confirmedPreedit`），安全收起浮窗（`flushOrCancel`）后原样放行，绝不阻断宿主快捷键与普通按键。
  - 前台焦点事件钩子（`SetWinEventHook`）限制仅在 Win11 开始菜单/搜索宿主中按需注册，普通应用程序（RDP、浏览器、编辑器等）钩子注册数物理降至 0，彻底杜绝系统级事件风暴对远程桌面与宿主按键时序的干扰。
  - 修复 macOS 下单元测试因目录路径较长触发 Unix domain socket 104 字节上限导致 bind 失败的问题。

- **修宿主进程崩溃/卡死：DLL 动态卸载后 VEH 回调查进已卸载内存（2026-11-07 日志排查）**：
  - Windows 应用日志证据（2026-11-05 18:05，安装升级后）：ApplicationFrameHost.exe
    崩溃，错误模块 **ChaosetIME.dll_unloaded**、0xc0000005、偏移 0xb840。
  - 根因：崩溃取证用的 `AddVectoredExceptionHandler` 在 DLL 动态卸载（TSF
    FreeLibrary）时从未撤销，宿主进程下一次任意异常（shell 宿主里 COM/RPC
    first-chance 异常很常见）就跳进已卸载代码，导致宿主进程崩溃/无响应。
  - 修复：保存 VEH 句柄，`DllMain(DLL_PROCESS_DETACH)` 先 `removeCrashHandler()`
    再停 worker/注销窗口类；checkuiparity 新增「DLL 卸载安全」断言。
  - **进一步（用户硬要求：输入法绝不能给系统/app 增加负担）**：TSF 宿主进程
    **不再安装 VEH**，只由 UI 宿主自己的进程注册写 crash.log；宿主进程零全局
    异常回调（从根上消除卸载悬空与每个 first-chance 异常的回调开销），宿主崩溃
    由系统 WER 记录。
  - 另有系统级 GPU TDR 证据（nvlddmkm 4101 + LiveKernelEvent 141，2026-11-07
    00:38，ToDesk 随后重启机器）——整机/远程会话卡死与该显示驱动超时相关，不归
    输入法；explorer 18:53 的 shell 重启也在日志中，与 IME 无模块级证据。
- **非激活零副作用 + 托盘可见性（active）（2026-11-07 用户真机反馈 bug1/bug2）**：
  - 托盘中英图标改为 `active` 生命周期：只有 TIP 上报 `active 1`（前台激活本输入法）
    才 NIM_ADD 显示；`active 0`（失焦/停用/切换输入法/前台离开）防抖 300ms 后
    NIM_DELETE 隐藏；`mode` 只更新图标，启动 `--mode` 不再点亮托盘。切换输入法但
    TSF 不调用 Deactivate 的宿主，由按键闸门每键复核 + 500ms 激活档案定时器兜底。
  - 未激活零动作：所有 TSF 按键/焦点回调以 `inputActive_` 为唯一闸门；未激活时
    立即 `*pfEaten = FALSE` 放行，不清理/不查询/不写盘/不挂钩子/不读鼠标。修饰键
    KeyUp 恒放行（绝不消费），全壳禁止键鼠钩子与输入注入。
  - 中英状态对齐：system scope 托盘为权威（写 HKCU，app 跟托盘）；process/window
    scope 托盘跟随当前前台 app（TIP 获得焦点时重新上报，不写 HKCU）；非 system
    scope 托盘左键不再反向改全局。修「桌面 Shift 切模式后切 app，托盘与 app 不一致」。
  - daemon 预热改为每实例只发一次且 AppContainer 内不 spawn：修 tsf.log/daemon.log
    里反复 "daemon lazy start / 已有 chaosetd 在运行"（后台 TIP 实例每次焦点都尝试）。
  - 重复 spawn 根因修复：`daemonPipeExists` 把 `ERROR_PIPE_BUSY` 当「不存在」
    （go-winio 同一时刻只有一个监听实例，真机约 80% 探测返回 BUSY）→ 改三态
    （只有真正无管道才算不存在）；spawn 前先查 `Local\ChaosetDaemon` 互斥体；
    `chaosetd` 单实例锁前移到词库加载之前（重复实例不再白载 65k 词典）；
    后台预激活 TIP 实例不再创建 worker 线程/消息窗口（延迟到真正激活）。
- **方案 A2（Windows 试点）：设置窗接 chaoset-settingsctl（2026-11-07）**：
  - Windows 设置窗的联想开关/中英状态读写改调 `chaoset-settingsctl get/set`
    （1s 硬超时、CREATE_NO_WINDOW、失败回退 native HKCU 读写并记限流 diag）；
    CLI 随 Windows 安装包/zip 分发（NSIS 安装+卸载清单同步）。
  - checkuiparity 新增「非激活零副作用」「托盘可见性（active）」两组断言，并锁住
    A2 的 CLI 调用/超时/打包清单与「非 system scope 托盘只能指示、不写 HKCU」。
- **架构整理 + 方案 A 设置核心（2026-11-06 用户要求：shims 大文件按职责拆分、不要埋屎山）**：
  - 新增 `docs/architecture.md`：分层/依赖方向/模块地图/文件预算；`checkuiparity` 新增
    「模块布局与文件预算」「架构文档」「设置核心（方案 A）」「Linux 设置窗装配」四组机器断言，
    模块文件缺失/超预算、注册表子键被改、GtkStack/radio 装配回退都会在 `make vet` 直接 FAIL。
  - 方案 A 第一期：新增 `internal/settings`（三端设置 schema/默认值/校验/持久化单一真源；
    Windows HKCU、macOS UserDefaults 域 `ime.chaoset.inputmethod.ChaosetIME`、Linux
    `$XDG_CONFIG_HOME/chaoset/config.json`）与 `cmd/chaoset-settingsctl`
    （`get` 输出单行 JSON；`set associate|mode-scope|ui-english`；退出码 0/1/2）；
    Windows 真机验证 get/set 与 HKCU\Software\Chaoset 逐字一致。三端设置窗改调 CLI 的 A2 下一批。
  - Windows `win32.cpp` 7801→4815 行，拆出 9 个真实编译单元（diagnostics/winutil/theme/
    config/transport/daemon/ui_client/settings_window/ui_host），build.sh/Makefile 源列表改
    wildcard；macOS 4 个千行 Swift 文件拆成 13 个职责文件（最大 636 行）；Linux 设置窗拆成
    窗口骨架 + 3 个页面 + 3 个逻辑模块（最大 516 行）。
  - 修复 Linux 设置窗两个回归（484aae4 引入）：三个内容页从未挂到 GtkStack（设置窗内容区为空）、
    中英状态 radio 组类型混用（三选一失效）。WSLg 实跑 stderr 0 critical/warning，三页可切换、
    三个 radio 同组且恰一个选中；「Linux 设置窗装配」断言 + 变异测试锁死。
- **零阻塞改造（Windows 壳，2026-11-06 用户要求）**：
  - TSF 宿主 UI 线程上的跨进程等待/建进程/同步写盘全部移除：新增每进程 ShellWorker 后台线程，
    回调只做内存操作与 PostMessage；daemon/UI 宿主懒启动、模式查询、设置窗拉起、日志写盘都在 worker。
  - 修复零阻塞审计 P0：uiQueryMode 在同步句柄上的伪 200ms 上限（真机实测无上限）→ FILE_FLAG_OVERLAPPED +
    移出 TSF 线程 + HKCU UiEnglish 微秒读镜像；pipeIo 的 WaitForSingleObject(INFINITE) → 有界截止
    （TSF 30ms / worker 200ms）+ 5s 熔断；齿轮点击 20 次重试 + 同线程 CreateProcessW → 入队 worker；
    按键路径同步懒启动 daemon → ensureDaemonFast（单次非阻塞探测，失败 fail-open 原样放行）。
  - spawn 前非阻塞探测管道，消除每次宿主首次激活的空转拉起 chaosetd（实测 115/125 次空转，子进程先加载 65k 词库）。
  - 按键路径零写盘（删除每键 candidate shown 日志）并改 AsyncLog 有界队列；前台钩子每线程一个、回调只
    PostMessage、前台离开自动撤销；配置查询 TTL 缓存；worker/消息窗口计入 DllCanUnloadNow。
  - 归因口径：Notepad 事件中 17:38:20–17:43:30 输入法并未加载进 notepad.exe（不能直接阻塞它），
    但 shell 进程里存在无上限阻塞路径；本次改造后 TSF 回调不再有跨进程等待（阶段1），daemon 半死时
    按键最多约 90ms 有界后 fail-open，阶段2 异步按键请求另排期。
- **设置窗可读性 + 齿轮鼠标语义 + 托盘懒显示（2026-11-06 用户反馈）**：
  - Windows 设置窗：卡片标题带与内容行分离（「中英状态」不再被三选一按钮压住），
    子控件背景改用卡片刷（不再出现深色横条），第一张卡片标题改为「输入行为」、
    去掉与行标签重复的标题，补全说明文字并删除 7 个未布局的标题 Static 控件。
  - 候选栏齿轮改为鼠标抬起（mouseup）才打开设置窗（Windows/macOS 同步实现）；
    候选词点选保持按下即选的既有语义。
  - 托盘中英图标不再随登录启动：安装器与便携包清掉旧 ChaosetUi Run 项，UI 宿主
    由 TIP 首次激活时带 --mode 懒启动，收到首条模式上报后才显示图标。
  - 三端设置窗统一契约：640×560pt、所有页页脚版本号、左导航选中态、关于页
    标题与正文、词库导入统一文案；macOS/Linux 同步实现并由 checkuiparity 断言。
- **托盘点击语义修正 + 中英状态作用域 + 设置窗卡片化（2026-11-06 用户要求）**：
  - 托盘左键改为切换中英（旧实现是打开设置窗），右键弹出菜单（打开设置… / 关于）；
    「关于」不再另开 MessageBox，直接跳设置窗的关于页。
  - 中英状态作用域可配置（system/process/window，默认 system）：UI 宿主命令管道
    改双向（PIPE_ACCESS_DUPLEX），新增 query/toggle 请求-应答；状态同步只在获得
    焦点/激活时做一次，按键热路径零 IPC。
  - process/window 档下托盘只反映当前活动窗口的状态（只有前台进程才上报模式）。
  - 设置窗改为卡片式布局（左导航 + 右侧圆角卡片 + 页脚版本号 + 滚动），640×560pt；
    三端同步新增「中英状态」配置项（macOS UserDefaults / Linux 配置文件）。
  - 删除 skin/ 包与 tools/render_metrics.go：产品里零调用、只在 macOS
    visual-parity.sh 里当对照物，容易误认为跨端视觉真源；基准值改为内联在该脚本。
- **版本号规则简化为单一环境变量（2026-11-06 用户要求）**：用户原话「本地测试的
  时候不想一直累加版本号，同时又想知道我本地测试的到底是什么版本」、
  「除了在 github ci 上通过 tag 触发的时候走规范的版本号，其他都年月日+commit
  hash」，最后明确「这不是很简单通过一个环境变量读取就可以做到么」。此前
  `make version` 用 `git describe --tags`，本地/日常 CI 打的包永远显示最近一次
  发布的 tag；中间一版在脚本里判断了四个 `GITHUB_*` 变量，属于过度设计。
  现在 `scripts/version.sh` 只读一个环境变量 `CHAOS_VERSION_TAG`：
  - `release.yml` 顶层 `env:` 用 `github.event_name == 'push' &&
    startsWith(github.ref, 'refs/tags/v') && github.ref_name || ''` 注入——
    **只有 push tag 触发**时该变量是 tag 号（去 v），`workflow_dispatch`、
    日常 CI、PR、本地全部为空。
  - 读不到变量就生成「构建当天年月日 + commit hash」：`20261106-8b2eb0ca0c38`，
    工作区有未提交改动追加 `-dirty`。`--source` 返回 `date-hash`。
  - `--quad` 派生 4 段数字版本（tag → `0.3.17.0`；其余 → `0.0.A.B`，
    A/B 由 hash 前 8 位十六进制转十进制），供 NSIS/PE `VS_FIXEDFILEINFO`；
    日期与完整显示版本放在属性页字符串字段。`--pkg` 派生
    `0.0.0+<日期>.<hash>[.dirty]`，供 deb/Arch（版本号必须以数字开头）。
  - Makefile 的 `$(VERSION)`/`$(VERSION_QUAD)`/`$(PKG_VERSION)` 全部来自该脚本；
    三端 `build.sh` 与 `release.yml` 构建后断言都复用 `version.sh` 输出。
  - `make check-version` 与 `checkuiparity` 的「版本号来源」契约锁死
    `CHAOS_VERSION_TAG`、年月日格式与 tag 注入条件（含变异测试）。

- **Windows 安装程序与二进制补齐版本信息 / app 信息（2026-11-06 用户反馈）**：
  此前 `ChaosetSetup.exe` 没有任何版本资源（右键「属性 → 详细信息」全空），
  用户无法判断下载/安装的是哪一版；`ChaosetIME.dll` / `chaoset-ui.exe` 等
  PE 二进制同样没有 `VS_VERSION_INFO`。现在：
  - NSIS 用 `VIProductVersion` / `VIFileVersion` + `VIAddVersionKey` 写入
    产品名、公司、版权、文件说明、原始文件名、注释与「产品/文件版本」；
    向导欢迎页标题带版本；版本号由 Makefile 的 `$(VERSION)`（git tag 单一真源）
    与归一化 4 段数字 `$(VERSION_QUAD)` 注入，源码不写死。
  - 「已安装的应用/控制面板」卸载项补全 `DisplayVersion`、
    `InstallLocation`、`InstallDate`（`${GetTime}` 取 YYYYMMDD，不用 locale
    相关的 `%DATE%`）、`EstimatedSize`（安装后 `${GetSize}` 实测）、
    `HelpLink` / `URLInfoAbout` / `URLUpdateInfo`、
    `NoModify` / `NoRepair` 等 app 元数据。
  - `shims/windows/resources/chaoset.rc` 改为「图标 + VS_VERSION_INFO」模板，
    `@VERSION@` 等占位符由 Makefile / `build.sh` 经 sed 注入后 windres 编译；
    64/32 位前端 DLL 与 `chaoset-ui.exe` 各带一份带自身 `FileDescription` 的
    版本资源，中文用 `windres -c 65001` 保证属性页不乱码（默认 ANSI 会把
    「拼音输入法」存成 mojibake）。
  - `make check-version` 与 `tools/checkuiparity` 新增契约组锁死版本信息，
    `release.yml` 冒烟校验四个 PE 的版本资源与中文编码；补了变异测试。
  - 同批修复打包回归：`ChaosetRunUi` 的 `__declspec(dllexport)` 会让 mingw
    停止自动导出其余全局符号，导致 `DllRegisterServer` 等 COM 入口消失、
    `regsvr32` 注册失败。两条构建链都加 `-Wl,--export-all-symbols`，
    `build.sh` 从「只打印导出」升级为逐个断言 COM 入口必须存在。

- **Windows 中英文模式提示/任务栏图标乱套、开始菜单里设置窗打不开（2026-11-05
  用户反馈）**：两个用户可见问题的根因是同一类架构错误——把「每用户会话唯一」
  的 UI 放进了会被 TSF 加载进每个宿主进程的 TIP 里。
  1. 模式指示器：`ModeTray` 是 `ChaosetTIP` 成员，TSF 每个线程/TIP 实例各一份
     （实机 explorer.exe 单进程 4 个实例、4 个托盘窗口），且 `enMode_` 每实例
     独立 → 任务栏同时出现「中 英 中 英」；托盘不可用时的浮层兜底加上候选窗
     锚点的鼠标兜底 → 用户看到「光标附近 + 鼠标附近」两处提示。
  2. 设置窗由 TIP 所在宿主进程创建：开始菜单/搜索（SearchApp.exe，Low IL
     AppContainer）里被沉浸式 z 序 band 压住，且 AC 中文件对话框、子进程与
     HKCU 注册表都不可用，表现为点齿轮无反应。
  修复：新增每用户会话唯一的 UI 宿主 `chaoset-ui.exe`（`ChaosetRunUi` 导出；
  exe 只是 LoadLibrary 壳，设置窗/托盘实现仍在 `win32.cpp` 一份）：
  - 唯一托盘「中/英」图标；TIP 通过命名管道 `\\.\pipe\chaoset-ui-<SID>`
    上报模式（DACL/SACL 与 daemon 完全同规则，AC 宿主可写）。删除 TIP 内的
    `ModeTray` 与模式 toast 兜底，模式提示只剩任务栏一处。
  - 设置窗改由 UI 宿主创建（候选栏齿轮或托盘图标左键单击均可），TIP 内只发
    管道请求；AC 宿主里能打开，宿主退出也不再带走设置窗。
  - 登录 Run 项 + TIP 懒启动 + `Local\ChaosetUi-<SID>` 单例 + 第二实例
    `--settings` 转发；安装器/便携脚本升级与卸载结束后台旧 UI 进程。
  同批回归审计修复（P1/P2）：失焦时「挂起 Shift」被无条件清除（切窗口/
  Shift+点击后误切中英）；SetWinEventHook 改为按 hook 句柄映射实例（回调不再
  触碰 TSF，缓存 `composeHostWnd_`）；陈旧组词清理先落盘已确认段；
  高 DPI 托盘掩码缓冲动态分配并修掉双重 `DeleteDC`；提示条拒绝鼠标兜底锚点
  （`caretFromMouse_`）；`bailOutIfInactive` 清理联想态；`reconcileShiftState`
  覆盖 Key 阶段；`ActivateEx` 拒绝 UIElementOnly 线程；DllMain 注销新增窗口类；
  删除死代码 `shiftOtherInputDown`。`checkuiparity` 新增「单例 UI 宿主/模式
  指示器」契约组（禁止回退为 TIP 自绘托盘或 toast 兜底），构建/安装器/发行包
  自检都必须带 `chaoset-ui.exe` 与 `ChaosetRunUi` 导出。
  已在用户的 Windows 10 22H2 上对 `chaoset-ui.exe` 做过独立冒烟：单例唯一托盘
  图标、管道 `mode` 更新图标、托盘左键打开设置窗、第二实例 `--settings` 转发
  不重复、管道 `quit` 被忽略、关闭走「停管道线程 → join → 销毁窗口」后进程
  干净退出且无 crash.log 记录；TSF 内完整链路需安装后真机验收（重点：开始菜单
  搜索框里点齿轮、任务栏是否只剩一个中英图标）。

- **选定的输入法不是 chaoset 时，按 Shift 仍出中英提示**（2026-11-05 用户反馈）：
  实机日志里能看到这种情况下我们的 `shift toggle` 仍在触发——TSF 在某些宿主里
  会把按键继续投给未激活的 TIP 按键下沉（`Deactivate` 未被调用或不及时）。
  现在四个按键入口（Test/Key × 按下/抬起）都先查「本线程激活的输入处理器
  是不是我们」：`ITfInputProcessorProfileMgr::GetActiveProfile(GUID_TFCAT_TIP_KEYBOARD)`
  返回的 clsid 不是 `CLSID_ChaosetTIP`（或类型不是输入处理器）→ **整键放行**，
  同时收掉候选窗/提示条/托盘图标并复位 Shift 记账。
  `checkuiparity` 新增「输入法激活判据」断言，锁死 4 处闸门。
- **任务栏中英图标一直没出现：`CreateIconIndirect` 收到 NULL 掩码而失败**
  （2026-11-05 实机定位）。托盘图标的 `ICONINFO.hbmMask` 不能为 NULL，部分系统
  上会直接失败，于是图标永远建不出来、只能一直退回浮动提示——这正是用户看到
  「光标下方和鼠标右下角两处提示、任务栏却没有中英状态」的原因。
  现在创建 1bpp 掩码位图一并传入（32bpp 图标实际由 alpha 通道决定透明，掩码
  全 0 即不透明），并对三条失败分支（建窗 / 建图标 / Shell_NotifyIcon）分别
  记录错误码。
- **诊断预算修正**：`diagBudget()` 每进程 200 次，`mode compartment` 每次切换
  都记会让预算提前耗尽，失败日志被静默吞掉（实机：0 条失败日志却确实失败）。
  隔间日志改为「成功一次 / 失败每次」，托盘失败日志**绕开预算**。
- **中英文模式提示重复出现在两处（鼠标位置与输入光标）**：模式提示现在只出一处
  ——托盘图标可用就用托盘（常驻、不挡输入），只有托盘不可用（AppContainer 宿主，
  如开始菜单/搜索）才退回浮动提示；同时浮动提示**不再回退到鼠标位置**，
  光标坐标未知时直接不显示，避免「提示跟着鼠标跑」。
- **crash.log 变干净了：只记录错误级异常**。此前 VEH 处理器把宿主自己抛出
  又接住的 COM/RPC 首次异常也记成「崩溃」，实机上开始菜单/搜索的 AC 目录堆了
  197 条，主目录 57 条（`0x80040155` × 32、`0x800706BA`、`0x6D9` 等），
  而对应 pid 从未变化、进程一直活着——全是正常控制流。现在按 NTSTATUS 严重性位
  过滤，只有 `0xC0000000` 档（访问违例/栈溢出/fail-fast/页内错误）才记。
- **文档澄清：开始菜单能切 Shift 归功于 0.3.19 的双阶段跟踪**，与
  `ITfTextInputProcessorEx`（UILess）无关——实机 `activateEx flags=0x40000000
  uielementonly=0`，UILess 至今一次都没触发过。重启 explorer 只起「让新 DLL
  生效」的作用。
- **开始菜单失焦重开后仍残留上一轮拼音**：`stale state dropped` 已在触发，但
  清理只复位了本地状态，**没有结束 TSF 合成**——宿主文档里的预编辑文本还在，
  重开后自然又出现。现在清理第一步就 `endComposition()` 把预编辑从文档里删掉，
  并在 `Activate`（TSF 对这类宿主会重复调用）也执行一次。
- **任务栏「中/英」模式指示器**：实机确认线程隔间写入成功
（`set_hr=0 get_hr=0 read=1/0`，AppContainer 宿主里也成功），但 Windows 并**不**
  为第三方 TIP 绘制任务栏输入指示器。改为自建托盘图标：`ModeTray` 用 GDI 画
  16×16 圆角底 + 白色「中」/「英」，只在本地输入上下文有焦点期间显示，
  `TaskbarCreated` 后自动重加，窗口计数纳入 `DllCanUnloadNow` 防卸载。
  AppContainer 宿主（开始菜单/搜索）无托盘权限，添加失败只记一次日志。
- **按 Shift 时另一个窗口被顶到前面（Chrome/Firefox 多窗口，实机复现）**：
  根因是候选窗创建时的一行兜底——`popupOwner()` 对普通宿主返回 `nullptr`，
  但紧接着 `if (owner == nullptr) owner = GetForegroundWindow();` 又把前台窗口
  设成了 owner，绕过了「只给沉浸式 shell 宿主设 owner」的规则。带 owner 的
  TOPMOST 窗口一显示就把 owner 所在窗口组抬到前面，表现为「弹到别的窗口、
  键盘焦点却不动」。实机证据：chrome pid=5372 `candidate shown ownpid=5372`
（本进程窗口）而同一进程 `toast shown ownpid=0`（提示条没这条兜底，所以不弹）。
  现在改为只在 `popupOwner()` 允许时才设 owner；`checkuiparity` 增加排除断言。
- **诊断：中英隔间写入结果可验证**：`mode compartment en=... set_hr=... get_hr=... read=...`
  回读确认 `GUID_COMPARTMENT_KEYBOARD_OPENCLOSE` 是否真的写进去，用于判断
  「任务栏中/英不显示」是写入失败还是系统未绘制。
- **诊断修正**：`shift toggle` 里的 `samewnd` 在未组词时恒为 0、没有信息量；
  改为同时记 `comppid`（合成上下文所属窗口的进程），只在 `pending=1` 时
  `samewnd` 才有意义。
- **开始菜单关闭后重开仍残留上一轮拼音与候选窗**：开始菜单/搜索宿主
  **不投递 `OnSetFocus(FALSE)`**（实机确认），原来的失焦清理路径完全不触发，
  本地组词状态与候选窗都留着。改用系统级 `EVENT_SYSTEM_FOREGROUND` 钩子
  （`SetWinEventHook`，事件驱动、非轮询）作为「用户已离开本窗口」的可靠信号：
  前台回调里只收起浮窗并打标记（**不碰 TSF**——旧上下文可能已销毁，
  贸然发编辑会话会失败甚至干扰宿主），真正的状态清理由下一次按键在
  TIP 线程上完成。
- **补任务栏「中/英」模式指示器**：把中英模式写进 TSF 线程级隔间
  `GUID_COMPARTMENT_KEYBOARD_OPENCLOSE`（VT_I4，非零 = 键盘打开/中文），
  由 Windows 自带的输入指示器显示——与微软拼音、搜狗同一机制，不需要自建
  托盘图标也不需要额外进程。在 `Activate`、模式切换、焦点回来三处刷新。
  注：若任务栏没显示，需在「设置 → 个性化 → 任务栏 → 系统托盘图标」里打开
  「输入指示器」。
- `shift toggle` 日志补充 `pending` 与 `samewnd`（合成上下文窗口是否就是
  前台窗口），用于定位 Firefox 多窗口下切换时把别的窗口顶到前面的问题。
- **补齐 `ITfTextInputProcessorEx`（UILess 模式的第一步）**：微软《UILess Mode
  Overview》要求 TIP 实现该接口，否则在 UILess 线程里**不会被激活**。此前
  只实现 `ITfTextInputProcessor`，因此对 UILess 宿主（游戏/全屏/部分 WinUI 应用）
  完全不可见——而且因为不会被激活，`tsf.log` 里连一行都不会有。这也修正了此前
  一个错误推论：「日志里 uiless=0」不等于「不存在 UILess 宿主」，那是被排除的结果。
  本次只补接口与 `ActivateEx`（记录 `flags` 与 `uielementonly`），**渲染路径不变**；
  是否改用 `ITfUIElementMgr` 提交候选，等日志出现真实 UILess 宿主再决定。
  仍未注册 `TFCAT_TIPCAP_UIELEMENTENABLED`——声明了就必须让所有 UI 走 UI 元素
  管理器，否则就是 2026-10-30「开始菜单里候选窗消失」的原因；`checkuiparity`
  已加断言锁死这一点。
- **按 Shift 仍会把同进程的另一个窗口弹到前面（Firefox 多窗口/PWA）**：
  加了 `ownpid`/`fgpid`/`selfpid` 诊断后实机确认三者相等，说明 owner 既不是
  跨进程也不是陈旧上下文——问题在更基础的一层：**带 owner 的 TOPMOST 窗口
  一显示就会调整 owner 所在窗口组的 z 序**，同进程有多个窗口时另一个窗口被抬
  上来。owner 本来只为 Windows 11 开始菜单/搜索的沉浸式 z 序 band 而设
（2026-10-30 实机取证），普通桌面宿主不需要。现按宿主类型区分：
  只有 explorer / SearchApp / StartMenuExperienceHost / ShellExperienceHost /
  TextInputHost / ApplicationFrameHost 这类沉浸式 shell 宿主才设 owner，
  其余宿主一律无 owner 的 topmost 浮窗，不再动别人的 z 序。
  checkuiparity 增加 `shellHostNeedsOwner` 契约断言。
- **候选窗/模式提示条一出现就把别的窗口顶到前面**：这两个浮窗都以 TSF
  上下文推出的窗口为 owner（`hostWindowOf(ctx)`）。若上下文停留在**另一个
  窗口**（用户先在别处打过字、或窗口内部焦点抖动），带 owner 的 TOPMOST 窗口
  显示时会把 owner 所在窗口组抬到前面，表现为「弹到另一个窗口，但键盘焦点
  仍在原输入框」。现统一改为 `popupOwner()`：只接受**当前前台窗口且属于本
  进程**，跨进程 owner 一并排除（否则会把别人进程的窗口带上来）。
  owner 本身仍保留——Windows 11 开始菜单/搜索是沉浸式 z 序 band，无主 topmost
  窗口会被压在整个 band 之下（2026-10-30 实机取证）。
  checkuiparity 增加「浮窗 owner」契约断言。

- **诊断：候选窗/模式提示条记录 owner 与前台窗口的 pid**
  用户反馈按 Shift 切中英时会「弹到另一个窗口、但焦点仍在原输入框」。
  这类现象只有两种可能：带 owner 的 TOPMOST 窗口显示时把 owner 所在窗口组
  抬到前面，或 owner 指向了另一个进程的窗口。为定位，`candidate shown` 与
  新增的 `toast shown` 两行现在带 `ownpid` / `fgpid` / `selfpid`：
  若 `ownpid` 与 `selfpid` 不同，说明 owner 指到了别的进程的窗口。

- **切到英文模式后仍继续出拼音候选（Chrome / 终端 / 命令行 / 开始菜单）**：
  英文模式的放行判断此前只写在 OnTestKeyDown（预告点），而实测这些宿主连
  内容键也走 Key 阶段（预告点根本不执行），于是 enMode_ 虽然已翻转、
  handleKey 仍照常组词。实机日志：16:13:42 shift toggle enMode=1 之后
  16:13:43 仍在 candidate shown。现于 handleKey（真正的处理点）也判断
  enMode_ 并放行，checkuiparity 增加「两处判断」断言。
- **开始菜单重开后残留上一轮拼音**：宿主关闭时可能不投递 OnSetFocus(FALSE)，
  本地 comp_ 与合成文本就留下来，重新打开开始菜单后旧拼音又冒出来。
  现于 OnSetFocus(TRUE) 以「有没有活合成」为准清理：composition_ 为空而
  本地还有组词时丢弃残留（合成仍在则保持不动）。

- **Chrome / conhost / Windows Terminal 里 Shift 完全无法切换（实机日志定位）**：
  2026-10-31 曾把 Shift 跟踪从 OnKeyDown/OnKeyUp 整体移到
  OnTestKeyDown/OnTestKeyUp，依据是「Test 返回 FALSE 的按键不会再进 Key 阶段」。
  实机 tsf.log 证明这个前提只对部分宿主成立：宿主分成两类——
  Firefox 把修饰键送进 **Test** 阶段；Chrome / conhost / Windows Terminal
  只送 **Key** 阶段（同一进程里 modifier in OnKeyDown vk=0x10 有、
  modifier in OnTestKeyDown 无）。只挂 Test 阶段等于对后一类宿主彻底失效。
  现改为**两阶段都跟踪**：shiftHeld_ 位掩码与 begin/end 都是幂等的，
  同一按键被两阶段各投递一次不会双计，重复释放会被「孤立释放」拦住。
  checkuiparity 增加断言锁死调用点数量（Test/Key × 按下/释放 = 4 处调用）。

- **能打字但 Shift 切不了中英：焦点抖动把按住状态吃掉了**（用户反馈：
  Chrome「安装应用」窗口、Firefox 装到任务栏的独立窗口里 TIP 明确是激活的、
  字母能组词，只有 Shift 不生效）：
  浏览器会在**窗口内部**把焦点在地址栏 / 页面元素之间搬动，这同样会触发
  `OnSetFocus(FALSE)`。旧实现一收到失焦就 `resetShiftTracking()`，于是
  「按下 Shift → 焦点抖一下 → 松开」永远走不到结算，表现为 Shift 完全失效。
  现改为：失焦时若 Shift 仍物理按下，只**挂起**而不丢弃（松开前焦点回来就
  照常切换；真的切走应用则松开时只清状态不切换）。
- **撤除 v0.3.16 的 `GetKeyState` 轮询兜底，恢复纯事件驱动**：输入法应当由
  按键事件触发，不该常驻定时器轮询。轮询每 40ms 唤醒一次，而 TSF TIP 会被
  加载进每个宿主进程，开销是实打实的；且 40ms 采样无法准确判断「快速轻按」
  与「Shift+点击」，存在误切换风险。真因（焦点抖动丢状态）在事件路径上修好后，
  轮询不再有必要。保留的兜底是**事件驱动**的：每次按键到达时若发现物理 Shift
  已松开、但按住状态还挂着，就地收口（零常驻开销），并留一条一次性证据
  `tsf.log: shift: 宿主丢弃了 Shift 释放事件`。
## v0.3.17（2026-11-05）

- **候选窗在拿不到光标矩形时会盖住输入行**：锚点退回鼠标位置的分支只加了 6pt
  间距，而鼠标通常正停在正在输入的那一行上，候选窗因此直接盖住刚打的字
  （Firefox 装到任务栏的独立窗口实测）。现改为把鼠标锚点先下移一行再算间距，
  候选窗落在输入行下方。
- **Shift 切换不再做无谓的 daemon 往返**：切中英本身是纯壳本地状态，只有
  「还有未上屏内容要丢弃」时才需要通知 daemon。无未上屏内容时改为零 IPC
  （双拼例外：引擎的暂存首键壳侧看不见，仍需清一次）。同步往返在 daemon 忙时
  可达 250ms，且发生在按键回调里——PWA/独立窗口这类宿主对 TSF 编辑会话更敏感。
- **诊断增强（PWA 排障）**：`activate` 与 `shift toggle` 两行日志补 `fgpid`/`selfpid`，
  用于判断本 TIP 是否落在拥有焦点的进程里；`shims/windows/README.md` 补充
  PWA/「安装应用」窗口的排查步骤。
## v0.3.16（2026-11-05）

- **Windows 部分宿主里 Shift 仍切不了中英：补物理状态轮询兜底**
  （用户反馈 v0.3.15 之后 Windows Terminal / cmd 命令行 / 开始菜单仍然无效）：
  上一版把记账改成幂等位掩码，解决了「计数器只增不减」的永久失效，但结算
  仍要求 Shift 的按下与释放**成对**到达。这些宿主对修饰键的投递不完整——
  只送按下不送释放，或干脆不把修饰键送进 TSF 按键下沉——于是永远走不到
  结算那一步。README 早先承诺「宿主不投递 Test 阶段就退回 GetKeyState 轮询」，
  这次真正兑现：新增 40ms 的 message-only 窗口计时器（`onShiftPollTick`），
  按 `GetAsyncKeyState` 的物理状态自行推断「按住 → 松开」并结算；结算前用
  `GetKeyboardState` 与鼠标键状态确认期间没有别的输入，避免 Shift+点击被
  误判成空按。轮询与事件路径共用同一套状态，先结算的一方会让另一方自然跳过。
  门控用 TSF 的 `OnSetFocus` 焦点信号而不是前台 PID——conhost 的窗口属于
  conhost.exe，用 PID 判断会把控制台永远当成后台、导致兜底不生效。
  轮询窗纳入 `DllCanUnloadNow` 防卸载计数。`tsf.log` 新增一次性证据行：
  `shift poll: 宿主未投递 Shift 按下` / `释放事件缺失，由物理状态结算`。
## v0.3.15（2026-11-05）

- **reset 语义分层：区分「继续打字」与「会话结束」两类收尾**：
  原先只有一个 `reset`，它同时清组词**和**学习上下文（prev/prevSylls/
  history/seqSegs）。壳在切中英、Esc、透传、退格删空、顶屏等「用户还会继续
  打字」的场景全都复用它，于是每次收尾都把引擎刚记住的上屏词一起抹掉，
  紧接着的候选失去 bigram 上下文加成（实测：学习过「我们→时」后输入 `shi`，
  带 prev 的首候选是「时」，清掉后退回「是」）。
  现拆成两条链路：`ResetComposition` 只丢弃当前组词、保留学习上下文；
  `Reset` 保持原语义（连上下文一起清），只用于**会话真的结束**的场景——
  Windows `Deactivate`/`OnSetFocus(FALSE)`/`OnCompositionTerminated`、
  macOS `commitComposition`/`deactivateServer`、Linux `focusOut`/IBus `reset`。
  IPC 新增 `resetComposition` 方法，C++ 核心层与三端壳同步；Windows 侧新增
  `resetComposition()` 收尾入口并把 8 处「继续打字」调用点迁过去。
  新增三向回归：`ResetComposition` 保留 prev、`Reset` 仍清空 prev/history、
  保留的 prev 确实抬高候选分数（证明不是形式上的保留）；`checkuiparity`
  新增「reset 语义」断言，防止三端误用。

## v0.3.14（2026-11-05）

- **全/半角标点宽度改为显式参数，daemon 彻底不保存该状态**（架构收敛）：
  全/半角开关的唯一真源是壳（macOS `halfWidth`、Windows/Linux `halfWidth_`），
  引擎此前还存了一份镜像字段，靠 `SetHalfWidth` / `InputWithHalfWidth` 在请求
  之间“同步”。这是**重复真源**：任何一次未携带 `half` 的请求都会让 daemon 用
  上一次的残留值产出标点——正是 2026-10-30 记录的「刚切全/半角就单按 `;` 按
  旧宽度出标点」1 键瞬态的根源。
  现改为 `Input(text, halfWidth)` / `Punct(key, halfWidth)` 的**必填参数**：
  宽度只对本次调用生效，不写入任何会话状态，结构上不可能残留。
  **不保留兼容层**（项目处于快速迭代期）：`ToggleHalfWidth` / `SetHalfWidth` /
  `HalfWidth` / `InputWithHalfWidth` / `PunctWithHalfWidth` 全部删除，
  `engine.Controller` 接口与 `ipc.Client` 同步改为新签名，全部调用点（含 165 处
  测试）一并迁移。新增 `TestHalfWidthNoResidual` 锁死「半角后紧接全角请求必须
  立即恢复全角」。

> 设计原则（本次确立）：**状态归属按「唯一真源在哪」划分**——词库/用户词典/
> 组词进度/标点循环等算法状态归 daemon；UI 开关（中英模式、全/半角）归壳；
> 壳交给 daemon 的状态一律**随请求传参、不在 daemon 存镜像**，且不留向后兼容
> 包装层（迭代期直接改契约）。


- **全/半角标点宽度改为纯参数下发，daemon 不再保存该状态**（架构收敛）：
  全/半角开关的唯一真源一直是壳（macOS `halfWidth`、Windows `halfWidth_`、
  Linux `halfWidth_`），但引擎里还存了一份镜像字段，靠 `SetHalfWidth` /
  `InputWithHalfWidth` 在请求之间“同步”。这属于**重复真源**：任何一次没携带
  `half` 的请求（旧壳、或只调 `punct` 的路径）都会让 daemon 沿用上一次的
  残留值产出标点——正是 2026-10-30 记录的「刚切全/半角就单按 `;` 按旧宽度
  出标点」1 键瞬态的根源。
  现改为 `PunctWithHalfWidth(key, half)` 与请求内生效的 `reqHalfWidth`：
  宽度只对本次调用有效，不写入会话状态，**结构上不可能残留**。
  `ToggleHalfWidth` / `SetHalfWidth` / `HalfWidth` 保留为兼容 API（仅供测试与
  CLI 回读，不再影响 `Punct` 输出），三端壳无需改动（本来就按参数下发）。
  新增回归测试 `TestHalfWidthNoResidual` 锁死「半角后紧接全角请求必须立即
  恢复全角」，并更新 `TestHalfWidthMode` / `TestHalfWidthComposing` /
  `TestDoublePinyinSessionSemicolonPunct` 到新 API。

> 设计原则（本次确立）：**状态归属按「唯一真源在哪」划分**——词库/用户词典/
> 组词进度/标点循环等算法状态归 daemon；UI 开关（中英模式、全/半角）归壳；
> 壳交给 daemon 的状态一律**随请求传参、不在 daemon 存镜像**。`reset` 语义过重
> （连带清 prev/history）留待后续单独收敛。

- **Windows Shift 空按切中英在部分程序里永久失效**（用户反馈：Terminal、开始
  菜单、Chrome 里按 Shift 不能切中英；Firefox 可以）：
  旧实现用 `shiftDown_++/--` 记账，只有计数回到 0 才结算。**只要丢失任意一次
  Shift KeyUp，计数器就只增不减、永远回不到 0**，该进程内 Shift 空按从此彻底
  失效，只能重启应用恢复。丢失 KeyUp 不是宿主 bug，而是 Windows 的既定行为：
  按住 Shift 时切换窗口（Alt+Tab / 点别的窗口），KeyUp 交给**新前台窗口**，
  原线程永远收不到；TIP 被 TSF 停用期间发生的按键同理。长驻进程 + 频繁切窗口
  的宿主（Terminal/Chrome/开始菜单宿主）最容易命中，这正是实机症状的分布。
  改为**幂等位掩码**（`shiftHeld_`：置位/清位天然幂等，auto-repeat 与重复投递
  不再累积；左右 Shift 各记一位，双 Shift 先松一侧不切换）；补上
  `Deactivate`/`OnSetFocus(FALSE)` 复位（对齐 Linux `shift_.reset()`，
  Windows 此前完全没有这一步）；新增 `reconcileShiftState` 在每次按键前用
  `GetAsyncKeyState` 自愈丢失的 KeyUp（**只复位不结算**——丢失时无法判断是否
  空按，凭空切换比少切一次更糟），兑现 README 原承诺的兜底且不引入常驻定时器。
  `checkuiparity` 新增「Shift 记账」检查组锁死契约（含禁止 `++shiftDown_`
  回归），已接入 `make vet` 与 CI。

## v0.3.13（2026-11-04）

- **Windows 安装版 daemon 起不来导致只能打英文**（用户反馈；v0.3.0–v0.3.12 的
  Windows 包都受影响）：`chaosetd` 用命名管道启动时也先去解析 Unix socket
  默认路径，而 `internal/appdirs.SocketPath()` 在 Windows 上一律返回错误，daemon
  在建立监听之前就退出——TSF 壳连不上命名管道，按键全部透传成英文、候选窗不出现。
  现仅在真正需要 Unix 传输时解析，并加回归测试固定「命名管道启动不得因 socket
  路径解析失败而退出」。
- Windows README 的排障章节：改用 `chaoset-cli.exe -version` 确认构建
  （`chaosetd.exe` 是 GUI 子系统，cmd 里 `-version` 无输出），并补齐
  「装完只能打英文」按 daemon 日志断点/命名管道/CLI 验 IPC 的定位流程。
- **Linux 全新会话下 daemon 启动失败**：单实例锁在创建套接字目录之前执行，而锁
  文件是 `<socket>.lock`——`$XDG_RUNTIME_DIR/chaoset` 尚不存在时 daemon 直接以
  「打开锁文件 … no such file or directory」退出。改为先建目录再拿锁，并加回归测试。
- **Linux socket 路径两套口径**：原生引擎用 `g_get_user_runtime_dir()`，该函数在
  `XDG_RUNTIME_DIR` 缺失时会回退到 `~/.cache`，而 Go 的 `appdirs.SocketPath`
  回退到数据目录——两边算出不同 socket，管理工具连不上且能起两个 daemon。引擎改为
  直接读 `XDG_RUNTIME_DIR`，与 Go 侧一致（实测修复后两端路径相同）。
- **macOS GUI 安装会撤销 LaunchAgent 的 QoS 设置**：`setup/main.swift` 与
  `install.sh` 写同一个 plist，前者漏了 `ProcessType=Interactive`，走 app 内一键
  安装/更新后高负载下的 IPC 延迟修复被静默回退。两份写入者现已一致。
- **Windows AppContainer 宿主（开始菜单/搜索框/UWP）现在可正常使用**：命名管道
  的安全描述符补上强制完整性标签 `S:(ML;;NW;;;LW)`。此前 DACL 已放行
  ALL APPLICATION PACKAGES，但低完整性的 AppContainer 宿主受 MIC no-write-up
  限制只能连、写不进——表现为「开始菜单里按了键、daemon 也在、却没有任何候选」。
  设置标签失败时降级为仅 DACL 并记日志，不会让 daemon 起不来。
  同时明确 TSF 壳在 AppContainer 宿主里**不发起进程**（`CreateProcessW` 会让子进程
  继承 AppContainer 身份，写错用户目录、抢同一管道名）：daemon 由登录 Run 项常驻，
  或由任意普通程序里的首次输入懒启动恢复，AppContainer 宿主只负责连接。
- **三端懒启动行为对齐**：macOS 壳此前完全没有「发现 daemon 没起来就拉起它」的
  路径，只依赖 LaunchAgent——plist 缺失、被 `launchctl bootout` 或安装后未重新登录
  时会永久降级成英文。现在 `EngineClient` 在连接失败时拉起 app 内 daemon（参数与
  LaunchAgent 完全一致、5 秒节流、不阻塞输入法主线程），与 Windows TSF 壳
  （`ensureDaemonStarted`）、Linux 引擎（`spawnDaemon`）一致；三端都有静态断言。
- Windows 上 `chaoset-userdict` 不带 `-pipe` 时的报错补上可操作提示；
  Windows README 澄清 `chaoset-cli` 本身不会拉起 daemon。
- **移除 TCP 传输**：daemon/CLI/Windows 壳不再有 `-tcp` 旗标、没有回退传输，
  只剩 Unix socket（macOS/Linux）与命名管道（Windows）两种。TCP 原是命名管道
  上线前的迁移期形态，安装器早已切到 `-pipe`，回退代码从未清理；保留它意味着
  一个无 ACL、本机任意进程可连的端口——输入法 IPC 承载的是击键内容。
  Windows 壳随之去掉 winsock 依赖，核心层行为测试改用 Unix socket 驱动。

## v0.3.12（2026-11-03）

- **Windows 设置窗导入词库后不再卡死/杀掉 daemon**（审查发现）：原实现用
  等待子进程退出的 `settingsRunProcess` 启动常驻 daemon，会阻塞 120 秒后
  `TerminateProcess`。新增 detached 启动路径；便携版/缺失 Run 项时也能从
  安装目录按 `-pipe -seeddict` 兜底拉起 daemon。
- **安装器/便携版刷新词库不再覆盖用户导入词**：改用 Go `chaoset-import`
  以 `-seed` 合并，旧文件按 `-backup` 备份；不再用 `CopyFiles` 直接覆盖
  `%LOCALAPPDATA%\Chaoset\dict.tsv`。
- **词库种子恢复**：`-seeddict` 对 0 字节目标词库也重新初始化（此前只处理
  文件缺失，空文件会让 daemon 加载 0 条词条后退出）。
- **TSF 懒启动细节**：已连接时不再重复拉起 daemon；尝试脱离宿主 Job
  （浏览器等宿主退出时不易带走 daemon）；daemon 起不来时后续按键不再逐键
  长时间阻塞；TCP 回退连接也计入“已连接”，避免旧 daemon 升级期重复拉起。
- **安装器语言列表注册**：改用 64 位 Sysnative PowerShell，避免 32 位 NSIS
  被 WOW64 重定向到 32 位 PowerShell；安装/卸载共用。
- **install.bat 对齐**：安装时用 Go 导入器刷新词库、卸载时清理 HKLM/HKCU
  的 Run 项；不再有 TCP/强杀宿主的旧描述。
- 验证：`go test ./...`、`make vet`、`checkuiparity` 全绿；Windows DLL
  交叉编译、NSIS 编译、`make package-windows` 成功；Windows 真机行为待验。

## v0.3.11（2026-11-03）

- **Windows 安装器不再关闭已打开的程序**（用户反馈：安装时浏览器/编辑器/
  聊天软件被强制关闭，未保存内容可能丢失）：删除「枚举加载 ChaosetIME*.dll
  的进程并 Stop-Process」以及强停 ctfmon/TextInputHost/SearchHost/StartMenu
  等系统宿主的逻辑；只终止本输入法自己的旧 daemon，被占用的 DLL 由改名/
  重启替换兜底。
- **Windows daemon 启动修复**（用户反馈：装完 daemon 未运行，只能打英文）：
  安装器不再以提升后的管理员身份启动 daemon（不同管理员账户会导致
  LOCALAPPDATA/命名管道 ACL 属于错误用户）。改为：
  - 安装器只写 REG_EXPAND_SZ 的 Run 项，`%LOCALAPPDATA%` 由每个登录用户展开；
  - TSF 在首次激活/首键时以当前用户身份懒启动 daemon，短暂等待管道就绪；
  - 安装版统一切到命名管道（`-pipe`），去掉 TCP 端口/多用户/回环豁免依赖；
  - `chaosetd` 新增 `-seeddict`：首次从安装目录复制种子词库到用户目录并创建
    用户词典/日志目录，不覆盖已有导入词库。
- **NSIS PowerShell 变量未转义**：`$tip/$l/$z/$x` 被 NSIS 当变量吞掉，语言
  列表注册命令实际损坏；统一写成 `$$`，恢复自动加入输入法列表。
- 便携版 `install.bat` 同步：不枚举/强杀加载旧 DLL 的宿主，不强制重启 ctfmon。
- `checkuiparity` 新增「Windows 安装安全」契约，防止批量杀进程/管理员启动
  daemon 的回归。
- **验证**：Windows DLL 交叉编译、NSIS 安装脚本编译、`make package-windows`
  成功；`chaosetd -seeddict` 单测与真实 daemon 集成通过；`go test ./...`、
  `make vet`、CI 全绿。Windows 真机行为待用户用新安装包验证。

## v0.3.10（2026-11-03）

- **单字读音词频去掉固定下限**（用户反馈 `readingFloor` 把不对的字混进来）：
  单字词频严格按「Jun Da 字频 × large_pinyin 读音占比」折算，占比低就词频低，
  不再保底 30% 字频。例：血 xie 10→1、佛 fu 9→1、得 dei 19→1、的 di 40→14、
  调 tiao 17→9；刷新后词库 65,051 条 / 单字 8,699。
- **补充 GSC 漏收的常用简体字**（用户反馈屌打不出来）：按 wordfreq 中文前
  5 万词扫描、剔除繁体/异体/粤语/日文新字体后补录 屌/囧/艹/诶/磺；词频按
  字符常用度折算，屌 22 分排在 掉27/调26 之后，不特殊置顶。
- **单段输入的拼音匹配层只出单字**（用户反馈 jie 出现 结果/节约 等前缀词）：
  单音节全拼与单字母声母不再出现任何词语，连写全匹配词（jie 的 饥饿、
  xian 的 西安、tuan 的 图案）也不出；要打词用显式分段 ji'e/xi'an/tu'an
  或完整多音节输入，联想是上屏后的独立路径。回归测试遍历 406 个单音节 +
  26 个单字母声母。
- **验证**：`go test ./...` 全绿；候选质量扫描（26,766 词条池 / 1,071 采样）
  口径不变：full 80.02/97.67、tail1 47.38/77.20、tail2 30.17/57.12、
  initials 9.99/17.46、mixed 30.16/57.70；CI 通过，发布 v0.3.10。

## v0.3.9（2026-11-02）

- **macOS Shift 切换中英文真机生效**（用户反馈：只能用拼音后回车输入英文）：
  IMK 只投递 `recognizedEvents` 声明过的事件类型，默认只声明 `.keyDown`，
  `flagsChanged`（Shift 空按）到不了输入法。`ChaosetController` 覆盖
  `recognizedEvents` 加入 `.keyDown, .flagsChanged`；`key_routing` 测试补断言，
  真机用 CGEvent 投递 Shift + 字母验证中英切换正常。
- **macOS 高负载打字卡顿 / 偶发丢首字母修复**（用户反馈：系统负载高时打字卡顿、
  丢拼音第一个字母）：LaunchAgent 未声明 `ProcessType` 时默认按 Background 调度，
  CPU 争抢下 IPC 被严重节流。`install.sh` 写入 `ProcessType=Interactive` 并加
  机器断言。8 核满载实测：p99 452ms → 17–20ms、最大 775ms → 31–57ms、
  >250ms 超时 27 次 → 0；空闲 p99 约 7ms 不变。
- **声母/半截拼音渐进消费**（用户反馈：`kkdz` 选「看看」后 `d'z` 被整串吞掉）：
  `consumeInputPrefix` 支持声母（k→kan）与半截前缀（gu→guo、zho→zhong、g→guo），
  选词只消耗候选覆盖的音节；完整音节不匹配时不退回声母（zheng≠zen 不被拆）。
  `kkdz` 选「看看」剩 `d'z` 继续出候选；`zhongg` 选「中国」的 g 属于 guo 一并消耗。
- **长串纯声母不再误判错拼、单字始终可兜底**：纯声母切分（含撇号分隔
  `k'k'd'z` 与长简拼）不再判错拼；`v` 作为声母参与切分（`zxcv` 不再 0 候选）。
  任何多段输入都在列表末尾追加首段全量单字（≤120，来自词典/用户词库），
  整词/组合候选仍在前面、单字块收尾；错拼输入除外（保持纠错固定第 3 位）。
  2~3 音节、强整词、两段简拼同样适用；词库没有的词可逐字拼出并学习。
- **用户输入 > 默认字频**：简拼/混拼下不再用「候选音节数 > token 数」误判超段
  （token 数不是音节数），学过的词优先于默认高频词（`ysfa` → 原生方案从 143
  位进首屏）；歧义切分（`tuan` = `tuan`/`tu'an`）按所有合法切分的最大段数判断，
  学过「图案」后 `tuan` 首选图案、排到「团」之前；`hao`/「好家伙」这类
  单音节输入 vs 多字词组仍保持「好」优先，非用户候选的超段排序不变。
- **发布包不再提供 DMG**（用户不会申请 Apple 开发者账号）：`Makefile` /
  `release.yml` / `ci.yml` 移除 DMG 构建与上传，只保留 `chaoset-macos.zip`；
  `checkuiparity` 断言同步；README / AGENTS / 安装器注释改为 ZIP 安装。
  历史 Release 中的 DMG 资产已删除。
- **macOS 安装推荐终端一行命令**：`install-online.sh` 用 `curl` 下载 zip、
  清除 `com.apple.quarantine` 后启动安装器，不需要开发者账号 / `sudo`，不会被
  Gatekeeper 拦截；README 与 macOS README 以一行命令为推荐方式，zip 作为
  离线/手动备选；澄清 Gatekeeper 只拦截带 quarantine 的文件。
- **测试与质量**：新增/更新声母消费、单字兜底、用户词优先、歧义切分、Shift
  按键路由等回归；`go test ./...` 全绿。固定口径扫描（1071 采样）full / tail1 /
  tail2 / mixed 持平，initials top3 17.27% → 17.46%、rankAvg 34.1 → 32.9；
  组词耗时约 514µs → 559µs/输入（daemon IPC 约 4ms，无感）。

## v0.3.8（2026-11-01）

- **常见反序双字母手误强制归位**（用户反馈：经常把 `ding` 打成 `dign`，要求把
  类似 `ng` 的字母组合都加入纠错）：新增 `forcedMistakePairs` ——
  `gn→ng`、`hz→zh`、`hc→ch`、`hs→sh`、`ev→ve`。这些反序组合在普通话拼音里
  几乎不出现、也不可能是合法混拼；`misspelledFromPatterns` 遇到即判定手误，
  归位修正排在 `typoCorrections` 最前，且只把归位修正无条件提升到精确层
  （不受「纠错词频须达常规候选 2 倍」门槛限制），通用编辑保持低层兜底，单字
  候选始终保留；同一输入多处连错会一起翻回（`dignhzi` → `dingzhi`）。实测：
  `dign→定`、`hzong→中`、`hcang→常`、`hsang→上`、`lev→略`。
- **韵母级反序软纠错**：`oa→ao`、`au→ua`、`eu→ue` 在拼音音节里不出现，但可能
  与合法混拼同形，归为软纠错——只在纠错路径里作为最高优先级修正，不强制触发、
  不无条件提升。实测：`hoa→好`、`doa→到/大`、`hau→话/化`、`xeu→需/学`。
- **组合由音节表推导而非手写清单**：取所有「正序在拼音里出现、反序从不出现」的
  双字母组合，剔除反序本身可能是合法混拼的组合（如 `ab/ba`、`id/di`），只保留
  语音单位（zh/ch/sh/ng/ve）与韵母级（ao/ua/ue）。两侧都合法的相邻互换
  （ai/ia、ui/iu、ou/uo、ei/ie、an/na、en/ne、in/ni、un/nu）不做强行纠错，
  避免误伤正常输入/混拼；边界写入 README 与项目 skill。
- **回归测试**：新增 `engine/typo_common_mistakes_test.go`（强制/软两档、词内
  多处连错、正常输入首选不变、候选必须来自词库）；既有纠错/候选/词库契约测试
  全部保持通过。

## v0.3.7（2026-11-01）

- **候选生成重构：去掉 DP 拼合造词（用户要求）**：`nihaoma` 首选「你号码」、
  `womende` 首选「我们的」、`zhonghuarenmingongheguo` 首选「中华人民共和国」
  这类「把词典片段拼成词库里不存在的复合词」全部移除；首屏候选只来自词典 /
  用户词库 / 学习记录中的真实词条，长输入按「最长已有词前缀 → 逐级回退前缀 →
  首音节单字」排序，单字候选始终保留。固定口径扫描 top1/top3 持平或上升
  （tail2 top1 23.61% → 28.14%），组词性能约 25µs → 12µs/键。
- **连写 DP 匹配已存在长词**：新增 `Dictionary.QueryJoinedPinyin` 与
  `UserDict.QueryJoinedPinyin`，按「输入连写 == 词条标准拼音连写」做音节 Trie
  遍历，只匹配词库里真实存在的词；修复音节切分枚举上限导致的漏词（内置词库
  规则多音节词采样漏匹配 1 → 0，如西安交通大学）。
- **错拼纠正组合候选**：长拼音打错一两个字母（`chagnshuru`、`tuzhogng`）时，
  对「相邻互换 / 删除」类修正串按已有词做分词 DP，给出「常输入 / 长输入」
  「图中」等组合候选（每一段都必须在词库 / 用户词库中真实存在）；仅当该输入
  没有任何整词级纠错命中时才提升到精确层（最多 8 个），避免拼接结果压过
  「关系 / 发票 / 你好」等真正的纠错；候选带整串消费标记，选中即上屏。
- **正确输入也给出已有词组合候选（只在列表末尾）**：正确拼写 `changshuru` →
  「长输入」、`tuzhong` → 「图中」、`womende` → 「我们的」等组合候选作为
  列表末尾的额外选项（新增 `rankComposition` 层级，排在所有真实词条与逐级
  回退碎片之后），不改变任何原有首选；回归测试锁定「组合候选不得进首屏」。
- **内置词库替换：jieba → 常用性门槛流水线（方案 A）**：词表改为
  《现代汉语常用词表》∪ HSK 3.0 ∪ 高频用法 ∪ 现代 / 热词手工表
  （`data/dict_hot.tsv`，随版本维护）；词频来自 wordfreq 与 THUOCL 领域加权；
  单字用《通用规范汉字表》8105 + 字频排序；修复 phrase-pinyin-data 缺拼音
  导致整词被丢弃的链路问题；生成器 `cmd/chaoset-dictgen` 重写并保持逐字节
  幂等。新词库 67,268 条 / 1.28MB（旧 145,262 条 / 3.18MB）；固定词表
  649 常用词 + 200 单字：full 94.7/99.8、tail1 70.6/93.6、tail2 38.8/60.9、
  initials 26.9/51.2、mixed 66.5/91.9；规范常用词覆盖 100% / 精度 94.5%
  （旧 95.8% / 38.8%）；GSC 8105 全覆盖；现代热词探针 98/98；加载
  101ms/22MB（旧 322ms/56MB）。**许可**：代码 MIT，词库数据 CC BY-SA 4.0，
  署名与来源见 `data/NOTICE.md`、README 与词库文件头。
- **学习数据按词删除（用户要求）**：`UserDict.DeleteWord` + IPC `userDelete` +
  `chaoset-userdict delete 词 [音节…]`；macOS 候选窗右键菜单「删除该词」、
  Windows 候选窗右键 TrackPopupMenu，右键不上屏 / 不抢焦点，删除后刷新候选并
  toast；只删本机学习记录（Words 调频与 Phrases 词组进化），不动内置 / 导入
  词库；Linux ibus 面板不支持自定义右键，已在 README 记录替代入口。
- **回归修复（v0.3.6 之后）**：`lianxiagn` 错拼候选整串消费残留拼音；macOS
  `jin` 退格末字母需按两次（客户端忽略空 insertText 时分级清理 marked text）；
  安装器词库版本升级缺陷（旧种子词库永久保留导致 `长(chang)` 缺失）。
- **测试与文档**：新增 / 重写候选边界与纠错回归（`sentence_test`、
  `typo_dp_test`、`user_history_test`、`dp_match_test`、
  `data_contract_test` 等）；README 重写多音节长输入规则并新增「引擎候选规则
  速览（当前）」；项目 skill 打分陷阱口径同步；词库替换同步更新数据契约与规模
  基准。

> **发版注意**：单字字频仍基于 Jun Da 语料（原作者声明研究 / 教学用途，商用需
> 书面许可）；商业发行前应替换为 Unihan `kHanyuPinlu`/`kFrequency` 或取得授权。

## v0.3.6（2026-11-01）

- **修复安装后出现两个 `chaosetd`**：macOS 安装器 `restartDaemon` 删除
  「LaunchAgent 未就绪时直接再 exec 一个 daemon」的兜底；改为 bootout 后
  等待 launchd job 消失、killall 并等待旧进程退出，再 bootstrap。LaunchAgent
  是唯一 owner，安装器不再与它竞争。`install.sh` 同样先清理旧 daemon。
- **Windows daemon 单实例**：新增会话级命名互斥体（`Local\ChaosetDaemon`），
  即使 TCP/命名管道参数不同也不会启动第二个；TCP 端口绑定仍是第二道防线。
- **回归断言**：macOS `verify.sh` 检查 `chaosetd` 进程数必须为 1；Linux e2e
  检查自动拉起的 daemon 只有一个；`checkuiparity` 静态锁住上述安装/单实例
  约束，防止以后重新引入双进程兜底。

## v0.3.5（2026-11-01）

- **Linux ibus 壳迁为原生 C++/libibus（用户要求：shim 用平台原生语言）**：
  `shims/linux/engine/` 用 GObject 子类实现 `IBusEngine`，只保留 IBus 按键路由、
  预编辑/候选表/辅助文本等平台 UI；拼音、词库、联想与用户词典全部经 Unix
  socket 调用 Go `chaosetd`。删除 Go `shims/linux/ibus-engine` 与 `godbus` 依赖。
- **共享 C++ IPC 核心**：Windows 的 `core.{hpp,cpp}` 移到 `shims/common/ipc/`，
  Windows 与 Linux 原生壳共用帧协议、JSON 解析和 `Session` 封装。
- **daemon 生命周期**：原生引擎首次按键自动拉起 `chaosetd`（支持绿色包同目录
  daemon/词库回退）；Linux 设置窗在导入/清空学习数据前先停止 daemon，避免旧
  内存状态回写；ibus 引擎与 daemon 的退出/重启路径统一。
- **Linux 打包/CI**：Makefile、Arch PKGBUILD、Debian control、CI/release
  安装 `libibus-1.0-dev` 并用 `g++` 构建；`test-ci.sh` 改为真实原生引擎 +
  daemon 的端到端（含导入词候选、鼠标点选、多实例隔离）。

## v0.3.4（2026-11-01）

- **macOS 安装器增加图形化卸载**：停止 LaunchAgent/daemon、移除输入法 app 与
  安装器（TIS 条目在注销/重登后由系统刷新），保留 Application Support 里的
  学习数据/词库；用户不再需要终端命令。
- **安装文档面向普通用户重排**：明确每平台只下载一个推荐安装包；Windows `.zip`、
  macOS `.zip`、Linux `.tar.gz` 标为高级；修正 macOS 最低版本（15+）、Linux 安装后
  仍需在系统输入源中添加 Chaoset 的说明。
- **Linux 安装器清理**：Arch post_install 不再重启所有登录用户的 ibus（避免打断正在
  打字的人）；tar 安装不再写 `/usr/local/bin` 重复符号链接，只保留 `/usr/bin`。

## v0.3.3（2026-11-01）

- **macOS 输入法改为自包含 app 安装（用户要求）**：`ChaosetIME.app/Contents/Resources/`
  内置 `chaosetd`、`chaoset-import`、`dict_common.tsv`，LaunchAgent 指向 app 内
  daemon；`~/Library/Application Support/chaoset` 只保留用户数据/当前词库/socket/日志。
  安装器不再把 daemon 单独拷到 Application Support，并把自己装到
  `~/Applications/Chaoset.app`。新增 app 资源、签名、等价性与 universal 自检。

## v0.3.2（2026-11-01）

- **修复 release 产物版本号**：v0.3.1 的 macOS app 因 release 构建步骤
  未显式注入 tag 版本，Info.plist 写成了 `0.0.0-dev`，用户无法判断是否更新。
  release workflow 现在从 `github.ref_name` 注入 `VERSION`，并在构建后断言
  ChaosetIME/ChaosetSetup 的 `CFBundleShortVersionString` 必须等于 tag；
  DMG 命名/卷名也使用同一版本。

## v0.3.1（2026-11-01）

- **macOS 安装器修复**：GUI 安装器现在自己写 daemon 的 LaunchAgent（指向
  `~/Library/Application Support/chaoset/chaosetd`），不再从 DMG 里直接启动
  `chaosetd`——此前 daemon 占着 DMG 导致推出时提示「文件正在使用」，且重启后
  没有进程被拉起。安装器状态页改用真实 IPC 连接探活，不再把僵尸 socket 当运行中。
- **版本可见性（三端统一）**：设置窗底部显示 `Chaoset 版本 x.y.z`；macOS Setup
  状态/窗口显示版本与已安装输入法版本；DMG 文件名改为 `Chaoset-<版本>.dmg`、
  卷名带版本；Windows/Linux 设置窗同步显示版本。
- **联想回归测试**：新增 macOS `EngineClient + 真实 daemon` 的 associate 端到端
  测试（输入词组→选词上屏→续接候选），并接入 macOS CI。

- **设置窗无头冒烟核对窗口几何**：CI 安装 Noto CJK + xdotool，Xvfb 下启动
  `chaoset-setup` 并断言窗口已映射、尺寸合理（缺 CJK 字体会退化成 32767 高）。
- **README 去 changelog 化**：历史阶段记录迁入本文件，README 只保留当前文档。

## v0.3.0（2026-11-01）

- **Linux 设置窗迁原生 GTK3 C，彻底移除 Python/PyGObject**：删除 5 个 `.py`；
  Makefile/deb/tar/Arch/`install.sh`/component XML/CI/release/checkuiparity
  全部改到 `chaoset-setup` 二进制与 `setup_ctl.c`；依赖改为 `libgtk-3`/`gtk3`。
- **Windows/Go 工具命名管道**：`chaosetd -pipe`、C++ 壳命名管道优先/TCP 回退、
  `chaoset-cli`/`chaoset-userdict -pipe`；安装器切换与真机验收仍待 Windows 环境。
- **`internal/appdirs` 统一路径**：daemon/tools/Linux shim 共用 socket、用户词典、
  配置路径；Linux socket 优先 `$XDG_RUNTIME_DIR`。
- **Linux CI 无头启动冒烟**：Xvfb 下验证 GTK 初始化/窗口构建；设置窗 C 单测覆盖
  配置往返与保留未知键。
- 发布资产：Linux tar.gz/deb/Arch、macOS zip/dmg、Windows zip/Setup.exe。

## 历史路线图与验收结论

- [x] 阶段 0：引擎 + 终端测试台 + IPC 骨架
- [x] 阶段 1：macOS IMK 壳（编译 + IPC 运行时验证通过；真机启用见
      shims/macos/README。**bundle 有效时无需注销重登**，装好并 `TISRegisterInputSource`
      后即可在系统设置里添加——早期「必须注销重登」的结论已更正）
- [x] 阶段 2：用户词典 + 自动调频增强（时间衰减、bigram、词组进化）
- [x] 阶段 3：热词导入管道（Rime dict.yaml 解析、chaoset-import 合并）
- [x] 阶段 4 主体：三端壳全部实现 + 统一候选窗
      - Windows TSF 壳：已实现——核心层行为级验证（真实 daemon TCP 17 项）、
        DLL mingw 交叉编译级验证；真机联调（COM 注册/按键/渲染）待 Windows 环境
      - Linux ibus 壳：已实现——Go 引擎（godbus），容器内端到端验证通过
        （组件注册/激活/按键序列处理/CommitText 中文上屏信号）；
        真机桌面闭环待 Linux 桌面环境
      - 统一候选窗：已实现——daemon 侧渲染（renderer + candidateImage，
        样例人工目检通过）+ macOS 壳贴图面板（编译级验证，回退 IMKCandidates）；
        布局与皮肤统一由 daemon 的 `renderer.Skin` 决定：默认横排候选栏 + 圆角 +
        系统浅/深色配色，壳只按 `candidateImage` 的 `dark`/`scale` 传参（见阶段 20）；
        Windows 壳贴图待真机环境。（**v0.2.0 起 macOS 改为壳内原生自绘候选窗**，
        见阶段 24；**2026-10-27 起贴图渲染链路整体删除**——macOS/Windows 壳为
        原生自绘、Linux 用 ibus 原生面板，视觉参数保留在 `skin/` 包，见阶段 27）
- [x] 阶段 5：中文标点——引擎侧统一映射（Session.Punct + IPC punct），
      CLI 与三端壳转发接入
- [x] 阶段 6：候选翻页——引擎内分页（Composition.page/page_count，
      每页 6 个，2026-10-25 由 9 调整：数字键 1-6 全在标准指位内且候选行
      不易换行）+ IPC nextPage/prevPage，CLI 与三端壳（- / =）接入
- [x] 阶段 7：候选可视化收尾——渲染器页码指示（竖排布局并进预编辑行；
      横排布局改为末尾一项 `‹1/5›` 的淡色文字，因为横排不再绘制预编辑行）；
      ibus 壳 UpdateLookupTable 候选表上报（容器内端到端验证，数字键选择不再盲选）
- [x] 阶段 8：中英切换——Shift 空按切换，英文模式全透传；ibus 壳
      容器内端到端验证（含切回恢复），Windows 编译级，macOS 修饰键
      事件路由待真机验证
- [x] 阶段 9：标点循环（句号/逗号同键连按全角⇄半角，引擎侧）+
      ibus Page Up/Down 键盘翻页（容器内端到端验证）
- [x] 阶段 10：模糊音——声母/韵母等价展开查询（zh/z、ch/c、sh/s、
      n/l、an/ang、en/eng、in/ing），默认关闭 -fuzzy 开启
- [x] 阶段 11：全角/半角标点——Shift+空格切换，半角下标点原样输出
      （引擎侧，组词合并行为不变）；ibus 容器验证路径同上
- [x] 阶段 12：简繁转换——零错误单字映射（歧义字保留原形），
      候选与上屏整体转繁体，默认关闭 -traditional 开启
- [x] 阶段 13：双拼——Rime algebra 忠实转写管线 + 全音节反向键码表
      （完整性测试兜底），流式两键解码；方案 zrm（自然码）与 mspy
      （微软），默认关闭 -shuangpin 开启；小鹤后于阶段 18 落地
- [x] 阶段 19：中国风视觉（**已被阶段 20 取代**）——当时应用图标取中国印章语汇、
      候选窗默认皮肤取中国传统色（宣纸底 #F7F2E7、朱红外框 + 米金细内框、浓墨候选、
      淡墨预编辑、朱红首选高亮 + 「朱批」竖记、石青学习标记）。**当前默认皮肤是系统风格的
      横排候选栏**（见阶段 20）。宣纸/朱红观感**不再是任何内置默认**；该观感的
      绘制代码已随 renderer 包删除（2026-10-27，见阶段 27），如需恢复参考 git 历史；
      输入源图标也已由「印章」改为与系统输入法一致的扁平 keycap。
- [x] 阶段 14：CC-CEDICT 开源词库直连导入（约 12 万词条，自动识别
      格式、声调数字/u: 记法归一、非汉字过滤、合成词频让用户词库优先）
- [x] 阶段 15：规模化验证与基准固化——10 万级词库加载 42ms、组词
      0.36ms/键、前缀扫描 0.34ms/键（Apple M1 基准入库）；真实 120k
      词库 daemon 全链路实测通过
- [x] 阶段 16：简繁转换升级——内嵌 OpenCC 官方数据（STCharacters
      4,029 单字 + STPhrases 49,270 词级，Apache-2.0），词级最长匹配
      消歧（头发→頭髮、出发→出發、公里→公里），惰性解析
- [x] 阶段 17：ibus 引擎多实例——工厂按 InputContext 创建独立实例
      （独立会话/中英模式/Shift 跟踪），容器内端到端验证状态分离
      （两实例交错打字各自上屏 你好 / 好）
- [x] 阶段 18：小鹤双拼（flypy）——依 Rime double_pinyin_flypy
      schema 键位接入三方案体系，已知键位 25 项抽查全过
- [x] 阶段 23：错拼容忍（手滑修正）
      - 相邻颠倒（zhogn）、多敲（zhonng / niihao）、漏敲（zhonguo / jisuanjii）
        这些手滑输入，旧版要么给碎片假词要么无候选。现在做「单次编辑」修正后
        重查，24 个典型场景中 13 个修复为正确首选；两次以上编辑不做（那时已不是
        手滑而是不会打）。
      - **绝不污染正常输入**：手误与合法混拼在字形上高度同形（nihap vs chifanl），
        因此触发条件收紧为「连简拼路径都排不出来」，且纠错候选的词频必须达到
        常规单词级候选的 2 倍才提升到首位。实测直接放开会让混拼首选率从 34.32%
        掉到 20.75%。正常输入各项指标零回归，性能零额外开销（22.0µs/键）。
      - 已知漏判：nihap 能被切成 ni|ha|p 因而不修正——与合法写法 ni'ha'p 同形，
        强行纠错会误伤所有混拼。
- [x] 阶段 24：原生候选窗 + 键帽序号 + 纠错/联想排序修复（v0.2.0）
      - **macOS 候选窗切原生（AppKit 自绘）**：替代 daemon PNG 贴图——消除
        跨进程像素管线（渲染+PNG+base64+JSON+socket ≈5ms/帧 @Retina），
        深浅色用 NSColor 动态色自动跟随，补齐贴图方案两大缺口：鼠标点选
        候选（原 ignoresMouseEvents=true 无法点选）与 VoiceOver 读取（原
        无任何无障碍标注）。Windows/Linux 壳当时继续贴图；candidateImage 与
        renderer 已于 2026-10-27 删除（macOS/Windows 原生自绘、Linux 用 ibus
        面板；视觉参数迁至 skin/ 包）。
      - **序号键帽视觉**：候选序号收进 keycap（浅色白底黑边深黑数字、
        深色 #5A5A5E 底亮边白字、首选蓝胶囊内半透白键帽），数字字号 80%；
        修复深色键帽配色随 WithDarkMode 漏拷失效（改反射同步全部配色字段）。
      - **空格选词**：改 keyCode 49 判定并前置（部分客户端 characters 对
        空格失灵导致漏选词、空格漏进文档）；组词中空格一律消费——有候选
        选首选，无候选顶屏原始拼音。
      - **纠错与联想排序**：zhengchagn 等相邻颠倒错拼现在纠错出「正常」
        （新增中间孤立声母形态触发 + 领跑分母只采信全消耗精确词）；
        xianzai 的用户词典联想词（现在时间）不再压过全消耗精确词「现在」
        （音节超段仲裁）。基线对比见「错拼修正」与 .workwork/verify/scan
        （full 零回归、tail1 +1.30、mixed -1.16 为中间声母形态改由高频
        纠错词领跑的预期代价）。
      - **渲染性能**：None 滤波快速 PNG 编码（-63%~-67%，产物更小）+
        圆角遮罩缓存；候选帧 @Retina 6ms → 3ms（贴图端收益，Win/Linux
        与 macOS 回退路径受益）。
      - **排序金标准**：段数匹配三态中的「全消耗优先」位于调频之前——
        单音节输入 hao 首选恢复为「好」、多段输入 yicha/sxj/ld 首选恢复
        为等长词组（遗产/数学家/领导），学习词组与单字不再越位；
        五场景首选率较起点全部提升（full 80.82/tail1 48.56/tail2 32.12/
        initials 20.55/mixed 39.11）。
      - [x] 阶段 26：QWERTY 物理键邻近纠错 + 拼音习惯与粘键容错（v0.2.2）
      - **QWERTY 物理键邻近手误建模**：建立 26 键标准错行错位布局物理邻键映射，手指按偏替换优先占满纠错名额；跨键区远端替换扣除惩罚分，防止远端巧合高频词反客为主（如 yishrng 纠错「以上」不再压过「一声/医生」）。
      - **末位声母手滑纠错**：引入 tailSuspicious 判定与 typoLen >= len(input) 防截断护栏，解决 nihap 出「你哈珀」痛点直接命中「你好」，同时零误伤 zhongg（中国）、chifanl（吃饭了）、shangxiaj（上下级）。
      - **拼音省写与撮口呼习惯归一**：uei->ui（dueibuqi->对不起、shueijiao->睡觉）、iou->iu（niourou->牛肉）、uen->un（chuentian->春天）、j/q/x/y+v->u（jvece->决策、xvesheng->学生）、xion/jion/qion 漏 g 补齐。
      - **连击粘键消除（Stutter Reduction）**：连续重复字母手误折叠（niihaoo->nihao、zhoonng->zhong）。
- [x] 阶段 25：联想机制 + 部分消耗降级 + 用户词典净化 + 排序金标准（v0.2.1）
      - **联想机制（v0.2.1）**：选词上屏后拼音区清空，候选窗继续展示
        以该词开头的续接词组，数字键 1-6 直接连选上屏（学习路径与普通
        选词一致）；字母键回到正常组词、Esc/切中英/标点立即收起联想窗。
        联想候选按词频降序 + Bigram 上下文加成（常打的联想词排前）。
      - **部分消耗降级（v0.2.1）**：打满多段的输入，只消耗部分段数的
        单字/短词（rankExact/rankInitialExact）排到全消耗词组之后——
        yicha 首选从「依」变为 2 音节词组、sxj 首选从「事」变为简拼
        词组；纠错与续接/回退层级不适用此键（各有独立门槛）。
      - **用户词典净化**：words 中 278 条不在静态词典的碎片假词清理
        （旧排序 bug 让碎片组合置顶、用户被迫选词形成调频污染的存量）。
- [x] 阶段 22：输入预测与拼音匹配强化
      - **末音节续接预测**：打字时每个音节都必然经过「打了一半」的状态
        （zhonggu / beijin / daxu / quanmia），旧版这些输入只能给出巧合碎词
        （中古 / 被进 / 大需 / 全米啊），目标词完全不可达。现在把末段当作待补全
        音节查词典续接，实测末音节残缺命中率 43.84% → **98.63%**、首选率
        13.49% → **43.42%**，而全拼完整输入首选率保持 80.68%（改动前 80.75%）。
        排序不靠特判：完整解析与续接解析同层竞争，由「词频 − 30×补全字符数」
        决出（详见「输入预测（末音节续接）」）。
      - **ü 输入别名**：用户敲 lue / nue 现在能打出「略 / 虐」（旧版只能出
        「路鄂 / 怒鄂」），音节表仍以 v 记写，别名只在输入侧归一。
      - **用户词库查询键 bug**：`QueryTokensExact` / `QueryTokens` 解析用户记录
        时用 `\t` 拆 `词\x00音节` 键，拆分恒失败导致整个循环 continue——
        被调频过的单字/词在简拼与整句路径里永远查不到（只有词组生效）。
        修复后简拼 `dw` 能按调频优先出「单位」。
      - **整句 DP 分数归一化**：用户调频分（次数×500）此前被当作静态词频再乘
        音节数放大，高频学过的单字会霸榜、把完整词挤成碎字拼接（`wmd` 出
        「我没的」而不是「我们的」）。现在用户词条以固定基础分参与规划，
        真实权重仍由上下文加成计算。
- [x] 阶段 21：输入体验修复（用户真机反馈）
      - **末段打错仍能出候选**：此前多打一个对不上的字母就整串「无候选」
        （nihaox / womenx / zhongguoo 全空，观感是输入法坏了）。现在按匹配层级
        排序：完全匹配 > 末段续接 > 偏长前缀 > **回退到已完整的前缀**，
        所以 nihaox 仍能打出「你好」。同时加了 Trie 根保护——无法切分的
        乱码（zzz）曾因 `Query([])` 从根节点返回整本词库，现在正确报无候选。
      - **退格不再删掉已上屏内容**：壳侧退格改为「有组词才消费、无组词放行」，
        且 `setMarkedText` 的 selectionRange 从「全选」改为「光标在末尾」、
        组词结束时用 `insertText("")` 收掉 inline session。此前无条件吞掉退格键，
        既删不动文档里的字，又可能在收尾时连带替换掉相邻已上屏文本。
      - **输入源图标与系统一致**：改为 16×16+32×32 双帧、**ExtraSamples=2
        （非预乘 alpha）**、RGB 恒 0、单字符「C」字形，并用 8× 超采样后降采样
        生成（16px 直接画字体笔画会断裂）。此前用 CGImageDestination 写多帧
        得到 ExtraSamples=1，系统按模板渲染时 alpha 计算不同，表现为颜色/明暗
        与 ABC、简体拼音不一致、hover 也不跟随；字形用「Cs」两字符则让图标
        看起来比系统图标更胖更小。
- [x] 阶段 20：可用性修复（本轮 5 项）
      - **内置常用词库**：默认词库由 `data/dict_seed.tsv` 换成 `data/dict_common.tsv`，
        解决「jingchang 打不出经常」与生僻字淹没常用字的问题；新增 `cmd/chaoset-dictgen`
        可从 jieba 词频表 + 词语拼音表再生成（规则见「内置词库与再生成」）。
      - **候选排序**：优先级依次为 ① 用户数据加权（调频 / 上下文共现）
        ② 音节数与输入完全一致 ③ 得分 ④ 词长 ⑤ 字典序。② 排在 ③ 之前是有意的：
        单音节输入 `jing` 先出单字「经」而不是词频更高的「经济」——打满几个音节就
        优先给同等长度的词，长词退到后面；而用户选过的词（①）始终压过 ②③。
      - **候选窗改系统风格**：横排候选栏 + 圆角 + 系统浅/深色皮肤（`Skin.Horizontal` /
        `MaxWidth` / `CornerRadius`、`renderer.DefaultSkinFor(dark)`）；
        `candidateImage` 新增可选 `dark`/`scale` 参数支持深色与 Retina；
        横排模式不再绘制预编辑行（预编辑由壳的 marked text 承担）。
      - **macOS 壳**：候选窗改为跟随光标定位（不再贴鼠标，下方空间不足时翻到上方），
        输入源图标改为对齐系统规格的 16×16 + 32×32 双帧 TIFF，显示名统一为 `Chaoset`。
      - **仓库卫生**：`.gitignore` 忽略 `.DS_Store`；`go.mod` 把
        `github.com/godbus/dbus/v5` 归正为直接依赖（`shims/linux/ibus-engine` 直接 import）。
- [x] 阶段 27：删除 daemon 贴图渲染链路（2026-10-27）
      - **为什么删**：macOS 壳自阶段 24 起 AppKit 原生自绘、Windows 壳 GDI 自绘、
        Linux 壳用 ibus 原生面板——`renderer` 包（约 1900 行含测试）与
        `candidateImage` IPC 在三端壳中均无调用方，daemon 每次启动还在探测
        CJK 字体并打印「candidateImage 渲染已启用」，纯属死代码与误导性日志。
      - **删了什么**：`renderer/` 整目录、IPC `candidateImage` 方法与服务端
        `WithSkin`、客户端 `CandidateImage` 方法族、daemon 的 `-font`/`-fontsize`
        参数、macOS `EngineClient.candidateImage`（Swift 侧同样零调用）。
      - **留了什么**：视觉参数（几何 + 浅/深配色）迁至新包 `skin/`
        （`DefaultSkinFor(dark)`，去掉只服务渲染的 `FontPath`/`Horizontal` 字段），
        作为**设计基准**继续服务 `tools/render_metrics.go` 与
        `tests/visual-parity.sh`（2026-10-30 起：Go↔macOS 布局 5 项 + macOS
        参考常量 + 浅/深各 13 色逐项断言；**不覆盖 Windows**，Windows 无运行时
        视觉断言——见阶段 29 与 docs/ui-parity.md）。
        后续若做「构建期由 skin 包生成 Swift 常量」（单一真源），生成源即此包。

- [x] 阶段 28：整句输入习惯 + 整词贪婪防护（2026-10-30）
      - **背景（用户需求）**：①核查三端 UI 一致性；②核查「多字拼音没有合适
        整体候选时可逐段选择」这条链路是否完备；③逐段选择的过程要进入用户
        输入习惯学习，后续完整输入时更符合用户想打的词组；④**不能整词贪婪
        匹配**——学过 今天星期一 之后，打 今 不能直接给候选 天星期一，否则用户
        只想打 今天 时会被整词候选/联想尾巴挤掉（原话「不能直接就匹配整个词」）。
      - **现状探针（HEAD b4785e6，改动前实测）**：逐段选择引擎侧已完备
        （SelectPrefix 只消耗候选音节前缀、剩余回灌；三端键盘/空格已接），
        每段也已进学习（Words+Bigram，同串复用被顶前）；但 5 音节以上的组合
        **学不到**（maxLearnSylls=4 只约束 Phrases 表），而「整句候选一键上屏」
        会把任意长整句写进**不带分段**的 Words 表 → 之后打 jin 出现整句候选
        （#59，页 10）、上屏「今」的联想首条直接是**天星期一**（score 500，
        第 2 条才是「天」70）；该尾巴还能被再次上屏学习（AssociateSelect），
        之后 tian 下「天星期一」#40 反超「天气」#46——学习污染循环成立。
      - **本次修复（engine）**：Words/Phrases 学习记录携带**分段**（`Segs`，
        旧数据无分段时按主词典「分段数最少」DP 推断，见下方边界加固 ②）；
        整句学习（`BumpWordWithSegs`）
        只在**完整输入**时作为整句候选参与调频加权，**不参与部分输入的前缀
        候选**；联想剥前缀只允许在用户确认过的分段边界发生（今天→星期一 ✓、
        今→天星期一 ✗）；逐段选择整串耗尽时把整句固化成带分段的整句习惯
        （≤12 音节，超过视为误触不学）；「一键整句上屏」按逐段确认的等价路径
        再学一遍分段词与共现，两种上屏路径学习数据一致；顺带修复撇号输入
        （jin'tian'…）下渐进失效（比较忽略 '，剩余串不以 ' 开头），
        `engine.Controller`/IPC Client 补齐 SelectPrefix，CLI 数字键走渐进
        上屏（本地测试台可验证渐进链路）。
      - **边界加固（复检发现的两个边界 bug，同日修）**：① 渐进段累积
        （seqSegs）此前只在 Reset/Select/AssociateSelect 清理，退格到空或标点
        丢弃残缺输入后重新组词，会把上一轮已上屏段拼成伪整句（实测「今天」→
        退格到空→打 wo 选「我」→学出「今天我 segs=[今天 我]」）；现在
        Input/Backspace/BackspaceWord/Punct 在组词被清空时一律作废未完成分段。
        ② 旧数据分段推断此前用「最长词贪心」，会被词典长词带偏（无 Segs 的
        「天星期一」被切成 [天星,期,一]，联想上屏「天」后丢失「星期一」、错误
        分段词「天星」污染候选），改为「分段数最少」DP 后恢复 [天,星期一]。
      - **双向回归**：engine/sequence_learn_test.go —— 正向（完整输入出带
        FromUser 的整句、今天→星期一/天气 续接保留、旧数据分段推断生效、
        撇号输入可渐进）；反向（jin/jintian 不出现整句候选、今 的联想没有
        天星期一/天天气、只想输入今天时 今天 仍 #1、退格清空后不拼伪整句、
        旧数据尾巴学到的分段必须正确）。
      - **兼容性/边界口径（有意为之）**：① 末音节残缺或多打尾字母
        （jintianxingqiy、jintianxingqiyix）仍给整句候选——这是续接预测形态，
        选择时只消耗已覆盖音节、剩余字母保留（与渐进上屏一致），不属于
        「部分输入整词匹配」；② 旧数据里「不在词典的多字词」一律按整句式处理
        （带推断分段），不再作为部分输入的前缀候选——完整输入与分段边界续接
        不受影响，是本次安全规范的兼容性权衡。
      - **五场景基线**：改动前后 full/tail1/tail2/initials/mixed 对比见提交
        说明与 `.workwork/verify/delta-2026-10-30.md`（gitignore，扫描口径见
        pinyin-quality-tuning skill）。

- [x] 阶段 29：三端 UI 一致性审计与整改（2026-10-30）
      - **审计（只读）**：对照 36 项三端 UI 行为/参数，发现 **17 项不一致**
        （12 项一致、4 项已文档化平台差异、3 项需真机），核心问题：①Windows
        候选窗单位口径混用——字号按 pt（1.0×）、间距/键帽/圆角/最大宽按 px@96
        （0.75×），同一面板两套换算基准；②Linux 设置窗口关「词组联想」不生效
        （引擎只在启动时读一次 config）；③学习标记 `*` 只有 macOS 渲染，
        Windows IPC 结构根本没解析 `from_user`；④Windows 鼠标点选仍走非渐进
        `select`（长串下整串清空）；⑤联想态 Shift+空格实际三端都是「选第一个
        联想词」，与文档/注释冲突；⑥机器检查名不副实（visual-parity 只比 5 个
        标量、Swift 色值从未参与、equivalence 却宣称「布局与色值两段一致」）。
      - **整改**：Windows 候选窗布局全部统一 pt 口径（`pt(v)=MulDiv(v,dpi,72)`）
        并修正 maxContent 漏扣 gear+pill（窗宽 ≤714pt<720）、minW 84pt；Windows
        学习标记全链路补 `from_user`（core.hpp/core.cpp/win32.cpp 解析 + 绘制，
        几何/色值与 mac 一致）；Linux 联想开关改为每次进联想前重读 config.json
        （与 mac/Win 每次选词前重读同语义，保存即生效）+ 3 个测试；Windows 鼠标
        点选改走 selectPrefix 渐进上屏；小键盘数字选词、联想态 Shift+PageUp/Down
        对齐；P1 视觉细节（深色派生色 alpha 有效值、页码居中、分隔线口径、单页
        箭头置灰、Toast 位置/点击穿透、设置窗字号几何）一并修正。
      - **机器检查**：visual-parity.sh 断言 5 → 46 条（另有 2 条「导出成功」提示；新增 macOS 参考常量 C 段 +
        真正消费 divider、浅/深各 13 色 Go↔Swift D 段；负向验证可 FAIL）；
        skin 补 divider/chevron 与深色 13 色期望值（加强非降低）；docs/ui-parity.md
        按代码全量重写并明确未覆盖项（Windows 无运行时视觉断言、key_routing 未接
        CI、无 checkuiparity）。平台限制与待真机项（per-monitor DPI、Windows
        设置窗深色、多行折行、半透明/阴影、无障碍、滚轮）写入 docs「已知功能差异」；
        按键边角 3 处与 Linux 设置窗单例已于 2026-10-31 解决（见阶段 48/49/50）。
      - **验证**：本机 gofmt/build/go test ./.../make vet/visual-parity 46 条断言（+2 条导出提示）/
        shims/windows/build.sh（core_test 含 from_user 断言 + mingw 交叉编译）
        全过；重活交 CI。真机验收清单见 docs/ui-parity.md。

- [x] 阶段 30：审计后 P1 修复批次（2026-10-30）
      - **触发**：阶段 29 之后的三端 UI 一致性复审 + 引擎功能完整性审计（4 路只读审计，
        共 P0=0 / P1=15 / P2 若干）。除 Windows TSF 两阶段语义（需 Windows 真机
        确认，已记入已知限制）外，P1 清单全部修复：
      - **双拼 `;` 键（mspy ing）全链路打通**：引擎 `Session.Input` 双拼模式接受 `;`
        （dpPending 参与 pair 解码；否则按标点语义处理），`Composition` 新增 `shuangpin`
        字段供壳识别，三端壳在双拼模式把 `;` 路由到 `input` 并上屏 `Committed`。
      - **渐进上屏丢字**：`SelectPrefix` 前缀消费改按 `canonicalSyllable`/模糊等价逐音节
        匹配（`xveshengwomen` 选「学生」不再清空剩余 `women`）；并补 alias/fuzzy 双向回归。
      - **单一预编辑模型收尾统一**：Linux 的 Ctrl+Backspace/Ctrl+U、Shift 中英切换、
        退格删空、空格无候选四条路径补齐「取消/落盘确认段」；Windows 的 Alt/Win 透传、
        Shift 切换、空格无候选、翻页失败同样改为落盘；mac 本就正确。
      - **Windows 壳**：设置窗不再保存裸 TIP 指针（消除 UAF）；描边线宽改 pt 口径；
        `Comp` 解析 `shuangpin` 并支持双拼 `;`。
      - **macOS 壳**：首项超长候选按字符截断加「…」，面板内容宽恒 ≤638pt、窗宽 ≤714pt，
        不再越出屏幕（与 Windows 策略对齐）。
      - **IPC/持久化**：`userExport` 分页（>1MiB 学习数据可导出）；用户词典写盘串行化 +
        唯一临时文件（消除 clear 被旧快照覆盖/写坏的竞态）；Linux ibus 引擎启用延迟落盘
        （不再每次选词全量重写）；daemon 关停先停 ticker 再落盘。
      - **Linux 打包**：Arch 二进制包补装 `chaoset-setup.py`（ibus-setup「首选项」不再失效），
        release 冒烟对 deb/tar.gz/Arch 三个包都断言该文件存在。
      - **文档**：本轮同步修正 README/AGENTS/docs/ui-parity 与各端 README 的过期宣称
        （词库规模/再生成命令 `-extra`/`.Pinyin`/release workflow/IPC 方法表/Linux page_size 等）。
      - **未修（明确记录）**：Windows TSF 两阶段下 Shift 跟踪可达性（需真机确认，见
        `shims/windows/README.md`「已知限制」）；双拼 `;` 的标点宽度 1 键瞬态
        （记入各端 README）。Option/Alt+Backspace 三端语义已于 2026-10-31 统一为
        按音节回退（见阶段 46）。

- [x] 阶段 31：Windows 真机首轮验证 + 审计 A 批收尾（2026-10-30/31）
      - **触发**：v0.2.17 发布后用户 Windows 11 真机反馈「开始菜单/搜索框候选窗
        不显示」。真机定位到两层根因：候选窗 owner 恒为 nullptr（无主 topmost
        窗口被沉浸式 z 序 band 压住）+ 安装器硬编码进程名漏杀 SearchApp.exe
        （旧 DLL 一直活着），另有 UILess 类别声明与实现矛盾。修复后真机确认恢复。
      - **本轮 follow-up（审计 A 批）**：候选窗 owner 生命周期回收（owner 连带
        销毁时清句柄 + 归还 DLL 防卸载计数，此前会永久泄漏计数）、ModeToast 补
        owner/防卸载计数、便携 ZIP 升级路径清理旧 UILess 类别并按模块杀宿主
        （此前只有 NSIS 安装器做了）、NSIS 改用 Sysnative 64 位 PowerShell 枚举
        64 位进程模块、全屏免打扰（D3D 全屏/演示模式抑制候选窗，注册表可关）、
        Shift 跟踪一次性诊断日志、CI 新增 windows-build job（日常提交即编译
        Windows C++/NSIS，不再等到打 tag）。
      - **验证**：本机 gofmt/build/go test ./.../make vet/shims/windows/build.sh
        （core_test + mingw 交叉编译）全过；CI 新增 job 随推送验证。
      - **未修（明确记录）**：Windows TSF 两阶段 Shift 跟踪仍待真机确认（已加
        tsf.log 诊断，见 shims/windows/README.md）；无边框全屏不触发全屏抑制；
        游戏内 WASD 被组词是 IME 固有行为（游戏前切英文模式）。

- [x] 阶段 32：遗留 P2 批次（2026-10-31）
      - **Windows 中英切换（P1 级风险）**：Shift 空按跟踪从 `OnKeyDown/OnKeyUp`
        移到 `OnTestKeyDown/OnTestKeyUp`——TSF 两阶段模型下，Test 阶段返回 FALSE
        的按键不会再进 Key 阶段，原实现可能永远收不到修饰键（中英切换失效）。
        「非 Shift 键取消空按窗口」同步移到 Test 阶段（Shift+方向键不再误切换）。
        诊断日志保留，真机按一次 Shift 即可确认。
      - **Windows 主题切换重绘**：`WM_SETTINGCHANGE`/`WM_THEMECHANGED` 立即失效
        重绘候选窗与模式提示条，不再保持旧配色直到下次输入。
      - **命令行工具**：`chaoset-cli`/`chaoset-userdict` 新增 `-tcp`（Windows 壳
        同款回环形态，Windows 上 CLI 从此可连 daemon）；四个工具新增 `-version`
        （读 Go 构建信息里的 VCS 提交号，无需打包注入，真机排障先确认新构建）。
      - **安全/健壮性**：Unix 套接字权限显式 0600（同机其它用户不能再连 daemon
        旁听键盘输入）；IPC 单连接 panic 隔离（一个畸形请求不再带走整个 daemon）。
      - **macOS**：LaunchAgent 的 daemon 参数补 `-log`（launchd 下日志落
        `~/Library/Application Support/chaoset/chaosetd.log`，此前日志丢失）。
      - **仓库卫生**：`.gitignore` 补 `*.sock.lock` 与 `userdict.json.corrupt-*`。
      - **三端一致性复核补记（2026-10-31）**：① Shift 空按的取消条件已对齐——
        mac（flagsChanged 清 shiftArmed）/Linux（任何非 Shift 事件清 sinceShift）
        都会在 Ctrl/Alt/Win 按下或释放时取消空按窗口，Windows 此前漏了这一步
        （Shift→Ctrl→松 Shift 会误切换中英），本轮补齐并用 Linux 单测锁语义；
        ② Windows TCP 回环没有 Unix socket 权限等价物（mac/Linux 已 0600，
        同机其它用户不可连；Windows 本机任意用户进程仍可连 daemon），命名管道
        + ACL 是后续方案；③ `-version` 覆盖四个 Go 工具（daemon/cli/userdict/
        import），shell 层版本仍看包信息（mac Info.plist / Windows DLL 暂无
        VERSIONINFO 资源）；④ Linux 进程内引擎不经过 daemon 的 IPC panic 隔离
        （引擎 panic 会重启 ibus 引擎进程，会话状态丢失；mac/Windows 走 daemon
        的隔离路径）；⑤ 随包工具集——Linux 包含 daemon/cli/import/userdict/
        engine，Windows 包含 daemon/cli/import/userdict（2026-10-31 补 userdict，
        与 Linux 对齐；`-tcp` 可直接用），mac 包含 daemon/import（GUI 内嵌，
        CLI 工具仍从源码构建）。
      - **未做（明确记录）**：滚轮翻页在 Windows 不可行（`WM_MOUSEWHEEL` 只发给
        焦点窗口，候选窗 `WS_EX_NOACTIVATE` 永不获得焦点；要接收需全局鼠标钩子，
        与用户「游戏不干扰鼠标」的硬要求冲突）；无障碍 UIA、per-monitor DPI、
        设置窗深色、多行折行仍记录在 docs/ui-parity 与各端 README；
        Option/Alt+Backspace 三端语义已于 2026-10-31 统一（见阶段 46）。

- [x] 阶段 33：双拼 `;` 标点宽度瞬态修复 + Windows 工具集对齐（2026-10-31）
      - **双拼 `;` 半角瞬态（三端一致性问题）**：全/半角开关归壳本地持有，此前
        壳只在 punct 请求里下发 `half`；双拼模式 `;` 经 input 路径处理
        （dpPending==0 时按 Punct 语义上屏），引擎用的是上一次 punct 留下的旧
        状态 → 刚切全/半角后单按 `;` 可能按旧宽度出标点。现在 `input` 新增可选
        `half` 字段，三端壳每次按键下发全/半角状态（引擎 `InputWithHalfWidth`
        先同步再处理）；旧壳省略字段时保持会话内旧状态（协议向后兼容）。
      - **Windows 工具集对齐**：`chaoset-userdict.exe` 随 Windows zip 发布（此前
        只有 Linux 包带该工具，Windows 用户需源码构建才能用 `-tcp` 管理用户
        词典）；release 包内容自检同步断言该文件存在。
      - **测试**：engine 新增 `TestDoublePinyinSemicolonHalfWidth`；IPC 新增
        `TestServerInputHalfWidth`（含旧客户端省略 half 的兼容路径）；Linux 新增
        `TestDoublePinyinSemicolonHalfWidth`（壳侧路由 + 引擎状态联动）。
      - **文档**：README IPC 协议补 `half` 字段；ui-parity 双拼 `;` 行更新；
        mac/windows README 的「已知限制」改为「已修复」说明。

- [x] 阶段 34：学习数据管理搬进 GUI（2026-10-31）
      - **产品原则**：输入法面向普通用户，能走 GUI 的操作不走 CLI。学习数据
        的统计/清空/导出此前只有 `chaoset-userdict` 命令行；现在 macOS 设置窗与
        Windows 设置窗都内置「学习数据」区（统计行 + 清空 + 导出 TSV），经同一
        IPC 通道作用于 daemon（内存与磁盘一次生效，无覆盖竞态）。
      - **macOS**：`EngineClient` 新增 `userStats`/`userClear`/`userExportAll`
        （分页拼接），`SettingsWindow.swift` 增加学习数据区（清空二次确认、导出用
        NSSavePanel）；窗口 380×330pt。
      - **Windows**：`core::Session` 新增同名方法，设置窗口用独立 `TcpTransport`
        连 daemon（不持有 TIP 指针，UAF 防线不变），导出用 `GetSaveFileNameW`；
        窗口 380×330pt 与 mac 对齐。
      - **CLI 定位**：`chaoset-userdict` 保留为高级/排障/无 GUI 环境兜底，文档不再
        作为普通用户路径；Linux 因引擎在 ibus 进程内、无 IPC，GUI 管理学习数据
        需要引擎侧 DBus 接口（后续），当前仍用 CLI。
      - **测试**：mac smoke 增加 `userStats`/`userExportAll` 只读检查（不调用
        `userClear`，避免安装自检误清真实学习数据）；Windows 编译级 + core 行为
        测试；Go 侧无协议改动。

- [x] 阶段 35：Windows 设置窗深色 + per-monitor DPI（2026-10-31，编译级待真机）
      - **设置窗深色跟随系统**：DWM 深色标题栏（动态加载 dwmapi，Win10 1903+
        属性 20、1809 回退 19）+ 自绘背景/文字/复选框/按钮；`WM_SETTINGCHANGE`/
        `WM_THEMECHANGED` 即时重绘。分组标题从 BS_GROUPBOX 改为加粗 Static（与
        mac 的 bold label 同构，避免 GroupBox 在深色下留下浅色边框）；复选框/
        按钮改 BS_OWNERDRAW 自绘（标准按钮不随系统深色自动重绘）。
      - **per-monitor DPI**：新增 `dpiForPoint`（动态加载 shcore!GetDpiForMonitor，
        旧系统回退主屏）与三处 `WM_DPICHANGED` 处理——候选窗按光标锚点所在显示器
        布局并缓存 `layoutDpi_` 供绘制复用；Toast 按提示位置取 DPI；设置窗按打开
        时所在显示器布局并在跨屏时重排控件/窗口尺寸。
      - **验证**：Windows 壳 `build.sh` 编译 + `core_test` 通过（无警告）；macOS/
        Linux 无代码改动。真机验收点：深浅色切换即时重绘、副屏/混合 DPI 打开
        设置窗与候选窗、跨屏拖动设置窗。

- [x] 阶段 36：Windows 词库导入搬进 GUI（2026-10-31，编译级待真机）
      - **产品原则延续**：普通用户不再需要 `chaoset-import` 命令行。Windows 设置窗
        新增「词库」区：选择 Rime dict.yaml / CC-CEDICT txt / TSV（可多选）→ 调
        随包 `chaoset-import.exe` 与 `%LOCALAPPDATA%\Chaoset\dict.tsv` 合并 → 旧词库
        备份为 `dict.tsv.bak.<时间戳>` → 同卷 staging + `MoveFileEx` 原子替换 →
        读安装器 `ChaosetDaemon` Run 项命令行重启 daemon，新词库立即生效。
      - **安全边界**：导入器退出码非 0 / 无产物 / 备份或替换失败都中止并提示；
        不会出现「旧词库已删、新词库没到位」的中间态。仅作用于安装器管理的默认
        词库路径（自定义 `-dict` 部署需手工替换，已写入 windows README）。
      - **与 macOS 对齐**：mac Setup.app 自 2026-10-26 起已有同语义 GUI 导入（种子+
        现用+所选 → 备份 → 原子替换）；Windows 现在等价。Linux 当时仍只有 CLI
        `chaoset-import`（GTK 设置窗导入已于阶段 54 补齐，2026-10-31）。
      - **验证**：Windows 壳 `build.sh` 编译 + `core_test` 通过；`make package-windows`
        通过（zip 内含 chaoset-import.exe，导入器路径与 DLL 同目录）；真机验收点：
        多选文件、取消对话框、错误词库文件（非法音节应失败且不改词库）、成功后
        daemon 重启与备份文件生成。

- [x] 阶段 37：Linux 学习数据管理搬进 GTK 设置窗（2026-10-31，engine 零改动）
      - **架构红线**：engine 包保持绝对纯净（只做拼音匹配，无任何平台相关代码）。
        本阶段**没有**给引擎/ibus 壳加 DBus 或管理接口；GTK 设置窗改为调用随包
        `chaoset-userdict` 的**本地文件模式**（`-userdict FILE` stats/clear/export，
        与 daemon 模式共用同一份 `engine.UserDict` 逻辑）完成学习数据管理。
      - **GTK 设置窗**：新增「学习数据」区（统计行、清空二次确认、导出 TSV），
        与 macOS/Windows 设置窗同构；`userdict_ctl.py` 与设置窗同目录安装。
      - **清空的安全编排**：引擎内存态会在延迟落盘（≤2s）时把旧数据写回文件，
        所以清空前必须确认没有 `chaoset-engine` 进程存活——流程为 `ibus exit`
        → SIGTERM 全部引擎并等待退出（超时 SIGKILL）→ 清空文件 → `ibus restart`
        （失败再试 `ibus start`）；任何一步失败都中止且提示，绝不带着存活引擎清空。
        清空会短暂重启输入法，确认框已提示。
      - **测试**：`test_userdict_ctl.py` 11 项单测（依赖注入，无 GTK/ibus/引擎）覆盖
        编排顺序与失败边界；本机用真实 `chaoset-userdict` 对临时 userdict 验证
        stats/export/clear 全链路；CI linux-e2e 增加该单测步骤。
      - **打包**：deb/tar.gz/Arch 三个包都安装 `userdict_ctl.py`，发布冒烟断言其
        存在（缺了设置窗 import 失败、整个窗口打不开）。

- [x] 阶段 38：Linux 设置窗单例 + hicolor 小尺寸图标（2026-10-31）
      - **设置窗单例**：`chaoset-setup.py` 从裸 `Gtk.main()` 改为
        `Gtk.Application`（application_id `org.chaoset.Setup`）——重复点
        ibus-setup「首选项」只会激活并前置已打开的窗口，不再开多个 GTK 窗口；
        与 macOS/Windows 设置窗单例语义对齐（ui-parity 的已知差异移除）。
      - **hicolor 小尺寸图标**：补齐 16x16/22x22/24x24/32x32/64x64 PNG
        （由 256px 母图 LANCZOS 缩放，视觉一致），托盘/任务栏/小图标列表不再
        只依赖 scalable 回退；deb/tar.gz/Arch 三个包均随 hicolor 目录安装，
        发布冒烟增加 16x16 图标存在性断言。
      - **验证**：`python3 -m py_compile` 通过；图标尺寸/alpha 用 Pillow 校验；
        `make package-linux` 通过且 tar 内含新尺寸；`make vet`/gofmt/go build
        通过；GTK 窗口单例行为需 Linux 桌面真机验收（重复点「首选项」应前置
        已有窗口而不是开新窗）。

- [x] 阶段 39：三端 UI 一致性静态检查器 checkuiparity（2026-10-31）
      - **背景**：设置窗是三套独立实现（AppKit / Win32 GDI / GTK3），共享的
        只有「应该一致」的文案与功能契约；此前靠人肉检查，改一端忘另一端
        （2026-10-28 齿轮漏同步 Windows 的教训）。
      - **新增 `tools/checkuiparity`**（Go，跨平台编译；`make vet` / CI lint 执行，
        审计 G6 落地）：六组静态契约——① 设置窗共享文案三端逐字一致（标题/输入
        行为/词组联想/学习数据/清空/导出/本机说明/底部提示）；② 三端设置窗单例
        实现存在（mac 静态 window / Windows g_settingsWnd / Linux Gtk.Application）；
        ③ 学习数据 GUI 三端存在，且 Linux 侧**不得**引入引擎 DBus/IBus 管理接口
        （engine 包纯净约束）；④ 词库导入入口（mac Setup.app + Windows 设置窗
        GUI；Linux 当时仅 CLI（阶段 54 已补齐）；⑤ 打包安装一致性
        （userdict_ctl.py 被 Makefile/install.sh/Arch/release 冒烟引用、hicolor
        小图标齐全、CI py_compile/单测步骤存在）；⑥ ui-parity 关键差异行保留。
      - **测试**：`tools/checkuiparity/main_test.go` 覆盖规则助手 + 在真实仓库上
        跑全量契约（删改任一约定文案/文件会先让 `go test ./...` 失败）。
      - **验证**：`make vet` 通过（新增 check-uiparity 目标）；`go test
        ./tools/checkuiparity` 通过；真实仓库检查 PASS。

- [x] 阶段 40：checkuiparity 扩展到候选窗静态几何/配色字段（2026-10-31）
      - **补机器检查盲区**：上一阶段 checkuiparity 只覆盖设置窗与打包；本阶段
        增加「候选窗（mac `CandidatePanel.swift` ↔ Windows `win32.cpp`）」静态契约：
        - 布局 pt 常量 12 项（正文字号/序号字号/padding/首项左内边距/候选内边距/
          候选间距/键帽横纵内边距/首选胶囊外扩/翻页区宽/齿轮区宽/面板最大宽度）；
        - 派生值：`textH = ceil(fontSize*1.4)` 与 Windows `pt(21)` 一致；
          `kMaxContentPt` 内联表达式按 mac 常量重建后去空白比对；
        - 齿轮矢量参数 6 项（圆环半径 3.4pt/线宽 1.8pt、8 齿内 4.6pt/外 7.2pt/
          线宽 2pt、chipDigit 颜色绑定）；分割线高度 `55/100` 公式；
        - 13 个主题色字段名集合两端一致（任一新增/删除会 FAIL）。
      - **边界**：这是静态常量/字段检查，不替代真机像素级验收（抗锯齿、真实
        阴影、毛玻璃、Toast、DPI 观感仍需 Windows 真机）；文档已按此修正，
        不再说「Windows 候选窗没有任何机器断言」。
      - **测试**：`TestCheckCandidateWindow` 用真实两端源码跑契约 + 变异测试
        （把 Windows 正文字号 15 改成 16 必须被捕获）。
      - **验证**：`go test ./tools/checkuiparity` 通过；`make vet` 通过；真实仓库
        `checkuiparity` PASS；`git diff --check` 通过。

- [x] 阶段 41：key_routing 回归测试接入 CI + 两处 Swift 警告清理（2026-10-31）
      - **key_routing 接入 CI（审计 G7）**：`shims/macos/tests/key_routing/main.swift`
        是合成 NSEvent 驱动 `ChaosetController.handle` 的按键路由回归（keyUp 放行/
        字母组词/快捷键放行/Shift 空按中英切换/Shift+字母不误切；2026-10-31 增
        Option+Backspace 3 条后共 11 项），此前未接入 Makefile/CI（死测试）。新增
        `tests/key_routing/run.sh`：编译测试二进制 → 起临时 daemon（临时 socket/
        userdict，结束 kill+wait 后清理）→ 跑断言；CI macos-build job 每次 push
        执行。本机实测 8/8（当时）/ 11/11（阶段 46 后）通过。
      - **checkuiparity 防退化**：新增「CI 覆盖」检查——run.sh 必须存在且
        ci.yml 必须调用 key_routing，防止以后又被摘掉变回死测试。
      - **顺带清理本次编译暴露的两处 Swift 警告**：
        - `ChaosetController.swift` 中 `return true` 后不可达的
          `return passThrough(client)`（删除，无行为变化）；
        - `SettingsWindow.swift` 的 `allowedFileTypes`（macOS 12 已废弃）改为
          `allowedContentTypes = [UTType(filenameExtension: "tsv")]`。
      - **验证**：`./shims/macos/build.sh` 通过（无警告）；key_routing 8/8 通过；
        `go test ./tools/checkuiparity` 通过；`make vet` 通过；真实仓库 checkuiparity
        PASS；`git diff --check` 通过。

- [x] 阶段 42：修复 key_routing CI 首跑失败（bash 3.2 + UTF-8 locale 坑）（2026-10-31）
      - **现象**：key_routing 接入 CI 后首次运行，macos-build 在
        `./shims/macos/tests/key_routing/run.sh: line 31: ARCH�: unbound variable`
        处失败。根因：macOS 自带 bash 3.2 在 UTF-8 locale 下把 `$ARCH）` 里的
        全角括号字节并入变量名（本地默认 C locale 不复现，CI 是 UTF-8 locale）。
      - **修复**：shims 下全部「`$VAR` 紧跟非 ASCII 字符」的写法改为 `${VAR}`
        （共 9 处：test-ci.sh/build.sh/install.sh/verify.sh/key_routing run.sh）；
        行为不变，仅消除 bash 3.2 解析歧义。
      - **防退化**：`tools/checkuiparity` 新增「shell 变量转义」组，扫描
        `shims/**/*.sh` 中 `$VAR` 紧跟非 ASCII 的行并 FAIL（附文件:行号与修法）；
        新增 `TestCheckShellVarEscaping` 覆盖正反例。
      - **验证**：本地用 `LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8` 复现原错误；
        修复后 `${VAR}` 形式通过；`go test ./tools/checkuiparity` 通过；
        `make vet` 通过；真实仓库 checkuiparity PASS；`git diff --check` 通过。

- [x] 阶段 43：mac 设置窗补「词库导入」入口（2026-10-31）
      - **动机**：Windows 设置窗已有「词库」区（2026-10-31），mac 的导入入口只在
        独立的 Setup.app 里——设置窗本身没有入口，三端 GUI 入口不对齐。
      - **实现**：mac 设置窗新增「词库」区（窗口 380×440pt）：「导入词库…」打开
        随包安装的 `~/Applications/Chaoset.app`（install.sh 安装位置）并传
        `--import`；Setup.app 收到该参数后在窗口就绪后直接弹出 NSOpenPanel 导入面板
        （合并/备份/原子替换/重启 daemon 仍是 Setup.app 原有实现，输入法壳不复制）。
        找不到 Setup.app 时给出明确提示（未安装/开发构建回退到同级 build/）。
      - **checkuiparity**：「词库导入入口」组增加断言——mac 设置窗必须含「导入词库」
        与「安装与设置」、Setup.app 必须识别 `--import`（防止入口被静默移除）。
      - **验证**：`ARCHS=arm64 ./shims/macos/build.sh` 通过（0 warning）；
        `ChaosetSetup --status` 无界面模式正常；`make vet` / `go test
        ./tools/checkuiparity` 通过；真实仓库 checkuiparity PASS；
        `git diff --check` 通过。GUI 交互（点按钮→弹导入面板）需真机点击验收。

- [x] 阶段 44：checkuiparity 补「词库导入安全流程」静态断言（2026-10-31）
      - **动机**：词库导入是高风险操作——只看产物文件存在会把「部分解析失败」
        当成功；remove+move 会制造「旧词库已删、新词库没到位」的丢词库中间态。
        这些安全点都是历史审查修出来的（2026-10-26 mac / 2026-10-31 Windows），
        但没有机器检查，重构时容易被删。
      - **新增 checkuiparity「词库导入安全」组**：
        - mac Setup.app 必须保留 `r2.status == 0`（导入器退出码检查）、`.bak.`
          （旧词库备份）、`replaceItemAt`（同卷原子替换）；
        - Windows 设置窗必须保留 `code != 0`、`.bak.`、`MoveFileExW(` +
          `MOVEFILE_REPLACE_EXISTING`（staging 原子替换）、`import.log`（失败可排查）；
        - 新增 `TestCheckImportSafety`：真实两端源码跑契约 + 变异测试（去掉
          Windows 原子替换标志必须被捕获）。
      - **验证**：`go test ./tools/checkuiparity` 通过（7 个测试）；`make vet` 通过；
        真实仓库 checkuiparity PASS；`git diff --check` 通过。

- [x] 阶段 45：Windows 关闭联想「即时收起」对齐 mac（2026-10-31）
      - **问题**：设置窗关闭词组联想后，Windows 已弹出的联想候选要等**下一次按键**
        才收起（2026-10-30 为避免设置窗持有 TIP 裸指针造成 UAF 而改成不即时）；
        mac 是设置窗 post 通知后立即收起，三端体验不一致。
      - **方案对比**：① 设置窗持有 TIP 指针直接调（退回 UAF，不可行）；
        ② TIP 定时轮询注册表（延迟/CPU 开销，且已收起的联想窗仍在）；
        ③ **跨进程广播消息（采用）**：设置窗 `RegisterWindowMessageW` 注册
        `Chaoset.AssociateChanged`，关闭时 `PostMessage(HWND_BROADCAST, …)`；
        各宿主进程的候选窗收到后只查注册表，关闭则转发 TIP `clearAssociate`
        立即收起。设置窗仍不持有 TIP 指针，无 UAF；消息 id 由字符串注册，
        跨进程一致，候选窗与设置窗不在同一宿主也能送达。
      - **实现**：win32.cpp 新增 `associateChangedMessage()`、候选窗 proc 消息处理
        与 `CandidateWindow::onAssociateChanged()`、TIP `clearAssociateFromSettings()`；
        设置窗复选框关闭时广播。
      - **checkuiparity**：新增「关闭联想即时性」组——mac 设置窗必须有通知
        `chaosetAssociateDisabled`、Windows 必须有 `Chaoset.AssociateChanged` /
        `HWND_BROADCAST` / `clearAssociateFromSettings`、ui-parity 必须保留 Linux
        「下一次按键」已知差异。
      - **验证**：`./shims/windows/build.sh` 通过（原生 core_test 对真实 daemon 全过
        + mingw 交叉编译 DLL）；`make vet`/`go test ./tools/checkuiparity` 通过；
        真实仓库 checkuiparity PASS；`git diff --check` 通过。窗口交互（关开关→
        联想窗立即消失）需 Windows 真机点击验收。

- [x] 阶段 46：Option/Alt+Backspace 三端统一为「按音节回退」（2026-10-31）
      - **背景**：docs/ui-parity「按键边角差异」记录三端语义不同——mac 按音节
        回退（`engine.backspaceWord`）、Windows 放行给宿主删词、Linux 视为整段
        清空（Mod1 与 Ctrl 同分支）。同一快捷键三种行为，用户无法形成肌肉记忆。
      - **决策**：统一为 mac 语义「按音节回退」——中文输入法里 ⌥⌫ 的通行含义
        是删一个音节（而非删宿主文档的一个词或清空整条拼音）；非组词态仍放行
        给宿主（此时没有拼音可退，让宿主按系统语义删词）。
      - **Windows**：core 层新增 `Session::backspaceWord()`（IPC `backspaceWord`）；
        `wouldHandle` 对 Alt+Backspace 在组词中预告吞键、非组词放行；
        `OnKeyDown` 在通用 Alt 透传前处理：组词中回退一个音节、剩余拼音删空时
        已确认段立即落盘收尾（与普通 Backspace 同路径）。
      - **Linux**：ibus 引擎把 Ctrl 与 Mod1 分支拆开——Ctrl+Backspace 仍整段清空、
        Ctrl+U 仅 Ctrl；Alt+Backspace 在组词中调 `sess.BackspaceWord()` 按音节
        回退，非组词放行；Alt+U 不再复用 Ctrl+U 清空分支（mac/Windows 都放行）。
      - **macOS**：行为原本正确，key_routing 回归测试新增 3 条断言（⌥⌫ 消费、
        回退到 ni、不再含 hao），防止未来退化。
      - **checkuiparity**：新增「退格键语义」组——三端壳都必须调用
        `backspaceWord`/`BackspaceWord`，ui-parity 必须保留「按音节回退」。
      - **验证**：`go test ./shims/linux/ibus-engine` 通过（新增 2 个用例）；
        `./shims/windows/build.sh` 通过（原生 core_test 含 backspaceWord 用例 +
        mingw 交叉编译）；mac key_routing **11/11 通过**；`make vet` /
        `go test ./tools/checkuiparity` 通过；真实仓库 checkuiparity PASS；
        `git diff --check` 通过。
      - **真机验收**：Windows/Linux 组词中按 Alt+Backspace 应回退一个音节并消费；
        非组词时仍由宿主删词。

- [x] 阶段 47：Linux E2E 覆盖 Alt+Backspace/Alt+U 真实 ibus 路由（2026-10-31）
      - **动机**：阶段 46 统一了三端 ⌥⌫ 语义，但 Linux 侧只在 Go 单测里验证引擎
        行为；真实 ibus 是否把 Alt+Backspace 交给引擎（还是被 ibus/GTK 先拦截）
        没有端到端覆盖——若被上游拦截，用户实际按不出来，单测全绿也没用。
      - **新增**：`shims/linux/test-ci.sh` 在真实 ibus-daemon + 私有 session bus 下
        增加 4 条断言：
        - 组词 `nihao` 中 Alt+Backspace 被消费（回退一个音节），再按一次删空；
        - 非组词态 Alt+Backspace 放行给宿主；
        - Ctrl+Backspace 仍整段清空（与 Alt 区分）；
        - Alt+U 放行给宿主（不得复用 Ctrl+U 清空分支）。
      - **验证**：`bash -n shims/linux/test-ci.sh` 通过；真实 ibus 环境由 CI
        linux-e2e job 执行（本机为 macOS，无 Linux/ibus 运行时）。

- [x] 阶段 48：三端修饰键映射统一（Command↔Win/Super、Control↔Ctrl、Option↔Alt）（2026-10-31）
      - **用户要求**：Windows/Linux 的 Ctrl 对应 macOS Control、Alt 对应 Option、
        Win/Super 对应 Command，要保持用户习惯在不同平台间的对应关系。
      - **审计发现的不一致**：
        - mac 组词中 Cmd/Ctrl/Shift+Delete 都「全部删除」，但 Windows Win+Backspace、
          Linux Super+Backspace 仍放行；
        - mac 把 ForwardDelete 当 Backspace 删拼音，Windows/Linux 的 Delete 在组词中
          清空组词后放行（宿主删文档字符）；
        - mac Option+Delete 按音节回退，Windows/Linux Alt+Delete 放行；
        - mac Shift+Option+Backspace 因 Shift 优先而全部删除，Windows/Linux 只按
          Alt 分支回退一个音节（优先级不同）。
      - **统一规则（三端实现一致）**：组词中 Backspace/Delete——无修饰逐字符回退；
        Option/Alt 按音节回退（`backspaceWord`）；Command(Win/Super)/Control(Ctrl)/Shift
        全部删除；组合键按 mac 优先级 Command/Ctrl/Shift > Option。非组词态一律放行。
      - **实现**：Windows 新增 `kVkDelete`，`wouldHandle`/`handleKey` 统一按修饰键映射；
        Linux 新增 `keysymDelete`，拆分 Ctrl/Mod1/Mod4 分支并按 Shift 优先级；mac 行为
        原本正确，补充 8 条 key_routing 断言（现 19/19）。
      - **机器检查**：checkuiparity 新增「修饰键映射」组（三端修饰键标识 + ui-parity
        映射表），「退格键语义」组增加 `kVkDelete`/`keysymDelete`。
      - **文档**：ui-parity 新增「修饰键映射」节与组词退格/清空矩阵，移除
        「组词态 ForwardDelete」差异行；三端 README 同步。
      - **验证**：`go test ./shims/linux/ibus-engine` 通过（新增 Delete/修饰键矩阵 7 例）；
        `./shims/windows/build.sh` 通过（原生 core_test + mingw）；mac key_routing 19/19；
        `make vet`/`go test ./tools/checkuiparity` 通过；真实仓库 checkuiparity PASS；
        `git diff --check` 通过。**Win/Super 组合可能被系统外壳先拦截，需 Windows/Linux
        真机确认实际可达性。**

- [x] 阶段 49：非退格键修饰键一致性收尾（2026-10-31）
      - **审计发现**：
        - Windows 用虚拟键判定 `Ctrl+U`，`Ctrl+Shift+U` 也被当整行清空；
          mac 按字符判定、Linux 按 keysym 判定都放行 → 三端不一致；
        - Windows 组词中 `Shift+PageUp/PageDown` 因「翻页键不带 Shift」被放行，
          而 mac（keyCode）/Linux（keysym）无论 Shift 都翻页；
        - mac 联想态按 Ctrl/Alt/Super/Delete 等键时只清组词、联想窗残留到下一次
          输入；Windows/Linux 都是先收起联想再透传（mac 联想态 Backspace 还会
          透传误删文档，其联想分支里的 Backspace 处理是死代码）。
      - **修复**：Windows `Ctrl+U` 要求不带 Shift、PageUp/PageDown 不看 Shift；
        mac 联想态统一「先收起联想再透传」（Backspace 消费、Delete/其它键透传），
        并删除死代码分支。
      - **测试**：mac key_routing 增 3 条（联想态 Backspace、Shift+PageUp、
        Ctrl+Shift+U），现 22/22；Linux behavior_test 增 `TestNonDeleteModifierParity`；
        Linux E2E 增 Shift+PageUp / Ctrl+Shift+U 真实 ibus 断言。
      - **文档**：ui-parity 组词翻页行补 Shift 语义，删除「按键边角差异」节（已无
        已知未统一项）；mac README 计数更新。
      - **验证**：`go test ./shims/linux/ibus-engine` 通过；`./shims/windows/build.sh`
        通过；mac key_routing 22/22；`make vet`/checker 通过；`git diff --check` 通过。

- [x] 阶段 50：文档计数同步 + key_routing 断言用例数守卫（2026-10-31）
      - **过期计数**：ui-parity 的 key_routing「11 项断言」实际已 22 项；
        AGENTS.md 的 checkuiparity「11 组」已随新增检查增长——改为不写死数字
        （以 `make check-uiparity` 输出为准），避免每次加检查都要改说明。
      - **测试守卫**：key_routing 末尾断言 `pass == expectedChecks(22)`，删掉一条
        断言会直接 FAIL（否则删断言 CI 仍绿，回归测试静默变弱）；checkuiparity
        「CI 覆盖」组断言该守卫存在。
      - **验证**：key_routing 22/22；`make vet` / checker 通过；`git diff --check` 通过。

- [x] 阶段 51：checkuiparity 校验 key_routing expectedChecks 与实际断言数一致（2026-10-31）
      - **问题**：上一阶段给 key_routing 加了运行时 `expectedChecks` 守卫，但若新增
        断言后忘记同步 expectedChecks，运行时守卫会 FAIL（好事）；反过来若有人把
        `expectedChecks` 也一起改小，静态检查无法发现。
      - **新增**：checkuiparity「CI 覆盖」组解析 `let expectedChecks = N`，统计文件中
        `^check(` 断言数并比对——加/删断言必须同步 expectedChecks，否则 `make vet`
        直接红（运行时守卫是第二道）。
      - **测试**：`TestCheckKeyRoutingCount` 正例 + 变异（expectedChecks 改大必须被捕获）。
      - **文档**：AGENTS.md 的 Windows 视觉断言说明按事实修正（有 checkuiparity 静态
        契约，但无运行时视觉断言）；README 阶段 30 历史条目补「按键边角/设置窗单例
        已于 2026-10-31 解决」注记；ui-parity 说明 checkuiparity 校验计数一致。
      - **验证**：`go test ./tools/checkuiparity` 通过；`make vet` 通过；真实仓库
        checkuiparity PASS；`git diff --check` 通过。

- [x] 阶段 52：Windows 候选窗 3 行折行（与 macOS 同算法）（2026-10-31）
      - **背景**：docs/ui-parity 长期记录「mac 3 行折行 / Windows 单行布局」的已知
        差异；Windows 单行在 6 个长候选时只能截断/丢弃，信息展示弱于 mac。
      - **实现**：把折行布局抽成纯函数 `shims/windows/src/candidate_layout.hpp`
        （无 Win32 依赖，与 mac `layoutRows` 同算法：按内容宽 638pt 折行、最多 3 行、
        行内 gap、行高 textH+4pt lineSpacing、首项超宽才截断），win32.cpp 逐项测量后
        调用它回填 rect；面板宽/高按行数计算，折叠区（齿轮/翻页/页码）仍垂直居中。
      - **测试**：`tests/core_test.cpp` 增加布局数学单测（折行数、行宽、maxRows 截断、
        单条超宽钳制）——在 macOS 上原生跑，不依赖 Windows。
      - **机器检查**：checkuiparity 增加「最大行数」契约（mac `maxRows=3` ↔ Windows
        `kMaxRows=3`），并断言 win32.cpp 使用 `candidate_layout.hpp`。
      - **文档**：ui-parity 布局表/已知差异更新（多行折行差异移除）；windows README
        删除「单行布局」限制。
      - **验证**：`./shims/windows/build.sh` 通过（原生 core_test 含布局单测 + mingw
        交叉编译 DLL）；`make vet`/checker 通过；`git diff --check` 通过。**像素级观感/
        点击仍需 Windows 真机验收**。

- [x] 阶段 53：修正 Windows 多行折行的「非首项超宽」截断（2026-10-31）
      - **问题**：阶段 52 的 Windows 折行只对**首项**做超宽截断；后续某项若
        自身宽度 > maxContent（换行也放不下），`candidate_layout.hpp` 会把它的
        布局宽钳到上限，但文本未截断 → 文本/键帽可能画到面板外。mac 的
        `layoutRows` 实际对「独占一行且超宽」的任意项截断。
      - **修复**：Windows 改为**任意单条**候选宽 > maxContent 时逐字截断加…
        （能超上限的项必然独占一行，与 mac 语义等价）；同步修正 mac 源码注释
        里「Windows 按首项截断」的过期表述。
      - **机器检查**：checkuiparity 增加「行距」契约（mac lineSpacing=4 ↔
        Windows `lineSpacing = pt(4, dpi)`）。
      - **验证**：`./shims/windows/build.sh` 通过（原生 core_test + mingw）；
        `make vet`/checker 通过；`git diff --check` 通过。

- [x] 阶段 54：Linux 设置窗补「词库导入」GUI（三端入口对齐）（2026-10-31）
      - **背景**：Linux 词库导入此前只有 CLI `chaoset-import`，设置窗没有入口；
        而系统词库 `/usr/share/chaoset/dict.tsv` 需要 root 才能改。
      - **实现**：
        - 引擎侧：ibus 引擎新增可选 `-imported-dict`（默认
          `$XDG_DATA_HOME/chaoset/dict.tsv`，文件不存在则忽略），启动时与系统
          词库一起宽松加载——**engine 包零改动**，只是壳多传一个词库路径。
        - 设置窗：GTK 新增「词库」区「导入词库…」（文件多选，Rime yaml /
          CC-CEDICT txt / TSV）；`dictimport_ctl.py` 调随包 `chaoset-import`
          合并到用户词库（临时文件 + 备份 + `os.replace` 原子替换），成功后
          复用 `userdict_ctl.restart_engines()` 重启 ibus 引擎让新词生效。
        - 打包/CI：`dictimport_ctl.py` 随 deb/tar.gz/Arch 安装到
          `/usr/share/chaoset/`（release 冒烟断言）；CI py_compile + 两个单测文件
          （`unittest discover`）。
        - checkuiparity：「词库导入入口」组改为三端 GUI 断言（Linux 设置窗/
          dictimport_ctl/ibus `-imported-dict`），移除「Linux 仅 CLI」；打包安装组
          增加 dictimport_ctl.py 引用检查。
      - **测试**：`test_dictimport_ctl.py` 7 例（参数拼接/已有词库底表/备份/原子
        替换/失败保留旧词库/重启失败提示）；`imported_dict_test.go` 验证可选文件
        存在才追加；linux-e2e 真实 ibus 环境启动引擎并断言「用户导入词库已加载」。
      - **验证**：`go test ./shims/linux/ibus-engine` 通过；py_compile + unittest
        discover 通过；`make vet`/checker 通过；`git diff --check` 通过。GUI 交互
        （点按钮→选文件→重启生效）需 Linux 桌面/ibus 真机验收。

- [x] 阶段 55：Linux 导入词库的端到端候选断言 + 路径一致性守卫（2026-10-31）
      - **E2E**：linux-e2e 原来只断言引擎日志出现「用户导入词库: 路径」，
        不能证明词条真的可用。现在捕获引擎 `UpdateLookupTable` 信号，打
        `ceshici`（测试词 ce shi ci），断言候选列表出现「测试词」。
      - **路径一致性**：checkuiparity 断言 Go 引擎与 `dictimport_ctl.py` 的默认
        路径都拼 `chaoset/dict.tsv`（XDG_DATA_HOME 口径），防止两端漂移导致
        设置窗写入的文件引擎找不到。
      - **测试**：Go 增加 `TestDefaultImportedDictPath`（XDG_DATA_HOME）；Python
        增加空 XDG 回退 `~/.local/share` 用例。
      - **验证**：go test ./shims/linux/ibus-engine、Python 21 例、make vet/checker、
        git diff --check 通过；linux-e2e 由 CI 真实验证。

- [x] 阶段 56：清理过期文档 + 锁死 Windows 多行折行接入（2026-10-31）
      - **过期文档**：README 阶段 36/39 仍写「Linux 仍只有 CLI / 导入待后续」——
        阶段 54 已补齐 Linux GUI 导入。历史条目改为「当时仍只有 CLI（已于阶段 54
        补齐）」，并加阶段 56 记录。
      - **checkuiparity**：「文档对照」组新增 README 断言——必须保留阶段 54 的
        Linux 词库导入条目，且不得再出现过期现状（如「Linux 仍只有 CLI」/
        「导入待后续」）；「候选窗几何」组新增 Windows 必须实际调用
        `wrapRows(..., kMaxRows)` 且使用 `lineSpacing = pt(4, dpi)`，防止只留下
        常量/头文件而退回单行布局。
      - **验证**：`make vet`/checker、`go test ./tools/checkuiparity`、`git diff
        --check` 通过。

- [x] 阶段 57：Windows 候选窗基础 UIA 无障碍（窗口级）（2026-10-31，编译级待真机）
      - **背景**：docs 长期记录 Windows「无 WM_GETOBJECT/UIA/MSAA（待做）」；
        macOS 已有 VoiceOver 虚拟子元素。本轮先补**窗口级**基础能力，不做逐条
        候选子项（需要自定义 Fragment/Invoke provider，风险高且无真机可验）。
      - **实现**：候选窗处理 `WM_GETOBJECT`（`UiaRootObjectId`），动态加载
        `uiautomationcore.dll` 的 `UiaHostProviderFromHwnd` +
        `UiaReturnRawElementProvider`，返回 UIA 宿主 provider；窗口标题改为
        「Chaoset 候选」。动态加载避免静态导入依赖（旧/精简系统无该 DLL 时
        输入法照常工作）。
      - **机器检查**：checkuiparity 断言 win32.cpp 保留 `WM_GETOBJECT` /
        `UiaRootObjectId` / `uiautomationcore.dll`，防止能力被误删。
      - **文档**：ui-parity 无障碍行、windows README 更新；明确「候选子项未实现」。
      - **验证**：`./shims/windows/build.sh` 通过（mingw 编译 + core_test）；
        `make vet`/checker 通过。**需 Windows 真机 + Narrator 验收**。

- [x] 阶段 58：Linux 安装包自检进日常 CI（2026-10-31）
      - **问题**：安装包冒烟只在 release.yml 打 tag 时跑；日常改动若漏装
        设置窗模块（如阶段 54 新加的 `dictimport_ctl.py`），要到发版才暴露。
      - **实现**：linux-e2e job 新增「Linux 安装包自检」步骤——`make
        package-linux` 后断言 tar.gz 内 `setup/chaoset-setup.py`、
        `setup/userdict_ctl.py`、`setup/dictimport_ctl.py`、`data/dict.tsv` 都在；
        若生成了 deb，再用 `dpkg-deb -c` 断言 `dictimport_ctl.py`。
      - **机器检查**：checkuiparity「CI 覆盖」组断言 ci.yml 保留
        `make package-linux` 与「Linux 安装包自检」，防止步骤被删。
      - **验证**：本机 `make package-linux` 通过并人工核对 tar 内容；
        `make vet`/checker 通过；CI linux-e2e 实际执行该步骤。

- [x] 阶段 59：macOS 安装包自检进日常 CI（2026-10-31）
      - **问题**：macos-build 此前只跑 `build.sh` + universal 检查；打包脚本
        `make package-macos`（zip/dmg、Setup.app 资源清单）只在 release 打 tag
        时验证，日常改动可能悄悄漏装随包工具。
      - **实现**：macos-build 改为直接跑 `make package-macos`（与 release 同一条
        打包链）：构建 universal app + 全部随包工具 universal 自检 + zip（有
        hdiutil 时再出 dmg）；步骤内断言 zip 含主壳与 Setup 随包
        `chaoset-import`。注意保留 `make build`：`equivalence.sh` 的 B 段走
        `import-dict.sh`，用的是 `bin/chaoset-import`（首跑 CI 因漏掉它失败，
        已修）。
      - **机器检查**：checkuiparity「CI 覆盖」组断言 ci.yml 保留
        `make package-macos` 与打包步骤名，防止被删。
      - **验证**：`make vet`/checker 通过；CI macos-build 实际执行打包步骤。

- [x] 阶段 60：Linux 词库导入安全流程纳入 checkuiparity（2026-10-31）
      - **背景**：checkuiparity「词库导入安全」此前只锁 mac Setup.app 与 Windows
        设置窗（退出码检查/备份/原子替换/日志）；阶段 54 新增的 Linux
        `dictimport_ctl.py` 没有对应静态契约，重构时可能悄悄退回「直接覆盖」。
      - **实现**：安全组新增 Linux 断言——`r.returncode != 0`、`shutil.copy2`
        备份、`tmp = target + ".tmp"` 临时文件、`os.replace` 原子替换；
        新增 `TestCheckImportSafety` 变异测试（Linux 侧去掉 os.replace 必须被捕获）。
      - **文档**：ui-parity 机器检查说明改为三端；README 阶段 60。
      - **验证**：`go test ./tools/checkuiparity`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 61：Linux 鼠标点选候选（CandidateClicked）（2026-10-31）
      - **问题**：ibus 面板点击候选会调用引擎标准方法 `CandidateClicked`，
        但 Linux 引擎此前没实现——点击候选无反应（mac/Windows 原生候选窗
        都支持点选）。
      - **实现**：抽出 `selectCandidate(idx)` 与数字键共用（联想中选联想词并
        继续联想；组词中渐进上屏、剩余回灌；越界忽略且不改会话），新增
        `CandidateClicked(index, button, state)` D-Bus 方法（全程持锁）。
      - **测试**：4 个行为单测（点选上屏/渐进点选/越界保持组词/联想连选）；
        linux-e2e 直接 `dbus-send` 调用 CandidateClicked 并捕获 CommitText，
        断言点选上屏「你好」。
      - **机器检查**：checkuiparity 新增「候选点选」组（mac mouseDown /
        Windows WM_LBUTTONDOWN / Linux CandidateClicked / 文档矩阵行）。
      - **文档**：ui-parity 按键矩阵新增「鼠标点选候选」行；README 阶段 61。
      - **验证**：`go test ./shims/linux/ibus-engine`、`make vet`/checker、
        `git diff --check` 通过；linux-e2e 由 CI 真实验证。

- [x] 阶段 62：Linux 设置窗耗时操作改后台线程（2026-10-31）
      - **问题**：清空学习数据要 `ibus exit`/重启（~2s），导入大词库可能数秒；
        此前同步跑在 GTK 主线程，设置窗口点按钮后「假死」无响应。
      - **实现**：新增 `run_async(work, done)`——工作线程只跑 subprocess/文件 IO，
        完成后经 `GLib.idle_add` 回主线程恢复窗口敏感度并显示结果；清空/导出/
        导入三处接入。GTK 非线程安全，工作线程绝不碰控件。
      - **机器检查**：checkuiparity「学习数据 GUI」组断言 linuxSetup 保留
        `GLib.idle_add` / `threading.Thread`。
      - **文档**：Linux README、README 阶段 62。
      - **验证**：`py_compile`、Python 单测、`make vet`/checker、`git diff --check`
        通过；GUI 线程行为需桌面环境真机点验。

- [x] 阶段 63：候选点选 E2E 与滚轮差异文档守卫（2026-10-31）
      - **候选点选**：checkuiparity「候选点选」组新增断言——linux-e2e 必须保留
        `CandidateClicked` + `CommitText` 的真实调用（防止 E2E 用例被删只剩单测）。
      - **文档对照**：ui-parity 关键行新增「鼠标点选候选」「滚轮翻页」；机器检查
        说明补候选点选三端回调与 linux-e2e 断言、滚轮三端一致缺失。
      - **验证**：`make vet`/checker、`go test ./tools/checkuiparity`、
        `git diff --check` 通过。

- [x] 阶段 64：UIA 动态加载的构建期守卫（2026-10-31）
      - **约定**：Windows 候选窗 UIA 宿主 provider 必须动态加载
        `uiautomationcore.dll`，不能给 DLL 增加静态导入依赖（旧/精简系统没有
        该 DLL 时输入法仍要能跑）。
      - **实现**：`shims/windows/build.sh` 在交叉编译后用 `objdump -p` 断言
        `ChaosetIME.dll` 的导入表里没有 `uiautomationcore.dll`；checkuiparity
        「候选窗几何」组断言该构建期检查保留。
      - **验证**：本机 `./shims/windows/build.sh` 通过，objdump 导入表确认无
        `uiautomationcore`；`make vet`/checker 通过。

- [x] 阶段 65：候选点选的分页语义测试（2026-10-31）
      - **背景**：ibus 面板 LookupTable 只展示当前页，`CandidateClicked(index)`
        是页内序号；`CandidateClicked` 首版只测了第 1 页，翻页后点选是否按
        「当前页切片」换算没有回归。
      - **测试**：新增 2 例——组词第 2 页点选第 1 项应上屏第 2 页首条；联想
        第 2 页点选第 1 项应上屏全局第 7 条（assocPage*PageSize + index）。
      - **验证**：`go test ./shims/linux/ibus-engine -run TestCandidateClicked`
        6 例全过。

- [x] 阶段 66：第 2 页鼠标点选的 linux-e2e 覆盖（2026-10-31）
      - **背景**：CandidateClicked 的页内序号语义由单测覆盖，但 linux-e2e 只
        验证了第 1 页；翻页后 D-Bus 调用是否仍正常没有端到端保障。
      - **实现**：test-ci.sh 新增一段——组词 `ni` → PageDown → 调用
        CandidateClicked(0)，捕获 CommitText 断言上屏成功（具体页内映射由
        behavior_test 的 PageTwo 用例锁定）。
      - **验证**：`bash -n shims/linux/test-ci.sh`、`make vet`/checker、
        `git diff --check` 通过；linux-e2e 由 CI 真实验证。

- [x] 阶段 67：联想中越界点选收起联想（与数字键一致）（2026-10-31）
      - **问题**：数字键越界时联想分支会 `exitAssociate()` 收起联想；
        `CandidateClicked` 越界只忽略，联想窗会挂着（面板/引擎页码不同步时
        留下不可操作的状态）。
      - **修复**：`CandidateClicked` 在 `selectCandidate` 返回 false 且当前在
        联想中时调用 `exitAssociate()`；普通组词越界仍保持原样（不清用户拼音）。
      - **测试**：新增 `TestCandidateClickedAssociateOutOfRangeExits`；ui-parity
        鼠标点选行同步语义描述；README 阶段 67。
      - **验证**：`go test ./shims/linux/ibus-engine`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 68：引擎 API 契约测试补齐 + NormalizePinyin 数字调号修复（2026-10-31）
      - **背景**：引擎覆盖率审计（`go test -cover ./engine`，90.4%）发现若干
        公共 API 0% 覆盖：`NormalizePinyin`、`PrevWord`、`PageSize`、
        `FindSentenceCandidates`、`QueryPhrasesPrefix`、`queryVariants`。
      - **修复**：`NormalizePinyin` 文档声称支持「去音调数字后缀」，但实现只
        `TrimRight` 掉**末尾**数字——`ni3hao3` 这类连写带调号整串归一化失败。
        改为删掉 token 内所有 `1-5`（拼音编码里数字只可能是调号），再走
        `IsSyllable`/`segmentRunon` 判定；空格分隔的 `ni3 hao3` 行为不变。
      - **测试**：新增 `engine/api_contract_test.go` 6 组契约（音调/数字后缀/ü/
        连写切分/非法输入、PrevWord、PageSize、FindSentenceCandidates 包装、
        QueryPhrasesPrefix、queryVariants 模糊展开）。
      - **验证**：`go test ./engine ./cmd/chaoset-dictgen` 通过；`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 69：IPC 客户端便捷方法与 Server Option 契约测试（2026-10-31）
      - **背景**：`go test -cover ./ipc`（73.7%）审计发现 `Client.Backspace` /
        `Reset` / `NextPage` / `PrevPage` 四个便捷方法 0% 覆盖；
        `Server.WithFuzzy` / `WithTraditional` 两个 daemon 选项也 0%。
      - **测试**：新增 `ipc/client_nav_test.go`——
        ① 真实 Unix socket 往返覆盖四个导航/编辑方法（含翻页边界与 Reset）；
        ② `WithTraditional(true)` 下 `guo` 首选必须是「國」；
        ③ `WithFuzzy(true)` 下 `zongguo` 必须出「中国」。
        顺带记录 macOS Unix socket 路径 104 字节上限：测试用 `os.MkdirTemp`
        短路径而不是含长测试名的 `t.TempDir()`。
      - **验证**：`go test ./ipc`、`make vet`/checker、`git diff --check` 通过。

- [x] 阶段 70：IPC Client 补联想便捷方法 + server 联想分支覆盖（2026-10-31）
      - **背景**：daemon 协议支持 `associate` / `associateSelect`（mac/Windows
        壳直接走 `call`），但 Go `ipc.Client` 没有对应便捷方法，协议 API
        不对称；server dispatch 的联想分支此前也 0% 覆盖。
      - **实现**：`Client.Associate(sylls)` 返回续接候选、
        `Client.AssociateSelect(word, sylls)` 返回上屏文本（与 engine.Session
        同名方法同语义）。
      - **测试**：新增 `ipc/associate_test.go`——上屏「你」后联想应有续接候选，
        再 `AssociateSelect` 上屏首条并与候选文本一致；顺带覆盖 server
        dispatch 的 associate/associateSelect 分支。
      - **验证**：`go test ./ipc`、`make vet`/checker、`git diff --check` 通过。

- [x] 阶段 71：engine 平台中立性机器守卫 + 归一化代码整理（2026-11-01）
      - **用户要求**：engine 永远保持纯净，不携带任何平台相关内容；架构/可读性
        优先，不做屎山。
      - **守卫**：新增 `engine/purity_test.go`——解析 engine 目录全部 .go：
        ① 只允许标准库 import（第三方路径首段带 `.` 一律拒绝，含项目内部包）；
        ② 禁止 `import "C"` / `syscall` / `unsafe` / `os/exec` / `net` /
        `runtime/cgo` 等平台/外部能力；③ 禁止任何 `//go:build` 构建标签与
        `*_windows.go` / `*_linux.go` / `*_darwin.go` 等平台文件。
      - **整理**：把 `normalizePinyin` 里内联的调号数字清理提取为
        `stripToneDigits`（命名表达意图，注释说明为什么全删而非 TrimRight）。
      - **验证**：`go test ./engine`（含 purity/normalize 用例）、`make vet`/
        checker、`git diff --check` 通过。

- [x] 阶段 72：engine 纯净守卫的规则自测（2026-11-01）
      - **背景**：阶段 71 的 `engine/purity_test.go` 只对真实目录跑；守卫规则
        本身没有测试，改坏了（例如误删第三方 import 检查）不会有人发现。
      - **实现**：把规则抽成纯函数 `enginePurityProblems(name, src)`，真实目录
        测试与合成源码变异测试共用；新增
        `TestEnginePurityGuardCatchesViolations`——平台文件名 / `//go:build` /
        `syscall` / `import "C"` / 第三方与项目内部 import 都必须被捕获，
        干净文件不得误报。
      - **验证**：`go test ./engine`（含 7 条变异用例）、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 73：chaoset-import 原子写 + CLI 行为测试（2026-11-01）
      - **问题**：`chaoset-import` 直接 `os.Create(outPath)` 写词库；写一半失败/
        进程被杀会把用户词库变成半截文件。壳侧导入流程虽然自己做了临时文件+
        原子替换，但直接调用 CLI 的用户没有这层保护。
      - **修复**：改为同目录 `os.CreateTemp` 写完整 TSV → `os.Chmod(0644)` →
        `os.Rename` 原子替换；任何一步失败都删除临时文件、旧词库保持原样。
      - **测试**：新增 `cmd/chaoset-import/main_test.go`——默认合并取较大词频、
        `-replace` 覆盖、输入缺失时旧输出不变、输出目录自动创建。
      - **验证**：`go test ./cmd/chaoset-import`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 74：chaoset-userdict 导出原子写 + CLI 测试（2026-11-01）
      - **问题**：`chaoset-userdict export -o` 用 `os.WriteFile` 直接覆盖；导出
        失败/进程被杀会把已有导出文件截成半截（与 chaoset-import 同类问题）。
      - **修复**：`writeOut` 改为同目录 `os.CreateTemp` → `Chmod(0644)` →
        `os.Rename`；失败时删除临时文件、旧文件保持不变。
      - **测试**：新增 `cmd/chaoset-userdict/main_test.go`——本地模式
        stats/clear/export 全链路；覆盖已有导出文件整体替换；目标不可替换时
        报错且不残留临时文件。
      - **验证**：`go test ./cmd/chaoset-userdict`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 75：daemon 生命周期可测试化（runWithContext）+ 启停测试（2026-11-01）
      - **问题**：`chaosetd.run()` 内部直接 `signal.NotifyContext`，测试无法驱动
        完整启停（不能向测试进程自己发 SIGTERM），daemon 主流程覆盖率仅 12.8%。
      - **重构**：`run` 只负责把 SIGINT/SIGTERM 转成 context，再调
        `runWithContext(ctx, ...)`；生命周期（加载词库/用户词典、单实例锁、监听、
        serve、取消后停 flush ticker → Save → 清套接字）全部在后者，职责更清晰。
      - **测试**：新增 `cmd/chaosetd/run_test.go`——完整启动 → Ping/Input →
        cancel → 优雅退出 → 套接字清理；以及词库不存在/为空、非法 TCP 端口的
        启动期错误。
      - **验证**：`go test ./cmd/chaosetd`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 76：daemon 优雅退出落盘 + 单实例拒绝的端到端测试（2026-11-01）
      - **测试**：`cmd/chaosetd/run_test.go` 增加——
        ① 优雅退出后学习数据必须落盘（上屏「你好」→ cancel → 重载 userdict，
        Score > 0）；② 同套接字第二个 daemon 必须被单实例锁拒绝，不能 unlink
        运行中实例的套接字；顺带把启动/停止收敛为 `startDaemon`/`stop` 辅助，
        三个生命周期测试共用，减少重复。
      - **验证**：`go test ./cmd/chaosetd`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 77：工具收口——chaoset-import 支持 seed/backup，Linux 壳改为只调用工具（2026-11-01）
      - **用户要求**：shims 只负责平台相关 + UI，工具类逻辑用 Go 实现，不要在
        shims 混入工具实现；每个模块单一职责。
      - **工具**：`chaoset-import` 新增 `-seed`（种子词库先并入底表）与 `-backup`
        （写目标前备份 `<target>.bak.<时间戳>`，备份失败则中止）；合并/备份/原子
        替换全部在 Go 工具内，壳不再自己 copy/rename。
      - **Linux**：`dictimport_ctl.py` 删除临时文件、`shutil.copy2`、`os.replace`
        逻辑，只保留路径/调用/重启 ibus；测试同步改为断言 `-o target -backup`
        参数与失败时旧词库不变。
      - **守卫**：checkuiparity「词库导入安全」组改为断言 Linux 调用 Go 工具
        `-backup`、且不得再出现 `os.replace`/`shutil.copy2`/temp；同时断言 Go
        工具保留 `seed`/`backup`/`backupFile`/`os.Rename`。
      - **文档**：AGENTS 职责边界、ui-parity 词库导入行与机器检查说明同步。
      - **验证**：`go test ./cmd/chaoset-import`、Python 单测、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 78：mac/Windows 词库导入收口到 Go 工具（2026-11-01）
      - **mac Setup.app**：删除临时目录/base/merged/`.bak.`/`replaceItemAt` 逻辑，
        改为 `chaoset-import -o dict.tsv -seed 种子 -backup 所选文件...`，只检查
        退出码并重启 daemon。
      - **Windows 设置窗**：删除 `dict.tsv.import`/staging/`CopyFileW`/
        `MoveFileExW` 备份替换，改为 `chaoset-import -o dict.tsv -backup 所选文件`；
        只检查退出码 + `import.log`。
      - **守卫**：checkuiparity「词库导入安全」组改为断言三端壳都调用 Go 工具
        （mac `-seed`/`-backup`，Windows/Linux `-backup`）、检查退出码，且 mac 不得
        再出现 `replaceItemAt`、Windows 不得再出现 `MoveFileExW`/
        `MOVEFILE_REPLACE_EXISTING`；Go 工具保留 seed/backup/backupFile/
        `os.Rename` 断言。
      - **文档**：AGENTS 迁移进度、ui-parity 词库导入行/机器检查同步。
      - **验证**：`./shims/macos/build.sh`、`./shims/windows/build.sh`（mingw 编译级）
        通过；`go test ./tools/checkuiparity`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 79：IPC Endpoint 抽象 + Windows 命名管道传输实现（2026-11-01）
      - **用户方案**：shims 只做平台/UI，工具用 Go；IPC 不用固定 TCP 端口，
        macOS/Linux 用 Unix socket，Windows 用命名管道。
      - **Endpoint**：新增 `ipc.Endpoint{Network,Address}` 与 `ipc.Listen`/
        `ipc.DialEndpoint`；协议帧不变，`Dial` 保留兼容。Network 支持 `unix`/
        `pipe`/`tcp`（tcp 仅旧壳/测试过渡）。
      - **命名管道**：`ipc/pipe_windows.go`（go-winio）——管道名
        `\\.\pipe\chaoset-<当前用户SID>`，ACL `当前用户 + SYSTEM +
        ALL APPLICATION PACKAGES`（开始菜单/搜索框 AppContainer 需要）；
        `DefaultPipeName()` 供 daemon/壳按同一规则计算，无端口/发现文件。
      - **测试**：`ipc/endpoint_test.go` Unix socket 完整协议往返 + 未知传输报错；
        `GOOS=windows go test -c ./ipc` 与 Windows 目标二进制交叉编译通过。
      - **依赖**：新增 `github.com/Microsoft/go-winio v0.6.4`（仅 Windows 文件引用）。
      - **验证**：`go test ./ipc`、`go build ./...`、`go vet ./...`、
        `make vet`/checker、`git diff --check` 通过。

- [x] 阶段 80：daemon 接入 Endpoint（-pipe + Unix socket 权限收口）（2026-11-01）
      - **daemon**：`runWithContext` 改为接收 `ipc.Endpoint`；新增 `-pipe`（按当前
        用户 SID 自动生成命名管道名）与 `-tcp` 互斥；Unix socket 单实例锁/残留
        清理只在 unix 端点做，TCP/命名管道由系统命名空间互斥。
      - **权限**：Unix socket chmod 0600 从 daemon 的 `listenUnix` 移入
        `ipc.Listen`（传输层负责自己的安全属性），原权限测试改为
        `TestEndpointUnixRestrictsPermission`。
      - **测试**：`TestResolveEndpoint`（默认 unix / -tcp / -pipe 与 -tcp 互斥）；
        生命周期测试全部改用 `ipc.Endpoint`；`go test ./cmd/chaosetd` 通过。
      - **文档**：README 架构图/传输形态、AGENTS IPC 条目同步（Windows 壳迁移
        仍待做，当前保留 TCP 兼容）。
      - **验证**：`go build ./...`、`go test ./cmd/chaosetd ./ipc`、
        `GOOS=windows go build ./cmd/chaosetd`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 81：Windows C++ 壳命名管道 client（TCP 回退，编译级）（2026-11-01）
      - **C++ 壳**：新增 `PipeTransport`——`CreateFileW` 打开
        `\\.\pipe\chaoset-<当前用户SID>`，overlapped `ReadFile`/`WriteFile` +
        200ms 超时（与旧 TCP 壳同量级），连接失败 2s 冷却并自动回退 `TcpTransport`；
        `ChaosetTIP` 成员改为 `PipeTransport`。
      - **兼容策略**：安装器暂不改（仍 `-tcp`），DLL 管道失败自动回退 TCP；
        daemon 可用 `-pipe` 手动验证命名管道，真机通过后再切安装器。
      - **设置窗**：学习数据管理原本直接 `TcpTransport`，一并改为 `PipeTransport`
        （管道优先/TCP 回退），避免 daemon 切 `-pipe` 后设置窗连不上。
      - **测试/构建**：`./shims/windows/build.sh` 通过（mingw 交叉编译 + 原生
        core_test TCP 测试台 + 安装包）；命名管道运行时行为需 Windows 真机验收。
      - **文档**：shims/windows/README、根 README 传输形态、AGENTS IPC 条目同步。
      - **验证**：`./shims/windows/build.sh`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 82：Go 工具（chaoset-cli / chaoset-userdict）接入命名管道（2026-11-01）
      - **chaoset-cli**：新增 `-pipe`，`-socket`/`-tcp`/`-pipe` 三选一；走
        `ipc.DialEndpoint(Endpoint{pipe, DefaultPipeName()})`，Unix 上明确报
        「命名管道仅 Windows 支持」。
      - **chaoset-userdict**：新增 `-pipe`，daemon 模式改用 `ipc.Endpoint`；
        本地文件模式不变；`-socket`/`-tcp`/`-pipe` 三选一。
      - **测试**：CLI 新增 Unix `-pipe` 报错用例；userdict 新增真实 TCP daemon
        stats 用例；Windows 目标交叉编译通过。
      - **文档**：README CLI/userdict 示例与平台限制、shims/windows/README
        命令行说明同步。
      - **验证**：`go test ./cmd/chaoset-cli ./cmd/chaoset-userdict`、
        `GOOS=windows go build` 两个工具、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 83：internal/appdirs 统一用户目录/socket 默认路径（2026-11-01）
      - **新增 `internal/appdirs`**：`DataDir`（macOS Application Support /
        Linux XDG / Windows LOCALAPPDATA）、`SocketPath`（Linux 优先
        `$XDG_RUNTIME_DIR`）、`UserDictPath`/`ImportedDictPath`；另提供
        `LinuxDataDir` 等固定 XDG 口径，供只在 Linux 运行的 ibus 引擎在 macOS
        单测里也按 Linux 断言。
      - **接入**：`chaosetd`/`chaoset-userdict` 的 `-socket` 默认值改为按平台生成；
        daemon 启动时自动创建 socket 父目录（0700）；Linux ibus 引擎的用户词典/
        导入词库路径改用 appdirs。
      - **测试**：`internal/appdirs`（macOS/Linux 路径、Linux 固定 XDG、aux 路径）；
        daemon/userdict/engine 测试通过；checkuiparity 的路径一致性断言改为
        `appdirs.LinuxImportedDictPath`。
      - **文档**：README「默认路径」表、AGENTS 用户目录约定、阶段 83。
      - **验证**：`go test ./internal/appdirs ./cmd/chaosetd ./cmd/chaoset-userdict
        ./shims/linux/ibus-engine`、`make vet`/checker、`git diff --check` 通过。

- [x] 阶段 84：daemon 用户词典默认路径按平台生成（2026-11-01）
      - **问题**：socket 已按平台生成，但 `chaosetd -userdict` 默认仍是 cwd 下的
        `chaoset-userdict.json`，默认启动时学习数据会散落在不同工作目录。
      - **实现**：`-userdict` 默认值改为空；用 `flag.Visit` 区分「没传」与
        「显式传空串」——没传走 `appdirs.UserDictPath()`，显式空串仍表示不落盘。
      - **测试**：`TestResolveUserDictPath`（默认平台路径 / 显式空串 / 显式路径）。
      - **文档**：README 默认路径表加「默认用户词典」列、阶段 84。
      - **验证**：`go test ./cmd/chaosetd`、`make vet`/checker、
        `git diff --check` 通过。

- [x] 阶段 85：工具路径 API（-print-paths）；设置窗边界澄清（2026-11-01）
      - **用户澄清**：设置窗属于 shims 平台 UI（macOS Swift / Windows C++ /
        Linux Python GTK），**不搬进 Go**；Go 只承载工具逻辑。
      - **保留**：`chaoset-userdict -print-paths` 输出平台路径 JSON
        （appdirs 单一来源，供壳/脚本调用）。
      - **移除**：上一轮误加的 `internal/setupctl`（无消费者，且把 shim 业务
        放进了 Go internal 包，违背边界）。
      - **验证**：`go test ./cmd/chaoset-userdict`、`go build/vet ./...`、
        `make vet`/checker、`git diff --check` 通过。

- [x] 阶段 86：Linux shim 路径改问 Go 工具（-print-paths）（2026-11-01）
      - **背景**：Linux 设置窗的 `userdict_ctl.py` / `dictimport_ctl.py` 仍各自拼
        XDG 路径，是 `internal/appdirs` 之外的第二份规则。
      - **实现**：Python 侧 `tool_paths()` 调 `chaoset-userdict -print-paths` 取
        `userdict`/`imported_dict`；工具不可用（源码树未构建）时回退 XDG，设置窗
        仍可用。设置窗仍是 shim，不搬 Go。
      - **守卫/测试**：Python 单测新增 tool_paths/优先 Go 工具/回退；checkuiparity
        路径一致性组断言 Python 调 `-print-paths`、Go 引擎走
        `appdirs.LinuxImportedDictPath`。
      - **验证**：Python 单测（15+9 例）、`go test ./cmd/chaoset-userdict`、
        `make vet`/checker、`git diff --check` 通过。

- [x] 阶段 87：Linux 设置窗迁原生 GTK3 C，彻底移除 Python（2026-11-01）
      - **用户要求**：Linux shims 用原生实现，不额外引入 Python；macOS Swift /
        Windows C++ 都是原生，Linux 不应是 Python。
      - **本轮**：新增 `shims/linux/setup/chaoset-setup.c` + `setup_ctl.c/h`：
        GTK3 设置窗（输入行为/学习数据/词库导入）与编排逻辑全部 C；只调用
        Go 工具（chaoset-userdict / chaoset-import）和 ibus，不实现词库合并/
        用户词典读写；`chaoset-userdict -print-paths` 新增 `config` 字段，
        `appdirs.LinuxConfigPath` 与 ibus 引擎共用同一配置路径。
      - **打包切换**：Makefile/deb/Arch/tar/install.sh/component XML/CI/release/
        checkuiparity 全部切到 `chaoset-setup` 二进制 / `setup_ctl.c` /
        `test_setup_ctl.c`；删除 5 个 Python 文件（设置窗、控制模块、两个单测）。
      - **依赖**：deb/tar/Arch 由 `python3-gi` 改为 `libgtk-3`/`gtk3`；CI 安装
        `libgtk-3-dev` 并编译 C、跑 `test_setup_ctl` 无 GTK 单测。
      - **验证**：Ubuntu/Alpine 容器内 GTK3 编译 + C 单测通过；
        `go test ./...`、`make vet`/checker、shell 语法检查通过。

- [x] 阶段 88：原生设置窗无头启动冒烟 + Linux 测试脚本去 Python（2026-11-01）
      - **CI**：linux-e2e 用 Xvfb 启动 `chaoset-setup` 3 秒，GTK 初始化/窗口
        构建/配置读取在真实显示后端跑一遍；超时杀掉（124）视为正常常驻。
      - **测试脚本**：`shims/linux/test.sh` 不再用 Python 启动引擎，改为 shell
        `nohup ... &`；shim 与测试脚本均无 Python 依赖。
      - **文档**：修正 `shims/linux/README` 的配置保存语义（C 实现只更新
        associate、保留其它键，不再整文件覆盖）。
      - **验证**：`bash -n shims/linux/test.sh`、checkworkflows、checkuiparity
        通过。

## 验收范围确认（2026-09-23）

经会话内穷尽全部可脚本化手段（mingw-w64 交叉工具链、Docker 容器 ibus
环境、TIS 注册、系统安装、哈希比对均已完成）后确认：以下三项联调收尾
**移出本轮验收范围**，其余任务全部完成并验收。

| # | 移出项 | 硬门控条件 | 恢复方式 |
|---|---|---|---|
| 1 | macOS 打字闭环验证 | 需人工在系统设置添加 ChaosetIME 并实际打字（已实测 `AXIsProcessTrusted=false`，按键模拟被系统策略禁止，无法脚本化） | 按 shims/macos/README 打字验证 |
| 2 | Windows TSF 真机联调 | 需 Windows 环境（COM 注册/按键/渲染） | 已于 2026-10-30/31 在 Windows 11 真机完成首轮验证（开始菜单/搜索候选窗、安装器升级、注册表类别）；游戏/全屏、UWP/多会话、无障碍、per-monitor DPI 仍待验，按 shims/windows/README |
| 3 | Linux 桌面闭环 + 候选窗贴图接入 | 需 Linux 桌面环境（DBus 层端到端已在容器验证） | 按 shims/linux/README |

各项的具体实现位置、部署命令、诊断工具均已在对应 README 与脚本中就位，
条件满足后无需额外准备即可继续。
- **重构(引擎)：Rust 联想（Associate）迁移——按上屏词的字续接 + 上下文共现排序**：
  - 主词典新增 `query_word_prefix`（Go `QueryWordPrefix`）：只续接「以刚上屏词开头且更长」的词条，按词频降序；
  - 用户词库新增 `QueryPhrasesByWordPrefix` / `QueryWordsByWordPrefix`（词组进化 + 调频词两张表）与 `score_full`（Words 调频 + Bigram 共现，与组词打分的 `score()` 口径分开）；`UserRecord` 补 `sylls` 字段（serde 默认，向后兼容）；
  - 会话侧补 `prev_sylls`（Go `s.prevSylls`）与 `infer_segs` / `entry_segs` / `strip_at_segment_boundary`：**整句式学习记录只在用户确认过的分段边界剥前缀**（项目红线：上屏「今天」给「星期一」，上屏「今」绝不给「天星期一」）；
  - 排序与去重对齐 Go：字前缀层优先、层内按加成后得分降序、同分按文本稳定排序，上限 30；
  - 差分场景一致 27→28（`associate-basic` 由「错误地按拼音前缀返回」修正为与 Go 相同的空结果）；语料 5,655 场景指标与本轮前完全一致（组词路径未受影响）；
  - 新增双向回归：`test_associate_word_prefix`（同字续接、不混入同音其它字、词频降序）与 `test_associate_sentence_only_at_segment_boundary`（边界给续接、非边界不给整词尾巴）。
