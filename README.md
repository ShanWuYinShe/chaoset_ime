# chaoset-ime

跨平台中文拼音输入法（Windows / macOS / Linux）。

## ⚡ 极简新手安装指南（零门槛·傻瓜式一键使用）

> **致普通用户**：输入法面向的是广大日常电脑用户，不应该要求您懂命令行。
> 每台电脑**只下载下面「推荐安装包」这一列**，按步骤做一次即可；`.zip` /
> `.tar.gz` 是给开发者或绿色便携用户的高级选项，普通用户不用管。

| 操作系统 | 推荐安装包（小白首选） | 极简安装步骤 | 切换输入法快捷键 |
| :--- | :--- | :--- | :--- |
| **Windows** (10 / 11) | [`ChaosetSetup.exe`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 双击 → UAC 点「是」→ 一直「下一步」。**若 SmartScreen 提示未知发布者：点「更多信息」→「仍要运行」**（仅首次；免费签名证书尚未配置） | `Win + 空格` |
| **macOS** (15+) | [`Chaoset-<版本>.dmg`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 双击 DMG → 双击「Chaoset 安装与设置」→「一键安装」。**若提示无法验证开发者：右键安装器 →「打开」→ 再点「打开」**（仅首次） | `Ctrl + 空格` 或 `Fn / 🌐` |
| **Linux (Ubuntu / Debian / Mint)** | [`chaoset_amd64.deb`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 双击 `.deb` → 应用中心点「安装」；装完后在系统「键盘 → 输入源」添加 **Chaoset Pinyin**（列表暂时没有就注销重登一次） | `Super + 空格` 或 `Ctrl + 空格` |
| **Linux (Arch / Manjaro / EndeavourOS)** | [`chaoset-ime-x86_64.pkg.tar.zst`](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | 应用商店（Pamac/发现）双击安装；装完后在「键盘 → 输入源」添加 **Chaoset Pinyin** | `Super + 空格` 或 `Ctrl + 空格` |

> **macOS 首次打开被 Gatekeeper 拦截（无需开发者账号）**：
> 截图里的提示针对的是 **`ChaosetIME.app`（输入法本体）**，不是安装器；请先打开 DMG 里的
> **「Chaoset 安装与设置」**（`ChaosetSetup.app`）。若安装器也被拦截，在 Finder 中右键安装器 →「打开」，
> 或到「系统设置 → 隐私与安全性 → 安全性」点「仍要打开」。
>
> 如果不想处理任何拦截提示，可在「终端」粘贴下面一行（脚本用 `curl` 下载 release 包、
> 清除隔离标记后启动安装器；不需要 Apple 开发者账号，也不需要 `sudo`）：
>
> ```bash
> /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/ShanWuYinShe/chaoset_ime/main/shims/macos/install-online.sh)"
> ```
>
> **卸载**：macOS 打开「Chaoset 安装与设置」→「卸载」；Windows 在「设置 → 应用」卸载；Linux 用系统软件中心或包管理器卸载。
>
> 📦 **高级选项**：`chaoset-windows-x64.zip` / `chaoset-macos.zip` / `chaoset-linux-x86_64.tar.gz` 是绿色或排障用的高级包，需要管理员权限或命令行；普通用户不要下载。AUR/源码用户见 [GitHub Releases](https://github.com/ShanWuYinShe/chaoset_ime/releases)。

---

## 架构概览

架构：**Go 引擎 + 守护进程（daemon）+ 各平台原生薄壳**。拼音/联想算法只写一份
在 `engine/`；平台壳只负责「按键转发 + 文本上屏 + UI」。三端壳都通过 IPC 连
daemon（Linux 为原生 C++/libibus 壳 + Unix socket；macOS 为 Swift IMK；
Windows 为 C++ TSF）。该形态已被业界验证（小狼毫的 TSF DLL + WeaselServer、
搜狗的多进程形态、macOS InputMethodKit 本身就是进程间模型）。

```
┌──────────────── chaosetd (Go，一份代码) ────────────────┐
│  engine：拼音切分 / 词典检索 / 候选排序 / 词组进化自学习  │
│  ipc  ：length-prefixed JSON（Unix socket / 命名管道 / TCP）│
└───────┬───────────────────┬───────────────────┬────────┘
        │ macOS             │ Windows           │ Linux
        │ Swift IMK 薄壳    │ C++ TSF 薄壳      │ C++/libibus 薄壳
        │ （IPC）           │ （IPC）           │ （Unix socket）
```

## 与 Squirrel / Rime 的关系（本项目不含其代码）

本项目是**从零实现**的：Go 引擎（`engine/`）、IPC（`ipc/`）、视觉参数真源
（`skin/`）、三端薄壳（`shims/`）全部为本仓库自研代码，
`git ls-files` 中没有任何 Squirrel / librime 源码，也没有引用其图标素材。
来自第三方的只有**数据**：

- OpenCC 简繁词典（Apache-2.0，内嵌于 `engine/dp_data/`，见其 README）；
- 可选导入的 CC-CEDICT 词库（CC-BY-SA 4.0，仓库不分发，由用户自行下载后合并）；
- 内置常用词库 `data/dict_common.tsv`：由 `cmd/chaoset-dictgen` 按「常用性门槛」
  流水线生成——词表来自《现代汉语常用词表》+ HSK 3.0 + 高频用法 + 现代热词补充表，
  词频来自 [wordfreq](https://github.com/rspeer/wordfreq) 中文词频（**数据 CC BY-SA 4.0**）
  与 [THUOCL](https://github.com/THUNLP/THUOCL)（MIT），拼音来自
  [phrase-pinyin-data](https://github.com/mozillazg/phrase-pinyin-data) /
  [pinyin-data](https://github.com/mozillazg/pinyin-data)（均 MIT）。它是本仓库的
  **生成产物**而非拷贝的词库文件，再生成方式见「内置词库与再生成」。
  **代码 MIT，生成的词库数据 CC BY-SA 4.0**（署名见 `data/NOTICE.md`）；
- `engine/double_pinyin.go` 的双拼键位按 Rime schema 的 speller algebra 规则
  **转写生成**（表由本项目正向生成并做完整性测试，不是拷贝实现）。

> 排障提示：系统「输入法」列表里若出现**鼠须管**（`im.rime.inputmethod.Squirrel`），
> 那是官方 Rime/Squirrel 应用。2026-10-05 排查"macOS 为何不枚举本输入法"时，
> 曾把官方 Squirrel 重签后放进 `~/Library/Input Methods` 作为对照样本；
> 它**不属于本项目**，其代码、名称与图标都不得复用。详见 `shims/macos/README.md`。

## 目录结构

```
engine/            核心引擎（纯逻辑，不依赖任何 OS API，全部可单测）
  syllable.go        无声调标准音节表（ü 以 v 输入：nv=女、lve=略）
  dict.go            音节 Trie 词典 + TSV 词库解析 + 多词库合并加载
  session.go         组词会话状态机（输入/退格/重置/选候选/词组进化）
  punct.go           中文标点映射（直映表 + 引号开闭配对 + 循环）
  fuzzy.go           模糊音等价展开（zh/z、n/l、an/ang 等，默认关闭）
  s2t.go             简繁单字映射（零错误策略，默认关闭）
  double_pinyin.go   自然码双拼键码管线（Rime algebra 忠实转写，默认关闭）
  userdict.go        用户数据：调频记录、合成词组、上下文共现
  import.go          Rime dict.yaml / TSV 宽容解析（热词导入）
ipc/               IPC 协议与服务端/客户端（client 实现 engine.Controller）
skin/              候选窗视觉参数设计基准（几何 + 浅/深配色，DefaultSkinFor(dark)）：
                   无运行时消费者——实际渲染是 macOS CandidatePanel.swift 与
                   Windows win32.cpp 两份实现；Go↔macOS 的布局/色值一致性由
                   shims/macos/tests/visual-parity.sh 机器断言
                   （2026-10-27 起 daemon 贴图渲染链路已删）
cmd/
  chaosetd/          守护进程二进制（-dict 可多次叠加）
  chaoset-cli/       终端测试台（本地内嵌引擎或连接 daemon）
  chaoset-import/    热词导入工具（合并输出 TSV）
  chaoset-dictgen/   内置词库生成工具（常用性门槛词表 + wordfreq/HanLP 词频 → TSV）
  chaoset-userdict/  用户词典管理工具（统计/清空/导出，IPC 或本地文件）
data/
  dict_common.tsv    内置常用词库（cmd/chaoset-dictgen 生成产物，勿手工编辑）
  dict_extra.tsv     手工补充词表（口语高频词/常用组合）
  dict_hot.tsv       手工补充词表（现代/热词，随版本更新）
  dict_seed.tsv      种子词库（演示/回归用的小词库，词 \t 拼音 \t 词频）
  NOTICE.md          词库数据许可与署名说明（数据 CC BY-SA 4.0，代码 MIT）
shims/
  macos/             InputMethodKit 薄壳（Swift，已实现，见其 README；候选窗为壳内原生自绘）
  windows/           TSF COM 薄壳（已实现，核心层行为级 + DLL 编译级验证）
  linux/             ibus engine 胶水（已实现，见其 README）
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
4. **整句输入习惯（2026-10-30）**：多字拼音没有合适的整体候选时，用户
   可以逐段选择（如 `jintianxingqiyi` 选「今天」→ 剩余 `xing'qiyi` 再选
   「星期一」）；整串拼完后，各段拼成的整句会记为**带分段**的习惯记录
   （5 音节以上也能学），下次完整输入同一串时整句候选被调频加权排前。
   **但绝不整词贪婪匹配**：整句记录只在完整输入时作为整句候选，部分输入
   （`jin`/`jintian`）不出现整句候选；联想也只在用户确认过的**分段边界**
   剥前缀——上屏「今天」会联想「星期一」，上屏「今」绝不会给出「天星期一」，
   所以学过整句后「只想输入今天」的路径完全不受影响。旧版本学习数据没有
   分段信息时，用主词典「分段数最少」DP 推断边界（不是最长词贪心）；推断不出的按普通词处理。
   学习数据由 daemon 每 2 秒批量原子落盘（temp+rename），正常退出前同步落盘；异常强杀最多丢最近 2 秒（Windows 无优雅退出，只靠定时器）。

## 中文标点

标点键由引擎统一映射（`engine.IsPunctKey` / `Session.Punct`），三端壳只转发按键：
组词中打标点先上屏首选候选再出标点（`nihao` + `,` → `你好，`）；空组词直接映射
（`,` → `，`、`\` → `、`、`<` → `《`）；双/单引号各自按开闭交替配对；
句号/逗号同键连按在「全角→半角」间循环（。→.→。，，→,），任何其他
输入/上屏即回到首项。组词中的 `'` 仍是音节分隔符，不作为标点。
完整映射表见 `engine/punct.go`。

## 输入预测（末音节续接）

用户在打字时，**每个音节的最后一个字母之前都处于「没打完」状态**——这是输入法
最高频的输入形态。旧版对这些输入只能给出巧合的碎词：`zhonggu` 出「中古」、
`beijin` 出「被进」、`daxu` 出「大需」，真正想打的「中国 / 北京 / 大学」
完全找不到。现在把末段当作**待补全的音节**去词典续接（`engine.QueryContinuation`）：

| 输入 | 旧首选 | 新首选 | 续接依据 |
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

多字拼音没有单条词库词条时，**首屏候选只来自已存在的词**（旧版会用多词 DP
把词典片段自由拼成「你号码」「我们的」「中华人民共和国」并占据首屏，用户明确
要求移除）。候选来源与优先级：

- 词典词条：精确命中、末段续接、前缀续接、逐级回退得到的前缀词；
- 用户词库 / 学习记录（含用户逐段拼出的带分段整句记录）；
- 没有词组时回退到首音节单字（≥4 音节长输入同音字全量召回），用
  `SelectPrefix` 逐字挑选；
- 2~3 音节全拼若整词候选都很弱（**词典**全消耗单词词频 < 40，如 `shipei` 的
  「失陪」30、`peiyin` 的「配音」39），列表末尾补首音节同音字全量（至多
  120 个），可以直接逐字挑（选「适」后剩 `pei` 再选「配」）。有常用整词时
  （长江 48、你好 52、北京 69、世界 73）保持原有短列表，避免每次组词翻十几页；
  弱整词判定只看词典词频、不采信用户学习加权（学过「失陪」不会抑制单字补全）。
  追加候选层级 `rankSingleBackfill`（=`rankComposition+16`），排在**组合候选
  之后、列表最末**（纯单字收尾，单字块之后不再出现词组），不改变首选与原有排序。

排序按「最长已有词前缀 → 更短前缀 → 首音节单字」：
`zhonghuarenmingongheguo` 首选「中华」，选掉后剩余串再给「人民」、最后
「共和国」；`woxiangqubeijingdaxue` 没有前缀词组，逐字给「我/想/去」，
剩余 `beijingdaxue` 再整词出「北京大学」。

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

回归测试把边界锁死：`engine/sentence_test.go`（真实词条优先 + 组合只能由已有词
组成）、`engine/typo_dp_test.go`（错拼提升 / 正确输入低优先级 / 分段存在性）、
`engine/typo_test.go`（既有纠错首选不变）、`engine/user_history_test.go`
（用户历史优先）。

同口径扫描（**旧 jieba 词库**，2026-11-01 词库替换前；新词库指标见「内置词库与
再生成」）：移除造词后各场景 top1/top3 **持平或上升**（拼合假词原本占着候选前排）：

| 场景 | top1 前 → 后 | top3 前 → 后 | 命中率 前 → 后 |
|---|---|---|---|
| `full` | 79.68% → 79.75% | 97.39% → 97.39% | 100% → 100% |
| `tail1` | 48.80% → 49.01% | 76.18% → 76.39% | 99.38% → 99.38% |
| `tail2` | 23.61% → **28.14%** | 38.85% → **45.16%** | 82.98% → 82.84% |
| `initials` | 15.99% → 15.92% | 22.58% → 22.58% | 89.91% → 89.91% |
| `mixed` | 33.77% → 33.91% | 55.80% → 56.07% | 98.01% → 98.01% |

热路径同时变快（少了一次逐键多词 DP 搜索）：`BenchmarkComposeCommonDict`
约 25µs/键 → 12µs/键（Apple M1，同机紧邻 A/B 测量）。

### 引擎候选规则速览（当前）

- **候选来源**：词典 / 用户词库 / 学习记录中的真实词条；首屏不含任何多词
  拼接产物，组合候选只出现在列表末尾（`rankComposition`）。
- **匹配层级**：`rankExact`（全拼整词）→ `rankInitialExact`（简拼/混拼）→
  `rankContinuation`（末音节续接）→ `rankInitialPrefix`/`rankPrefix`（前缀词）
  → `rankBackoff+drop`（逐级回退前缀）→ `rankComposition`（已有词组合）。
- **连写 DP 匹配**：`QueryJoinedPinyin` 只为「词库里真实存在的整词」兜底，
  不依赖音节切分枚举上限，也不会造词（西安交通大学这类长词不再漏）。
- **错拼修正**：单次编辑修正出的词典整词 + （相邻互换/删除类）修正串的分词
  组合；仅当该输入没有任何整词级纠错命中时，组合候选才提升到精确层（最多 8 个）。
  反序双字母（gn/hz/hc/hs/ev 强制；ao/ua/ue 软纠错）走最高置信度归位路径；
  高置信度纠错候选固定排在候选列表第 3 位起。
- **用户历史优先**：上屏过的词 / 单字 / 整句记录优先于静态词频；与词库冲突时
  用户历史优先（`engine/user_history_test.go`）。
- **学习数据删除**：IPC `userDelete` / `chaoset-userdict delete`；候选窗右键
  「删除该词」只删本机学习记录，不动内置/导入词库。
- **词库**：内置 67k 条常用性门槛词库（方案 A，数据 CC BY-SA 4.0，见
  `data/NOTICE.md`）；手工补充表 `data/dict_extra.tsv`（口语）与
  `data/dict_hot.tsv`（现代/热词，随版本更新）。
- **回归测试**：候选来源总闸门 `engine/sentence_test.go`、错拼/组合
  `engine/typo_dp_test.go`、用户历史 `engine/user_history_test.go`、连写匹配
  `engine/dp_match_test.go`、词库契约 `engine/data_contract_test.go`。

### ü 的两种写法

音节表与小词库统一用 `v` 记写 ü（`nv`=女、`lve`=略），但用户键盘上更常敲 `u`。
现在 `lue`/ `nue` 会归一为 `lve`/`nve`（`engine.syllableAliases`），
两者都能打出「略 / 虐」。`jue/que/xue/yue` 本来就以 u 记写，不在别名表里，不受影响。

### 实测数据（可复现）

```sh
# 扫描器只读仓库：加载 data/dict_common.tsv，按固定口径统计
go run ./.workwork/verify/scan    # 该脚本在 .workwork/（gitignore），口径见其文件头注释
```

词条池 36,496 条（2–4 音节、词频 ≥30、词长 == 音节数），按 step=25 采样 1,460 条：

| 指标 | 改动前 | 改动后 |
|---|---|---|
| 末音节残缺**命中率**（去掉最后 1 个字母） | 43.84% | **98.63%** |
| 末音节残缺**首选率** | 13.49% | **43.42%** |
| 末音节残缺前 9 位命中率 | 31.92% | **85.41%** |
| 末音节残缺命中率（去掉最后 2 个字母） | 55.82% | **80.82%** |
| 全拼完整输入首选率 | 80.75% | 80.68% |
| 简拼首选率 / 命中率 | 17.53% / 42.67% | 17.53% / 42.67% |
| 混拼命中率 | 95.27% | **96.44%** |

关键取舍：`continuationPenalty` 取值经过扫描（20 / 25 / 30 / 35 / 40 全跑过），
30 是唯一同时保住完整输入首选率（不降）与残缺输入首选率的点；
性能无实质变化（`BenchmarkComposeCommonDict` 约 21–23µs/键，与改动前同量级）。

## 错拼修正（手滑打错一两个字母）

打字手滑是常态：相邻两键颠倒（`zhogn`）、多敲一个字母（`zhonng` / `niihao`）、
漏敲一个字母（`zhonguo`）、敲错一个键。旧版对这些输入要么给出一堆碎片假词
（`zhogn` → 「中欧国内」），要么直接无候选。

现在对输入做**单次编辑与粘键折叠**修正后重新查询（`engine/typoCorrections`），覆盖六类手误与拼音习惯：

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

其中 **gn→ng、hz→zh、hc→ch、hs→sh、ev→ve** 在普通话拼音里几乎不出现，走独立的
最高置信度路径（`forcedMistakePairs`）：`misspelledFromPatterns` 遇到这些组合
直接判定手误，归位修正排在 `typoCorrections` 最前；`mistakeInput` 时**只把归位
修正无条件提升到精确层**（不受「纠错词频须达到常规候选 2 倍」门槛约束，避免
`hzong` 的常规混拼「汇总」、`chagn` 删 g 的「产」或用户学习词压过正确修正），
通用编辑保持低层兜底（保证单字仍可选）。同一输入多处连错会一起翻回
（`dignhzi` → `dingzhi`）。

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

回归测试：`engine/typo_test.go`、`engine/typo_common_mistakes_test.go` 中的
`assertTypoCorrectionPosition`。

**刻意不做**：`ai/ia`、`ui/iu`、`ou/uo`、`ei/ie`、`an/na`、`en/ne`、
`in/ni`、`un/nu` 这类**两侧都合法**的相邻互换（如 `shuoji` 对 `shouji`、
`mieyou` 对 `meiyou`、`zoutian` 对 `zuotian`）不做强行纠错——它们与正常输入/
合法混拼同形，强行纠正会误伤正常候选（历史实测 mixed 首选率大幅下降）。这类错误
仍可通过通用单次编辑在「确实像手误」的输入上被修正。

### 判据与置信度分层

- **QWERTY 物理按键邻近建模**：手指物理按偏产生的相邻键替换（如 `e` $\rightarrow$ `r`、`o` $\rightarrow$ `p`）保留完整词频；跨键区非邻近替换（如 `r` $\rightarrow$ `a`）扣除 `15` 分惩罚分且不参与直接晋升，彻底杜绝远端巧合高频词反压真实目标词。
- **拼音习惯自动规范**：`uei` $\rightarrow$ `ui`（`shueijiao` $\rightarrow$ `睡觉`）、`iou` $\rightarrow$ `iu`（`niourou` $\rightarrow$ `牛肉`）、`uen` $\rightarrow$ `un`（`chuentian` $\rightarrow$ `春天`）、`j/q/x/y + v` $\rightarrow$ `u`（`xvesheng` $\rightarrow$ `学生`）。

### 关键取舍：绝不污染正常输入

手误与**合法简拼/混拼在串形态上高度同形**：
- **触发条件**：无法完整切分（`zhogn`）、切分中段存在孤立声母（`zhengchagn`），或末段存在孤立声母且无常规词匹配（`tailSuspicious`，如 `nihap`）；
- **防截断护栏**：末段声母形态纠错强制要求 `typoLen >= len(input)`，**严格禁止删除末位声母**——既能纠正 `nihap` $\rightarrow$ `你好`，又确保 `zhongg`（中国）、`chifanl`（吃饭了）、`shangxiaj`（上下级）等合法混拼与前缀输入首选完全不受干扰。

### 实测与性能

正常输入指标**逐项零回归**（全拼首选 80.68%、简拼 17.53%、混拼 34.32%、
末音节残缺 43.42%，均与前一轮完全一致），24 个典型错拼场景中 13 个修复为正确首选。

性能：纠错只在「切不动」时触发，正常打字路径零额外开销
（`BenchmarkComposeCommonDict` 22.0µs/键，与改动前一致）。触发纠错的输入
（如 `zhogn`）本身就要 2ms 量级的切分扫描，纠错只在其上增加约 6%；
查询条数由 `typoMaxQueries` 封顶（生成便宜、每条 Trie 查询较贵，
实测 160 条会让 `zhonguo` 从 0.8µs 涨到 25ms，压到 32 条后回到百微秒级）。

## 模糊音

默认关闭，`-fuzzy` 开启（daemon 与本地测试台均支持）：

```sh
bin/chaosetd -fuzzy          # 或 chaoset-cli -fuzzy（本地模式）
```

规则（微软拼音常用集）：声母对 zh/z、ch/c、sh/s、n/l；韵母对
an/ang、en/eng、in/ing（含 ian/iang、uan/uang）。输入侧展开等价
音节后并集查询：`ni` 同时出「泥/里」，`zhu` 同时出「主/组」，
声母+韵母可叠加（zhang → zang/zhan/zan）。残缺音节前缀查询不做
模糊；变体组合每路径封顶 8 条。规则表见 `engine/fuzzy.go`。

## 简繁转换

默认关闭，`-traditional` 开启（daemon 与本地测试台均支持）：

```sh
bin/chaosetd -traditional       # 候选与上屏均为繁体
```

数据来自 OpenCC 官方词典（Apache-2.0，内嵌于 `engine/dp_data/`）：
**词级最长匹配**优先（头发→頭髮、出发→出發、公里→公里——一对多字
的上下文判定由 4.9 万条词组表完成），未命中词组的单字查 4,029 条
单字表（中国→中國、时间→時間），表外字符原样保留。首次使用惰性
解析。数据与许可说明见 `engine/dp_data/README.md`。

## 双拼

默认关闭，`-shuangpin zrm|mspy|flypy` 选择方案（daemon 与本地测试台均支持）：

```sh
bin/chaosetd -shuangpin zrm     # 自然码：两键一音节，nihao = n i h k
bin/chaosetd -shuangpin mspy    # 微软双拼：nihao = n i h k；ing 在 ; 键
bin/chaosetd -shuangpin flypy   # 小鹤双拼：nihao = n i h c；iao 在 n 键
```

键位表不手工维护——分别忠实转写 Rime 官方三份 schema（自然码/
微软/小鹤）的 speller algebra 规则管线，对全部合法音节正向生成两键
码后建反向表，完整性测试保证全表无冲突、逐音节可解码。首键暂存、
成对解码，无效键码对整体丢弃；退格按按键事件粒度整段回滚。
见 `engine/double_pinyin.go`。

**`;` 键的三端路由（2026-10-30）**：mspy 的 `ing` 韵母键是 `;`，而 `;` 平时是
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
# 本地内嵌引擎模式
go run ./cmd/chaoset-cli

# 或经 IPC 连接 daemon（先起 daemon；默认 socket 在平台用户目录）
go run ./cmd/chaosetd &
# 显式指定 cwd 下的 socket 便于本地调试：
# go run ./cmd/chaosetd -socket chaoset.sock &
# go run ./cmd/chaoset-cli --socket chaoset.sock

# Windows 命名管道（推荐；daemon/CLI 都按当前用户 SID 自动算管道名）
go run ./cmd/chaosetd -pipe &
go run ./cmd/chaoset-cli -pipe

# Windows 迁移期 TCP 形态（旧安装器兼容）
go run ./cmd/chaosetd -tcp 127.0.0.1:7949 &
go run ./cmd/chaoset-cli -tcp 127.0.0.1:7949

# 构建信息（提交号/构建时间）：真机排障第一步，确认装的是不是新构建
go run ./cmd/chaosetd -version
go run ./cmd/chaoset-cli -version
```

测试台与 daemon 默认都加载内置常用词库 `data/dict_common.tsv`（见「内置词库与再生成」），
想换小词库或叠加自备词库用 `-dict` 指定（可多次）。

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
- 构建期**严格模式**只用于内置产物与测试：要求恰好 3 列、每个音节合法、词频为正（`engine/data_contract_test.go` 在 CI 严格解析）。

上限之所以是 99 而不是「越大越好」，是因为静态词频只是**基础分**：用户学习权重
按次累加（`engine.UserBoostPerCount = 500`，上下文共现
`BigramBonusPerCount = 250`），**一次上屏就远超整个 1–99 区间**。
所以把上游词频压缩进 1–99 完全够用，且不妨碍「越用越顺手」：选过的词
靠 500×次数×时间衰减排到前面。反过来说，把词库词频写成几百上千既没有收益，
还会让调频效果被静态分淹没。

### 内置词库与再生成

仓库自带 `data/dict_common.tsv`（内置常用词库），是 daemon 与测试台的默认词库；
`data/dict_seed.tsv` 退化为演示/回归用的小词库（数百条，跑测试与容器验证时够用）。

当前这份的实测规模：**67,268 条 / 1.3 MB**，词频区间 **1–99**。按词长分布：
1 字 10,916、2 字 42,744、3 字 7,310、4 字 6,150、5 字 120、6 字 28。
质量基线（固定词表口径见 `.agents/skills/pinyin-quality-tuning` 与
`.workwork/eval/`）：规范常用词覆盖 **100%**、规范精度 **94.5%**；通用规范汉字表
8105 单字全覆盖；98 个现代/热词探针 **98/98** 覆盖。固定词表扫描（649 常用词 +
200 单字）：全拼首选 **94.7%**、末音节残缺 **70.6%**、简拼 **26.9%**、混拼 **66.5%**、
单字 **66.5%**（旧 jieba 库同口径 92.8% / 64.6% / 22.7% / 60.0% / 66.5%）。

**词表（常用性门槛）**：

- 《现代汉语常用词表》（约 5.3 万词）+ HSK 3.0 词表；
- 所选词频源的高频用法 top 20,000——必须被 phrase-pinyin-data / THUOCL / HanLP
  词典交叉验证为「真词」，否则 wordfreq 等语料词表里的分词噪声（这是/这我/一给）
  会被挡在词库外；
- `data/dict_hot.tsv`（现代/热词）+ `data/dict_extra.tsv`（口语补充）里的所有词；
- 以上词表与 phrase-pinyin-data 求交后，**不在 phrase 表里的词不再整词丢弃**：
  生成器按「词内已知词组拼音 → HanLP 拼音词典 → 大词表字读音统计 → pinyin-data
  首读音」逐级补拼音，并输出人工校对清单（`-review-out`）。

**单字**：通用规范汉字表 8105 字（一级/二级/三级）∩ Jun Da 现代汉语字频排名；
多音字每个读音一条，词频按「该读音在 large_pinyin 词条拼音里的占比」折算
（`-readingfloor` 下限 0.3，由固定词表扫描定型），口语助词（吗/呢/吧）小幅补偿。
这样「率」的 lǜ/shuài/lüè 三读不再同分：输入 lue 时「略」排在「率」前。

**词频**（统一压缩到 1–99，静态词频只是基础分，见「词库格式」）：

| 模式 | flag | 词频源 | 许可 |
|---|---|---|---|
| 方案 A（默认） | `-freq wf` | wordfreq 中文词频为主 + THUOCL 领域兜底 + 词长先验兜底 | 代码 Apache-2.0；**数据 CC BY-SA 4.0** |
| 方案 B（后备） | `-freq hanlp` | HanLP CoreNatureDictionary（full+mini 取 max）+ THUOCL | 全 Apache-2.0/MIT |

方案 A 的归一化：zipf → `(zipf-1)/6.5` → 按 `-freqgamma`（默认 **1.15**，由固定
词表扫描定型：1.0/1.15/1.3 三档中 1.15 在 full/tail1 上最优且简拼不退化）拉开高频
差距 → 映射 1..99。手工词表权重按 `ln(w)/ln(1e6)` 压缩到常用词中高区间
（4000≈60 分），与主词频源取较大值。

**许可（重要）**：

- **代码 MIT**（根 `LICENSE`）；生成的 `data/dict_common.tsv` **数据**按
  **CC BY-SA 4.0** 分发，署名 wordfreq（Luminoso）、THUOCL、开放词表等上游，
  详见 `data/NOTICE.md`。修改词库再分发时须继续以 CC BY-SA 4.0 提供数据部分。
- 单字字频源 **Jun Da**：原作者声明仅限研究/教学，商业使用需书面许可——发布前
  应替换为 Unihan `kHanyuPinlu`/`kFrequency`（Unicode License）或取得授权。
- phrase-pinyin-data 的 large_pinyin 由汉典 zdic + CC-CEDICT（CC BY-SA 4.0）等
  来源合并，再分发需保留来源说明。

生成源文件（不随仓库分发；下载地址）：

- 《现代汉语常用词表》副本（词表门槛，正式发布请确认授权来源）：
  <https://github.com/liangqi/chinese-frequency-word-list>
- HSK 3.0 词表（MIT）：<https://github.com/elkmovie/hsk30>
- 通用规范汉字表 8105（一级/二级/三级字表）：
  <https://github.com/shengdoushi/common-standard-chinese-characters-table>
- wordfreq 中文词频（方案 A；导出格式「词<tab>zipf」）：
  <https://github.com/rspeer/wordfreq>
- HanLP CoreNatureDictionary + 拼音词典（方案 B / 高频词校验 / 补拼音，Apache-2.0）：
  <https://github.com/hankcs/HanLP/tree/1.x/data/dictionary>
- THUOCL 领域词表（MIT）：<https://github.com/THUNLP/THUOCL>
- phrase-pinyin-data `large_pinyin.txt`（MIT）：
  <https://raw.githubusercontent.com/mozillazg/phrase-pinyin-data/master/large_pinyin.txt>
- pinyin-data `pinyin.txt`（MIT）：
  <https://raw.githubusercontent.com/mozillazg/pinyin-data/master/pinyin.txt>
- Jun Da 现代汉语字频 `hanzi_db.csv`：<https://github.com/ruddfawcett/hanziDB.csv>
  （许可风险见上）

再生成（在仓库根目录；方案 A 为主交付）：

    go run ./cmd/chaoset-dictgen -freq wf \
      -norm changyongcibiao.txt -hsk hsk30_words.txt \
      -wordfreq wordfreq_zh.tsv -thucl THUOCL_data \
      -hanlp CoreNatureDictionary.txt -hanlpmini CoreNatureDictionary.mini.txt \
      -pinyin large_pinyin.txt -single pinyin.txt \
      -charfreq hanzi_db.csv -hanlppinyin hanlp_pinyin.txt \
      -gsc1 level-1.txt -gsc2 level-2.txt -gsc3 level-3.txt \
      -extra data/dict_extra.tsv -hot data/dict_hot.tsv \
      -review-out .workwork/pinyin_review.tsv \
      -o data/dict_common.tsv

方案 B 把 `-freq wf ... -wordfreq ...` 换成 `-freq hanlp`（`-hanlp/-hanlpmini`
同时作为词频源）即可，其余参数相同。

flag 说明：

| flag | 作用 | 必填 |
|---|---|---|
| `-freq` | 词频源：`wf`（wordfreq，数据 CC BY-SA）或 `hanlp`（Apache-2.0） | 否（默认 wf） |
| `-norm` | 《现代汉语常用词表》TSV（首列词） | 是 |
| `-hsk` | HSK 3.0 词表（「编号 词」或每行一个词） | 是 |
| `-wordfreq` | wordfreq 中文词频 TSV（`-freq wf` 必填） | 否 |
| `-hanlp` / `-hanlpmini` | HanLP 词典（`-freq hanlp` 必填；wf 模式用作高频词交叉验证） | 否 |
| `-thucl` | THUOCL 目录（领域词频兜底） | 否 |
| `-pinyin` | phrase-pinyin-data large_pinyin.txt | 是 |
| `-single` | pinyin-data pinyin.txt（单字读音） | 是 |
| `-charfreq` | Jun Da 字频表（单字词频排名） | 是 |
| `-hanlppinyin` | HanLP 拼音词典（补多音字/个别字读音） | 否 |
| `-gsc1/-gsc2/-gsc3` | 通用规范汉字表 一级/二级/三级字表 | `-gsc1` 必填 |
| `-extra` | 手工口语补充词表 `data/dict_extra.tsv` | 是 |
| `-hot` | 现代/热词补充词表 `data/dict_hot.tsv` | 是 |
| `-review-out` | 补拼音人工校对清单输出路径 | 否 |
| `-highfreqtop` | 高频用法取前 N 词（默认 20000） | 否 |
| `-highfreqconf` | 高频词只用字读音统计补拼音时的最低置信度（默认 0.9） | 否 |
| `-freqgamma` | 词频归一化幂次（默认 1.15，由固定词表扫描定型） | 否 |
| `-readingfloor` | 单字多音字按读音占比折算的系数下限（默认 0.3，由固定词表扫描定型） | 否 |
| `-o` | 输出 TSV | 是 |

生成过程约 2 秒；同一输入重复运行逐字节一致（`TestGenIdempotent` 兜底）。
`-review-out` 输出的补拼音校对清单只包含不在 phrase-pinyin-data 的词，重点复核
`confidence < 0.9` 与含多音字的行。

生成规则（实现见 `cmd/chaoset-dictgen`）：

- 只收纯 CJK 汉字词条——基本区、扩展 A、兼容表意区与「〇」，长度 1–6 字；
- 拼音经 `engine.NormalizePinyin` 归一化并逐音节 `IsSyllable` 校验，音节数必须
  等于字数，否则丢弃（防止错位拼音进词库）；
- 高频用法词必须通过词表交叉验证（phrase-pinyin-data / THUOCL / HanLP），
  wordfreq 的分词噪声（这是/这我/一给）不会入库；
- 繁体变体剔除**只对高频用法来源生效**：`engine.ToSimplified` 是 OpenCC 简→繁表
  的反向映射，对「像/象」这类简繁同形字会误判（好像→好象），规范词表/HSK/
  手工词表不做该过滤，避免误删常用词；
- 输出按词频降序、同频按词、再按音节排序，确定性可复现。

### 手工补充词表 `data/dict_extra.tsv` 与 `data/dict_hot.tsv`

这两个是词库链路里**允许手工编辑**的词表（格式：词 / 拼音 / 权重，一行一条）：

- `data/dict_extra.tsv`：口语高频词与常用组合（真好/来不及/对不起/很多/坐着 等），
  补词表与拼音链路漏收的词；
- `data/dict_hot.tsv`：**现代/热词**（智能家居/健康码/微信支付/大模型/内卷 等），
  **随 app 版本内置更新**；每次发版前复核一次（新增热词、下架过时词）。

权重是「估算原始词频」量级，生成器按 `ln(w)/ln(1e6)`
压缩到常用词中高区间（4000≈60 分、1000≈50 分、100≈35 分）；与主词表重复时取
较大词频，不需要从词表删除重复词。改完必须重新生成 `data/dict_common.tsv` 并
提交两者，生成产物仍禁止手改；`engine/data_contract_test.go` 会在 CI 断言
「手工词条格式合法 + 已合并进内置词库」。

维护规则：只收「确实常用、且上游词表/词频源没有或明显偏低」的词；拼音必须逐条
核对（生成器会用 `engine.IsSyllable` + 音节数==字数强校验，非法即让生成失败）。

### 词库校验：构建期严格，运行期健壮

两件事分开处理，不混为一谈：

| 阶段 | 对象 | 策略 | 保证方式 |
|---|---|---|---|
| 构建期 | 内置词库（生成产物 + extra 补充表） | **严格**：一行脏数据即失败 | `cmd/chaoset-dictgen` 生成时强校验；`engine/data_contract_test.go` 在 CI 严格解析 `data/dict_common.tsv` 与 `data/dict_extra.tsv`/`data/dict_hot.tsv`（规模、音节数==字数、词频区间、手工词表必须已合并） |
| 运行期 | 用户自备的第三方词库（`-dict`） | **健壮**：跳过坏行并汇总警告 | `engine.LoadDictFilesLenient`：坏行跳过、拼音归一化（去声调/ü→v/连写切分）、缺省词频补 20；daemon 把汇总写日志 |

为什么这样分：内置词库是**我们的**产物，脏数据必须在构建期暴露，不能靠运行时
宽容掩盖；而用户从网上下载或手写的词库，一行编码/格式问题不该让整个输入法起
不来——那是可用性问题，不是数据质量问题。

两条硬边界：
- 文件**打不开**（路径写错、权限不足）仍然报错退出——配置错误要立刻暴露，
  静默少加载一个词库只会让用户以为输入法坏了；
- 所有词库都加载不出词条（`Len()==0`）也报错退出——给用户一个静默打不出字
  的输入法比明确报错更糟。

另外保证：内置词库经宽松加载与严格加载得到**完全相同的词条集合**（宽松路径
先按严格规则校验、通过则原样保留音节），因此 daemon 在用户机器上的候选行为
与 CI 验证过的行为一致。

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

### 规模基准

测量方式（**数据来自这里，可复现**）：

```sh
# 环境：Apple M1 / macOS，Go 版本见 go.mod（go 1.27）
# 四个基准都在 engine/scale_bench_test.go，词库为代码合成的 10 万条双音节词，
# 不依赖任何外部词库文件，因此在任何机器上都能复跑。
go test ./engine/ -bench . -benchtime 3x
```

基准项与对应测量对象（数字为历史基准记录值，Apple M1）：

| 基准函数 | 测的是什么 | 记录值（Apple M1） |
|---|---|---|
| `BenchmarkLoadDict100k` | 解析 10 万行 TSV 建索引（daemon 启动路径，只跑一次） | ~42ms/次 |
| `BenchmarkCompose100k` | 单次组词，含全量候选排序 + 分页切片 | ~0.36ms/键 |
| `BenchmarkQueryPrefix100k` | 残缺音节前缀查询（线性扫描） | ~0.34ms/键 |

> 这些是**历史记录值**，不是硬指标：同一台 M1 上复跑会受机器负载、Go 版本、
> 电源策略影响（2026-10-22 复跑同命令得 ~60ms / ~0.47ms / ~0.59ms，即量级一致、
> 绝对值偏高）。判断回归时请在**同一环境**前后对比，不要跨机比绝对值。

真实词库侧的另一组实测：120,282 条 CC-CEDICT 合并词库，daemon 秒级就绪，
逐键链路 74ms（含进程启动），翻页在数千候选下正常（如 h → 738 页）。

#### 内置词库（真实数据）

上面那组是代码合成的词库。默认加载的 `data/dict_common.tsv` 是**真实数据**，另有
两个基准直接读仓库里的词库文件、测的就是用户实际启动路径（`engine/common_dict_bench_test.go`）：

```sh
go test ./engine/ -run XXX -bench 'CommonDict' -benchtime 3x
```

| 基准函数 | 测的是什么 | 结果（Apple M1 / go1.27.3） |
|---|---|---|
| `BenchmarkLoadCommonDict` | 解析 67,268 条真实词库建索引（daemon 启动时跑一次） | ~100ms；堆 22MB（旧 14.5 万条库 ~320ms / 56MB） |
| `BenchmarkComposeCommonDict` | `jingchang` 完整组词（含全量候选排序 + 分页切片） | ~25–31µs/键（2026-10-30 复测） |

词库自身规模：**67,268 条 / 1.3 MB**，词频区间 1–99；按词长分布为
1 字 10,916、2 字 42,744、3 字 7,310、4 字 6,150、5 字 120、6 字 28。
即换用真实词库后 daemon 冷启动多花约 0.1 秒，而逐键组词仍在**数十微秒级**（本机复测约 25–31µs，负载下偏高），
对打字延迟没有可感知影响。

## IPC 协议

帧格式：4 字节大端长度 + JSON 载荷（上限 1 MiB）。

- 请求：`{"id":1,"method":"input","params":{"text":"nihao","half":false}}`
  （`half` 可选：壳侧全/半角标点状态，2026-10-31 起每次 input 下发；旧壳省略
  时保持会话内旧状态。双拼 dpPending==0 的 `;` 走 Punct 语义，宽度必须用本次
  壳状态，否则会有 1 键瞬态。）
- 响应：`{"id":1,"result":{...}}` 或 `{"id":1,"error":"..."}`

方法（16 个）：`ping` / `input` / `backspace` / `backspaceWord`（按音节回退） /
`reset` / `commitRaw`（回车：返回原始拼音并清空组词）/ `select`（`{"index":0}`，
返回 `{"committed":"你好"}`）/ `selectPrefix`（`{"index":0}`，**渐进上屏**：只消耗
候选音节对应的 input 前缀，返回剩余组词状态 + `committed`，壳据此上屏前缀段并
用剩余 `preedit` 继续组词——三端选词走此路径）/ `nextPage` / `prevPage`（候选翻页，
返回当前组词状态，含 `page`/`page_count`）/ `punct`（`{"key":","}` 中文标点：
组词中返回 `{"committed":"你好，"}`，空组词返回 `{"committed":"，"}`）/
`associate`（`{"sylls":["xian","zai"]}`，返回与 Composition 同构的联想候选列表）/
`associateSelect`（`{"word":"现在时间","sylls":[...]}`，联想词上屏）/
`userStats` / `userClear` / `userExport`（用户词典导出：可选 `offset`/`limit`
按行分页，返回 `{"tsv":"...","next":N}`，`next=-1` 表示已导出完；客户端
自动循环拼接，词典再大也不会撞 1 MiB 帧上限）。
`Candidate` 新增可选 `segments`（仅整句式候选携带，引擎学习用的分段词文本；
壳无需渲染，解码时忽略即可），学习记录在 Words/Phrases 表内以 `segs` 持久化。
`Composition` 另有 `shuangpin`（双拼方案名，全拼为空；壳据此把双拼模式下的
`;` 键路由到 `input` 而不是 `punct`——mspy 的 ing 韵母键是 `;`）与顶层
`committed`（本次调用需壳写入文档的文本：渐进上屏段，或双拼 `;` 触发的
「首选词+标点」）。
IPC 是纯数据协议：daemon 只返回 `engine.Candidate` / `engine.Composition` 结构，
候选窗由 mac/Windows 壳原生自绘（macOS 用 AppKit，见
`shims/macos/src/CandidatePanel.swift`；Windows 用 GDI），Linux 不重绘面板、
用 ibus 原生 LookupTable/Preedit 呈现。历史上的 `candidateImage` 贴图方法
（daemon 渲染 PNG）随壳不再使用，已于 2026-10-27 删除。

视觉参数（几何 + 浅/深配色）的设计基准是 `skin/skin.go`（`DefaultSkinFor(dark)`，
**无运行时消费者**）；mac/Windows 两份壳实现按它手写。Go↔macOS 的布局常量
（5 项）+ macOS 参考常量（含 divider）+ 浅/深各 13 色由
`shims/macos/tests/visual-parity.sh` 逐项机器断言；Windows 侧目前只有 pt
口径约束与交叉编译/核心行为测试，无运行时视觉断言（见 docs/ui-parity.md
「机器检查覆盖」）。

传输形态：macOS 用 Unix socket；Linux 用 Unix socket（`$XDG_RUNTIME_DIR/chaoset/chaoset.sock`）；
Windows daemon 支持命名管道（`-pipe`，按当前用户 SID 生成、ACL 限当前用户），
壳/安装器迁移期仍兼容 TCP 回环（daemon `-tcp 127.0.0.1:7949`，仅本机回环）。

## 下载与安装（GitHub Release）

推 `v*` tag 时，Release workflow（`.github/workflows/release.yml`）会自动三端打包并创建 Release：

| 平台 | 推荐（普通用户） | 高级/备用（不要选） |
|---|---|---|
| macOS | `Chaoset-<版本>.dmg` | `chaoset-macos.zip`（绿色/排障） |
| Windows | `ChaosetSetup.exe` | `chaoset-windows-x64.zip`（需管理员 + install.bat） |
| Linux Debian/Ubuntu | `chaoset_amd64.deb` | `chaoset-linux-x86_64.tar.gz`（需 sudo 脚本） |
| Linux Arch | `chaoset-ime-x86_64.pkg.tar.zst` | 同左，无脚本包 |

每个包都自带**完整的 14 万条常用词库**，装完即可用，不需要另配词库。

> **查看版本**：三端设置窗底部都会显示 `Chaoset 版本 x.y.z`；macOS 的 DMG
> 文件名/卷名也带版本号。升级后先看这里确认装的是新版本，再排查其他问题。

> 2026-10-25 修复的发行缺陷（此前 Release 产物不可用）：Linux 包只带了 558 条的
> 演示词库、引擎却要 `data/dict_common.tsv`，且相对路径依赖 cwd——装完引擎启动
> 即退出，ibus 里一个字都打不出来；三个平台的打包命令都没显式指定
> `GOOS/GOARCH`，在 macOS 开发机上打包会把 Darwin 二进制塞进 Linux/Windows 包
> （CI 跑在对应平台所以看不出来）；macOS 只出 arm64，Intel 机器无法打开；
> Windows 没有任何安装脚本，用户得手工敲 `regsvr32`。现在四项都已修复，
> 并在 CI 里加了对应的验证步骤（架构断言 + 真实安装后启动引擎的冒烟校验）。

## 平台壳

| 平台 | 壳 | 状态 |
|---|---|---|
| Linux | ibus engine（原生 C++/libibus 薄壳），Unix socket 连 daemon；候选窗为 ibus 原生 LookupTable | 已实现（容器内端到端验证；真机桌面闭环待环境） |
| macOS | InputMethodKit `.app`（Swift），Unix socket 连 daemon；候选窗为壳内 AppKit 原生自绘（v3） | 已实现（真机启用，v0.2.0） |
| Windows | TSF COM DLL（C++ 薄壳），命名管道优先（TCP 回环作迁移期回退）连 daemon；壳内 GDI 原生自绘候选窗（键帽序号/首选胶囊/学习标记）+ 合成内预编辑 | 已实现（Windows 11 真机首轮验证：记事本/浏览器/终端/开始菜单/搜索候选窗；命名管道运行时、游戏/全屏、UWP/多会话、无障碍与 per-monitor DPI 待验） |

### 渐进上屏的跨程序兼容模型（2026-10-30）

三端壳对「逐段渐进拼句」（`jintianxingqiyi` 选「今天」→ 剩余继续组词）采用
**同一模型：已确认段并入预编辑/合成，整条组词结束才一次落盘**：

- macOS：已确认段并入 marked text——壳对文档只做两类操作：「`setMarkedText`
  更新」（与逐键输入同原语）与「组词结束 `insertText` 终态落盘」（与整句
  上屏同原语），不再在 marked 区激活期间插入正文；
- Windows：已确认段并入**当前合成**（同一条合成内 `SetText`），不再
  「`EndComposition` → 立刻 `StartComposition`」；落盘 = 同一合成
  `SetText` + `EndComposition` 一次完成；
- Linux：已确认段并入 `UpdatePreeditText`，不再在预编辑激活期间发
  `CommitText`；落盘 = 整串一次 `CommitText`。

为什么：逐段选择若「先写正文、再刷新预编辑」（旧实现），是对宿主的**两步
文档变更**，部分程序处理不好——实测会把刚选的字丢掉、候选清空、剩余拼音
卡死在文档里（macOS 部分版本 Notes/Electron/终端对「marked 区激活期间收到
insertText」有缺陷；Windows 部分宿主对合成结束/重建的选区记账有缺陷，新
合成锚点落回旧文本处直接覆盖刚上屏的段；Linux XIM 桥接/自绘输入对预编辑
激活期间的提交同理）。逐键输入在所有程序都正常，因为它只有一步预编辑更新
——本模型让渐进上屏的兼容面与逐键输入完全一致，并与 macOS 系统拼音 /
微软拼音「合成内已转换段」的行为对齐。

随之统一的行为（三端一致，矩阵见 `docs/ui-parity.md`）：已确认段在预编辑里
以**已转换形态**（汉字而非拼音）随剩余拼音一起显示；Esc / 全部删除取消整条
组词（含未落盘的已确认段）；回车 = 已确认段按确认文本 + 剩余按原始拼音
上屏；退格只回退剩余拼音，剩余删空时已确认段立即落盘收尾；失焦 / 透传键 /
切换输入法时已确认段先落盘（用户确认过的字不丢）。引擎侧不变：
`SelectPrefix` 依旧只消耗候选音节对应的前缀、剩余回灌、即时学习（纯算法，
与平台无关）。

Windows 薄壳健壮性落实情况：daemon 未就绪时按键直接放行（已实现，核心层
行为验证）；传输失败即断开降级、不卡宿主（已实现）；传输优先命名管道
（2026-11-01 新增，无端口、ACL 限当前用户），管道不可用时回退 TCP 回环
（旧安装器仍 `-tcp`，升级期不中断）；自绘候选窗与 UWP/AppContainer 场景待真机。
逐项状态见 shims/windows/README。

> **macOS 输入源标识**：输入源 ID / `CFBundleIdentifier` 为
> `ime.chaoset.inputmethod.ChaosetIME`，输入模式为
> `ime.chaoset.inputmethod.ChaosetIME.Pinyin`（`.Hans` 仅作为历史别名保留在
> 安装器的语言映射里）。**bundle id 必须含内层
> `.inputmethod.` 段**（仅以 `inputmethod` 结尾的写法 TIS 不收录）。
> 安装用 `shims/macos/install.sh`（含 `TISRegisterInputSource`）。
>
> 2026-10-21 实测更正：**bundle 有效时不需要注销重登**——装好并调用
> `TISRegisterInputSource` 后 TIS 立即枚举到本输入法，系统设置里可直接添加。
> 早期「新增输入法只能在登录扫描时收录」的结论，是当时 bundle id 不含内层
> `.inputmethod.` 段导致的误判。真机细节（灰色条目、缓存重建等坑）
> 见 `shims/macos/README.md` 的「输入源标识与显示名」与「验证状态」两节。

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

> **平台限制**：`chaoset-cli` / `chaoset-userdict` 支持 Unix socket（macOS/Linux）、
> Windows 命名管道（`-pipe`）与迁移期 `-tcp`；Windows 包里的同名 exe 连接 daemon 时
> 传输需与 daemon 一致（`-pipe` 或 `-tcp`）。daemon 未运行时只能加 `-userdict` 走本地
> 文件模式（daemon 运行时会被其内存态覆盖，建议先停 daemon）。
>
> 导出经 IPC 分页传输（客户端自动拼接），学习数据再大也不会撞 1 MiB 单帧上限。

### 文件损坏时的自动恢复

学习数据是增量优化，**不能因为它让输入法起不来**。daemon 与 ibus 引擎启动时
用宽松加载：`chaoset-userdict.json` 若无法解析（断电写坏、磁盘满、手工编辑、
旧版本残留），会把它改名备份为 `chaoset-userdict.json.corrupt-<时间戳>`
并日志记一条警告，然后以空词典继续启动——不会退出、不需要手工删文件。

备份文件保留现场便于排查；确认无用后可自行删除。读取失败（权限不足等）
仍会报错退出——那类问题改名解决不了，静默忽略只会让用户误以为数据还在。

全量导出为多段 TSV（词 / 词组 / 上下文，含次数与最近使用时间，
按次数降序、UTC 时间，输出稳定可 diff）；空段省略。`clear` 不可撤销，
建议先 `export` 备份。

## 版本与变更记录

当前版本与安装包见 [GitHub Releases](https://github.com/ShanWuYinShe/chaoset_ime/releases)；按阶段的功能、修复与验收记录见 [CHANGELOG.md](CHANGELOG.md)。

## 开发

```sh
make fmt && make vet && make test && make build
make cover    # 覆盖率报告（写入 .workwork/cover.out）
make race     # 并发竞争检测（提交前建议）
```

### 词库相关的测试

内置词库是**生成产物**，所以它本身也被测试守着（都读真实的 `data/dict_common.tsv`）：

| 测试 | 守什么 |
|---|---|
| `engine.TestCommonDictJingchang` | 用户反馈的原始场景：输入 `jingchang` 第一候选必须是「经常」 |
| `engine.TestCommonDictSingleSyllableRanksCommonChar` | 单音节 `jing` 第一候选是常用字「经」，不被生僻字淹没 |
| `engine.TestExactSyllableCountPreferred` | 完全匹配优先：「经常」排在「经常化」前 |
| `engine.TestUserBoostBeatsExactMatch` | 反向约束：用户选过的长词要能压过「完全匹配优先」 |
| `engine.TestCommonDictScale` | 规模与完整性回归：≥10 万条、常用词都在、词频全部落在 1..99 |

生成工具自身（`cmd/chaoset-dictgen/main_test.go`，用小样本，不依赖那三个大源文件）：

| 测试 | 守什么 |
|---|---|
| `TestGenBuildsCommonDictWithPinyinGapFilled` | 常用性门槛词表 + 补拼音链路（phrase/词内词组/字读音统计）都能出词，不整词丢弃 |
| `TestGenWritesPinyinReviewList` | 补拼音人工校对清单内容和来源标记正确 |
| `TestGenSingleCharsFromGSC` | 单字只收通用规范汉字表内字，多音字降权 |
| `TestGenFreqMappingAndFallback` | 词频映射单调、落在 1..99，无词频词用长度先验 |
| `TestGenManualWeightWinsWhenHigher` | 手工词表权重优先于主词频源 |
| `TestGenHanLPMode` / `TestGenWFHeaderLicense` | 方案 B 可用、两种模式文件头许可声明正确 |
| `TestGenIdempotent` | 同一输入重复生成逐字节一致 |
| `TestGenDropsWordWithoutPinyin` | 查不到拼音的词整条丢弃（不猜读音） |
| `TestGenDropsSyllableCountMismatch` | 音节数与字数不符的整条丢弃 |
| `TestGenIdempotent` | 同一输入跑两次输出逐字节一致 |
| `TestCJKWordOK` | CJK 范围判定（基本区/扩展 A/兼容区/〇 与长度上限） |

改了生成逻辑后请同时跑：`go test ./cmd/chaoset-dictgen/ ./engine/`。

推送后由 GitHub Actions（`.github/workflows/ci.yml`）自动执行
fmt 检查、vet、`-race` 测试与构建。