# chaoset-ime

跨平台中文拼音输入法（Windows / macOS / Linux）。

## ⚡ 极简新手安装指南（零门槛·傻瓜式一键使用）

> **致普通用户**：输入法面向的是广大日常电脑用户。Windows/Linux 直接双击安装包即可；
> **macOS 推荐用「终端一行命令」安装**（见下方，macOS 自带 bash，不需要开发者账号、
> 不需要 `sudo`，也不会被 Gatekeeper 拦截）。`.zip` / `.tar.gz` 是开发者或绿色便携的
> 高级选项。

| 操作系统 | 推荐安装方式（小白首选） | 极简安装步骤 | 切换输入法快捷键 |
| :--- | :--- | :--- | :--- |
| **Windows** (10 / 11) | [`ChaosetSetup.exe`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 双击 → UAC 点「是」→ 一直「下一步」。**若 SmartScreen 提示未知发布者：点「更多信息」→「仍要运行」**（仅首次；免费签名证书尚未配置） | `Win + 空格` |
| **macOS** (15+) | **终端一行命令**（见下方） | 打开「终端」→ 粘贴一行命令 → 安装器自动打开 →「一键安装」。不需要开发者账号 / `sudo`，不会被 Gatekeeper 拦截 | `Ctrl + 空格` 或 `Fn / 🌐` |
| **Linux (Ubuntu / Debian / Mint)** | [`chaoset_amd64.deb`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 双击 `.deb` → 应用中心点「安装」；装完后在系统「键盘 → 输入源」添加 **Chaoset Pinyin**（列表暂时没有就注销重登一次） | `Super + 空格` 或 `Ctrl + 空格` |
| **Linux (Arch / Manjaro / EndeavourOS)** | [`chaoset-ime-x86_64.pkg.tar.zst`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 应用商店（Pamac/发现）双击安装；装完后在「键盘 → 输入源」添加 **Chaoset Pinyin** | `Super + 空格` 或 `Ctrl + 空格` |

> **Windows 安装不会关闭已打开的程序**：安装器只替换文件，不会强杀浏览器/
> 编辑器/聊天软件或开始菜单等系统宿主；已有导入词由 Go 导入器合并保留。已打开
> 程序重新打开后加载新版。daemon 在首次输入时以当前用户身份自动启动（命名管道，
> 无需管理员权限）；系统宿主在注销/重启后刷新。若切换后暂时没有候选窗，先注销
> 并重新登录一次。

> **macOS 推荐安装（终端一行命令，无需开发者账号）**：打开「终端」（聚焦搜索 `Terminal` /「终端」），粘贴并回车：
>
> ```bash
> /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/ShanWuYinShe/chaoset_ime/main/shims/macos/install-online.sh)"
> ```
>
> 脚本会下载最新 `chaoset-macos.zip`、解包并清除 `com.apple.quarantine`，把「Chaoset 安装与设置」
> 装到 `~/Applications` 后自动打开；在窗口里点「一键安装 / 更新」即可。全程不需要 `sudo`，
> 也不会出现「Apple 无法验证开发者」提示。
>
> **离线/手动方式**：从 Release 下载 `chaoset-macos.zip` 解压后双击 `ChaosetSetup.app`。
> 浏览器下载的 zip 会带隔离标记，首次打开若被拦截：右键安装器 →「打开」（仅首次），
> 或到「系统设置 → 隐私与安全性 → 安全性」点「仍要打开」。提示针对的是 `ChaosetIME.app`
> （输入法本体）而不是安装器，请先运行解压目录里的 `ChaosetSetup.app`。
>
> **卸载**：macOS 打开「Chaoset 安装与设置」→「卸载」；Windows 在「设置 → 应用」卸载；Linux 用系统软件中心或包管理器卸载。
>
> 📦 **高级选项**：`chaoset-windows-x64.zip` / `chaoset-linux-x86_64.tar.gz` 是绿色或排障用的高级包，需要管理员权限或命令行；`chaoset-macos.zip` 是 macOS 的离线/手动备选（推荐用上面一行命令）。AUR/源码用户见 [GitHub Releases](https://github.com/ShanWuYinShe/chaoset_ime/releases)。

---

## 架构概览

架构：**Rust 引擎 + 守护进程（daemon）+ 各平台原生薄壳**。拼音/联想算法只写一份
在 `crates/chaoset-engine/`；平台壳只负责「按键转发 + 文本上屏 + UI」。三端壳都通过 IPC 连
daemon（Linux 为原生 C++/libibus 壳 + Unix socket；macOS 为 Swift IMK；
Windows 为 C++ TSF）。该形态已被业界验证（小狼毫的 TSF DLL + WeaselServer、
搜狗的多进程形态、macOS InputMethodKit 本身就是进程间模型）。

```
┌─────────────── chaosetd (Rust，crates/chaosetd) ───────────────┐
│  引擎：拼音切分 / 词典检索 / 候选排序 / 词组进化自学习  │
│  ipc  ：length-prefixed JSON（Unix socket / 命名管道）      │
└───────┬───────────────────┬───────────────────┬────────┘
        │ macOS             │ Windows           │ Linux
        │ Swift IMK 薄壳    │ C++ TSF 薄壳      │ C++/libibus 薄壳
        │ （IPC）           │ （IPC）           │ （Unix socket）
```

## 与 Squirrel / Rime 的关系（本项目不含其代码）

本项目是**从零实现**的：Rust 引擎（`crates/chaoset-engine/`）、IPC（`ipc/`）、视觉参数真源
、三端薄壳（`shims/`）全部为本仓库自研代码，
`git ls-files` 中没有任何 Squirrel / librime 源码，也没有引用其图标素材。
来自第三方的只有**数据**：

- OpenCC 简繁词典（Apache-2.0，编入 `crates/chaoset-engine/src/data/s2t/`，见其 README）；
- 可选导入的 CC-CEDICT 词库（CC-BY-SA 4.0，仓库不分发，由用户自行下载后合并）；
- 内置常用词库 `data/dict_common.tsv`：由 `cmd/chaoset-dictgen` 按「常用性门槛」
  流水线生成——词表来自《现代汉语常用词表》+ HSK 3.0 + 高频用法 + 现代热词补充表，
  词频来自 [wordfreq](https://github.com/rspeer/wordfreq) 中文词频（**数据 CC BY-SA 4.0**）
  与 [THUOCL](https://github.com/THUNLP/THUOCL)（MIT），拼音来自
  [phrase-pinyin-data](https://github.com/mozillazg/phrase-pinyin-data) /
  [pinyin-data](https://github.com/mozillazg/pinyin-data)（均 MIT）。它是本仓库的
  **生成产物**而非拷贝的词库文件，再生成方式见「内置词库与再生成」。
  **代码 MIT，生成的词库数据 CC BY-SA 4.0**（署名见 `data/NOTICE.md`）；
- `crates/chaoset-engine/src/shuangpin.rs` 的双拼键位按 Rime schema 的 speller algebra 规则
  **转写生成**（表由本项目正向生成并做完整性测试，不是拷贝实现）。

> 排障提示：系统「输入法」列表里的**鼠须管**（`im.rime.inputmethod.Squirrel`）是
> 官方 Rime/Squirrel 应用，**不属于本项目**；本项目不复用其代码、名称或图标。

## 目录结构

```
crates/chaoset-engine/  核心引擎（纯 Rust 逻辑，不依赖任何 OS API/UI，全部可单测）
  src/syllable.rs       无声调标准音节表（ü 以 v 输入：nv=女、lve=略）
  src/dict.rs           音节 Trie 词典 + TSV 词库解析 + 多词库合并加载
  src/session.rs        组词会话状态机（输入/退格/重置/选候选/词组进化/联想）
  src/punct.rs          中文标点映射（直映表 + 引号开闭配对 + 循环）
  src/fuzzy.rs          模糊音等价展开（zh/z、n/l、an/ang 等，默认关闭）
  src/s2t.rs            简繁字词映射（数据在 src/data/s2t/，默认关闭）
  src/shuangpin.rs      自然码/微软/小鹤双拼键码管线（默认关闭）
  src/userdict.rs       用户数据：调频记录、合成词组、上下文共现
  tests/dict_contract.rs     内置词库严格契约（CI 门禁）
  tests/parity_scenarios.rs  41 个冻结回放场景（行为回归基线）
crates/chaosetd/        守护进程（Rust；-dict 可多次叠加）
ipc/               IPC 帧协议与客户端（client 实现 ipc.Controller；服务端在 Rust）
docs/ui-parity.md  候选窗视觉基准与三端差异表（期望值同时内联在 visual-parity.sh）：
                   无运行时消费者——实际渲染是 macOS CandidatePanelView.swift 与
                   Windows candidate_window.cpp 两份实现；三端布局/色值期望由
                   shims/macos/tests/visual-parity.sh 机器断言；daemon 不提供贴图
                   渲染链路
cmd/
  chaoset-cli/       终端测试台（连接 daemon 的 IPC 客户端）
  chaoset-import/    热词导入工具（合并输出 TSV）
  chaoset-dictgen/   内置词库生成工具（常用性门槛词表 + wordfreq/HanLP 词频 → TSV）
  chaoset-userdict/  用户词典管理工具（统计/清空/导出，IPC 或本地文件）
data/
  categories/        中文互联网分类词库（流行梗/社交互动/职场/游戏二次元/数码AI等 8 大分类）
  dict_common.tsv    内置常用词库（cmd/chaoset-dictgen 生成产物，勿手工编辑）
  dict_extra.tsv     手工补充词表（口语高频词/常用组合）
  dict_hot.tsv       手工补充词表（现代/热词，随版本更新，聚合自 categories/）
  dict_seed.tsv      种子词库（演示/回归用的小词库，词 \t 拼音 \t 词频）
  NOTICE.md          词库数据许可与署名说明（数据 CC BY-SA 4.0，代码 MIT）
shims/
  macos/             InputMethodKit 薄壳（Swift；候选窗为壳内 AppKit 原生自绘，见其 README）
  windows/           TSF COM 薄壳（C++；命名管道；壳内 GDI 自绘候选窗）
  linux/             原生 C++/libibus 薄壳（Unix socket 连 daemon；候选窗为 ibus 原生面板）
```

## 越用越顺手：四层自学习

1. **调频**：每个词记住上屏次数与最近使用时间，
   `得分 = 次数 × 500 × 时间衰减`（<1 天 ×1.0 / <7 天 ×0.6 /
   <30 天 ×0.3 / 更久 ×0.1）——最近常用的词自动往前排。
2. **上下文（bigram）**：记录「前一个上屏词 → 当前词」共现，
   顺着上下文的候选每次共现加 250 分（封顶 2 次）。
3. **词组进化**：相邻两次上屏的组合（如分字打出「键盘」「鼠标」）
   共现满 2 次且主词典没有该词时，自动固化为用户词组（≤4 音节），
   之后整词输入 `jianpanshubiao` 直接出「键盘鼠标」。
4. **整句输入习惯**：多字拼音没有合适的整体候选时，用户
   可以逐段选择（如 `jintianxingqiyi` 选「今天」→ 剩余 `xing'qiyi` 再选
   「星期一」）；整串拼完后，各段拼成的整句会记为**带分段**的习惯记录
   （5 音节以上也能学），下次完整输入同一串时整句候选被调频加权排前。
   **但绝不整词贪婪匹配**：整句记录只在完整输入时作为整句候选，部分输入
   （`jin`/`jintian`）不出现整句候选；联想也只在用户确认过的**分段边界**
   剥前缀——上屏「今天」会联想「星期一」，上屏「今」绝不会给出「天星期一」，
   所以学过整句后「只想输入今天」的路径完全不受影响。缺少分段信息的学习记录
   用主词典「分段数最少」DP 推断边界（不是最长词贪心）；推断不出的按普通词处理。
   学习数据由 daemon 每 2 秒批量原子落盘（temp+rename），正常退出前同步落盘；异常强杀最多丢最近 2 秒（Windows 无优雅退出，只靠定时器）。

## 中文标点

标点键由引擎统一映射（`crates/chaoset-engine/src/punct.rs` 的 `is_punct_key` 与标点状态机），三端壳只转发按键：
组词中打标点先上屏首选候选再出标点（`nihao` + `,` → `你好，`）；空组词直接映射
（`,` → `，`、`\` → `、`、`<` → `《`）；双/单引号各自按开闭交替配对；
句号/逗号同键连按在「全角→半角」间循环（。→.→。，，→,），任何其他
输入/上屏即回到首项。组词中的 `'` 仍是音节分隔符，不作为标点。
完整映射表见 `crates/chaoset-engine/src/punct.rs`。

## 输入预测（末音节续接）

用户在打字时，**每个音节的最后一个字母之前都处于「没打完」状态**——这是输入法
最高频的输入形态。普通匹配容易给出巧合碎词：`zhonggu` 出「中古」、
`beijin` 出「被进」、`daxu` 出「大需」，真正想打的「中国 / 北京 / 大学」
反而找不到。本项目把末段当作**待补全的音节**去词典续接（`Dictionary::query_continuation`）：

| 输入 | 普通匹配首选 | 续接首选 | 续接依据 |
|---|---|---|---|
| `zhonggu` | 中古 | **中国** | gu → guo |
| `beijin` | 被进 | **北京** | jin → jing |
| `daxu` | 大需 | **大学** | xu → xue |
| `bianchen` | 边臣 | **变成** | chen → cheng |
| `liangha` | 两哈 | **良好** | ha → hao |
| `zhiyo` | 之哟 | **只有** | yo → you |
| `lilia` | 里俩 | **力量** | lia → liang |
| `quanmia` | 全米啊 | **全面** | mia → mian |
| `shangmia` | 商谜啊 | **上面** | mia → mian |

排序规则没有人为规定谁优先，而是让「完整解析」与「续接解析」**同层竞争**，
由 `词频 - 30×补全字符数` 决定（参数 `continuationPenalty`）：

- 中国(85) 补 1 个字母扣 30 后 55 仍高于 中古(36) → 残缺输入出「中国」；
- 一霎(30) 对比 以上(73)：`yisha` 本身是完整输入，首选仍是「一霎」。

因为末音节既能当「已打完」也能当「还差几个字母」，这两种解析在输入串上完全
无法区分；用统一的代价函数处理，等价于真实输入法的「拼音格 lattice + 语言模型」，
不会出现「按哪个键都要猜用户意图」的特判。

只有一种情况会强制续接领跑：输入**无法**被切分成干净音节序列时
（`quanmia` 只能切成 `quan'mi'a`），同级候选全是「全米啊」这类碎片假词。

### 多音节长输入：真实词条优先，组合候选只在末尾

多字拼音没有单条词库词条时，**首屏候选只来自已存在的词**；多词 DP 拼合只允许
出现在列表末尾的组合候选里，不允许占据首屏。候选来源与优先级：

- 词典词条：精确命中、末段续接、前缀续接、逐级回退得到的前缀词；
- 用户词库 / 学习记录（含用户逐段拼出的带分段整句记录）；
- **任何时候都可以逐字挑选**：任何多段输入（全拼/混拼/简拼）都会在列表末尾
  追加首段全量单字（至多 300 个，覆盖当前词典全部同音字，如 yi 141；
  来自词典/用户词库真实单字），选字后剩余
  输入继续组词（`shipei` 选「适」剩 `pei` 再选「配」；`kkdz` 选「烤」剩
  `k'd'z`），词库没有的词也能逐字拼出来并学习。整词/组合候选仍在前面，
  单字块收尾（`rankSingleBackfill` = `rankComposition+16`，纯单字收尾，
  之后不再出现词组），不改变首选与原有排序。错拼输入除外：纠错候选是主路径，
  单字兜底不参与「纠错固定第 3 位」的排序。

「最长已有词前缀 → 更短前缀 → 首音节单字」描述的是**逐级回退的匹配顺序**
（长输入优先出最长前缀词），不是最终排序的全序；完整排序键见下方「引擎候选
规则速览」。例：`zhonghuarenmingongheguo` 首选「中华」，选掉后剩余串再给
「人民」、最后「共和国」；`woxiangqubeijingdaxue` 没有前缀词组，逐字给
「我/想/去」，剩余 `beijingdaxue` 再整词出「北京大学」。

「去掉拼合」不等于「去掉 DP 匹配」：词库里**真实存在**的词条（以及用户学过的
整句记录）仍会用「输入连写 == 词条标准拼音连写」的 DP/字典树匹配兜底
（`Dictionary.QueryJoinedPinyin` / `UserDict.QueryJoinedPinyin`），避免音节切分
枚举上限把词库里有的长词漏掉（例：西安交通大学 xi'an'jiao'tong'da'xue 连写输入
xianjiaotongdaxue）；这一步只沿词库已有词条边前进，不会造词。单字候选始终保留
（首音节层），长输入也可以单字逐字挑。

**组合候选（只在列表末尾）**：词库里存在「我们」「的」但没有「我们的」词条时，
正确输入 `womende` 仍在候选末尾额外给出「我们+的」=「我们的」；`changshuru`
末尾给出「长+输入」=「长输入」，`tuzhong` 末尾给出「图+中」=「图中」。约束：

- 每一段都必须在词典/用户词库中真实存在（引擎不造词）；
- 层级为 `rankComposition`（=`rankBackoff+64`），排在**所有**真实词条与逐级
  回退碎片之后，不改变任何原有候选的先后；
- 错拼修正路径（手滑打错字母，如 chagnshuru）是例外：只有在该输入**没有任何
  整词级纠错命中**时才把组合候选提到精确层（避免「古巴+西」压过真正纠错出来的
  「关系」），最多提升 8 个；候选带整串消费标记，选中即上屏；
- 单字候选始终保留，组合候选不妨碍逐字挑选。

回归测试把边界锁死：`crates/chaoset-engine/src/session.rs` 的单元测试（真实词条优先 +
组合只能由已有词组成 / 错拼提升 / 用户历史优先），以及 `tests/parity_scenarios.rs`
的 41 个冻结回放场景（候选与打分整体行为）。

固定口径扫描的 top1/top3 见下方「实测数据」；热路径为微秒级。

### 引擎候选规则速览（当前）

- **候选来源**：词典 / 用户词库 / 学习记录中的真实词条；不做多词拼合造词。
  首屏不含组合产物，组合候选只在列表末尾（`rankComposition`）。
- **生成管线**（从按键到候选列表）：
  1. 输入切分：全拼 / 简拼 / 混拼 / 歧义多路径（`tokenizeChunk`、
     `enumerateAllTokens`），模糊音开启时展开变体；
  2. 连写 DP：`QueryJoinedPinyin` 按「输入连写 == 词条拼音连写」匹配真实长词；
  3. 常规查询：词典 + 用户词库的精确匹配、前缀词、末段续接
     （`QueryContinuation`，得分 = 词频 + 用户加权 − `continuationPenalty` ×
     补全字符数）、逐级回退前缀（每次丢弃末尾一段，最多 6 段）；
  4. 组合候选：只由已有词段组成，`rankComposition` 层；
  5. 单字兜底：任何多段非错拼输入，在列表末尾追加首段全量单字
     （≤`singleSyllableExactLimit`=300，词典 + 用户词库），`rankSingleBackfill` 层；
     简拼/混拼的**词**候选 exact 上限仍是 120（`initialsExactLimit`，避免 zg 等
     输入候选页膨胀）；单段声母（y/j/zh…）与单音节全拼一样走 300 的单字上限；
  6. 错拼修正：单次编辑 / 连击粘键折叠 / QWERTY 邻键 / 反序归位，命中词按
     「纠错词频 ≥ 常规候选词频 × `typoPromoteRatio`」提升；
  7. 排序 → 纠错固定第 3 位 → 分页（每页 `pageSize`=6）。
- **匹配层级**（数字越小越靠前）：`rankExact`（全拼整词）→
  `rankInitialExact`（简拼/混拼整词）→ `rankContinuation`（末段续接）→
  `rankInitialPrefix`（简拼偏长词）→ `rankPrefix`（全拼偏长词）→
  `rankBackoff+drop`（逐级回退）→ `rankContinuationLead`（无法干净切分时的
  续接兜底）→ `rankTypoFix`（纠错初始层）→ `rankComposition`（已有词组合）→
  `rankSingleBackfill`（首段全量单字）。
- **排序键**（从高到低，改动排序必须保持）：
  1. 常规层优先于逐级回退层（`rank < rankBackoff`）；
  2. **超段仲裁（仅全拼输入）**：候选音节数多于输入合法切分的最大段数
     （`maxInputSegs`）时排后。显式分段 `tu'an`（2 段）下，用户学过的
     「图案」（2 音节）不会被当成超段，可按用户输入优先于默认单字「图」；
     非用户候选仍按首选切分（`inputSegs`）严格判超段。简拼/混拼的 token 数
     不是音节数，**不做**超段仲裁；
  3. **用户输入 > 默认字频**：`FromUser` 候选优先（学过的词 / 单字 / 整句记录
     压过静态词频；与词库冲突时用户历史优先）；
  4. 匹配层级（rank）；
  5. 综合得分（词频 + 用户加权 − 续接代价）、词长、字典序。
  单段输入（`hao`/`tuan`）不出现词语，只出单字；超段仲裁只影响显式分段
  （`tu'an`）与多段输入。
- **单段输入只出单字候选**：单音节全拼（`diao`/`zhong`/`jie`）与单字母声母
  （`d`/`y`）的拼音匹配层只允许单字（≤300，词典 + 用户词库），
  **不出现任何词语**：既不出前缀扩展词（`diao` 的「调用」、`jie` 的
  「结果/节约」），也不出连写恰好等于输入的全匹配词（`jie` 的「饥饿」、
  `xian` 的「西安」、`tuan` 的「图案」）。要打这些词，用**显式分段**
  （`ji'e`/`xi'an`/`tu'an`）、完整多音节输入（`diaoyong`/`zhongguo`）
  或简拼（`dy`/`zg`）；上屏后的**联想**是独立路径，不受此限制。
  `zh`/`ch`/`sh` 是多字母简拼，仍按词候选处理。
- **连写 DP 匹配**：`QueryJoinedPinyin` 只为「词库里真实存在的整词」兜底，
  不依赖音节切分枚举上限，也不会造词（西安交通大学这类长词不再漏）。
- **错拼修正**：单次编辑修正出的词典整词 + （相邻互换/删除类）修正串的分词
  组合；仅当该输入没有任何整词级纠错命中时，组合候选才提升到精确层（最多 8 个）。
  反序双字母（gn/hz/hc/hs/ev 强制；ao/ua/ue 软纠错）走最高置信度归位路径；
  高置信度纠错候选固定排在候选列表第 3 位起；错拼输入不追加单字兜底。
- **学习数据删除**：IPC `userDelete` / `chaoset-userdict delete`；候选窗右键
  「删除该词」只删本机学习记录，不动内置/导入词库。
- **词库**：内置 65k 条常用性门槛词库（方案 A，数据 CC BY-SA 4.0，见
  `data/NOTICE.md`）；手工补充表 `data/dict_extra.tsv`（口语）与
  `data/dict_hot.tsv`（现代/热词，随版本更新）。
- **回归测试**：候选来源总闸门、错拼/组合、用户历史、单字兜底、歧义切分、连写匹配
  都在 `crates/chaoset-engine/src/session.rs` 的单元测试里；词库契约在
  `crates/chaoset-engine/tests/dict_contract.rs`。

### ü 的两种写法

音节表与小词库统一用 `v` 记写 ü（`nv`=女、`lve`=略），但用户键盘上更常敲 `u`。
现在 `lue`/ `nue` 会归一为 `lve`/`nve`（`crates/chaoset-engine/src/syllable.rs` 的 `canonical_syllable`），
两者都能打出「略 / 虐」。`jue/que/xue/yue` 本来就以 u 记写，不在别名表里，不受影响。

### 实测数据（固定口径）

下列数字来自内部固定口径扫描（扫描器不随仓库分发）；日常回归以 `crates/chaoset-engine` 的测试
与基准为准，改排序前后应使用同一口径重新取数，不要直接跨版本比较绝对值：

- 词条池：`data/dict_common.tsv` 中 2–4 音节、词频 ≥30、词长 == 音节数
  （当前 26,766 条）；
- 确定性采样：按 (词, 拼音) 排序后每 25 条取 1 条（当前 1,071 条）；
- 翻页取全部候选，命中判定同时匹配词与音节序列（`Text` + `Syllables`）。

当前基线（固定口径扫描）：

| 场景 | top1 | top3 | miss | rankAvg |
|---|---|---|---|---|
| `full` | 80.02% | 97.67% | 0 | 1.3 |
| `tail1` | 47.38% | 77.20% | 2 | 2.9 |
| `tail2` | 30.17% | 57.12% | 6 | 4.2 |
| `initials` | 9.99% | 17.46% | 67 | 33.1 |
| `mixed` | 30.16% | 57.70% | 10 | 4.4 |

关键取舍：`continuationPenalty` 取值经过扫描（20 / 25 / 30 / 35 / 40 全跑过），
30 是唯一同时保住完整输入首选率（不降）与残缺输入首选率的点；
性能无实质变化（`BenchmarkComposeCommonDict` 约 70–120µs/键，本机参考；
判断回归用同一环境前后对比）。

## 错拼修正（手滑打错一两个字母）

打字手滑是常态：相邻两键颠倒（`zhogn`）、多敲一个字母（`zhonng` / `niihao`）、
漏敲一个字母（`zhonguo`）、敲错一个键。普通匹配对这些输入要么给出一堆碎片
假词（`zhogn` → 「中欧国内」），要么直接无候选。

现在对输入做**单次编辑与粘键折叠**修正后重新查询（`crates/chaoset-engine/src/syllable.rs` 的 `typo_corrections`），覆盖六类手误与拼音习惯：

| 类型 | 例子 | 修正后 | 结果 |
|---|---|---|---|
| 相邻颠倒 | `zhogn` | `zhong` | **中** |
| 多敲一个字母 | `zhonng` / `niihao` | `zhong` / `nihao` | **中** / **你好** |
| 连击粘键折叠 | `niihaoo` / `zhoonng` | `nihao` / `zhong` | **你好** / **中** |
| 漏敲一个字母 | `zhonguo` / `jisuanjii` | `zhongguo` / `jisuanji` | **中国** / **计算机** |
| QWERTY 邻键敲错 | `yishrng` / `diannap` / `nihap` | `yisheng` / `diannao` / `nihao` | **一声** / **电脑** / **你好** |
| 省写与撮口呼习惯 | `dueibuqi` / `jvece` / `xionmao` | `dui'bu'qi` / `jue'ce` / `xiong'mao` | **对不起** / **决策** / **熊猫** |
| 双字母反序（强制归位） | `dign` / `hzong` / `hcang` / `hsang` / `lev` | `ding` / `zhong` / `chang` / `shang` / `lve` | **定** / **中** / **常** / **上** / **略** |
| 韵母反序（软纠错） | `hoa` / `doa` / `hau` / `xeu` | `hao` / `dao` / `hua` / `xue` | **好** / **大** / **化** / **学** |

其中 **gn→ng、hz→zh、hc→ch、hs→sh、ev→ve** 在普通话拼音音节内部几乎不出现，
走独立的最高置信度路径（`forcedMistakePairs`）：`misspelledFromPatterns` 在
**无法完整切分**时遇到这些组合才判定手误，归位修正排在 `typoCorrections` 最前；
`mistakeInput` 时**只把归位修正无条件提升到精确层**（不受「纠错词频须达到常规
候选 2 倍」门槛约束，避免 `hzong` 的常规混拼「汇总」、`chagn` 删 g 的「产」或
用户学习词压过正确修正），通用编辑保持低层兜底（保证单字仍可选）。同一输入
多处连错会一起翻回（`dignhzi` → `dingzhi`）。

**跨音节边界不算反序手误**：`gongneng` = `gong`+`neng` 的 `gn` 是合法拼写；
纯声母简拼 `gn`（= gong+neng）同样合法。整串能切成完整音节、或所有切分路径
都是纯声母碎片时，不触发反序手误判定，常规候选与单字兜底照常工作（回归：
`crates/chaoset-engine/src/session.rs` 的跨音节边界单元测试）。

韵母级反序 **ao/oa、ua/au、ue/eu** 在拼音音节里不出现，但可能与「合法混拼」同形
（如 `oa` = o+a），归为软纠错（`softMistakePairs`）：只在纠错路径里作为最高
优先级修正，不强制触发、也不无条件提升——`hoa`→好、`doa`→大、`hau`→化、
`xeu`→学。

这些组合不是拍脑袋清单，而是由音节表推导：**所有「正序在拼音里出现、反序从不
出现」的双字母组合**中，剔除反序形态本身可能是合法混拼的组合（如 `ab`/`ba`、
`id`/`di`——`ab` 可以是 a+b 声母混拼），只保留语音单位（zh/ch/sh/ng/ve）与
韵母级（ao/ua/ue）。

### 纠错候选固定第 3 位

错拼修正候选（已提升到精确层的高置信度纠错）固定排在候选列表**第 3 位**起，
用户可以直接按 `3` 选中，不必每次找位置。规则：

- 只搬高置信度纠错（`typoFix` 且已提升到精确层）；`tailSuspicious` 等低置信度
  纠错仍留在列表末尾，不干扰正常输入；
- 正常候选保持原有相对顺序：前两位正常候选 → 纠错候选 → 其余正常候选；
- 如果该输入没有任何纠错候选，列表完全按正常匹配排序；
- 如果纠错候选之外没有足够的正常候选（只有纠错结果，或候选总数不足 3 个），
  纠错从第 1 位开始（没有可填充的正常候选）。

回归测试：`crates/chaoset-engine/src/session.rs` 中 `promote_typo_corrections` 的单元测试。

**刻意不做**：`ai/ia`、`ui/iu`、`ou/uo`、`ei/ie`、`an/na`、`en/ne`、
`in/ni`、`un/nu` 这类**两侧都合法**的相邻互换（如 `shuoji` 对 `shouji`、
`mieyou` 对 `meiyou`、`zoutian` 对 `zuotian`）不做强行纠错——它们与正常输入/
合法混拼同形，强行纠正会误伤正常候选（mixed 首选率明显下降）。这类错误
仍可通过通用单次编辑在「确实像手误」的输入上被修正。

### 判据与置信度分层

- **QWERTY 物理按键邻近建模**：手指物理按偏产生的相邻键替换（如 `e` $\rightarrow$ `r`、`o` $\rightarrow$ `p`）保留完整词频；跨键区非邻近替换（如 `r` $\rightarrow$ `a`）扣除 `15` 分惩罚分且不参与直接晋升，彻底杜绝远端巧合高频词反压真实目标词。
- **拼音习惯自动规范**：`uei` $\rightarrow$ `ui`（`shueijiao` $\rightarrow$ `睡觉`）、`iou` $\rightarrow$ `iu`（`niourou` $\rightarrow$ `牛肉`）、`uen` $\rightarrow$ `un`（`chuentian` $\rightarrow$ `春天`）、`j/q/x/y + v` $\rightarrow$ `u`（`xvesheng` $\rightarrow$ `学生`）。

### 关键取舍：绝不污染正常输入

手误与**合法简拼/混拼在串形态上高度同形**：
- **触发条件**：无法完整切分（`zhogn`）、切分中段存在孤立声母（`zhengchagn`），或末段存在孤立声母且无常规词匹配（`tailSuspicious`，如 `nihap`）；
- **防截断护栏**：末段声母形态纠错强制要求 `typoLen >= len(input)`，**严格禁止删除末位声母**——既能纠正 `nihap` $\rightarrow$ `你好`，又确保 `zhongg`（中国）、`chifanl`（吃饭了）、`shangxiaj`（上下级）等合法混拼与前缀输入首选完全不受干扰。

### 实测与性能

正常输入指标不因纠错下降；当前基线以固定口径扫描为准（见「实测数据」）。

性能：纠错只在「切不动」时触发，正常打字路径零额外开销
（`BenchmarkComposeCommonDict` 约 70–120µs/键，本机参考）。触发纠错的输入
（如 `zhogn`）本身就要 2ms 量级的切分扫描，纠错只在其上增加约 6%；
查询条数由 `typoMaxQueries` 封顶：生成便宜、每条 Trie 查询较贵，160 条会让
`zhonguo` 从 0.8µs 涨到 25ms，压到 32 条后回到百微秒级。

## 模糊音

默认关闭：设置窗「模糊音」开关（写入平台设置，daemon 启动时读取），或 daemon 启动参数 `-fuzzy`：

```sh
bin/chaosetd -fuzzy          # 或设置窗「模糊音」开关后重启 daemon（持久生效）
```

规则（微软拼音常用集）：声母对 zh/z、ch/c、sh/s、n/l；韵母对
an/ang、en/eng、in/ing（含 ian/iang、uan/uang）。输入侧展开等价
音节后并集查询：`ni` 同时出「泥/里」，`zhu` 同时出「主/组」，
声母+韵母可叠加（zhang → zang/zhan/zan）。残缺音节前缀查询不做
模糊；变体组合每路径封顶 8 条。规则表见 `crates/chaoset-engine/src/fuzzy.rs`。

## 简繁转换

默认关闭，daemon 启动参数 `-traditional` 开启：

```sh
bin/chaosetd -traditional       # 候选与上屏均为繁体
```

数据来自 OpenCC 官方词典（Apache-2.0，编入 `crates/chaoset-engine/src/data/s2t/`）：
**词级最长匹配**优先（头发→頭髮、出发→出發、公里→公里——一对多字
的上下文判定由 4.9 万条词组表完成），未命中词组的单字查 4,029 条
单字表（中国→中國、时间→時間），表外字符原样保留。首次使用惰性
解析。数据与许可说明见 `crates/chaoset-engine/src/data/s2t/README.md`。

## 双拼

默认关闭：设置窗「双拼方案」（全拼/自然码/微软/小鹤），或 daemon 启动参数 `-shuangpin zrm|mspy|flypy`：

```sh
bin/chaosetd -shuangpin zrm     # 自然码：两键一音节，nihao = n i h k
bin/chaosetd -shuangpin mspy    # 微软双拼：nihao = n i h k；ing 在 ; 键
bin/chaosetd -shuangpin flypy   # 小鹤双拼：nihao = n i h c；iao 在 n 键
```

键位表不手工维护——分别忠实转写 Rime 官方三份 schema（自然码/
微软/小鹤）的 speller algebra 规则管线，对全部合法音节正向生成两键
码后建反向表，完整性测试保证全表无冲突、逐音节可解码。首键暂存、
成对解码，无效键码对整体丢弃；退格按按键事件粒度整段回滚。
见 `crates/chaoset-engine/src/shuangpin.rs`。

**`;` 键的三端路由**：mspy 的 `ing` 韵母键是 `;`，而 `;` 平时是
中文标点键。引擎在 `Composition` 里下发 `shuangpin` 方案名，三端壳据此把双拼
模式下的 `;` 路由到 `input`（`dpPending` 非 0 时参与成对解码；为 0 时按标点
语义上屏「首选词 + 分号」，文本经 `committed` 返回）；非双拼模式与 Shift+`;`
（`:`）仍走标点。会话级单测遍历三方案全部反向键码锁可达性，防止再出现
「解码层能解、会话层收不到」的断链。

小鹤按 Rime double_pinyin_flypy schema 键位实现；该 schema 与官方
小鹤键位存在出入（ia/ua 键位等），官方键位图为图片无法文本核验，
故以可核验的 schema 语义为准。

## 快速开始（终端测试台）

> 普通用户不需要命令行：联想开关、学习数据统计/清空/导出都在**设置窗口**
> （候选栏右侧齿轮）。下面的 CLI 是开发测试台与高级/排障工具；
> `chaoset-userdict` 仅作为无 GUI 环境/高级用户兜底。

```sh
# CLI 是 daemon 的 IPC 客户端：先起 daemon（另开终端；本地内嵌引擎已移除）
cargo run --release --bin chaosetd --manifest-path crates/Cargo.toml &
# 缺省连接平台默认 socket（与 daemon 同一路径规则）
go run ./cmd/chaoset-cli
# 显式指定 cwd 下的 socket 便于本地调试：
# cargo run --release --bin chaosetd --manifest-path crates/Cargo.toml -- -socket chaoset.sock &
# go run ./cmd/chaoset-cli -socket chaoset.sock

# Windows 命名管道（推荐；daemon/CLI 都按当前用户 SID 自动算管道名）
cargo run --release --bin chaosetd --manifest-path crates/Cargo.toml -- -pipe &
go run ./cmd/chaoset-cli -pipe

# 构建信息（提交号/构建时间）：真机排障第一步，确认装的是不是新构建
cargo run --release --bin chaosetd --manifest-path crates/Cargo.toml -- -version
go run ./cmd/chaoset-cli -version
```

daemon 默认加载内置常用词库 `data/dict_common.tsv`（见「内置词库与再生成」），
想换小词库或叠加自备词库在启动 daemon 时用 `-dict` 指定（可多次）。

测试台操作：输入拼音回车看候选（如 `nihao`、`xian`、`xi'an`、`nih`）；
单独一个数字 `1`–`9` 选择对应候选并「上屏」——与三端壳一致走**渐进上屏**：
只消耗候选音节对应的拼音前缀，剩余拼音自动回灌继续组词（输入
`jintianxingqiyi` 选「今天」后剩下 `xing'qi'yi`，再选「星期一」即拼完）；
`=` 下一页 / `-` 上一页（每页 6 个，超出部分可翻页查看）；标点键（如
`,` `.` `\` `"`）输出中文标点，组词中先上屏首选；`:b` 退格；`:r` 重置；
`:q` 退出。带 `*` 的候选表示被用户数据（调频/词组/上下文/整句习惯）加权过。

试一下词组进化：`jianpan` → `1`、`shubiao` → `1`，分字打两轮「键盘鼠标」，
再输入 `jianpanshubiao`——整词「键盘鼠标」会直接出现在第一位。

试一下逐段拼句：`jintianxingqiyi` 里选「今天」（数字键只吃掉 `jin'tian`，
预编辑剩 `xing'qi'yi`）→ 选「星期一」拼完；两轮之后完整输入
`jintianxingqiyi` 首位就是学过的「今天星期一」。此时只打 `jin` 不会出现
「今天星期一」这类整词候选，上屏「今」的联想也不会弹「天星期一」。

## 词库格式

TSV，每行三列（`#` 开头为注释）：

```
你好	ni hao	99
西安	xi an	70
```

拼音为无声调小写，音节间空格分隔；一字多音写多行；重复词条取较大词频。
非法音节会导致解析报错（防止脏数据静默进入词库）。

**词频的约定取值是 1–99**，内置词库与导入工具都按这个区间生成：

- 运行期 `-dict` 词库（含 `data/` 下两份）走**宽松模式**（见「词库校验」表）：
  坏行跳过并汇总警告、拼音归一化（去声调/ü→v/连写切分）、缺省词频补 20、超过 99 的**压到 99**；
- 构建期**严格模式**只用于内置产物与测试：要求恰好 3 列、每个音节合法、词频为正（`crates/chaoset-engine/tests/dict_contract.rs` 在 CI 严格解析）。

上限之所以是 99 而不是「越大越好」，是因为静态词频只是**基础分**：用户学习权重
按次累加（`crates/chaoset-engine/src/userdict.rs` 的 `USER_BOOST_PER_COUNT = 500`，上下文共现
`BigramBonusPerCount = 250`），**一次上屏就远超整个 1–99 区间**。
所以把上游词频压缩进 1–99 完全够用，且不妨碍「越用越顺手」：选过的词
靠 500×次数×时间衰减排到前面。反过来说，把词库词频写成几百上千既没有收益，
还会让调频效果被静态分淹没。

### 内置词库

仓库自带 `data/dict_common.tsv`（内置常用词库，约 20 万条），是 daemon 与测试台的
默认词库；规模、生成管线、手工补充词表、校验规则与相关测试见
[docs/dict.md](docs/dict.md)。

**许可（重要）**：

- **代码 MIT**（根 `LICENSE`）；生成的 `data/dict_common.tsv` **数据**按
  **CC BY-SA 4.0** 分发，署名 wordfreq（Luminoso）、THUOCL、开放词表等上游，
  详见 `data/NOTICE.md`。修改词库再分发时须继续以 CC BY-SA 4.0 提供数据部分。
- 单字字频源 **Jun Da**：原作者声明仅限研究/教学，商业使用需书面许可——发布前
  应替换为 Unihan `kHanyuPinlu`/`kFrequency`（Unicode License）或取得授权。
- phrase-pinyin-data 的 large_pinyin 由汉典 zdic + CC-CEDICT（CC BY-SA 4.0）等
  来源合并，再分发需保留来源说明。

### 接入 CC-CEDICT 开源词库

`chaoset-import` 与 `-dict` 直接识别 [CC-CEDICT](https://www.mdbg.net/chinese/dictionary?page=cc-cedict)
u8 格式（繁体 简体 [拼音] /释义/，空格分隔），无需转换：

```sh
curl -o cedict.txt.gz \
  https://www.mdbg.net/chinese/export/cedict/cedict_1_0_ts_utf-8_mdbg.txt.gz
gunzip cedict.txt.gz
bin/chaoset-import -o dict-full.tsv data/dict_common.tsv cedict.txt
bin/chaosetd -dict dict-full.tsv
```

CC-CEDICT 约 12 万词条，导入耗时亚秒；词条按固定词频 30 合成（用户
自有词库优先），非汉字词条自动过滤。**许可**：CC-CEDICT 为
CC-BY-SA 4.0——分发合并词库时须保留其署名与相同方式共享，
请一并查阅文件头部声明。

## IPC 协议

帧格式为 4 字节大端长度 + JSON 载荷（上限 1 MiB）。`reset`（清组词与学习上下文：Esc/
失焦/新会话）与 `resetComposition`（只清组词、保留学习上下文：切中英、透传等）的
双轨重置语义是三端壳契约；17 个方法的请求/响应参考、候选窗纯数据契约、传输形态
与命名管道安全描述符见 [docs/ipc.md](docs/ipc.md)。

## 下载与安装（GitHub Release）

推 `v*` tag 时，Release workflow（`.github/workflows/release.yml`）会自动三端打包并创建 Release：

| 平台 | 推荐（普通用户） | 高级/备用（一般不需要） |
|---|---|---|
| macOS | 终端一行命令（`install-online.sh`，见上） | `chaoset-macos.zip`（离线/手动） |
| Windows | `ChaosetSetup.exe` | `chaoset-windows-x64.zip`（需管理员 + install.bat） |
| Linux Debian/Ubuntu | `chaoset_amd64.deb` | `chaoset-linux-x86_64.tar.gz`（需 sudo 脚本） |
| Linux Arch | `chaoset-ime-x86_64.pkg.tar.zst` | 同左，无脚本包 |

每个包都自带**完整的内置常用词库（约 6.5 万条）**，装完即可用，不需要另配词库。

> **查看版本**：三端设置窗底部都会显示 `Chaoset 版本 x.y.z`；升级后先看这里
> 确认装的是新版本，再排查其他问题。Windows 还可以右键 `ChaosetSetup.exe`
> 或安装目录里的 `ChaosetIME.dll` / `chaoset-ui.exe` →「属性 → 详细信息」
> 查看产品名、公司、版权与文件/产品版本，或在「已安装的应用」的 Chaoset
> 条目里看 `DisplayVersion`。版本号统一由 `scripts/version.sh` 注入，源码不写死：
> CI 在 **push tag 触发**时通过 `CHAOS_VERSION_TAG` 注入 tag 号；其他一切构建
> （本地、日常 CI、`workflow_dispatch`、PR）不设该变量，显示「年月日-commit hash」
> （如 `20261106-8b2eb0ca0c38`，有未提交改动带 `-dirty`），便于排障时定位构建日期与提交。

发布包由 CI 在目标平台构建并做启动冒烟：Linux/Windows 包显式指定
`GOOS/GOARCH`，macOS 为 universal，Windows 提供 `ChaosetSetup.exe` 与
`install.bat`，词库数量与架构均在包内容自检中断言。

## 平台壳

| 平台 | 壳 | 状态 |
|---|---|---|
| Linux | ibus engine（原生 C++/libibus 薄壳），Unix socket 连 daemon；候选窗为 ibus 原生 LookupTable | 容器内端到端验证；真机桌面闭环待环境 |
| macOS | InputMethodKit `.app`（Swift），Unix socket 连 daemon；候选窗为壳内 AppKit 原生自绘 | 真机启用；Shift 空按、LaunchAgent `ProcessType=Interactive` QoS 已验证 |
| Windows | TSF COM DLL（C++ 薄壳），命名管道连 daemon；壳内 GDI 原生自绘候选窗 + 合成内预编辑；单例 UI 宿主 `chaoset-ui.exe` 负责托盘中英状态（显示当前输入上下文实际状态）与设置窗 | CI/核心行为验证；开始菜单/搜索候选窗已真机验证；命名管道、安装器、游戏/全屏、UWP/多会话、无障碍与 per-monitor DPI 待真机 |

### 渐进上屏

三端壳对「逐段渐进拼句」采用同一模型：已确认段并入预编辑/合成，整条组词结束才
一次落盘；统一行为矩阵与跨程序兼容性论证见 [docs/ui-parity.md](docs/ui-parity.md)。

> **macOS 输入源标识**：输入源 ID / `CFBundleIdentifier` 为
> `ime.chaoset.inputmethod.ChaosetIME`，输入模式为
> `ime.chaoset.inputmethod.ChaosetIME.Pinyin`（`.Hans` 仅作为兼容别名保留在
> 安装器的语言映射里）。**bundle id 必须含内层 `.inputmethod.` 段**（仅以
> `inputmethod` 结尾的写法 TIS 不收录）。安装用 `shims/macos/install.sh`；
> bundle 有效时 `TISRegisterInputSource` 后 TIS 即可枚举，无需注销重登。
> 真机细节（灰色条目、缓存重建等）见 `shims/macos/README.md`。

## 日志

- Windows：daemon 日志 `%LOCALAPPDATA%\Chaoset\chaosetd.log`，TSF 连接/渲染/
  请求诊断 `tsf.log`，宿主崩溃 `crash.log`；AppContainer 宿主的 TSF 日志实测写到
  `%LOCALAPPDATA%\Packages\<包族>\AC\Chaoset\tsf.log`（如 SearchApp 的
  `Microsoft.Windows.Search_cw5n1h2txyewy\AC\Chaoset\tsf.log`）。托盘/设置窗问题
  同时看单例 UI 宿主 `chaoset-ui.exe` 是否在运行。
- macOS：daemon 日志 `~/Library/Application Support/chaoset/chaosetd.log`；
  壳侧 IPC/候选窗失败写 macOS 统一日志：
  `log show --last 10m --predicate 'subsystem == "ime.chaoset.inputmethod"'`。
- Linux：daemon 日志 `~/.local/share/chaoset/chaosetd.log`；引擎 stderr 前缀
  `chaoset-engine:`，可用 `journalctl --user -b | grep chaoset-engine` 查看。
- 真机排障先跑 `chaosetd -version` / `chaoset-cli -version` 确认构建；
  daemon 只在启动、连接与失败路径写日志，正常按键不产生磁盘 I/O。

## 热词导入

```sh
# 把 Rime dict.yaml / TSV 热词文件与已有词库合并（自动去声调、ü→v、
# 词频压缩到 1..99；非法音节行跳过并汇报）
bin/chaoset-import -o dict-merged.tsv data/dict_common.tsv 热词.yaml

# daemon 叠加加载（-dict 可多次）
bin/chaosetd -dict data/dict_common.tsv -dict dict-merged.tsv
```

重复词条默认取较大词频，`-replace` 以导入词频覆盖。
封闭格式（搜狗 .scel 等）先用开源「深蓝词库转换」转成 Rime/TSV 再导入。

别人编辑好的词库可以直接导入：拼音**连写**（`hundun shurufa`、`haopengyou`）会
自动切成合法音节，只有切不干净的行才跳过并汇报——从各类导出工具拿到的词库
不必先手工规范格式。

### 导入词库：图形界面（推荐）或一条命令（macOS）

安装脚本会把图形化工具放进 `~/Applications/Chaoset.app`：**安装 / 更新输入法**、
**导入词库…**（文件选择器，可多选 Rime dict.yaml / CC-CEDICT txt / TSV）、**重新检查**、
**打开数据文件夹**——全部点界面完成，不必打开终端。

```sh
# 命令行等价物（给习惯终端的用户）
sh shims/macos/import-dict.sh 别人给的词库.yaml     # 可一次给多个文件
# 流程：合并（内置常用词库 + 现用词库 + 新词库）→ 备份旧词库 → 原子替换 →
#       重启 daemon 生效 → 复核 IPC；加 --no-current 则只并入新词库
```

## 默认路径（internal/appdirs）

daemon / Go 工具统一走 `internal/appdirs`；Linux 原生引擎的 `daemon.cpp` 按同一 XDG 口径解析路径：

| 平台 | 数据目录 | 默认 socket | 默认用户词典 |
|---|---|---|---|
| macOS | `~/Library/Application Support/chaoset` | `chaoset.sock`（同目录） | `userdict.json`（同目录） |
| Linux | `$XDG_DATA_HOME/chaoset`（缺省 `~/.local/share/chaoset`） | `$XDG_RUNTIME_DIR/chaoset/chaoset.sock`（无则数据目录） | `userdict.json`（同目录） |
| Windows | `%LOCALAPPDATA%\Chaoset` | 命名管道 `\\.\pipe\chaoset-<SID>` | `userdict.json`（同目录） |

`chaosetd` 的 `-socket`/`-userdict` 与 `chaoset-userdict` 的 `-socket` 默认值都按平台生成；显式传路径仍优先（`chaosetd -userdict ""` 表示不落盘）。

壳/脚本需要同一套路径时调用 `chaoset-userdict -print-paths`（JSON：`data`/`userdict`/`imported_dict`/`socket`），不要各自拼路径。

macOS 的运行组件随输入法 app 安装：`~/Library/Input Methods/ChaosetIME.app/Contents/Resources/`
内含 `chaosetd`、`chaoset-import`、`dict_common.tsv`；Application Support 只放用户数据、
当前词库、socket 与日志，不再单独放 daemon 副本。

## 用户词典管理

```sh
# 经运行中的 daemon 操作（推荐：清空同时生效于内存与磁盘）
# 默认 socket 按平台用户目录生成；自定义 socket 时加 -socket PATH
bin/chaoset-userdict stats
bin/chaoset-userdict clear
bin/chaoset-userdict export -o userdict-backup.tsv

# daemon 未运行时直接操作文件（daemon 运行时清空会被其内存态覆盖）
bin/chaoset-userdict -userdict chaoset-userdict.json stats

# 只导出学到的词组（三列 TSV），回流词库：
bin/chaoset-userdict export -phrases -o learned.tsv
bin/chaoset-import -o dict-merged.tsv data/dict_common.tsv learned.tsv

# Windows 命名管道（daemon 以 -pipe 启动时）：
# bin/chaoset-userdict -pipe stats
# bin/chaoset-userdict -pipe export -o userdict-backup.tsv
```

> **平台限制**：`chaoset-cli` / `chaoset-userdict` 支持 Unix socket（macOS/Linux）与
> Windows 命名管道（`-pipe`）；Windows 包里的同名 exe 连接 daemon 时传输需与 daemon
> 一致（`-pipe`）。daemon 未运行时只能加 `-userdict` 走本地
> 文件模式（daemon 运行时会被其内存态覆盖，建议先停 daemon）。
>
> 导出经 IPC 分页传输（客户端自动拼接），学习数据再大也不会撞 1 MiB 单帧上限。

### 文件损坏时的自动恢复

学习数据是增量优化，**不能因为它让输入法起不来**。daemon 与 ibus 引擎启动时
用宽松加载：`chaoset-userdict.json` 若无法解析（断电写坏、磁盘满、手工编辑、
异常写入残留），会把它改名备份为 `chaoset-userdict.json.corrupt-<时间戳>`
并日志记一条警告，然后以空词典继续启动——不会退出、不需要手工删文件。

备份文件保留现场便于排查；确认无用后可自行删除。读取失败（权限不足等）
仍会报错退出——那类问题改名解决不了，静默忽略只会让用户误以为数据还在。

全量导出为多段 TSV（词 / 词组 / 上下文，含次数与最近使用时间，
按次数降序、UTC 时间，输出稳定可 diff）；空段省略。`clear` 不可撤销，
建议先 `export` 备份。

## 版本

当前版本与安装包见 [GitHub Releases](https://github.com/ShanWuYinShe/chaoset_ime/releases)；
版本历史见 [CHANGELOG.md](CHANGELOG.md)。

## 开发

提交前本地只跑轻量项（测试/构建重活交给 CI，本地跑会占用 CPU）：

```sh
gofmt -l $(git ls-files '*.go')   # 应无输出；make fmt 可自动格式化
go build ./...                    # 秒级
make vet                          # go vet + workflow/版本/GDI/三端一致性静态检查
# 单测、race、三端构建与打包由 CI 执行（见 AGENTS.md「CI 门禁与本地验证分工」）
```

推送后由 GitHub Actions（`.github/workflows/ci.yml`）自动执行上述重活。

