import Carbon
import CoreFoundation

// TIS（Text Input Services）验证工具：注册/列出/选择输入源。
// 用途：真机验证 ChaosetIME.app 被系统识别并可切换，无需注销重登。
// 用法：
//   tis register <app 路径>   注册新安装的输入法 bundle
//   tis list                  列出全部键盘输入源（chaoset 相关加 [*] 标记）
//   tis enable <输入源ID>     把输入源加入"已启用"列表（AppleEnabledInputSources）
//   tis select <输入源ID>     切换到指定输入源

func prop(_ source: TISInputSource, _ key: CFString) -> String {
    guard let raw = TISGetInputSourceProperty(source, key) else { return "?" }
    return (Unmanaged<CFTypeRef>.fromOpaque(raw).takeUnretainedValue() as? String) ?? "?"
}

func allSources() -> [TISInputSource] {
    let list = TISCreateInputSourceList(nil, true).takeRetainedValue() as NSArray
    return list as! [TISInputSource]
}

let args = CommandLine.arguments
let cmd = args.count > 1 ? args[1] : "list"

switch cmd {
case "register":
    guard args.count >= 3 else {
        FileHandle.standardError.write(Data("用法: tis register <app 路径>\n".utf8))
        exit(2)
    }
    let url = URL(fileURLWithPath: args[2]) as CFURL
    let status = TISRegisterInputSource(url)
    print("register status: \(status) \(status == noErr ? "(成功)" : "(失败)")")
    exit(status == noErr ? 0 : 1)

case "enable":
    guard args.count >= 3 else {
        FileHandle.standardError.write(Data("用法: tis enable <输入源ID>\n".utf8))
        exit(2)
    }
    let want = args[2]
    for source in allSources() where prop(source, kTISPropertyInputSourceID) == want {
        let status = TISEnableInputSource(source)
        print("enable \(want): \(status) \(status == noErr ? "(成功)" : "(失败)")")
        exit(status == noErr ? 0 : 1)
    }
    FileHandle.standardError.write(Data("未找到输入源 \(want)（表里没有就无法启用）\n".utf8))
    exit(1)

case "select":
    guard args.count >= 3 else {
        FileHandle.standardError.write(Data("用法: tis select <输入源ID>\n".utf8))
        exit(2)
    }
    let want = args[2]
    for source in allSources() where prop(source, kTISPropertyInputSourceID) == want {
        let status = TISSelectInputSource(source)
        print("select \(want): \(status) \(status == noErr ? "(成功)" : "(失败)")")
        exit(status == noErr ? 0 : 1)
    }
    FileHandle.standardError.write(Data("未找到输入源 \(want)\n".utf8))
    exit(1)

case "list":
    for source in allSources() {
        let id = prop(source, kTISPropertyInputSourceID)
        let name = prop(source, kTISPropertyLocalizedName)
        if id.lowercased().contains("chaoset") {
            print("[*] \(id) — \(name)")
        } else {
            print("    \(id) — \(name)")
        }
    }

default:
    print("用法: tis register <app 路径> | list | select <输入源ID>")
}
