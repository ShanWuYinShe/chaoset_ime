# chaoset-ime

跨平台中文输入法（macOS / Linux / Windows）。

架构：**Go 引擎 + 守护进程（daemon）+ 各平台薄壳**。引擎与组词逻辑只写一份，
平台壳只负责「按键转发 + 文本上屏 + 光标定位」，通过 IPC 与 daemon 通信。
该形态已被业界验证（小狼毫的 TSF DLL + WeaselServer、搜狗的多进程形态、
macOS InputMethodKit 本身就是进程间模型）。

```
┌──────────────────── chaosetd (Go，一份代码) ────────────────────┐
│  engine：拼音切分 / 词典检索 / 候选排序 / 词组进化自学习           │
│  ipc  ：length-prefixed JSON over Unix socket / 命名管道          │
└────────┬───────────────────┬───────────────────┬───────────────┘
   macOS │             Windows│              Linux │
  IMK 薄壳          TSF COM 薄壳 DLL       ibus engine（DBus 胶水）
  (Swift)           (C++，仅转发+上报)
```

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
                   皮肤为中国传统配色（宣纸底/朱红双框/浓墨候选/朱批首选竖记）
cmd/
  chaosetd/          守护进程二进制（-dict 可多次叠加；-font/-fontsize 候选窗渲染）
  chaoset-cli/       阶段 0 终端测试台（本地内嵌引擎或连接 daemon）
  chaoset-import/    热词导入工具（合并输出 TSV）
  chaoset-userdict/  用户词典管理工具（统计/清空/导出，IPC 或本地文件）
data/
  dict_seed.tsv      种子词库（演示规模，词 \t 拼音 \t 词频）
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

### 接入 CC-CEDICT 开源词库

`chaoset-import` 与 `-dict` 直接识别 [CC-CEDICT](https://www.mdbg.net/chinese/dictionary?page=cc-cedict)
u8 格式（繁体 简体 [拼音] /释义/，空格分隔），无需转换：

```sh
curl -o cedict.txt.gz \
  https://www.mdbg.net/chinese/export/cedict/cedict_1_0_ts_utf-8_mdbg.txt.gz
gunzip cedict.txt.gz
bin/chaoset-import -o dict-full.tsv data/dict_seed.tsv cedict.txt
bin/chaosetd -dict dict-full.tsv
```

CC-CEDICT 约 12 万词条，导入耗时亚秒；词条按固定词频 30 合成（用户
自有词库优先），非汉字词条自动过滤。**许可**：CC-CEDICT 为
CC-BY-SA 4.0——分发合并词库时须保留其署名与相同方式共享，
请一并查阅文件头部声明。

### 规模基准

`go test ./engine/ -bench . -benchtime 3x`（合成 10 万词库，Apple M1）：

- 词库加载 ~42ms/次（daemon 启动一次）
- 组词（全量候选排序+分页切片）~0.36ms/键
- 残缺音节前缀扫描 ~0.34ms/键

实测 120,282 条真实 CC-CEDICT 合并词库：daemon 秒级就绪，逐键链路
74ms（含进程启动），翻页在数千候选下正常（如 h → 738 页）。

## IPC 协议

帧格式：4 字节大端长度 + JSON 载荷（上限 1 MiB）。

- 请求：`{"id":1,"method":"input","params":{"text":"nihao"}}`
- 响应：`{"id":1,"result":{...}}` 或 `{"id":1,"error":"..."}`

方法：`ping` / `input` / `backspace` / `reset` / `select`（`{"index":0}`，
返回 `{"committed":"你好"}`）/ `nextPage` / `prevPage`（候选翻页，返回
current 组成状态，含 `page`/`page_count`）/ `punct`（`{"key":","}` 中文标点：组词中
返回 `{"committed":"你好，"}`，空组词返回 `{"committed":"，"}`）/
`candidateImage`（v2 统一候选窗：返回
`{"width","height","png":<base64>}`，daemon 以 `-font` 启用；无候选时
`png` 为空；未配置字体时报错，壳回退 v1 系统候选 UI）。候选与预编辑
结构见 `engine.Candidate` / `engine.Composition` 的 JSON 标签。

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

## 热词导入

```sh
# 把 Rime dict.yaml / TSV 热词文件与已有词库合并（自动去声调、ü→v、
# 词频压缩到 1..99；非法音节行跳过并汇报）
bin/chaoset-import -o dict-merged.tsv data/dict_seed.tsv 热词.yaml

# daemon 叠加加载（-dict 可多次）
bin/chaosetd -dict data/dict_seed.tsv -dict dict-merged.tsv
```

重复词条默认取较大词频，`-replace` 以导入词频覆盖。
封闭格式（搜狗 .scel 等）先用开源「深蓝词库转换」转成 Rime/TSV 再导入。

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
bin/chaoset-import -o dict-merged.tsv data/dict_seed.tsv learned.tsv
```

全量导出为多段 TSV（词 / 词组 / 上下文，含次数与最近使用时间，
按次数降序、UTC 时间，输出稳定可 diff）；空段省略。`clear` 不可撤销，
建议先 `export` 备份。

## 路线图与验收结论

- [x] 阶段 0：引擎 + 终端测试台 + IPC 骨架
- [x] 阶段 1：macOS IMK 壳（编译 + IPC 运行时验证通过；真机启用需
      注销重登后在系统设置添加，剩余步骤见 shims/macos/README）
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
        Windows 壳贴图待真机环境
- [x] 阶段 5：中文标点——引擎侧统一映射（Session.Punct + IPC punct），
      CLI 与三端壳转发接入
- [x] 阶段 6：候选翻页——引擎内分页（Composition.page/page_count，
      每页 9 个）+ IPC nextPage/prevPage，CLI 与三端壳（- / =）接入
- [x] 阶段 7：候选可视化收尾——渲染器预编辑行页码指示（样例目检
      通过）；ibus 壳 UpdateLookupTable 候选表上报（容器内端到端
      验证，数字键选择不再盲选）
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
- [x] 阶段 19：中国风视觉——应用图标改为**中国印章**语汇（中国红底、白色双框、
      宋体「超」、云纹暗纹，详见 shims/macos/README），候选窗默认皮肤同步取中国传统色：
      宣纸底 #F7F2E7、朱红外框 + 米金细内框（双框）、浓墨候选、淡墨预编辑、
      朱红首选高亮 + 左侧「朱批」竖记、石青学习标记；样例见 .workwork/render-sample.png
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

## 验收范围确认（2026-09-23）

经会话内穷尽全部可脚本化手段（mingw-w64 交叉工具链、Docker 容器 ibus
环境、TIS 注册、系统安装、哈希比对均已完成）后确认：以下三项联调收尾
**移出本轮验收范围**，其余任务全部完成并验收。

| # | 移出项 | 硬门控条件 | 恢复方式 |
|---|---|---|---|
| 1 | macOS 打字闭环验证 | 用户注销重登 + 系统设置添加 ChaosetIME（已实测 `AXIsProcessTrusted=false`，按键模拟被系统策略禁止，无法脚本化） | 按 shims/macos/README 打字验证 |
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

推送后由 GitHub Actions（`.github/workflows/ci.yml`）自动执行
fmt 检查、vet、`-race` 测试与构建。
