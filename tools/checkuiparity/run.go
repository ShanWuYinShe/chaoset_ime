// run.go：检查组注册表与 Run 调度。
//
// 每个检查组 = 「一个组名常量（groups.go）+ 一个检查函数」。Run 只遍历注册表执行，
// 成功提示由注册表生成，因此「实际运行的组」与「打印的组」永远是同一份数据。
package main

import (
	"io/fs"
)

// checker 是 Run 的收集器：聚合逻辑文件视图 + 原始文件树 + 问题列表。
//
// fsys 是 bundledFS 包装后的视图（winShell/macPanel 等逻辑文件由同目录多文件拼接）；
// physical 是未包装的真实文件树，模块布局行数/架构文档需要真实文件而不是聚合结果。
type checker struct {
	fsys     fs.FS
	physical fs.FS
	problems []string
}

// checkGroup 是注册表条目：组名 + 执行体。
type checkGroup struct {
	name string
	run  func(*checker)
}

// onLogical 包装「读聚合逻辑文件」的常规检查函数（绝大多数检查）。
func onLogical(f func(fs.FS, *[]string)) func(*checker) {
	return func(c *checker) { f(c.fsys, &c.problems) }
}

// onPhysical 包装「必须读真实文件树」的检查函数（模块布局/架构文档）。
func onPhysical(f func(fs.FS, *[]string)) func(*checker) {
	return func(c *checker) { f(c.physical, &c.problems) }
}

// checkGroups 是全部检查组的唯一注册表；顺序即执行顺序与成功输出顺序。
//
// 新增检查组：groups.go 加组名常量 → 这里加一行注册 → 写检查函数。
// 不要在 main() 里维护第二份组清单：那正是本注册表要消除的漂移源。
var checkGroups = []checkGroup{
	// 设置窗、学习数据与设置核心（方案 A）。
	{groupSettingsSharedText, onLogical(checkSettingsSharedText)},
	{groupSettingsSingleton, onLogical(checkSettingsSingleton)},
	{groupLearningGUI, onLogical(checkLearningDataGUI)},
	{groupEnginePurity, onLogical(checkEnginePurity)},
	// 词库导入与三端键位语义。
	{groupImportEntries, onLogical(checkImportEntries)},
	{groupImportSafety, onLogical(checkImportSafety)},
	{groupAssociateImmediacy, onLogical(checkAssociateImmediacy)},
	{groupBackspaceSemantics, onLogical(checkBackspaceSemantics)},
	{groupModifierMapping, onLogical(checkModifierMapping)},
	{groupShiftAccounting, onLogical(checkShiftToggleAccounting)},
	{groupResetSemantics, onLogical(checkResetSemantics)},
	// Windows TSF 壳零阻塞/生命周期契约。
	{groupPopupOwner, onLogical(checkPopupOwner)},
	{groupUILess, onLogical(checkUILessCapability)},
	{groupForegroundFallback, onLogical(checkForegroundFallback)},
	{groupModeIndicator, onLogical(checkModeIndicator)},
	{groupCrashLog, onLogical(checkCrashLogFilter)},
	{groupActiveGate, onLogical(checkActiveProfileGate)},
	// 打包/安装与版本信息。
	{groupPackaging, onLogical(checkPackaging)},
	{groupWindowsInstall, onLogical(checkWindowsInstallSafety)},
	{groupWindowsVersionInfo, onLogical(checkWindowsVersionInfo)},
	{groupVersionSource, onLogical(checkVersionSource)},
	// 非激活零副作用 / daemon 生命周期 / 托盘。
	{groupInactiveNoAction, onLogical(checkInactiveNoAction)},
	{groupInactiveZeroSide, onLogical(checkInactiveZeroSideEffects)},
	{groupDaemonSpawnGuard, onLogical(checkDaemonSpawnGuard)},
	{groupDllUnload, onLogical(checkDllUnloadSafety)},
	{groupTrayModeScope, onLogical(checkTrayAndModeScope)},
	{groupSettingsContract, onLogical(checkSettingsWindowContract)},
	{groupCandidateGearMouseUp, onLogical(checkCandidateGearMouseUp)},
	{groupTrayLifecycle, onLogical(checkTrayLazyLifecycle)},
	{groupTrayActive, onLogical(checkTrayActiveLifecycle)},
	{groupTsfZeroBlock, onLogical(checkTsfZeroBlockContract)},
	// daemon 单实例（安装器/LaunchAgent/TIP 三方）。
	{groupDaemonSingleton, onLogical(checkDaemonSingleton)},
	// 打包安装的产物存在段：同组第二段注册，保持既有失败输出顺序。
	{groupPackaging, onLogical(checkPackagingArtifacts)},
	// 候选窗静态契约与鼠标语义。
	{groupCandidateGeometry, onLogical(checkCandidateGeometry)},
	{groupCandidateGear, onLogical(checkCandidateGearGeometry)},
	{groupCandidateDivider, onLogical(checkCandidateDivider)},
	{groupCandidateTheme, onLogical(checkThemeFieldParity)},
	{groupCandidatePageSize, onLogical(checkCandidatePageSize)},
	{groupCandidateMouseUp, onLogical(checkCandidateMouseUpSemantics)},
	// CI 覆盖（原执行位置在候选窗之后），随后是候选点选与右键删除。
	{groupCICoverage, onLogical(checkCICoverage)},
	{groupCandidateClick, onLogical(checkCandidateClick)},
	{groupCandidateDelete, onLogical(checkCandidateDelete)},
	// issue#2：翻页箭头可用性与方向键页内预选（三端按键语义 + 双端绘制）。
	{groupCandidatePageArrows, onLogical(checkCandidatePageArrows)},
	{groupCandidateDirectionKeys, onLogical(checkCandidateDirectionKeys)},
	// shell 转义/文档对照/设置核心/Linux 装配/模块布局/架构文档。
	{groupShellEscaping, onLogical(checkShellVarEscaping)},
	{groupDocParity, onLogical(checkDocParity)},
	{groupSettingsCore, onLogical(checkSettingsCore)},
	{groupLinuxSetupAssembly, onLogical(checkLinuxSetupAssembly)},
	{groupModuleLayout, onPhysical(checkShimModuleLayout)},
	{groupArchitectureDoc, onPhysical(checkArchitectureDoc)},
}

// groupNames 返回实际会执行的检查组名称（与执行顺序一致，按组名去重）。
//
// 同一组允许分多段注册（目前只有「打包安装」需要按原执行位置拆两段），
// 汇总输出时只列一次：打印的组 = 实际执行过的组，不重复也不遗漏。
func groupNames() []string {
	seen := make(map[string]bool, len(checkGroups))
	names := make([]string, 0, len(checkGroups))
	for _, g := range checkGroups {
		if seen[g.name] {
			continue
		}
		seen[g.name] = true
		names = append(names, g.name)
	}
	return names
}

// Run 对给定文件树执行全部检查，返回问题列表（空 = 通过）。
func Run(base fs.FS) []string {
	c := &checker{fsys: bundledFS(base), physical: base}
	for _, g := range checkGroups {
		g.run(c)
	}
	return c.problems
}
