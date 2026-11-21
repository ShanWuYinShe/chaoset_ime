# 词库:规模、生成管线与维护

内置词库 `data/dict_common.tsv` 是 `cmd/chaoset-dictgen` 的生成产物,禁止手工编辑;
本文记录规模基准、再生成流程、手工补充词表规则、校验分层与相关测试。

## 内置词库与再生成

仓库自带 `data/dict_common.tsv`（内置常用词库），是 daemon 与测试台的默认词库；
`data/dict_seed.tsv` 退化为演示/回归用的小词库（数百条，跑测试与容器验证时够用）。

当前这份的实测规模：**200,000 条 / 4.6 MB**，词频区间 **1–99**。按词长分布：
1 字 8,710、2 字 86,370、3 字 47,951、4 字 51,287、5 字 3,826、6 字 1,856。
覆盖通用规范汉字表 8105 全部单字，以及现代日常生活、职场工作、信息科技AI、社会政务、医疗健康与现代消费各领域常用词汇。
数据规模与候选质量基线以上方「实测数据（可复现）」为准（同一 1,071 采样口径）。
`crates/chaoset-engine/tests/dict_contract.rs` 另保证：内置词库 ≥6 万条、单字 ≥8,000、音节数等于
字数、`dict_extra`/`dict_hot` 全部合并；当前词库实际覆盖通用规范汉字表 8105 全部单字。
规范常用词覆盖/精度与 98 热词探针属于词库生成期的固定词表评估（脚本未入库），
不作为候选排序回归指标。

**只收简体**：内置词库只收简体字、常用简体字词组；繁体、异体、粤语/方言用字
**不内置**。需要时通过额外词库加载——daemon `-dict` 可重复叠加多个 TSV，
或设置窗「词库导入」/`chaoset-import` 把自备词库合并进用户词库；额外词库
在运行时合并，不写进内置产物。

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
多音字**首读音必留**，其余读音只有在 large_pinyin 词条里占比 ≥ 1%
（`-readingshare`，默认 0.01）时才保留——pinyin-data 中混有
「刀 diāo」「跳 diāo」这类词条中零出现的误收读音。单字词频严格按
**「字频 × 该读音在 large_pinyin 词条里的占比」**折算，不设任何下限：
占比 1% 的读音只有字频的 1%（≤1 分），不会靠固定下限抬进高优先候选。
口语助词（吗/呢/吧）小幅补偿；输入 lue 时「略」排在「率」前。

**GSC 漏收的常用字**：原始单字表是通用规范汉字表 8105（国家标准，不在仓库内、
不修改），其中确实没有「屌/囧/艹/诶/磺」等字。生成前用 wordfreq 中文前 5 万词
扫出非 GSC 字，剔除繁体/异体/粤语/日文新字体后人工确认，写进
`data/dict_extra.tsv` 随词库生成合并；词频按 wordfreq 字符常用度折算
（屌≈22、囧≈19、艹≈20、诶≈21、磺≈13），**不特殊置顶**，与同音常用字
正常竞争（diao 下 掉27/调26/屌22/雕19…）。

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

单字读音质量刷新（没有完整生成源时）：以现有词库为基线，保留全部多字词，
只按 pinyin-data 读音 + large_pinyin 占比筛选单字读音，并按
「Jun Da 字频 × 读音占比（无下限）」重算词频，追加 `data/dict_extra.tsv` /
`dict_hot.tsv` 里基线还没有的新词：

    go run ./cmd/chaoset-dictgen -base data/dict_common.tsv \
      -pinyin large_pinyin.txt -single pinyin.txt \
      -charfreq hanzi_db.csv \
      -extra data/dict_extra.tsv -hot data/dict_hot.tsv \
      -o data/dict_common.tsv

刷新只删除误收/极低占比读音并重算单字词频，多字词原样保留（候选质量扫描口径不变）。

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
| `-readingshare` | 单字次读音保留门槛（默认 0.01；低于占比的读音丢弃，首读音必留） | 否 |
| `-base` | 单字刷新模式：以该词库为基线，只重算单字、保留多字词 | 否 |
| `-o` | 输出 TSV | 是 |

生成过程约 2 秒；同一输入重复运行逐字节一致（`TestGenIdempotent` 兜底）。
`-review-out` 输出的补拼音校对清单只包含不在 phrase-pinyin-data 的词，重点复核
`confidence < 0.9` 与含多音字的行。

生成规则（实现见 `cmd/chaoset-dictgen`）：

- 只收纯 CJK 汉字词条——基本区、扩展 A、兼容表意区与「〇」，长度 1–6 字；
- 拼音经 `internal/dict.NormalizePinyin` 归一化并逐音节 `IsSyllable` 校验，音节数必须
  等于字数，否则丢弃（防止错位拼音进词库）；
- 高频用法词必须通过词表交叉验证（phrase-pinyin-data / THUOCL / HanLP），
  wordfreq 的分词噪声（这是/这我/一给）不会入库；
- 繁体变体剔除**只对高频用法来源生效**：`internal/dict.ToSimplified` 是 OpenCC 简→繁表
  的反向映射，对「像/象」这类简繁同形字会误判（好像→好象），规范词表/HSK/
  手工词表不做该过滤，避免误删常用词；
- 输出按词频降序、同频按词、再按音节排序，确定性可复现。

## 手工补充词表 `data/dict_extra.tsv` 与 `data/dict_hot.tsv`

这两个是词库链路里**允许手工编辑**的词表（格式：词 / 拼音 / 权重，一行一条）：

- `data/dict_extra.tsv`：口语高频词与常用组合（真好/来不及/对不起/很多/坐着 等），
  补词表与拼音链路漏收的词；
- `data/dict_hot.tsv`：**现代/热词**（智能家居/健康码/微信支付/大模型/内卷 等），
  **随 app 版本内置更新**（聚合自 `data/categories/` 8 大分类词库：流行语、社交互动、
  职场打工人、游戏二次元、情绪心理、现代科技AI、网络特有字、现代生活网红消费）；每次发版前复核一次（新增热词、下架过时词）。

权重是「估算原始词频」量级，生成器按 `ln(w)/ln(1e6)`
压缩到常用词中高区间（4000≈60 分、1000≈50 分、100≈35 分）；与主词表重复时取
较大词频，不需要从词表删除重复词。改完必须重新生成 `data/dict_common.tsv` 并
提交两者，生成产物仍禁止手改；`crates/chaoset-engine/tests/dict_contract.rs` 会在 CI 断言
「手工词条格式合法 + 已合并进内置词库」。

维护规则：只收「确实常用、且上游词表/词频源没有或明显偏低」的词；拼音必须逐条
核对（生成器会用 `internal/dict.IsSyllable` + 音节数==字数强校验，非法即让生成失败）。

## 词库校验：构建期严格，运行期健壮

两件事分开处理，不混为一谈：

| 阶段 | 对象 | 策略 | 保证方式 |
|---|---|---|---|
| 构建期 | 内置词库（生成产物 + extra 补充表） | **严格**：一行脏数据即失败 | `cmd/chaoset-dictgen` 生成时强校验；`crates/chaoset-engine/tests/dict_contract.rs` 在 CI 严格解析 `data/dict_common.tsv` 与 `data/dict_extra.tsv`/`data/dict_hot.tsv`（规模、音节数==字数、词频区间、手工词表必须已合并） |
| 运行期 | 用户自备的第三方词库（`-dict`） | **健壮**：跳过坏行并汇总警告 | Rust 引擎的宽松加载（`Dictionary::load_tsv`）：坏行跳过、拼音归一化（去声调/ü→v/连写切分）、缺省词频补 20；daemon 把汇总写日志 |

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

## 词库规模与加载说明

内置词库 `data/dict_common.tsv` 的实际规模：**200,000 条 / TSV 4.6 MB**，词频区间 1–99；按词长分布为
1 字 8,710、2 字 86,370、3 字 47,951、4 字 51,287、5 字 3,826、6 字 1,856。

词库统一使用纯文本 TSV（Git 友好可审计）：Rust 引擎只实现 TSV 加载，不再维护二进制格式，
避免两套加载路径漂移。加载/组词性能以 `cargo test --release` 与真实输入体验为准；
旧 Go 引擎的 benchmark 已随引擎删除，本文不再写死跨版本性能数字。

## 词库相关的测试

内置词库是**生成产物**，所以它本身也被测试守着（都读真实的 `data/dict_common.tsv`）：

| 测试 | 守什么 |
|---|---|
| `crates/chaoset-engine` 单元测试（`session.rs`，读真实内置词库） | 用户反馈场景（`jingchang`→经常）、单音节常用字优先、完全匹配优先、用户学习加权、错拼不污染正常输入 |
| `crates/chaoset-engine/tests/dict_contract.rs` | 规模 ≥6 万条、单字 ≥8,000、音节数==字数、词频 1..99、手工词表已合并 |

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
| `TestCJKWordOK` | CJK 范围判定（基本区/扩展 A/兼容区/〇 与长度上限） |

改了生成逻辑后请同时跑：`go test ./cmd/chaoset-dictgen/` 与 `cargo test --manifest-path crates/Cargo.toml`。

## 发布产物：二进制词库（CSDB v1）

仓库只保留 **TSV**（可审阅、可 diff、可 git 版本管理）；发布包在打包阶段把 TSV 编译为
二进制词库随包分发，daemon 启动直接解析二进制（加载更快、内存更省）。源与产物同一份数据：

- 编译：`cargo build --release --bin dictc --manifest-path crates/Cargo.toml`，
  再 `crates/target/release/dictc data/dict_common.tsv out.bin`；同一 TSV 两次编译 sha256 相同；
- 加载：daemon 的 `-dict` 参数仍指向 TSV；`load_with_cache` 优先加载同目录同 stem 的 `.bin`
  （缓存 mtime 不旧于源时），缺失/过期/损坏则解析 TSV 并原子重建缓存（用户导入词库后自动刷新），
  只读目录只警告不失败；
- 实测（200,000 条，release）：4.67 MB → 3.61 MB（-22.7%），加载 139–170ms → 91–124ms（1.4–1.7x），
  daemon 启动 0.187s → 0.135s，进程 RSS 约 -5~6%；
- 行为等价：`crates/chaoset-engine/tests/dict_binary.rs` 断言 TSV/bin 加载逐条一致、41 场景回放
  stdout 逐字节相同、损坏二进制/缓存会被拒绝并回退；
- 随包位置：macOS app `Contents/Resources/dict_common.bin`、Linux `/usr/share/chaoset/dict.bin`、
  Windows 安装器用随包 `chaoset-dictc.exe` 为 `%LOCALAPPDATA%\Chaoset\dict.tsv` 预生成 `dict.bin`；
  `.bin` 是生成产物，已在 `.gitignore` 忽略。
