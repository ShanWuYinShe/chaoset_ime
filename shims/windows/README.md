# Windows 平台壳（TSF，规划中）

形态：**TSF COM DLL 薄壳（C++）+ 命名管道连 chaosetd**（同 macos 壳的架构）。
TSF 要求输入法以进程内 COM DLL 形式被系统加载进每个有文本输入的进程，
这一层无法用 Go/Rust 替代；引擎与候选逻辑不进该 DLL。

## 实施要点（健壮性清单，逐条落实）

1. **daemon 未就绪**：DLL 连不上管道时按键直接放行（不拦截、不等待），
   连接恢复后自动重建会话；daemon 由安装器注册开机自启。
2. **回调超时**：TSF 按键回调在宿主进程内同步执行，每次等 daemon
   必须带超时（建议 50–100ms），超时透传原键并复位组词状态，
   任何情况下不允许宿主应用卡顿。
3. **UAC 完整性级别**：v1 候选窗用系统 `ITfCandidateListUIElement`
   （由系统进程承载），天然规避 Medium IL 进程无法显示在
   High IL（管理员）窗口之上的问题；v2 自绘候选窗再申请 UIAccess
   （需正式签名 + 安全位置安装）。
4. **UWP/AppContainer 宿主**：命名管道 ACL 需允许 App Container
   访问（参考搜狗 ImeBroker 的代理形态）。
5. **多会话**（快切用户 / RDP）：每个登录会话一个 daemon 实例，
   管道名带 session id。
6. 协议复用 `ipc` 包的定义（4 字节长度 + JSON），传输层换成命名管道。

## 为什么暂未实施

本机无 Windows 工具链（mingw-w64 未安装），无法做编译级验证；
按项目红线「不编写无法在当前会话中验证的内容」，TSF DLL 留待具备
Windows 构建环境后开工。推荐起步参考：nathancorvussolis/tsf-sample-ime。
