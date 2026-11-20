// TIP 的 CLSID / 语言配置 GUID / TSF 核心接口与类别标识（Windows 壳常量 TU）。
//
// 本输入法私有 GUID 保留在此处；所有 Windows TSF 官方标准 GUID 均直接采用
// Windows SDK <msctf.h> 原生定义并通过 uuid.lib / initguid.h 解析。

#ifndef CHAOSET_WIN_TIP_IDS_HPP_
#define CHAOSET_WIN_TIP_IDS_HPP_

#include <windows.h>
#include <msctf.h>

namespace chaoset::win {

// 本壳 TIP 的 COM 类标识（固定值；注册表以它为键）
inline const GUID CLSID_ChaosetTIP = {
    0x8a5c7b31, 0x4e2d, 0x4c6a, {0x9f, 0x41, 0x77, 0xd0, 0xb3, 0xe5, 0xc1, 0xa8}};

// 本壳语言配置标识（固定值；用于 Windows TSF 0x0804 简体中文配置）
inline const GUID GUID_ChaosetProfile = {
    0x5a1b3c7d, 0x8e9f, 0x4a1b, {0xa2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18, 0x29}};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_TIP_IDS_HPP_
