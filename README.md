# chaoset-ime

跨平台中文拼音输入法（Windows / macOS / Linux）。

## ⚡ 极简新手安装指南（零门槛·傻瓜式一键使用）

> **致普通用户**：输入法面向的是广大日常电脑用户，绝不需要假设您有任何编程或命令行知识。直接下载对应系统的一键安装包，双击即可无障碍开始打字！

| 操作系统 | 推荐安装包（小白首选） | 极简安装步骤 | 切换输入法快捷键 |
| :--- | :--- | :--- | :--- |
| **Windows** (10 / 11) | [📥 ChaosetSetup.exe](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | **直接双击安装**，点击「下一步」完成（已自动注册并开机自启） | `Win + 空格` |
| **macOS** (12+) | [📥 Chaoset.dmg](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | **双击打开**，打开里面的「Chaoset 安装与设置」，点击「一键安装」 | `Ctrl + 空格` 或 `Fn / 🌐` |
| **Linux (Ubuntu / Debian / Mint)** | [📥 chaoset_amd64.deb](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | **双击文件**在系统应用中心点击「安装」即可自动生效 | `Super + 空格` 或 `Ctrl + 空格` |
| **Linux (Arch / Manjaro / EndeavourOS)** | [📥 chaoset-ime-x86_64.pkg.tar.zst](https://github.com/ShanWuYinShe/chaoset_ime/releases/latest) | **双击文件**在应用商店（Pamac/发现）点击「安装」，或 `sudo pacman -U <包名>` | `Super + 空格` 或 `Ctrl + 空格` |

> 📦 **开发者、绿色便携版与 AUR**：如需无需安装的独立归档，可前往 [GitHub Releases](https://github.com/ShanWuYinShe/chaoset_ime/releases) 获取 `.zip` (Windows/macOS) 或 `.tar.gz` (Linux)；Arch Linux 用户亦可直接使用仓库自带的 `shims/linux/arch/PKGBUILD`。

---

## 架构概览

架构：**Go 引擎 + 守护进程（daemon）+ 各平台薄壳**。引擎与组词逻辑只写一份，
平台壳只负责「按键转发 + 文本上屏 + 光标定位」，通过 IPC 与 daemon 通信。
该形态已被业界验证（小狼毫的 TSF DLL + WeaselServer、搜狗的多进程形态、
macOS InputMethodKit 本身就是进程间模型）。

```
┌──────────────────── chaosetd (Go，一份代码) ────────────────────┐
│  engine：拼音切分 / 词典检索 / 候选排序 / 词组进化自学习           │
│  ipc  ：length-prefixed JSON over Unix socket / TCP 回环          │
└────────┬───────────────────┬───────────────────┬───────────────┘
   macOS │             Windows│              Linux │
  IMK 薄壳          TSF COM 薄壳 DLL       ibus engine（DBus 胶水）
  (Swift)           (C++，仅转发+上报)
```

## 与 Squirrel / Rime 的关系（本项目不含其代码）

本项目是**从零实现**的：Go 引擎（`engine/`）、IPC（`ipc/`）、视觉参数真源
（`skin/`）、三端薄壳（`shims/`）全部为本仓库自研代码，
`git ls-files` 中没有任何 Squirrel / librime 源码，也没有引用其图标素材。
来自第三方的只有**数据**：

- OpenCC 简繁词典（Apache-2.0，内嵌于 `engine/dp_data/`，见其 README）；
- 可选导入的 CC-CEDICT 词库（CC-BY-SA 4.0，仓库不分发，由用户自行下载后合并）；
- 内置常用词库 `data/dict_common.tsv`：由 [jieba](https://github.com/fxsjy/jieba)
  的词频表 `dict.txt`（**MIT**）与
  [phrase-pinyin-data](https://github.com/mozillazg/phrase-pinyin-data) 的
  `large_pinyin.txt`（**MIT**）生成，是本仓库的**生成产物**而非拷贝的词库文件，
  再生成方式见「内置词库与再生成」。分发该文件时须保留上述两个上游的 MIT 署名；
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
  chaoset-cli/       阶段 0 终端测试台（本地内嵌引擎或连接 daemon）
  chaoset-import/    热词导入工具（合并输出 TSV）
  chaoset-dictgen/   内置词库生成工具（jieba 词频表 + 词语拼音表 → TSV）
  chaoset-userdict/  用户词典管理工具（统计/清空/导出，IPC 或本地文件）
data/
  dict_common.tsv    内置常用词库（cmd/chaoset-dictgen 生成产物，勿手工编辑）
  dict_seed.tsv      种子词库（演示/回归用的小词库，词 \t 拼音 \t 词频）
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

## 快速开始（阶段 0 测试台）

> 普通用户不需要命令行：联想开关、学习数据统计/清空/导出都在**设置窗口**
> （候选栏右侧齿轮）。下面的 CLI 是开发测试台与高级/排障工具；
> `chaoset-userdict` 仅作为无 GUI 环境/高级用户兜底。

```sh
# 本地内嵌引擎模式
go run ./cmd/chaoset-cli

# 或经 IPC 连接 daemon（先起 daemon）
go run ./cmd/chaosetd &
go run ./cmd/chaoset-cli --socket chaoset.sock

# Windows 壳同款 TCP 形态（daemon 用 -tcp 起，CLI 也用 -tcp 连；Windows 上只能这样）
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
所以把 jieba 词频对数压缩进 1–99 完全够用，且不妨碍「越用越顺手」：选过的词
靠 500×次数×时间衰减排到前面。反过来说，把词库词频写成几百上千既没有收益，
还会让调频效果被静态分淹没。

### 内置词库与再生成

仓库自带 `data/dict_common.tsv`（内置常用词库），是 daemon 与测试台的默认词库；
`data/dict_seed.tsv` 退化为演示/回归用的小词库（数百条，跑测试与容器验证时够用）。

当前这份的实测规模：**145,262 条 / 3.0 MB**，词频区间 **1–99**。按词长分布：
1 字 13,834、2 字 72,677、3 字 21,600、4 字 36,049、5 字 774、6 字 328。

词频来源：**多字词用 jieba**（对词组频率可靠），**单字用 Jun Da 现代汉语字频
与 jieba 融合**（jieba 的单字词频是分词口径，生僻人名用字虚高——灏 2196 > 豪
1182 这类倒挂会让生僻字抢占候选前排，详见 `cmd/chaoset-dictgen` 的
`parseCharFrequency` 注释）。另有口语助词小幅补偿（吗/呢/吧 等在书面语料里
被低估，否则「在吗」这类口语组合合成不出来）。

该文件**由工具生成，禁止手工编辑**。生成源是四个上游数据文件（均 **MIT** 或
Apache-2.0，不随仓库分发）：

- jieba 词频表 `dict.txt`（每行「词 词频 词性」，**多字词**词频）：
  <https://raw.githubusercontent.com/fxsjy/jieba/master/jieba/dict.txt>
- Jun Da 现代汉语字频表 `hanzi_db.csv`（**单字**字频排名，修 jieba 单字虚高）：
  <https://github.com/ruddfawcett/hanziDB.csv>
- Rime essay 语料 `essay.txt`（可选 `-corpus`，字频表缺省时的回退校正）：
  <https://github.com/rime/essay>
- phrase-pinyin-data 的 `large_pinyin.txt`（每行「词: pīn yīn」，词语拼音）：
  <https://raw.githubusercontent.com/mozillazg/phrase-pinyin-data/master/large_pinyin.txt>
- pinyin-data 的 `pinyin.txt`（单字读音，`U+4E00: yī,yí,yì  # 一` 格式，补多音字）：
  <https://raw.githubusercontent.com/mozillazg/pinyin-data/master/pinyin.txt>

再生成（在仓库根目录）：

```sh
go run ./cmd/chaoset-dictgen -jieba dict.txt \
  -charfreq hanzi_db.csv -corpus essay.txt \
  -pinyin large_pinyin.txt -single pinyin.txt \
  -extra data/dict_extra.tsv -o data/dict_common.tsv
```

七个 flag：

| flag | 作用 | 必填 |
|---|---|---|
| `-jieba` | 词频表（多字词词频） | 是 |
| `-pinyin` | 词语拼音表 | 是 |
| `-o` | 输出 TSV | 是 |
| `-single` | 单字拼音表（补多音字） | 否 |
| `-charfreq` | 单字字频表（修正 jieba 单字虚高） | 否 |
| `-corpus` | 真实语料词频（字频表缺省时的回退校正） | 否 |
| `-extra` | 手工补充词表（现代词/口语高频词，见 `data/dict_extra.tsv`） | 否 |

生成过程约 0.8 秒（含读源文件）。`-single` 的作用是补多音字：词语拼音表
里没有的单字，靠它拿到读音；**不传时这些单字会被跳过**（不会瞎猜拼音）。

生成规则（实现见 `cmd/chaoset-dictgen`）：

- 只收纯 CJK 汉字词条——基本区 U+4E00–9FFF、扩展 A U+3400–4DBF、兼容表意区
  U+F900–FAFF 与「〇」U+3007，长度 1–6 字；
- 拼音经 `engine.NormalizePinyin` 归一化（去声调、ü→v、`u:`→v，连写自动切分），
  该函数内部**逐音节**做 `IsSyllable` 校验，任一首节非法即返回 nil、整条丢弃
  （与热词导入走同一套规则，不把脏数据塞进词库）；
- **拼音音节数必须等于字数**，不等就丢弃（避免「西安」配成单音节这类错配）；
- **多字词**：jieba 原始词频按对数压缩映射到 1–99（单调、可复现）；
- **单字**：Jun Da 字频排名与 jieba 词频融合折算（几何平均，另给口语助词
  小幅补偿），jieba 里生僻人名用字的虚高分被语料排名压回；
- 输出按词排序，**同一输入重复运行逐字节一致**（幂等，由 `TestGenIdempotent` 兜底）。

文件头注释同样记录了来源 URL、许可与再生成命令；换了上游数据请重新生成而不是手改。

### 手工补充词表 `data/dict_extra.tsv`

这是整个词库链路里**唯一允许手工编辑**的词表（格式与输出一致：词 / 拼音 / 原始
词频，一行一条），经 `-extra` 合并进生成结果。用途是补上游词频表的空白：

- **现代数字生活词**——jieba 语料较老，微信/扫码/表情包/抖音/卡顿/断网等
  大面积缺失（2026-10-30 审计：92 个现代高频词缺 28 个）；
- **口语高频拼合词**——jieba 未收录的组合（真好/来不及/对不起），不补录时
  组句路径会被碎片过滤误杀，表现为「打不出这个词」；
- **提升已有词条词频**——与主源重复时取较大词频（因此可以纠正被压得过低的
  常用词，如支付宝/朋友圈）。

维护规则：只收「确实常用、且上游词频表没有或明显偏低」的词；词频参考
锚点（手机 4789→62 分、电脑 3427→59 分）。改完必须重新生成
`data/dict_common.tsv` 并提交两者，生成产物仍禁止手改。

### 词库校验：构建期严格，运行期健壮

两件事分开处理，不混为一谈：

| 阶段 | 对象 | 策略 | 保证方式 |
|---|---|---|---|
| 构建期 | 内置词库（生成产物 + extra 补充表） | **严格**：一行脏数据即失败 | `cmd/chaoset-dictgen` 生成时强校验；`engine/data_contract_test.go` 在 CI 严格解析 `data/dict_common.tsv` 与 `data/dict_extra.tsv`（规模、音节数==字数、词频区间、extra 必须已合并） |
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

基准项与对应测量对象（数字为阶段 15 入库时的记录值，Apple M1）：

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
| `BenchmarkLoadCommonDict` | 解析 145,262 条真实词库建索引（daemon 启动时跑一次） | ~109ms（阶段 15 记录值） |
| `BenchmarkComposeCommonDict` | `jingchang` 完整组词（含全量候选排序 + 分页切片） | ~25–31µs/键（2026-10-30 复测） |

词库自身规模：**145,262 条 / 3.0 MB**（3,180,344 字节），词频区间 1–92；按词长分布为
1 字 13,834、2 字 72,677、3 字 21,600、4 字 36,049、5 字 774、6 字 328。
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

传输形态：Linux/macOS 用 Unix socket；Windows 壳经 TCP 回环
（daemon `-tcp 127.0.0.1:7949`，仅本机回环）。

## 下载与安装（GitHub Release）

推 `v*` tag 时，Release workflow（`.github/workflows/release.yml`）会自动三端打包并创建 Release：

| 平台 | 下载文件 | 安装方式 |
|---|---|---|
| macOS | `chaoset-macos.zip` | 双击里面的 **ChaosetSetup.app**，图形界面点安装。universal 二进制（arm64 + x86_64），Intel 与 Apple Silicon 通用；ad-hoc 签名，首次打开需右键 →「打开」 |
| Linux | `chaoset-linux-x86_64.tar.gz` | 解包后 `sudo ./install.sh`（装二进制 + 14 万条词库 + ibus 组件 + 图标，并自检） |
| Windows | `chaoset-windows-x64.zip` | 解包后**右键 install.bat → 以管理员身份运行**（自动 regsvr32 注册 + 启动 daemon） |

每个包都自带**完整的 14 万条常用词库**，装完即可用，不需要另配词库。

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
| Linux | ibus engine（Go 内嵌引擎），DBus 直连 ibus-daemon | 已实现（容器内端到端验证；真机桌面闭环待环境） |
| macOS | InputMethodKit `.app`（Swift），Unix socket 连 daemon；候选窗为壳内 AppKit 原生自绘（v3） | 已实现（真机启用，v0.2.0） |
| Windows | TSF COM DLL（C++ 薄壳），TCP 回环连 daemon；壳内 GDI 原生自绘候选窗（键帽序号/首选胶囊/学习标记）+ 合成内预编辑 | 已实现（Windows 11 真机首轮验证：记事本/浏览器/终端/开始菜单/搜索候选窗；游戏/全屏、UWP/多会话、无障碍与 per-monitor DPI 待验） |

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
行为验证）；传输失败即断开降级、不卡宿主（已实现）；传输走 TCP 回环
（替代早期命名管道方案）；自绘候选窗与 UWP/AppContainer 场景待真机。
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

## 用户词典管理

```sh
# 经运行中的 daemon 操作（推荐：清空同时生效于内存与磁盘）
bin/chaoset-userdict -socket chaoset.sock stats
bin/chaoset-userdict -socket chaoset.sock clear
bin/chaoset-userdict -socket chaoset.sock export -o userdict-backup.tsv

# daemon 未运行时直接操作文件（daemon 运行时清空会被其内存态覆盖）
bin/chaoset-userdict -userdict chaoset-userdict.json stats

# 只导出学到的词组（三列 TSV），回流词库：
bin/chaoset-userdict -socket chaoset.sock export -phrases -o learned.tsv
bin/chaoset-import -o dict-merged.tsv data/dict_common.tsv learned.tsv
```

> **平台限制**：`chaoset-cli` / `chaoset-userdict` 目前只支持 Unix socket（macOS/Linux）。
> Windows 包里的同名 exe 连不上走 TCP 回环的 daemon，只能加 `-userdict` 走本地文件模式
> （daemon 运行时会被其内存态覆盖，建议先停 daemon）。
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

## 路线图与验收结论

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
        设置窗深色、多行折行、半透明/阴影、无障碍、滚轮、按键边角 3 处、Linux
        设置窗单例）写入 docs「已知功能差异」。
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
        `shims/windows/README.md`「已知限制」）；Option/Alt+Backspace 三端语义差异
        （mac 按音节回退 / Windows 放行 / Linux 清空，记入 docs/ui-parity 按键边角差异）；
        双拼 `;` 的标点宽度 1 键瞬态（记入各端 README）。

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
        设置窗深色、多行折行、Option/Alt+Backspace 三端语义仍记录在
        docs/ui-parity 与各端 README。

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
        现用+所选 → 备份 → 原子替换）；Windows 现在等价。Linux 目前仍只有 CLI
        `chaoset-import`（GTK 设置窗导入待后续）。
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
        GUI；Linux 仅 CLI 且 ui-parity 必须明确记录）；⑤ 打包安装一致性
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
        是合成 NSEvent 驱动 `ChaosetController.handle` 的 8 项按键路由回归
        （keyUp 放行/字母组词/快捷键放行/Shift 空按中英切换/Shift+字母不误切），
        此前未接入 Makefile/CI（死测试）。新增 `tests/key_routing/run.sh`：编译
        测试二进制 → 起临时 daemon（临时 socket/userdict，结束 kill+wait 后清理）
        → 跑断言；CI macos-build job 每次 push 执行。本机实测 8/8 通过。
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
| `TestGenFiltersNonCJK` | 非汉字词条被过滤 |
| `TestGenNormalizesPinyin` | 带声调 / ü / `u:` 的拼音被归一化成无声调音节 |
| `TestGenFreqScaling` | 词频对数压缩单调、落在 1..99 |
| `TestGenDropsWordWithoutPinyin` | 查不到拼音的词整条丢弃（不猜读音） |
| `TestGenDropsSyllableCountMismatch` | 音节数与字数不符的整条丢弃 |
| `TestGenIdempotent` | 同一输入跑两次输出逐字节一致 |
| `TestCJKWordOK` | CJK 范围判定（基本区/扩展 A/兼容区/〇 与长度上限） |

改了生成逻辑后请同时跑：`go test ./cmd/chaoset-dictgen/ ./engine/`。

推送后由 GitHub Actions（`.github/workflows/ci.yml`）自动执行
fmt 检查、vet、`-race` 测试与构建。