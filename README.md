# chaoset-ime

跨平台中文输入法（macOS / Linux / Windows）。

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

本项目是**从零实现**的：Go 引擎（`engine/`）、IPC（`ipc/`）、候选窗渲染
（`renderer/`）、三端薄壳（`shims/`）全部为本仓库自研代码，
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
renderer/          统一候选窗渲染（v2）：把组词状态画成 PNG，三端壳贴图显示
                   默认横排候选栏 + 圆角 + 系统浅/深色皮肤（Skin.Horizontal /
                   MaxWidth / CornerRadius，DefaultSkinFor(dark)）；竖排列表布局
                   仍可用 Horizontal=false 走通（配色由 Skin 颜色字段决定）
cmd/
  chaosetd/          守护进程二进制（-dict 可多次叠加；-font/-fontsize 候选窗渲染）
  chaoset-cli/       阶段 0 终端测试台（本地内嵌引擎或连接 daemon）
  chaoset-import/    热词导入工具（合并输出 TSV）
  chaoset-dictgen/   内置词库生成工具（jieba 词频表 + 词语拼音表 → TSV）
  chaoset-userdict/  用户词典管理工具（统计/清空/导出，IPC 或本地文件）
data/
  dict_common.tsv    内置常用词库（cmd/chaoset-dictgen 生成产物，勿手工编辑）
  dict_seed.tsv      种子词库（演示/回归用的小词库，词 \t 拼音 \t 词频）
shims/
  macos/             InputMethodKit 薄壳（Swift，已实现，见其 README）
  windows/           TSF COM 薄壳（已实现，核心层行为级 + DLL 编译级验证）
  linux/             ibus engine 胶水（已实现，见其 README）
```

## 越用越顺手：三层自学习

1. **调频**：每个词记住上屏次数与最近使用时间，
   `得分 = 次数 × 500 × 时间衰减`（<1 天 ×1.0 / <7 天 ×0.6 /
   <30 天 ×0.3 / 更久 ×0.1）——最近常用的词自动往前排。
2. **上下文（bigram）**：记录「前一个上屏词 → 当前词」共现，
   顺着上下文的候选每次共现加 250 分（封顶 2 次）。
3. **词组进化**：相邻两次上屏的组合（如分字打出「键盘」「鼠标」）
   共现满 2 次且主词典没有该词时，自动固化为用户词组（≤4 音节），
   之后整词输入 `jianpanshubiao` 直接出「键盘鼠标」。
   所有学习数据随 daemon 即时原子落盘，重启不丢。

## 中文标点

标点键由引擎统一映射（`engine.IsPunctKey` / `Session.Punct`），三端壳只转发按键：
组词中打标点先上屏首选候选再出标点（`nihao` + `,` → `你好，`）；空组词直接映射
（`,` → `，`、`\` → `、`、`<` → `《`）；双/单引号各自按开闭交替配对；
句号/逗号同键连按在「全角→半角」间循环（。→.→。，，→,），任何其他
输入/上屏即回到首项。组词中的 `'` 仍是音节分隔符，不作为标点。
完整映射表见 `engine/punct.go`。

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

小鹤按 Rime double_pinyin_flypy schema 键位实现；该 schema 与官方
小鹤键位存在出入（ia/ua 键位等），官方键位图为图片无法文本核验，
故以可核验的 schema 语义为准。

## 快速开始（阶段 0 测试台）

```sh
# 本地内嵌引擎模式
go run ./cmd/chaoset-cli

# 或经 IPC 连接 daemon（先起 daemon）
go run ./cmd/chaosetd &
go run ./cmd/chaoset-cli --socket chaoset.sock
```

测试台与 daemon 默认都加载内置常用词库 `data/dict_common.tsv`（见「内置词库与再生成」），
想换小词库或叠加自备词库用 `-dict` 指定（可多次）。

测试台操作：输入拼音回车看候选（如 `nihao`、`xian`、`xi'an`、`nih`）；
单独一个数字 `1`–`9` 选择对应候选并「上屏」；`=` 下一页 / `-` 上一页
（每页 9 个，超出部分可翻页查看）；标点键（如 `,` `.` `\` `"`）
输出中文标点，组词中先上屏首选；`:b` 退格；`:r` 重置；`:q` 退出。
带 `*` 的候选表示被用户数据（调频/词组/上下文）加权过。

试一下词组进化：`jianpan` → `1`、`shubiao` → `1`，分字打两轮「键盘鼠标」，
再输入 `jianpanshubiao`——整词「键盘鼠标」会直接出现在第一位。

## 词库格式

TSV，每行三列（`#` 开头为注释）：

```
你好	ni hao	99
西安	xi an	70
```

拼音为无声调小写，音节间空格分隔；一字多音写多行；重复词条取较大词频。
非法音节会导致解析报错（防止脏数据静默进入词库）。

**词频的约定取值是 1–99**，内置词库与导入工具都按这个区间生成：

- 严格模式（`-dict` 加载的主词库，含 `data/` 下两份）只校验「正整数」，
  不强制上限；写成 200 不会报错，但**没有额外收益**（原因见下）；
- 宽容模式（`chaoset-import` 的热词导入）缺省补 20、超过 99 的**压到 99**。

上限之所以是 99 而不是「越大越好」，是因为静态词频只是**基础分**：用户学习权重
按次累加（`engine.UserBoostPerCount = 500`，上下文共现
`BigramBonusPerCount = 250`），**一次上屏就远超整个 1–99 区间**。
所以把 jieba 词频对数压缩进 1–99 完全够用，且不妨碍「越用越顺手」：选过的词
靠 500×次数×时间衰减排到前面。反过来说，把词库词频写成几百上千既没有收益，
还会让调频效果被静态分淹没。

### 内置词库与再生成

仓库自带 `data/dict_common.tsv`（内置常用词库），是 daemon 与测试台的默认词库；
`data/dict_seed.tsv` 退化为演示/回归用的小词库（数百条，跑测试与容器验证时够用）。

当前这份的实测规模：**143,824 条 / 3.0 MB**，词频区间 **6–99**（不是 1–99——jieba
最低频的词经对数压缩后落在 6，最高频的「了/在/是」为 99）。按词长分布：1 字 11,578、
2 字 72,994、3 字 21,696、4 字 36,452、5 字 775、6 字 329。

该文件**由工具生成，禁止手工编辑**。生成源是三个上游数据文件（均 **MIT**，不随仓库分发）：

- jieba 词频表 `dict.txt`（每行「词 词频 词性」）：
  <https://raw.githubusercontent.com/fxsjy/jieba/master/jieba/dict.txt>
- phrase-pinyin-data 的 `large_pinyin.txt`（每行「词: pīn yīn」，词语拼音）：
  <https://raw.githubusercontent.com/mozillazg/phrase-pinyin-data/master/large_pinyin.txt>
- pinyin-data 的 `pinyin.txt`（单字读音，`U+4E00: yī,yí,yì  # 一` 格式，补多音字）：
  <https://raw.githubusercontent.com/mozillazg/pinyin-data/master/pinyin.txt>

再生成（在仓库根目录）：

```sh
go run ./cmd/chaoset-dictgen -jieba dict.txt \
  -pinyin large_pinyin.txt -single pinyin.txt -o data/dict_common.tsv
```

四个 flag：`-jieba`（词频，必填）、`-pinyin`（词语拼音，必填）、
`-single`（单字拼音，可选）、`-o`（输出 TSV，必填）。
生成过程约 0.8 秒（含读三个源文件）。`-single` 的作用是补多音字：词语拼音表
里没有的单字，靠它拿到读音；**不传时这些单字会被跳过**（不会瞎猜拼音）。

生成规则（实现见 `cmd/chaoset-dictgen`）：

- 只收纯 CJK 汉字词条——基本区 U+4E00–9FFF、扩展 A U+3400–4DBF、兼容表意区
  U+F900–FAFF 与「〇」U+3007，长度 1–6 字；
- 拼音经 `engine.NormalizePinyin` 归一化（去声调、ü→v、`u:`→v，连写自动切分），
  该函数内部**逐音节**做 `IsSyllable` 校验，任一首节非法即返回 nil、整条丢弃
  （与热词导入走同一套规则，不把脏数据塞进词库）；
- **拼音音节数必须等于字数**，不等就丢弃（避免「西安」配成单音节这类错配）；
- jieba 原始词频按对数压缩映射到 **1–99**（单调、可复现），保证常用词压过生僻字；
- 输出按词排序，**同一输入重复运行逐字节一致**（幂等，由 `TestGenIdempotent` 兜底）。

文件头注释同样记录了来源 URL、许可与再生成命令；换了上游数据请重新生成而不是手改。

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
# 三个基准都在 engine/scale_bench_test.go，词库为代码合成的 10 万条双音节词，
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
| `BenchmarkLoadCommonDict` | 解析 143,824 条真实词库建索引（daemon 启动时跑一次） | ~109ms |
| `BenchmarkComposeCommonDict` | `jingchang` 完整组词（含全量候选排序 + 分页切片） | ~0.0097ms/键 |

词库自身规模：**143,824 条 / 3.0 MB**，词频区间 6–99；按词长分布为
1 字 11,578、2 字 72,994、3 字 21,696、4 字 36,452、5 字 775、6 字 329。
即换用真实词库后 daemon 冷启动多花约 0.1 秒，而逐键组词仍在**微秒级**（约 10μs），
对打字延迟没有可感知影响。

## IPC 协议

帧格式：4 字节大端长度 + JSON 载荷（上限 1 MiB）。

- 请求：`{"id":1,"method":"input","params":{"text":"nihao"}}`
- 响应：`{"id":1,"result":{...}}` 或 `{"id":1,"error":"..."}`

方法：`ping` / `input` / `backspace` / `reset` / `select`（`{"index":0}`，
返回 `{"committed":"你好"}`）/ `nextPage` / `prevPage`（候选翻页，返回
current 组成状态，含 `page`/`page_count`）/ `punct`（`{"key":","}` 中文标点：组词中
返回 `{"committed":"你好，"}`，空组词返回 `{"committed":"，"}`）/
`candidateImage`（v2 统一候选窗：返回
`{"width","height","png":<base64>,"scale"}`，daemon 以 `-font` 启用；无候选时
`png` 为空；未配置字体时报错，壳回退 v1 系统候选 UI）。候选与预编辑
结构见 `engine.Candidate` / `engine.Composition` 的 JSON 标签。

`candidateImage` 支持两个**可选**参数（Go 侧为 `ipc.CandidateImageOptions`）：

- `dark`（bool）：按系统外观选深色皮肤；不传则用 daemon 侧皮肤的原样配色。
- `scale`（int）：像素倍率，**只有 2 生效**（Retina 双倍像素），其余值一律按 1。
  壳侧应把图片显示尺寸设为 `像素 / scale`，高分屏才清晰。

返回体里的 `scale` 是 daemon **实际采用**的倍率（1 或 2）：传了不支持的倍率时
以它为准换算，不要用请求值。**不带参数时行为与旧版完全一致**（原皮肤、scale=1），
因此未接入新参数的 Linux / Windows 壳继续正常工作。

传输形态：Linux/macOS 用 Unix socket；Windows 壳经 TCP 回环
（daemon `-tcp 127.0.0.1:7949`，仅本机回环）。

## 平台壳

| 平台 | 壳 | 状态 |
|---|---|---|
| Linux | ibus engine（Go 内嵌引擎），DBus 直连 ibus-daemon | 已实现（容器内端到端验证；真机桌面闭环待环境） |
| macOS | InputMethodKit `.app`（Swift），Unix socket 连 daemon | 已实现（编译 + IPC 运行时验证；打字闭环待真机启用） |
| Windows | TSF COM DLL（C++ 薄壳），TCP 回环连 daemon；预编辑合成 + 数字盲选 | 已实现（核心行为级 + DLL 编译级；真机联调待环境） |

Windows 薄壳健壮性落实情况：daemon 未就绪时按键直接放行（已实现，核心层
行为验证）；传输失败即断开降级、不卡宿主（已实现）；传输走 TCP 回环
（替代早期命名管道方案）；自绘候选窗与 UWP/AppContainer 场景待真机。
逐项状态见 shims/windows/README。

> **macOS 输入源标识**：输入源 ID / `CFBundleIdentifier` 为
> `ime.chaoset.inputmethod.ChaosetIME`，输入模式为
> `ime.chaoset.inputmethod.ChaosetIME.Hans`。**bundle id 必须含内层
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
        Windows 壳贴图待真机环境
- [x] 阶段 5：中文标点——引擎侧统一映射（Session.Punct + IPC punct），
      CLI 与三端壳转发接入
- [x] 阶段 6：候选翻页——引擎内分页（Composition.page/page_count，
      每页 9 个）+ IPC nextPage/prevPage，CLI 与三端壳（- / =）接入
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
      横排候选栏**（见阶段 20）。宣纸/朱红观感**不再是任何内置默认**：要复现须显式
      把 `Skin.Horizontal` 设为 false **并**逐个覆盖 `BgColor`/`BorderColor`/`FgColor`
      等配色字段（仅切 `Horizontal` 只会变成竖排布局、配色仍是系统浅色）；
      输入源图标也已由「印章」改为与系统输入法一致的扁平 keycap。
      视觉样例不入库：跑 `go test ./renderer/ -run TestWriteVisualSample` 现场生成到
      `.workwork/`（已 gitignore），共 9 张：横排浅色/深色、浅色 scale=2、深色 scale=2、
      翻页、9 候选、窄栏换行、超 3 行截断，以及竖排 legacy 对照图。
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

## 验收范围确认（2026-09-23）

经会话内穷尽全部可脚本化手段（mingw-w64 交叉工具链、Docker 容器 ibus
环境、TIS 注册、系统安装、哈希比对均已完成）后确认：以下三项联调收尾
**移出本轮验收范围**，其余任务全部完成并验收。

| # | 移出项 | 硬门控条件 | 恢复方式 |
|---|---|---|---|
| 1 | macOS 打字闭环验证 | 需人工在系统设置添加 ChaosetIME 并实际打字（已实测 `AXIsProcessTrusted=false`，按键模拟被系统策略禁止，无法脚本化） | 按 shims/macos/README 打字验证 |
| 2 | Windows TSF 真机联调 | 需 Windows 环境（COM 注册/按键/渲染） | 按 shims/windows/README |
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