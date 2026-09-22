import Foundation

// ChaosetIME IPC 冒烟：用 EngineClient（与 app 相同的传输实现）驱动真实 daemon。
// 前置：daemon 已在指定套接字上运行。全部通过打印 PASS，任一失败非零退出。
// 用法：smoke <socket-path>

let args = CommandLine.arguments
guard args.count >= 2 else {
    FileHandle.standardError.write(Data("用法: smoke <socket-path>\n".utf8))
    exit(2)
}
let client = EngineClient(socketPath: args[1])
var failures = 0

func check(_ cond: Bool, _ msg: String) {
    if cond {
        print("ok: \(msg)")
    } else {
        failures += 1
        FileHandle.standardError.write(Data("FAIL: \(msg)\n".utf8))
    }
}

// 1. 连接与组词
let c1 = client.input("n")
check(c1 != nil, "连接 daemon（input 命令有响应）")

// 2. 追加输入 + 预编辑 + 候选
let c2 = client.input("ihao")
check(c2?.preedit == "ni'hao", "预编辑 ni'hao（实际 \(c2?.preedit ?? "nil")）")
check(c2?.candidates.first?.text == "你好", "第一候选 你好（实际 \(c2?.candidates.first?.text ?? "nil")）")

// 3. 选词上屏
let committed = client.select(0)
check(committed == "你好", "上屏 你好（实际 \(committed ?? "nil")）")

// 4. 歧义切分
let c3 = client.input("xian")
check(c3?.candidates.contains(where: { $0.text == "西安" }) == true, "xian 歧义切分候选含 西安")

// 5. 退格
let c4 = client.backspace()
check(c4?.input == "xia", "退格后输入 xia（实际 \(c4?.input ?? "nil")）")

// 6. 重置
let c5 = client.reset()
check(c5?.input.isEmpty == true, "重置后输入为空")

if failures == 0 {
    print("PASS: Swift IPC 全链路 6 项检查通过")
    exit(0)
}
FileHandle.standardError.write(Data("FAIL: \(failures) 项未通过\n".utf8))
exit(1)
