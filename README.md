# chaoset-ime

跨平台中文输入法（macOS / Linux / Windows）。

架构：**Go 引擎 + 守护进程（daemon）+ 各平台薄壳**。引擎与组词逻辑只写一份，
平台壳只负责「按键转发 + 文本上屏 + 光标定位」，通过 IPC 与 daemon 通信。
该形态已被业界验证（小狼毫的 TSF DLL + WeaselServer、搜狗的多进程形态、
macOS InputMethodKit 本身就是进程间模型）。

```
┌──────────────────── chaosetd (Go，一份代码) ────────────────────┐
│  engine：拼音切分 / 词典检索 / 候选排序 / 用户词典自动调频          │
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
  dict.go            音节 Trie 词典 + TSV 词库解析
  session.go         组词会话状态机（输入/退格/重置/选候选）
  userdict.go        用户词典：上屏计数 + 自动调频（原子落盘）
ipc/               IPC 协议与服务端/客户端（client 实现 engine.Controller）
cmd/
  chaosetd/          守护进程二进制
  chaoset-cli/       阶段 0 终端测试台（本地内嵌引擎或连接 daemon）
data/
  dict_seed.tsv      种子词库（演示规模，词 \t 拼音 \t 词频）
shims/             各平台薄壳（阶段 1+，暂为规划文档）
```

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
带 `*` 的候选表示来自用户词典调频——选过的词下次会排到前面。

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
返回 `{"committed":"你好"}`）。候选与预编辑结构见 `engine.Candidate` /
`engine.Composition` 的 JSON 标签。

Windows 命名管道传输在阶段 3 接入（同一协议，换传输层）。

## 平台壳规划

| 平台 | 壳 | 状态 |
|---|---|---|
| Linux | ibus engine（daemon 直接注册为 ibus 引擎进程） | 规划中 |
| macOS | InputMethodKit `.app`（Swift），Unix socket 连 daemon | 规划中 |
| Windows | TSF COM DLL（C++ 薄壳），命名管道连 daemon；v1 用系统候选窗 UI | 规划中 |

Windows 薄壳已知健壮性清单（实施时逐条落实）：daemon 未就绪时按键直接放行；
回调带超时防卡宿主；候选窗 v1 走系统 UI Element 规避 UAC 完整性级别问题；
命名管道 ACL 兼容 UWP/AppContainer 宿主；多登录会话每会话一个 daemon。

## 路线图

- [x] 阶段 0：引擎 + 终端测试台 + IPC 骨架
- [ ] 阶段 1：第一个平台壳跑通全链路（ibus 或 macOS IMK）
- [ ] 阶段 2：用户词典 + 自动调频增强（时间衰减、bigram）
- [ ] 阶段 3：热词导入管道（Rime dict.yaml 解析、深蓝词库转换衔接）
- [ ] 阶段 4：补齐三端壳 + 统一候选窗自绘（v2）

## 开发

```sh
make fmt && make vet && make test && make build
```
