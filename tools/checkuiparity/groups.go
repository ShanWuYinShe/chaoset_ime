// groups.go：检查组名称的唯一表。
//
// 组名只在这里定义：检查函数内部用 "const group = groupXxx" 引用，run.go 的注册表
// 也用同一个常量。这样「实际运行的组」与「成功输出打印的组」不会出现两份清单漂移
// （旧实现把组清单硬编码在成功信息里，漏掉了 11 个实际运行的组）。
package main

const (
	// 设置窗、学习数据与设置核心（方案 A）。
	groupSettingsSharedText = "设置窗共享文案"
	groupSettingsSingleton  = "设置窗单例"
	groupLearningGUI        = "学习数据 GUI"
	groupEnginePurity       = "engine 纯净约束"
	groupImportEntries      = "词库导入入口"
	groupImportSafety       = "词库导入安全"
	groupAssociateImmediacy = "关闭联想即时性"
	groupBackspaceSemantics = "退格键语义"
	groupModifierMapping    = "修饰键映射"
	groupShiftAccounting    = "Shift 记账"
	groupResetSemantics     = "reset 语义"
	groupSettingsContract   = "设置窗契约"
	groupSettingsCore       = "设置核心（方案 A）"
	groupLinuxSetupAssembly = "Linux 设置窗装配"

	// Windows 壳专属契约。
	groupUILess             = "UILess 能力"
	groupForegroundFallback = "焦点兜底"
	groupActiveGate         = "输入法激活判据"
	groupCrashLog           = "崩溃日志"
	groupModeIndicator      = "模式指示器/UI 宿主"
	groupPopupOwner         = "浮窗 owner"
	groupInactiveNoAction   = "非激活零动作"
	groupInactiveZeroSide   = "非激活零副作用"
	groupDaemonSpawnGuard   = "daemon 懒启动去抖"
	groupDllUnload          = "DLL 卸载安全"
	groupTrayModeScope      = "托盘与中英作用域"
	groupTrayLifecycle      = "托盘生命周期"
	groupTrayActive         = "托盘可见性（active）"
	groupTsfZeroBlock       = "TSF 回调零跨进程等待"
	groupWindowsInstall     = "Windows 安装安全"
	groupWindowsVersionInfo = "Windows 版本信息"

	// 构建/打包/CI/文档。
	groupPackaging       = "打包安装"
	groupVersionSource   = "版本号来源"
	groupCICoverage      = "CI 覆盖"
	groupDocParity       = "文档对照"
	groupModuleLayout    = "模块布局与文件预算"
	groupArchitectureDoc = "架构文档"

	// 候选窗与候选点选。
	groupCandidateGeometry    = "候选窗几何"
	groupCandidateGear        = "候选窗齿轮"
	groupCandidateDivider     = "候选窗分割线"
	groupCandidateTheme       = "候选窗配色"
	groupCandidatePageSize    = "候选每页数量"
	groupCandidateMouseUp     = "候选点选（mouseup）"
	groupCandidateClick       = "候选点选"
	groupCandidateDelete      = "候选右键删除"
	groupCandidateGearMouseUp = "候选窗齿轮 mouseUp"

	// 跨端行为与 daemon 生命周期。
	groupDaemonSingleton = "daemon 单实例"
	groupShellEscaping   = "shell 变量转义"
)
