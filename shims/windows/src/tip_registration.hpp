// COM 注册入口（regsvr32 / install.bat / NSIS 安装器走 DLL 导出表）——
// Windows 壳独立 TU（冷路径）。
//
// 从 win32.cpp 拆出（wave1 续）：DllRegisterServer/DllUnregisterServer/
// DllUninstallServer 及其注册表 helper。注册内容 = COM InprocServer32 +
// TSF 配置/类别（双击写 CTF 结构）+ 旧版 UILess 类别清理；细节与事故背景
// 见实现内注释。
//
// 模块边界（docs/architecture.md §3.1）：不 include/引用 TIP 类型；只依赖
// tip_ids（CLSID/GUID 常量）与 winutil（模块路径 g_hInst）。

#ifndef CHAOSET_WIN_TIP_REGISTRATION_HPP_
#define CHAOSET_WIN_TIP_REGISTRATION_HPP_

#include <windows.h>
#include <objbase.h>

// COM 注册入口：必须是 extern "C" 导出（regsvr32 只认这三个名字；build.sh 的
// objdump 断言锁住导出表）。定义见 tip_registration.cpp。
STDAPI DllRegisterServer();
STDAPI DllUnregisterServer();
STDAPI DllUninstallServer();

#endif  // CHAOSET_WIN_TIP_REGISTRATION_HPP_
