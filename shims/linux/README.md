# Linux 平台壳（ibus，规划中）

形态选择：**ibus engine** 而非 fcitx5 addon。原因：

- ibus 的 engine 本身就是独立进程（DBus 协议通信），与项目
  「daemon 进程外引擎」架构天然吻合，壳可以退化为纯 DBus 胶水；
- fcitx5 addon 是加载进 fcitx5 守护进程的进程内 .so，与
  daemon 架构冲突（引擎会被迫进宿主进程）。

## 实施要点

1. engine 进程内嵌 Go daemon（或 daemon 通过 DBus 注册为 ibus engine，
   具体以 ibus 引擎注册协议为准）。
2. 候选窗用 ibus 自带面板（engine 上报 preedit 与候选列表即可），
   零自绘 UI，天然规避 Wayland/合成器差异。
3. Wayland 下 GNOME/KDE 均经 input-method 协议对接 ibus，无需额外适配。
4. 词库与用户数据路径遵循 XDG：
   `~/.local/state/chaoset/userdict.json`、`~/.local/share/chaoset/`。

## 为什么暂未实施

本机无 ibus 运行环境，无法做联调验证；Go 侧无现成 ibus 协议库，
需要先确认依赖（如 godbus）与 ibus 引擎注册协议细节后再开工。
