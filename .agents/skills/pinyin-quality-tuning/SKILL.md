---
name: pinyin-quality-tuning
description: chaoset_ime 拼音匹配与输入预测的质量调优流程（Rust 引擎 crates/chaoset-engine）：如何量化「命中率/首选率」再改排序、常见打分陷阱（用户调频分被当词频、续接预测反噬完整输入）、参数扫描纪律。触发：改进候选排序、拼音匹配、输入预测/联想、末音节残缺输入出不来词、候选排序回归、需要前后对比证明改动有效。
---

# 拼音匹配与输入预测调优（本项目专用）

适用于 `crates/chaoset-engine`（引擎的**唯一实现**；旧 Go `engine/` 已删除）里与
「输入串 → 候选列表」相关的任何改动（切分、匹配层级、排序打分、续接预测、
联想、用户学习加权）。核心原则：**先用数据量化，再改代码；改完必须前后对比**。

## 一、先建量化基线（不要凭感觉改排序）

### 1. 写一个只读扫描器（用完删除，不提交）

在 worktree 里临时新建 `crates/chaoset-engine/examples/scan.rs`（`examples/` 目前
不存在，直接建目录即可），它通过 path 依赖直接 `use chaoset_engine::...`：

```sh
cargo run --release --manifest-path crates/Cargo.toml \
  -p chaoset-engine --example scan -- data/dict_common.tsv
```

骨架（按需补统计与输出）：

```rust
use std::sync::Arc;
use chaoset_engine::{Dictionary, PAGE_SIZE, Session};

fn main() {
    let path = std::env::args().nth(1).expect("用法: scan <dict.tsv>");
    let mut dict = Dictionary::new();
    dict.load_tsv(&path).expect("加载内置词库失败");
    let dict = Arc::new(dict);
    assert_eq!(PAGE_SIZE, 6);

    // TODO: 读 path，筛 2–4 音节、词频 ≥30、词长(rune)==音节数，
    //       按 (词, 拼音) 排序后每 25 条取 1（确定性采样）。
    let sample: Vec<(String, Vec<String>)> = todo!();

    let (mut hit, mut total, mut top1_hit) = (0usize, 0usize, 0usize);
    for (word, sylls) in &sample {
        for form in [
            sylls.join("'"),                    // full
            trim_tail(sylls, 1),                // tail1：最高频形态
            trim_tail(sylls, 2),                // tail2
            initials(sylls),                     // initials
            mixed(sylls),                        // mixed
        ] {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let Ok(mut comp) = sess.input(&form, false) else { continue };
            let mut all = comp.candidates.clone();
            while comp.page + 1 < comp.page_count {
                comp = sess.next_page();
                all.extend(comp.candidates.clone());
            }
            let key = sylls.join("'");
            let idx = all.iter().position(|c| c.text == *word && c.syllables == key);
            total += 1;
            if idx.is_some() { hit += 1; }
            if idx == Some(0) { top1_hit += 1; }
        }
    }
    println!("命中率 {:.1}%  首选率 {:.1}%  （{} 次输入）",
        100.0 * hit as f64 / total as f64,
        100.0 * top1_hit as f64 / total as f64, total);
}
```

（`trim_tail`/`initials`/`mixed` 是示意用的辅助函数，按下面场景表实现即可。）

口径（必须固定，否则前后不可比）：

- 词条池：`data/dict_common.tsv` 中 2–4 音节、词频 ≥30、且「词长(rune) == 音节数」
  的规则词条；
- **确定性采样**：按 (词, 拼音) 排序后每 25 条取 1。
  绝不能用 `HashMap` 遍历顺序采样——每次跑的词集不同，数字没有意义；
- 取全部候选用 `Session::next_page()` 翻到 `page_count`（每页 `PAGE_SIZE`=6）；
- 命中判定必须**同时匹配词与音节序列**（`c.text == word && c.syllables == sylls.join("'")`），
  否则同形词的不同切分会被误判为命中；
- release 构建下采样规模单次约 10 秒量级；性能数据要多次采样；
- README「实测数据」里的数字是某一固定口径下的历史基线，不是当前实测值；Rust 侧改动必须
  用本扫描器重新取数，不要把旧数字当现状；

场景定义（缺一不可，覆盖输入法的真实形态）：

| 场景 | 构造 | 为什么必须测 |
|---|---|---|
| `full` | 完整全拼 | 基准，必须不退化 |
| `tail1` | 去掉最后 1 个字母 | **最高频形态**：每个音节都必然经过「打了一半」 |
| `tail2` | 去掉最后 2 个字母 | 更早的中间态 |
| `initials` | 各音节首字母 | 简拼 |
| `mixed` | 首音节全拼 + 其余简拼 | 混拼 |

### 1.1 当前排序键与用户优先规则（改排序前先对齐）

候选管线（实现都在 `crates/chaoset-engine/src/session.rs`）：输入切分（全拼 /
简拼 / 混拼 / 多路径歧义）→ 连写 DP（`Dictionary::query_joined_pinyin`）→ 常规
查询（精确 / 前缀 / 末段续接 / 逐级回退）→ 组合候选
（`RANK_COMPOSITION`，`add_composition_candidates`）→ 单字兜底
（`RANK_SINGLE_BACKFILL`）→ 错拼修正与提升（`add_typo_corrections` /
`promote_typo_corrections`）→ 排序 → 纠错固定第 3 位 → 词典词组封顶 2 页
（`cap_dict_phrases`）→ 分页（`PAGE_SIZE`=6）。

匹配层级常量（数值越小越靠前，`session.rs` 顶部）：
`RANK_EXACT`=0、`RANK_INITIAL_EXACT`=1、`RANK_CONTINUATION`=2、
`RANK_INITIAL_PREFIX`=3、`RANK_PREFIX`=4、`RANK_BACKOFF`=5、
`RANK_CONTINUATION_LEAD`=6、`RANK_TYPO_FIX`=7、
`RANK_COMPOSITION`=`RANK_BACKOFF`+64、`RANK_SINGLE_BACKFILL`=`RANK_COMPOSITION`+16。

排序键从高到低：

1. **常规层 vs 逐级回退层**：`rank >= RANK_BACKOFF` 的候选后置；
2. **超段仲裁（仅全拼输入）**：用户候选按 `shape.max_input_segs`（所有合法切分的
   最大段数）判，非用户候选按 `input_segs`（首选切分）严格判；简拼/混拼的 token
   数不是音节数，**不做**超段仲裁。显式分段 `tu'an`（2 段）下，学过「图案」
   （2 音节）不被当超段，可按用户输入优先；单段输入只出单字、不存在词语候选，
   不参与该仲裁；
3. **完整消费输入优先**：`rank <= RANK_INITIAL_EXACT`（精确/简拼精确，含提升到精确层的
   纠错）先于续接/偏长词——`shiji` 的 shi'ji 全部排在 shi'jie 之前；学过的续接词
   （shiji 下学过「世界」）也不越过精确层；
4. **用户输入 > 默认字频**：`from_user` 候选在同一消费层级内置顶（词 / 单字 / 整句
   记录），完整消费输入的用户词组查询无上限（`USER_PHRASES_UNLIMITED`）；
5. 匹配层级 `rank`（干净切分下的续接落 `RANK_CONTINUATION`=2，不再与精确同层比分）；
6. 综合得分（词频 + 用户加权 − 续接代价 `CONTINUATION_PENALTY`）、词长、字典序。

排序之后：高置信度纠错候选固定插到第 3 位（`promote_typo_corrections` 的排序契约），
再由 `cap_dict_phrases` 把**非用户多字词组封顶 2 页**（`MAX_DICT_PHRASES`=12；用户词组
与单字不计、不截断；只数不剔位——回退层的真实词组排在用户学过的单字之后是既有语义，
不要加「单字之后无词组」之类的剔除，Linux E2E 的导入词用例会红），最后按 `PAGE_SIZE` 分页。

**单字兜底**：任何多段非错拼输入都在列表末尾追加首段全量单字
（≤`SINGLE_SYLLABLE_EXACT_LIMIT`=300），层级 `RANK_SINGLE_BACKFILL`，列表最末
纯单字收尾；错拼输入（`shape.typo_suspicious` / `mid_suspicious` / `tail_suspicious`）
不追加——纠错候选才是主路径。

**单段输入只出单字**：单音节全拼（`diao`/`zhong`/`jie`）与单字母声母（`d`/`y`）
的拼音匹配层**只允许单字**，不出现任何词语：前缀扩展词（`diao` 的「调用」、
`jie` 的「结果/节约」）和连写全匹配词（`jie` 的「饥饿」、`xian` 的「西安」、
`tuan` 的「图案」）都不出。要打词用显式分段（`ji'e`/`xi'an`/`tu'an`）或完整
多音节输入；联想是上屏后的独立路径。`zh`/`ch`/`sh` 是多字母简拼，仍出词候选。
回归：`session.rs::test_single_segment_only_single_chars`（及同模块内单段相关测试）。

**两个 exact 上限不要混**：单音节全拼/单段声母（y/j/zh…）/单字兜底用
`SINGLE_SYLLABLE_EXACT_LIMIT`=300（同音字全量，yi 141 等）；**多段**简拼/混拼的
词候选用 `INITIALS_EXACT_LIMIT`=120（放大到 300 会让 zg/dz 候选膨胀到 ~80 页，
稀释单字页位）。

### 2. 拿基线：用 git stash 而不是另开目录

```sh
git stash push -- crates/chaoset-engine/src/   # 暂时退回 main 的引擎实现
cargo run --release --manifest-path crates/Cargo.toml \
  -p chaoset-engine --example scan -- data/dict_common.tsv
git stash pop
git status --short                            # 确认改动回来了
```

扫描器放在 `examples/`，**不要**把它放进被 stash 的 `src/`；跑完记得删除
`examples/scan.rs`（`git status` 确认），不要提交扫描器。

## 二、七个打分陷阱

### 陷阱 1：拼合造词的边界

候选**首屏只能来自已存在的词**：词典词条 / 用户词库 / 学习记录。不得用多词 DP
把词典片段自由拼成复合候选（`nihaoma` →「你号码」、`womende` →「我们的」、
`zhonghuarenmingongheguo` →「中华人民共和国」）占据首屏。长输入的正确形态是
「最长已有词前缀 → 更短前缀 → 首音节单字」，由用户逐段/逐字 `SelectPrefix`
拼出整句；联想同样只查词典/用户词库。

**注意区分「组合」与「匹配」**：

- 词库里真实存在的词条仍要用 DP/连写匹配兜底（`query_joined_pinyin`，避免音节
  切分枚举上限漏掉长词），单字候选始终保留；
- 「已有词组合」候选（长输入=长+输入、图中=图+中，每一段都是词库已有词）只在
  `RANK_COMPOSITION` 层（所有真实词条与回退碎片之后）给出，不能改变原有首选；
- 错拼修正路径是唯一例外：只有该输入没有任何整词级纠错命中时，组合候选才提升
  到精确层（最多 8 个）。错误路径的 DP 在 `src/typo.rs::composed_phrase_entries`，
  正常输入的组合在 `session.rs::add_composition_candidates`；
- **反序双字母分两档**（`syllable.rs` 的 `SOFT_MISTAKE_PAIRS` 等规则表）：
  - 强制归位（gn/hz/hc/hs/ev → ng/zh/ch/sh/ve）：**只在无法完整切分时**才触发
    （`gongneng` 的 gn 跨 gong+neng 边界、纯简拼 `gn` 都合法，不能误判），
    触发时只提升归位修正、通用编辑保持低层，否则会被常规混拼（`hzong` 的
    「汇总」）或用户学习词挤掉；
  - 软纠错（oa/au/eu → ao/ua/ue）：只在纠错路径里最高优先级，不强制触发、不无条件
    提升（可能与合法混拼同形）。
  两侧都合法的相邻互换（ai/ia、ui/iu、ou/uo、ei/ie、an/na、en/ne、in/ni、un/nu）
  不要强行纠错。回归：`session.rs::test_common_mistake_bigrams_in_words` 等。
- **高置信度纠错候选固定第 3 位起**：排序后把 `typo_fix` 且已提升到精确层的
  纠错整体插到第 3 位；低置信度纠错仍在末尾；无纠错时按正常排序。改动候选排序
  必须保留该位置契约（`promote_typo_corrections` 与相关测试）。
- **任何多段输入都可逐字挑选（单字兜底）**：回退本身仍只取首段 top10（避免
  候选池/翻页膨胀）；但任何多段输入（全拼/混拼/简拼，含 `shipei`、`kkdz`、
  `zg`）都在列表末尾追加首段全量单字（`RANK_SINGLE_BACKFILL`，列表最末纯单字
  收尾，单字块之后不得再出现词组），保证 `SelectPrefix` 能选到 top10 之外的字
  （shipei → 适 + 配）。上限 `SINGLE_SYLLABLE_EXACT_LIMIT`=300；单段输入本身
  已是全量单字，不重复追加。错拼输入不追加（会破坏「纠错固定第 3 位」）。
  回归：`test_backoff_promotion_and_single_backfill`、
  `test_typo_input_has_no_single_char_backfill`。
- 任何改动都必须保持「真实词条优先」：组合候选不得进入首屏（`PAGE_SIZE` 内）。

若将来重新评估任何拼合逻辑，注意这个坑：`UserDict::score` 返回「次数 × 500 ×
时间衰减」，量级数千，静态词频只有 1–99——直接把它当词频再乘音节数，用户学过
的单字会放大成几千分霸榜，把完整词挤成碎字拼接（`wmd` 出「我没的」）。用户权重
必须走单独的上下文加成分支，不能当静态词频用。

### 陷阱 2：续接预测反噬完整输入

末音节在输入串上**无法区分**「已打完」与「还差几个字母」：
`yisha` 既可能是 `yi'sha`（一霎，完整）也可能是 `yi'shang` 的前缀。
如果无条件把续接候选置于最高优先级，「一霎」会被高频的「以上」压掉（旧 Go 实现
实测全拼首选率 80.75% → 77.95%，Rust 侧请用扫描器复测，不要沿用旧数字）。

现行做法（用户明确要求「完全消耗所有输入拼音的结果优先」）：干净切分下续接落
`RANK_CONTINUATION`=2，**严格排在精确层之后**，层内再按「词频 − `CONTINUATION_PENALTY` ×
补全字符数」竞争；只对「输入压根无法切成干净音节」（`quanmia` → `quan'mi'a`）的情况
让续接强制领跑（`RANK_CONTINUATION_LEAD`）。固定口径扫描（1131 词 × 5 形态）：full 首选
80.3→80.5、首页命中不变；tail1 首选 46.5→43.0、首页 88.2→87.0——这是已知且被接受的
代价（`zhonggu` 先给 中古，中国 紧随首页第 2～4 位仍可达）。`CONTINUATION_PENALTY`=30
仍是层内定序的定型值，续接不要再提回 `RANK_EXACT` 同层比分（那是旧行为：高频续接
「世界」会插到 `shiji` 的精确词「事迹」前面）。

### 陷阱 3：把「末段是完整音节」当成不需要预测

`zhonggu` 的末段 `gu` 本身是合法音节，直觉会认为「精确匹配已经够了」。
反例：按此直觉实现，只能出「中古」，「中国」永远不可达。
判据应是「该音节还能否延长（是否存在以它为前缀的更长音节）」，
不是「它本身合不合法」。见 `syllable.rs::syllable_continuations` 与
`dict.rs::query_continuation`。

### 陷阱 4：形态判据必然误伤「手误 vs 合法混拼」

错拼修正（`zhogn` → 中）最容易写错的地方，是试图用**串形态**区分「手误」与
「合法简拼/混拼」。这条路走不通，因为两者在字形上高度同形：

| 输入 | 是手误吗 | 能切成什么 |
|---|---|---|
| `nihap` | 是（想打 nihao） | `ni + ha + p`（有完整音节） |
| `chifanl` | 否（吃饭 + l） | `chifan + l`（有完整音节） |
| `zhogn` | 是（想打 zhong） | `z + h? + o + g? + n?`（全是碎片） |

正确做法是**双层设计**：
1. 预筛只用形态做**保守排除**（能切成「完整音节 + 声母」的一律信任，见
   `syllable.rs::has_clean_full_split` / `has_clean_syllable_split`），目的是省
   开销，不是做最终判断；
2. 是否让纠错候选领跑，由**词典事实**决定：纠错命中的词频必须 ≥ 常规单词级
   候选最高词频的若干倍（本项目取 2，见 `session.rs` 的 `TYPO_PROMOTE_RATIO`）。

旧 Go 实现直接放开（纠错候选无条件置顶）的代价：合法混拼首选率 34.32% → 20.75%，
且 `zhongg` → 中国 这类既有用例直接失败。Rust 侧改动必须用扫描器复测该场景。

另一个必须避开的坑：比较「常规候选有多强」时**只能用单词级精确匹配**
（`RANK_EXACT` / `RANK_INITIAL_EXACT`），不能用 Score 或任何含「拼接/组合产物」的
候选。DP 拼接候选的 Score 含多字词奖励，碎片拼接「中欧个」能到很高分，而真实
高频单字「中」很低——拿它做门槛，纠错会被永远挡住。

### 陷阱 5：纠错生成的性能陷阱（毫秒级延迟）

单次编辑会产生上百个修正串。两个开销必须分开控制：

- **生成便宜**（纯音节 DP 校验，旧 Go 实现实测约 39µs；Rust 侧需复测）；
- **每条一次的词典 Trie 查询很贵**（旧 Go 实现 160 条约 2ms，`zhonguo` 从 0.8µs
  涨到 25ms）。

因此：① 修正串只取**首条切分**，不枚举全部歧义路径；② 实际查询条数用独立
常量封顶（`session.rs` 的 `TYPO_MAX_QUERIES`=32），不要让它等于生成上限。另外
生成时的候选过滤要用**纯音节 DP**（`syllable.rs::has_clean_syllable_split`），不能
用完整预筛——后者内部还会做一次切分，而上限外的前几千条候选都会被白跑一遍。

### 陷阱 6：学习到的整句变成「整词贪婪匹配」

**学过的整句不得变成整词贪婪匹配**：学过「今天星期一」后，打「今」不能直接给候选
「天星期一」；否则用户这次只想打「今天」时会被整词候选/联想尾巴挤掉，
之前的输入习惯反而变成负资产。

反例（整句候选不加分段限制时）：
- 整句候选一键上屏 → `bump_word` 把任意长整句写进 Words（无长度上限），
  打 `jin` 时整句排在「今天」之前，上屏「今」的联想首条直接是「天星期一」
  （score 500，第 2 条才是「天」70）；
- 尾巴还能被 `associateSelect` 再学习：之后 `tian` 下「天星期一」反超「天气」，
  形成污染循环；
- 音节数多的组合在只约束 Phrases 表时可能完全学不到。

正确做法（`session/learn.rs` 的上屏学习与联想剥前缀逻辑 + `userdict.rs` 的 `segs` 字段）：
1. 学习记录带**分段**：整句 `今天星期一` → `segs=[今天,星期一]`
   （`userdict.rs` 的 `bump_word` / `bump_phrase` 均接受 `segs`）；
2. 整句候选**只在完整输入**时参与（部分输入的前缀层过滤整句式记录，
   `session/learn.rs::is_sentence_entry`）；
3. 联想剥前缀只允许在**分段边界**（`strip_at_segment_boundary`）：今天→星期一 ✓、
   今→天星期一 ✗；普通词典词/词组进化的行为不变；
4. 「一键整句上屏」按逐段确认的等价路径学习分段词，与逐段选择落同一套数据；
5. 旧数据没有 `segs` 时用主词典切分推断边界（`infer_segs`），推断不出按普通词
   处理；推断必须用「分段数最少」DP，**不能用最长词贪心**（词典里「天星」这类
   长词会把「天星期一」切成 [天星,期,一]，联想上屏「天」后丢失「星期一」）。

回归测试必须双向（`session.rs::test_user_sentence_not_prefix_candidate`、
`test_associate_sentence_only_at_segment_boundary`）：正向证明完整输入时学到的
整句确实被加权、分段边界续接仍在，反向证明部分输入/联想不出现整词尾巴且
「今天」首选不受影响。只测正向的话，「有整句记录就无条件置顶」这种粗暴实现也能
通过。

两个容易漏的边界：

- **渐进段累积要随组词中止清理**：`seq_segs`（`session/learn.rs`）若只在 `select` 清，
  退格到空后重新组词会把已经上屏的段拼成伪整句（「今天」→退格到空→打 wo 选「我」
  →写出「今天我 segs=[今天 我]」）。现行清理点：`input` 从空组词开始时、`reset`、
  `select`、`associate_select`（回归：`learn::progressive_segments_reset_when_composition_restarts`）；
- **旧数据分段推断不能用最长词贪心**（见上）。

### 陷阱 7：移植/重构把学习路径整体丢掉

Rust 移植 Go 引擎时，`learnSelection`（history + `learnPhrase` 词组进化）与 `seqSegs`
（渐进选段整句固化）都没有带过来：`select`/`select_prefix` 只对单段 `bump_word`，
用户逐段拼出的「你好世界」从不进词库，表现为「自己输入过的词组没有自动记忆」。
这类丢失**不会让任何现有测试变红**（冻结快照只回放输入/选词，不验证学习结果），
只能靠「学过 → 再输入置顶」的端到端用例抓住。

核对方法（移植、跨语言重写、按职责拆文件后各跑一次）：

```sh
# 源实现里每个只被学习路径引用的符号，在新实现里必须能找到对应物；
# 找不到就是丢了，不是「优化掉了」
git log --oneline -S'seqSegs' -- engine/            # 源实现的符号出现在哪些文件
grep -rn 'seq_segs\|learn_phrase\|learn_selection' crates/chaoset-engine/src/
```

判据：学习入口必须只有一处（`session/learn.rs::learn_selection`），`select` /
`select_prefix` / `associate_select` 都走它；端到端回归至少覆盖「渐进拼词 → 再输入
置顶」「相邻共现满阈值 → 词组」「部分输入不出整句尾巴」三条（`learn.rs` tests）。

## 三、参数扫描纪律

任何引入的经验参数（惩罚系数、阈值）都必须：

1. 在**完整基线数据**上扫描至少 4–5 个取值，把每个取值的关键指标记下来；
2. 选值要有明确理由：本项目 `CONTINUATION_PENALTY = 30` 是唯一同时保住
   「full.top1 不降」与「tail1.top1 最大」的点（20 侵蚀完整输入，40 收益缩水）；
3. 把扫描结论写进代码注释与 README，不留在对话里——否则下次有人「顺手调一下」
   就会破坏平衡且无人知道边界在哪。

## 四、改动后的验证清单

```sh
# 格式与静态检查（engine 是 Rust；Go 只剩 dictgen 与 checker）
cargo fmt --all --manifest-path crates/Cargo.toml --check
go build ./...
make vet

# 单元测试 + 数据契约 + 41 场景冻结基线（行为回归会自动红）
cargo test --manifest-path crates/Cargo.toml

# Rust 发布构建与本机实测（Go-free：CLI + daemon + dictc）
make build

# 词库重新生成仍走 Go 构建期工具（`make build-tools` 产出 bin/chaoset-dictgen）
go run ./cmd/chaoset-dictgen ...

# CLI 统一用 chaosetctl（单二进制 multicall）：chaosetctl version / client / config ...
```

41 个冻结场景（`crates/chaoset-engine/tests/parity_scenarios.rs` +
`tests/data/parity_expected.jsonl`）覆盖候选文本/打分/预编辑/分页/上屏；**任何
匹配/排序行为的有意改动都会让它失败**，必须重新生成快照并在 PR 里说明依据：

```sh
cargo run --release --bin replay --manifest-path crates/Cargo.toml -- \
  --dict data/dict_common.tsv \
  --scenarios crates/chaoset-engine/tests/data/parity_scenarios.jsonl \
  > crates/chaoset-engine/tests/data/parity_expected.jsonl
```

性能数据必须多次采样对比（旧 Go 基准已随引擎删除；Rust 侧没有内建 benchmark，
用临时 `examples/scan` 计时或多次 release 构建实测），单次结果抖动大，容易把噪声
判成回归。

## 五、回归测试要守住的两个方向

新增排序/匹配能力时，测试必须**双向**覆盖，缺一不可：

- **正向**：新能力真的生效（`zhonggu` → 中国、`quanmia` → 全面）；
- **反向**：不反噬既有行为（`yisha` → 一霎、`nihao` → 你好、`jingchang` → 经常）。
  只写正向测试的话，「把所有预测候选置顶」这种粗暴实现也能通过。

参考 `session.rs` 的续接测试（预测生效 / 不反噬完整输入 / 常用词守门三段式），
以及 `tests/parity_scenarios.rs` 的冻结基线。
