// TIP 的 CLSID / 语言配置 GUID / TSF 核心接口与类别标识（Windows 壳常量 TU）。
//
// 从 win32.cpp 顶部拆出（wave1 续）：注册 TU（tip_registration.cpp）与 TIP TU
// 共用同一份常量。C++17 inline const GUID——同一定义可被多个 TU 包含，链接期
// 收敛为一份；用常量名而不是 msctf.lib 外部符号，避免不同 SDK/MinGW 版本的
// 符号缺失（见各常量上方原注释）。
//
// 叶子模块（docs/architecture.md §3.1）：不 include/引用 TIP 类型；只依赖
// <windows.h> 的 GUID 定义。

#ifndef CHAOSET_WIN_TIP_IDS_HPP_
#define CHAOSET_WIN_TIP_IDS_HPP_

#include <windows.h>

namespace chaoset::win {

// 本壳 TIP 的 COM 类标识（固定值；注册表以它为键）
inline const GUID CLSID_ChaosetTIP = {
    0x8a5c7b31, 0x4e2d, 0x4c6a, {0x9f, 0x41, 0x77, 0xd0, 0xb3, 0xe5, 0xc1, 0xa8}};

// 本壳语言配置标识（固定值；用于 Windows TSF 0x0804 简体中文配置）
inline const GUID GUID_ChaosetProfile = {
    0x5a1b3c7d, 0x8e9f, 0x4a1b, {0xa2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18, 0x29}};

// Windows TSF 核心接口与类别标识（避免不同 SDK/MinGW 版本的外部符号缺失）
inline const GUID kCLSID_TF_InputProcessorProfiles = {
    0x33c53a50, 0xf456, 0x4884, {0xb0, 0x49, 0x85, 0xfd, 0x64, 0x3e, 0xcf, 0xed}};
inline const GUID kIID_ITfInputProcessorProfiles = {
    0x1f02b6c5, 0x7842, 0x4ee6, {0x8a, 0x0b, 0x9a, 0x24, 0x18, 0x3a, 0x95, 0xca}};
inline const GUID kCLSID_TF_CategoryMgr = {
    0xa4b544a1, 0x438d, 0x4b41, {0x93, 0x25, 0x86, 0x95, 0x23, 0xe2, 0xd6, 0xc7}};
inline const GUID kIID_ITfCategoryMgr = {
    0xc3acefb5, 0xf69d, 0x4905, {0x93, 0x8f, 0xfc, 0xad, 0xcf, 0x4b, 0xe8, 0x30}};

// TSF 键盘与显示类别标识
inline const GUID kGUID_TFCAT_TIP_KEYBOARD = {
    0x34745c63, 0xb2f0, 0x4784, {0x8b, 0x67, 0x5e, 0x12, 0xc8, 0x70, 0x1a, 0x31}};
inline const GUID kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER = {
    0x046602e4, 0xc36a, 0x4f66, {0x82, 0xf7, 0xb4, 0x4d, 0x5f, 0xd8, 0x1a, 0xc4}};
// 现代 Windows (Windows 8/10/11) 沉浸式与托盘支持能力分类，确保在现代系统设置与 UWP 应用中可见。
//
// **刻意不注册 TFCAT_TIPCAP_UIELEMENTENABLED**（2026-10-30 修）：
// 该类别声明「本 TIP 以 UILess 模式提供候选 UI」——TSF 在沉浸式宿主
// （Windows 11 开始菜单 / 搜索框等）里据此期望 TIP 通过
// ITfUIElementMgr::BeginUIElement 提交 ITfUIElement，由宿主自行渲染。
// 本壳从未实现这些接口（全文件 ITfUIElementMgr / BeginUIElement 出现 0 次），
// 候选窗是进程内自建的 GDI 弹窗——声明与实现矛盾。
// 实机症状：记事本（普通 Win32 宿主）候选窗正常，开始菜单里完全看不到
// （2026-10-30 用户反馈）。撤掉声明让 TSF 按「TIP 自绘候选」的普通路径
// 对待，与实现保持一致。
//
// 保留 IMMERSIVESUPPORT / SYSTRAYSUPPORT：它们只影响「在现代设置与任务栏
// 中可见」，与候选窗渲染路径无关。
// 线程级隔间：键盘开/关（中文输入 = 打开）。Windows 自带的输入指示器
//（任务栏右下的「中/英」）就是读这个值来显示当前模式的——微软拼音、搜狗
// 等输入法都靠它，不需要自建托盘图标（2026-11-05 用户要求补模式提示）。
// 数值取自 Windows SDK 的 msctf.lib（此处按 ReactOS 的 msctf_g.c 抄录，一致）。
inline const GUID kGUID_COMPARTMENT_KEYBOARD_OPENCLOSE = {
    0x58273aad, 0x01bb, 0x4164, {0x95, 0xc6, 0x75, 0x5b, 0xa0, 0xb5, 0x16, 0x2d}};

inline const GUID kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT = {
    0x13a016df, 0x560b, 0x46cd, {0x94, 0x7a, 0x4c, 0x3a, 0xf1, 0xe0, 0xe3, 0x5d}};
inline const GUID kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT = {
    0x25504fb4, 0x7bab, 0x4bc1, {0x9c, 0x69, 0xcf, 0x81, 0x89, 0x0f, 0x0e, 0xf5}};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_TIP_IDS_HPP_
