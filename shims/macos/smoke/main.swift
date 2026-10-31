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
let c1 = client.input("n", halfWidth: false)
check(c1 != nil, "连接 daemon（input 命令有响应）")

// 2. 追加输入 + 预编辑 + 候选
let c2 = client.input("ihao", halfWidth: false)
check(c2?.preedit == "ni'hao", "预编辑 ni'hao（实际 \(c2?.preedit ?? "nil")）")
check(c2?.candidates.first?.text == "你好", "第一候选 你好（实际 \(c2?.candidates.first?.text ?? "nil")）")

// 3. 选词上屏
let committed = client.select(0)
check(committed == "你好", "上屏 你好（实际 \(committed ?? "nil")）")

// 4. 撇号强制音节边界：xi'an 走 [xi an] 切分，应直接出「西安」。
// （不用裸 xian 断言：真实词库里 xian 的单字候选太多，西安会排到十几页之后，
//  那是正常排序，不能作为链路是否正常的判据。）
_ = client.reset()
let c3 = client.input("xi'an", halfWidth: false)
check(c3?.preedit == "xi'an", "预编辑 xi'an（实际 \(c3?.preedit ?? "nil")）")
check(c3?.candidates.first?.text == "西安", "xi'an 第一候选 西安（实际 \(c3?.candidates.first?.text ?? "nil")）")
// 退格留在本次组词内（Reset 会清空输入，退格才对组词生效），故此处不 Reset

// 5. 学习数据只读接口（设置窗口「学习数据」区依赖；**不调用 userClear**，
//    避免安装后自检误清真实用户的学习数据）
let stats = client.userStats()
check(stats != nil, "userStats 可读（设置窗口学习数据统计）")
let exported = client.userExportAll()
check(exported != nil, "userExport 可读（设置窗口导出学习数据）")

// 6. 退格
let c4 = client.backspace()
check(c4?.input == "xi'a", "退格后输入 xi'a（实际 \(c4?.input ?? "nil")）")

// 6. 重置
let c5 = client.reset()
check(c5?.input.isEmpty == true, "重置后输入为空")

if failures == 0 {
    print("PASS: Swift IPC 全链路 8 项检查通过")
    exit(0)
}
FileHandle.standardError.write(Data("FAIL: \(failures) 项未通过\n".utf8))
exit(1)