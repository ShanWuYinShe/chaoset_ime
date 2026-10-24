// 输入源图标「实际渲染尺寸」自检工具（macOS）。
//
// 背景（2026-10-24/25 实测，用户多轮纠正后沉淀）：
// System Settings / 菜单栏 / 快捷键切换 HUD 显示输入源图标时，**不是**直接按像素尺寸绘制：
//   - 系统输入源（ABC、简体拼音、双拼…）由私有的 IPInputSource -(NSImage *)iconImage
//     统一渲染，本机 macOS 15.8.1 实测为 22×16 pt（44×32 @2x 快照）；
//   - 第三方输入源按 TIS 图标文件的**原始尺寸**显示：System Settings 读 bundle 级
//     图标键，菜单栏与快捷键切换 HUD 共用 mode 级图标键（没有第三套键可再区分）；
//     因此五个图标键统一指向同一份 22×16 模板图。
// 图标文件必须提供与系统参考源完全相同的点尺寸（22×16），
// 否则会在系统设置/菜单栏/HUD 里显示成正方形或比例不对。
//
// 本工具读取 IPInputSource +inputSources（PreferencePanesSupport 私有类，与 System Settings
// 同源），对比本输入法与系统参考源的 iconImage.size。
//
// 用法：
//   build/icon-size [参考系统输入源ID]      # 默认 com.apple.inputmethod.SCIM.ITABC（简体拼音）
// 退出码：0 = 一致 / 跳过（未安装等）；2 = 尺寸不一致（会显示成正方形/比例不对）。
// 注：未安装本输入法时打印 skip: 并返回 0，方便 verify/equivalence 在未安装环境跳过。

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <math.h>
#import <objc/runtime.h>
#import <objc/message.h>
#import <dlfcn.h>

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        // System Settings 的输入源模型在 PreferencePanesSupport 私有框架里
        dlopen("/System/Library/PrivateFrameworks/PreferencePanesSupport.framework/Versions/A/PreferencePanesSupport", RTLD_LAZY);
        Class cls = NSClassFromString(@"IPInputSource");
        if (!cls) {
            printf("skip: 当前系统没有 IPInputSource（跳过渲染尺寸比对）\n");
            return 0;
        }
        NSArray *list = ((id (*)(id, SEL))objc_msgSend)((id)cls, NSSelectorFromString(@"inputSources"));
        if (!list) {
            printf("skip: 无法枚举输入源（跳过渲染尺寸比对）\n");
            return 0;
        }
        NSString *refID = argc > 1 ? [NSString stringWithUTF8String:argv[1]]
                                   : @"com.apple.inputmethod.SCIM.ITABC";
        NSString *oursID = nil;
        NSImage *ours = nil, *ref = nil;
        for (id s in list) {
            NSString *sid = [NSString stringWithFormat:@"%@", [s valueForKey:@"inputSourceID"]];
            if ([sid hasPrefix:@"ime.chaoset.inputmethod.ChaosetIME"]) {
                oursID = sid;
                ours = [s valueForKey:@"iconImage"];
            }
            if ([sid isEqualToString:refID]) {
                ref = [s valueForKey:@"iconImage"];
            }
        }
        if (!ours) {
            printf("skip: 未找到已安装/启用的 Chaoset 输入源（跳过渲染尺寸比对）\n");
            return 0;
        }
        if (!ref) {
            printf("skip: 未找到系统参考输入源 %s（跳过渲染尺寸比对）\n", refID.UTF8String);
            return 0;
        }
        NSSize a = ours.size, b = ref.size;
        printf("系统参考 %s: %.0f×%.0f\n", refID.UTF8String, b.width, b.height);
        printf("Chaoset      %s: %.0f×%.0f\n", oursID.UTF8String, a.width, a.height);
        if (fabs(a.width - b.width) < 0.01 && fabs(a.height - b.height) < 0.01) {
            printf("PASS: 图标实际渲染尺寸与系统原生输入源一致\n");
            return 0;
        }
        printf("FAIL: 图标实际渲染尺寸与系统原生输入源不一致（会显示成正方形/比例不对）\n");
        return 2;
    }
}
