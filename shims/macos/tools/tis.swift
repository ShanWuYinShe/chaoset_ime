import Carbon
import CoreFoundation
import Foundation

// TIS（Text Input Services）验证工具：注册/列出/选择输入源。
// 用途：真机验证 ChaosetIME.app 被系统识别并可切换，无需注销重登。
// 用法：
//   tis register <app 路径>   注册新安装的输入法 bundle
//   tis list                  列出全部键盘输入源（chaoset 相关加 [*] 标记）
//   tis enable <输入源ID>     把输入源加入"已启用"列表（AppleEnabledInputSources）
//   tis select <输入源ID>     切换到指定输入源
//   tis sync                  同步已安装输入法到 com.apple.HIToolbox 首选项
//   tis dedupe                消除 TIS 表中同一输入源的重复实例（菜单栏/系统设置出现重复 Chaoset 时用）

func prop(_ source: TISInputSource, _ key: CFString) -> String {
    guard let raw = TISGetInputSourceProperty(source, key) else { return "?" }
    return (Unmanaged<CFTypeRef>.fromOpaque(raw).takeUnretainedValue() as? String) ?? "?"
}

func allSources() -> [TISInputSource] {
    let list = TISCreateInputSourceList(nil, true).takeRetainedValue() as NSArray
    return list as! [TISInputSource]
}

/// 同步输入源到 com.apple.HIToolbox.plist 中的 AppleEnabledInputSources。
/// 关键作用：现代 macOS（macOS 14/15）的沙盒宿主应用（如微信、Safari）在接收
/// 快捷键切换 HUD（CursorUIViewService / TextInputMenu）切至本输入法时，会在宿主进程
/// 内部通过 TISCopyInputSourceRefForInputSourceID 重新查询当前输入源。
/// 若 HIToolbox 首选项中未登记该输入源，宿主进程内的 TIS 会判定其为未启用（Disabled），
/// 查询返回 NULL，随后的 CFRelease(NULL) 会直接导致宿主应用崩溃（EXC_BREAKPOINT）。
func syncHIToolboxPreferences(enableSourceID: String) {
    let appID = "com.apple.HIToolbox" as CFString
    let key = "AppleEnabledInputSources" as CFString

    var current = (CFPreferencesCopyAppValue(key, appID) as? [[String: Any]]) ?? []

    // 清理旧版本可能残留的历史模式条目（如 Hans）
    current.removeAll {
        ($0["Bundle ID"] as? String) == "ime.chaoset.inputmethod.ChaosetIME" &&
        ($0["Input Mode"] as? String) != nil &&
        ($0["Input Mode"] as? String) != enableSourceID
    }

    for source in allSources() where prop(source, kTISPropertyInputSourceID) == enableSourceID {
        let bId = prop(source, kTISPropertyBundleID)
        let type = prop(source, kTISPropertyInputSourceType)

        if type == "TISTypeKeyboardInputMode" {
            let modeEntry: [String: Any] = [
                "Bundle ID": bId,
                "Input Mode": enableSourceID,
                "InputSourceKind": "Input Mode"
            ]
            let imEntry: [String: Any] = [
                "Bundle ID": bId,
                "InputSourceKind": "Keyboard Input Method"
            ]

            if !current.contains(where: { ($0["Input Mode"] as? String) == enableSourceID }) {
                current.append(modeEntry)
            }
            if !current.contains(where: { ($0["Bundle ID"] as? String) == bId && ($0["InputSourceKind"] as? String) == "Keyboard Input Method" }) {
                current.append(imEntry)
            }
        } else if type == "TISTypeKeyboardInputMethodModeEnabled" || type == "TISTypeKeyboardInputMethodWithoutMode" {
            let imEntry: [String: Any] = [
                "Bundle ID": bId,
                "InputSourceKind": "Keyboard Input Method"
            ]
            if !current.contains(where: { ($0["Bundle ID"] as? String) == bId && ($0["InputSourceKind"] as? String) == "Keyboard Input Method" }) {
                current.append(imEntry)
            }
        }
    }

    CFPreferencesSetAppValue(key, current as CFArray, appID)
    _ = CFPreferencesAppSynchronize(appID)
}

/// 同步当前选中输入源到 com.apple.HIToolbox.plist 中的 AppleSelectedInputSources。
func syncHIToolboxSelection(selectSourceID: String) {
    let appID = "com.apple.HIToolbox" as CFString
    let keySelected = "AppleSelectedInputSources" as CFString

    for source in allSources() where prop(source, kTISPropertyInputSourceID) == selectSourceID {
        let bId = prop(source, kTISPropertyBundleID)
        let type = prop(source, kTISPropertyInputSourceType)

        var selectedList: [[String: Any]] = []
        if let currentSel = CFPreferencesCopyAppValue(keySelected, appID) as? [[String: Any]] {
            // 保留系统辅助类条目（PressAndHold, EmojiFunctionRowItem 等）
            selectedList = currentSel.filter {
                ($0["InputSourceKind"] as? String) == "Non Keyboard Input Method"
            }
        }

        if type == "TISTypeKeyboardInputMode" {
            let modeEntry: [String: Any] = [
                "Bundle ID": bId,
                "Input Mode": selectSourceID,
                "InputSourceKind": "Input Mode"
            ]
            selectedList.append(modeEntry)
        } else {
            let imEntry: [String: Any] = [
                "Bundle ID": bId,
                "InputSourceKind": "Keyboard Input Method"
            ]
            selectedList.append(imEntry)
        }

        CFPreferencesSetAppValue(keySelected, selectedList as CFArray, appID)
        _ = CFPreferencesAppSynchronize(appID)
    }
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
    var found = false
    for source in allSources() where prop(source, kTISPropertyInputSourceID) == want {
        let status = TISEnableInputSource(source)
        syncHIToolboxPreferences(enableSourceID: want)
        print("enable \(want): \(status) \(status == noErr ? "(成功)" : "(失败)")")
        found = true
        break
    }
    if !found {
        FileHandle.standardError.write(Data("未找到输入源 \(want)（表里没有就无法启用）\n".utf8))
        exit(1)
    }
    exit(0)

case "select":
    guard args.count >= 3 else {
        FileHandle.standardError.write(Data("用法: tis select <输入源ID>\n".utf8))
        exit(2)
    }
    let want = args[2]
    for source in allSources() where prop(source, kTISPropertyInputSourceID) == want {
        let status = TISSelectInputSource(source)
        syncHIToolboxSelection(selectSourceID: want)
        print("select \(want): \(status) \(status == noErr ? "(成功)" : "(失败)")")
        exit(status == noErr ? 0 : 1)
    }
    FileHandle.standardError.write(Data("未找到输入源 \(want)\n".utf8))
    exit(1)

case "disable":
    guard args.count >= 3 else {
        FileHandle.standardError.write(Data("用法: tis disable <输入源ID>\n".utf8))
        exit(2)
    }
    let want = args[2]
    var found = false
    for source in allSources() where prop(source, kTISPropertyInputSourceID) == want {
        let status = TISDisableInputSource(source)
        print("disable \(want): \(status) \(status == noErr ? "(成功)" : "(失败)")")
        found = true
        break
    }
    if !found {
        print("未找到输入源 \(want)（跳过禁用）")
    }
    exit(0)

case "dedupe":
    // 消除 TIS 表中同一输入源的重复实例。
    //
    // 背景（2026-10-26 实测）：TIS 表里同一个 InputSourceID 可能出现两条
    // **属性完全相同、只有对象指针不同**的记录（缓存文件里 .Pinyin 出现两次），
    // 表现为菜单栏/系统设置里出现多个同样的 Chaoset。
    // 诱因是安装/刷新期间多次 TISRegisterInputSource 与 TIS 缓存重建交错，
    // 系统把同一条 mode 登记了两次。
    //
    // 实测有效的修法：把该 ID 的**全部实例**逐个 TISDisableInputSource，
    // 让 TIS 合并这些记录，然后只对第一个实例 TISEnableInputSource。
    // 直接 enable 无效果（重复条目保持），必须先全禁再启一个。
    let targets = ["ime.chaoset.inputmethod.ChaosetIME",
                   "ime.chaoset.inputmethod.ChaosetIME.Pinyin"]
    var fixedAny = false
    for target in targets {
        let insts = (TISCreateInputSourceList(nil, true).takeRetainedValue() as! [TISInputSource])
            .filter { prop($0, kTISPropertyInputSourceID) == target }
        if insts.count <= 1 { continue }
        print("发现 \(target) 有 \(insts.count) 个重复实例，正在合并…")
        for s in insts { _ = TISDisableInputSource(s) }
        Thread.sleep(forTimeInterval: 1.0)
        let after = (TISCreateInputSourceList(nil, true).takeRetainedValue() as! [TISInputSource])
            .filter { prop($0, kTISPropertyInputSourceID) == target }
        if let first = after.first {
            _ = TISEnableInputSource(first)
        }
        print("  \(target) 合并后实例数: \(after.count)")
        fixedAny = true
    }
    if fixedAny {
        syncHIToolboxPreferences(enableSourceID: "ime.chaoset.inputmethod.ChaosetIME.Pinyin")
        print("已消除重复输入源实例")
    } else {
        print("未发现重复实例，无需处理")
    }
    exit(0)

case "sync":
    // 只清理历史 .Hans 旧模式；**父容器必须保持启用**。
    //
    // 2026-10-26 实测踩坑：此前这里把父容器 ime.chaoset.inputmethod.ChaosetIME
    // 也一并 TISDisableInputSource，结果系统随即清空
    // com.apple.inputsources 的 AppleEnabledThirdPartyInputSources，
    // 表现为「装完输入法从菜单栏整个消失」——父容器是 TIS 的骨架节点
    // （系统「简体拼音」同样是 mode + container 两条且都启用），
    // 只允许禁用 .Hans 这类历史模式。
    for source in allSources() {
        let id = prop(source, kTISPropertyInputSourceID)
        if id == "ime.chaoset.inputmethod.ChaosetIME.Hans" {
            _ = TISDisableInputSource(source)
        }
    }
    // 父容器与 Pinyin 模式都启用，与系统输入法的结构保持一致。
    for source in allSources() {
        let id = prop(source, kTISPropertyInputSourceID)
        if id == "ime.chaoset.inputmethod.ChaosetIME"
            || id == "ime.chaoset.inputmethod.ChaosetIME.Pinyin" {
            _ = TISEnableInputSource(source)
        }
    }
    syncHIToolboxPreferences(enableSourceID: "ime.chaoset.inputmethod.ChaosetIME.Pinyin")
    print("已同步 ChaosetIME 到 com.apple.HIToolbox 首选项（父容器 + Pinyin 模式各 1 条）")
    exit(0)

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
    print("用法: tis register <app 路径> | list | select <输入源ID> | enable <输入源ID> | sync | dedupe")
}
