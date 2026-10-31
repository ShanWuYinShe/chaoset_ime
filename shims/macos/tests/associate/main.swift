import Foundation

// macOS 联想回归：通过 EngineClient 走真实 IPC（临时 daemon），验证
// 「输入词组 → 选词上屏 → associate 返回续接候选」这条用户实际路径。
// 引擎单测覆盖算法、ipc/associate_test 覆盖 Go 协议；这里补 shell 客户端
// 到 daemon 的端到端，防止壳侧参数/字段回归时只在真机才发现。
guard let socket = ProcessInfo.processInfo.environment["CHAOSET_SOCKET"],
      !socket.isEmpty else {
    fputs("FAIL: 缺少 CHAOSET_SOCKET\n", stderr)
    exit(1)
}

let client = EngineClient(socketPath: socket)
guard let comp = client.input("zhongguo", halfWidth: false), !comp.candidates.isEmpty else {
    fputs("FAIL: 输入 zhongguo 未返回候选\n", stderr)
    exit(1)
}
guard let committed = client.select(0), !committed.isEmpty else {
    fputs("FAIL: 选择首选候选未返回上屏文本\n", stderr)
    exit(1)
}
guard let assoc = client.associate(sylls: ["zhong", "guo"]),
      !assoc.candidates.isEmpty else {
    fputs("FAIL: 上屏「\(committed)」后 associate 无续接候选\n", stderr)
    exit(1)
}
print("ok: 上屏「\(committed)」后联想 \(assoc.candidates.count) 条，首选「\(assoc.candidates[0].text)」")