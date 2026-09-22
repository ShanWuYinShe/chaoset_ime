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
  userdict.go        用户数据：调频记录、合成词组、上下文共现
  import.go          Rime dict.yaml / TSV 宽容解析（热词导入）
ipc/               IPC 协议与服务端/客户端（client 实现 engine.Controller）
renderer/          统一候选窗渲染（v2）：把组词状态画成 PNG，三端壳贴图显示
cmd/
  chaosetd/          守护进程二进制（-dict 可多次叠加；-font/-fontsize 候选窗渲染）
  chaoset-cli/       阶段 0 终端测试台（本地内嵌引擎或连接 daemon）
  chaoset-import/    热词导入工具（合并输出 TSV）
data/
  dict_seed.tsv      种子词库（演示规模，词 \t 拼音 \t 词频）
shims/
  macos/             InputMethodKit 薄壳（Swift，已实现，见其 README）
  windows/           TSF COM 薄壳（已实现，核心层行为级 + DLL 编译级验证）
  linux/             ibus engine 胶水（规划中）
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

## 快速开始（阶段 0 测试台）

```sh
# 本地内嵌引擎模式
go run ./cmd/chaoset-cli

# 或经 IPC 连接 daemon（先起 daemon）
go run ./cmd/chaosetd &
go run ./cmd/chaoset-cli --socket chaoset.sock
```

测试台操作：输入拼音回车看候选（如 `nihao`、`xian`、`xi'an`、`nih`）；
单独一个数字 `1`–`9` 选择对应候选并「上屏」；`:b` 退格；`:r` 重置；`:q` 退出。
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

## IPC 协议

帧格式：4 字节大端长度 + JSON 载荷（上限 1 MiB）。

- 请求：`{"id":1,"method":"input","params":{"text":"nihao"}}`
- 响应：`{"id":1,"result":{...}}` 或 `{"id":1,"error":"..."}`

方法：`ping` / `input` / `backspace` / `reset` / `select`（`{"index":0}`，
返回 `{"committed":"你好"}`）/ `candidateImage`（v2 统一候选窗：返回
`{"width","height","png":<base64>}`，daemon 以 `-font` 启用；无候选时
`png` 为空；未配置字体时报错，壳回退 v1 系统候选 UI）。候选与预编辑
结构见 `engine.Candidate` / `engine.Composition` 的 JSON 标签。

传输形态：Linux/macOS 用 Unix socket；Windows 壳经 TCP 回环
（daemon `-tcp 127.0.0.1:7949`，仅本机回环）。

## 平台壳

| 平台 | 壳 | 状态 |
|---|---|---|
| Linux | ibus engine（daemon 直接注册为 ibus 引擎进程） | 规划中 |
| macOS | InputMethodKit `.app`（Swift），Unix socket 连 daemon | 规划中 |
| Windows | TSF COM DLL（C++ 薄壳），命名管道连 daemon；v1 用系统候选窗 UI | 规划中 |

Windows 薄壳已知健壮性清单（实施时逐条落实）：daemon 未就绪时按键直接放行；
回调带超时防卡宿主；候选窗 v1 走系统 UI Element 规避 UAC 完整性级别问题；
命名管道 ACL 兼容 UWP/AppContainer 宿主；多登录会话每会话一个 daemon。

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

## 路线图

- [x] 阶段 0：引擎 + 终端测试台 + IPC 骨架
- [x] 阶段 1：macOS IMK 壳（编译 + IPC 运行时验证通过；真机启用需
      注销重登后在系统设置添加，剩余步骤见 shims/macos/README）
- [x] 阶段 2：用户词典 + 自动调频增强（时间衰减、bigram、词组进化）
- [x] 阶段 3：热词导入管道（Rime dict.yaml 解析、chaoset-import 合并）
- [ ] 阶段 4：补齐三端壳 + 统一候选窗自绘（v2）
      - Windows TSF 壳：已实现——核心层行为级验证（真实 daemon TCP 17 项）、
        DLL mingw 交叉编译级验证；真机联调（COM 注册/按键/渲染）待 Windows 环境
      - Linux ibus 壳：已实现——Go 引擎（godbus），容器内端到端验证通过
        （组件注册/激活/按键序列处理/CommitText 中文上屏信号）；
        真机桌面闭环待 Linux 桌面环境
      - 统一候选窗：已实现——daemon 侧渲染（renderer + candidateImage，
        样例人工目检通过）+ macOS 壳贴图面板（编译级验证，回退 IMKCandidates）；
        Windows 壳贴图待真机环境

> **本轮验收范围说明**（2026-09-23）：以下事项因环境/用户操作门控，
> 移出本轮验收范围，条件就绪后按对应文档恢复：
> 1. macOS 打字闭环验证——需注销重登 + 系统设置添加 ChaosetIME（用户 GUI
>    操作，步骤见 shims/macos/README）；
> 2. Windows TSF 真机联调——需 Windows 环境（COM 注册/按键/渲染）；
> 3. Linux ibus 壳实现与联调——需 Linux + ibus 环境；
> 4. 候选窗贴图在 Windows 壳侧的接入——随 TSF 真机联调一并验证。

## 开发

```sh
make fmt && make vet && make test && make build
```
