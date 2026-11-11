// COM 注册入口实现（冷路径）；声明与职责说明见 tip_registration.hpp。
//
// 从 win32.cpp 拆出（wave1 续）：注册 TU 不 include/引用 TIP 类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <msctf.h>
#include <objbase.h>

#include <string>

#include "tip_ids.hpp"
#include "tip_registration.hpp"
#include "winutil.hpp"

using namespace chaoset::win;

namespace {

// 注册表 helper（内部链接）：只在注册/反注册路径使用（冷路径，非 TSF 回调）。
static std::string dllPath() {
    char path[MAX_PATH] = {0};
    if (g_hInst != nullptr) {
        GetModuleFileNameA(g_hInst, path, MAX_PATH);
    }
    return path;
}

static std::wstring dllPathW() {
    wchar_t path[MAX_PATH] = {0};
    if (g_hInst != nullptr) {
        GetModuleFileNameW(g_hInst, path, MAX_PATH);
    }
    return path;
}

static HRESULT regSet(HKEY root, const std::string& key, const std::string& name,
                      const std::string& data) {
    HKEY k = nullptr;
    LONG r = RegCreateKeyExA(root, key.c_str(), 0, nullptr, REG_OPTION_NON_VOLATILE, KEY_WRITE,
                             nullptr, &k, nullptr);
    if (r != ERROR_SUCCESS) return E_FAIL;
    r = RegSetValueExA(k, name.c_str(), 0, REG_SZ, reinterpret_cast<const BYTE*>(data.c_str()),
                       static_cast<DWORD>(data.size() + 1));
    RegCloseKey(k);
    return r == ERROR_SUCCESS ? S_OK : E_FAIL;
}

static HRESULT regSetDword(HKEY root, const std::string& key, const std::string& name,
                           DWORD data) {
    HKEY k = nullptr;
    LONG r = RegCreateKeyExA(root, key.c_str(), 0, nullptr, REG_OPTION_NON_VOLATILE, KEY_WRITE,
                             nullptr, &k, nullptr);
    if (r != ERROR_SUCCESS) return E_FAIL;
    r = RegSetValueExA(k, name.c_str(), 0, REG_DWORD, reinterpret_cast<const BYTE*>(&data),
                       sizeof(DWORD));
    RegCloseKey(k);
    return r == ERROR_SUCCESS ? S_OK : E_FAIL;
}

static std::string guidToString(const GUID& g) {
    wchar_t wide[64] = {0};
    StringFromGUID2(g, wide, 64);
    char narrow[64] = {0};
    WideCharToMultiByte(CP_ACP, 0, wide, -1, narrow, sizeof(narrow), nullptr, nullptr);
    return narrow;
}

// 旧版（≤v0.2.16）注册过 UILess/UIELEMENTENABLED 类别；v0.2.17 起本壳不再
// 声明该类别（不实现 ITfUIElementMgr）。NSIS 安装器有 DeleteRegKey 清理，
// 但便携 ZIP 走 regsvr32 → DllRegisterServer 的路径没有清理步骤，升级用户
// 会残留该类别，开始菜单/搜索继续按 UILess 模式期待宿主渲染候选窗
//（2026-10-31 审计 A2）。这里在每个 DLL 注册时幂等清掉自己注册表视图下的
// 残留：64 位 DLL 清 64 位视图，32 位 DLL（WOW64）清 32 位视图。
static void removeLegacyUILessCategory() {
    const GUID kLegacyUIElementEnabled = {
        0x49d2f9cf, 0x1f5e, 0x11d7, {0xa6, 0xd3, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}};
    // 1) 经类别管理器注销（若 COM 已初始化；失败不影响后续注册）
    ITfCategoryMgr* catMgr = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_CategoryMgr, nullptr, CLSCTX_INPROC_SERVER,
                                   kIID_ITfCategoryMgr, reinterpret_cast<void**>(&catMgr)))) {
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kLegacyUIElementEnabled, CLSID_ChaosetTIP);
        catMgr->Release();
    }
    // 2) 直接删注册表残留（类别管理器注销在部分环境可能不落盘；幂等兜底）
    std::string base = "Software\\Microsoft\\CTF\\TIPs\\" + guidToString(CLSID_ChaosetTIP) +
                       "\\Category\\";
    const std::string cat = guidToString(kLegacyUIElementEnabled);
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, (base + "Category\\" + cat).c_str());
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, (base + "Item\\" + cat).c_str());
}

}  // namespace

STDAPI DllRegisterServer() {
    std::string clsid = guidToString(CLSID_ChaosetTIP);
    std::string profid = guidToString(GUID_ChaosetProfile);
    std::string pathA = dllPath();
    std::wstring pathW = dllPathW();

    // 1. COM 基础服务注册 (HKLM\Software\Classes\CLSID\{CLSID})
    std::string base = "Software\\Classes\\CLSID\\" + clsid;
    HRESULT hr = regSet(HKEY_LOCAL_MACHINE, base, "", "Chaoset 拼音输入法");
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, base + "\\InprocServer32", "", pathA);
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, base + "\\InprocServer32", "ThreadingModel", "Apartment");

    // 2. 通过 Windows TSF 标准 COM 接口注册配置文件与类别
    HRESULT hrCo = CoInitialize(nullptr);

    // 升级清理：便携 ZIP（install.bat → regsvr32）路径没有 NSIS 的
    // DeleteRegKey 步骤，必须在注册时自己清掉旧版残留的 UILess 类别（A2）。
    removeLegacyUILessCategory();

    ITfInputProcessorProfiles* profiles = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_InputProcessorProfiles, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfInputProcessorProfiles,
                                   reinterpret_cast<void**>(&profiles)))) {
        profiles->Register(CLSID_ChaosetTIP);
        const wchar_t* desc = L"Chaoset 拼音输入法";
        profiles->AddLanguageProfile(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile,
                                     desc, static_cast<ULONG>(wcslen(desc)),
                                     pathW.c_str(), static_cast<ULONG>(pathW.size()), 0);
        profiles->EnableLanguageProfile(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile, TRUE);
        profiles->EnableLanguageProfileByDefault(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile, TRUE);
        profiles->Release();
    }

    ITfCategoryMgr* catMgr = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_CategoryMgr, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfCategoryMgr,
                                   reinterpret_cast<void**>(&catMgr)))) {
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIP_KEYBOARD, CLSID_ChaosetTIP);
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER, CLSID_ChaosetTIP);
        // 不注册 UIELEMENTENABLED：本壳不实现 ITfUIElementMgr（见常量处注释）
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT, CLSID_ChaosetTIP);
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT, CLSID_ChaosetTIP);
        catMgr->Release();
    }

    if (SUCCEEDED(hrCo)) {
        CoUninitialize();
    }

    // 3. 注册表双重保障：直接写入 CTF 结构（应对特殊环境与控制面板直接扫描）
    std::string tips = "Software\\Microsoft\\CTF\\TIPs\\" + clsid;
    regSet(HKEY_LOCAL_MACHINE, tips, "", "Chaoset 拼音输入法");

    const GUID* catGuids[] = {
        &kGUID_TFCAT_TIP_KEYBOARD,
        &kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER,
        // 不含 UIELEMENTENABLED：本壳不实现 ITfUIElementMgr（见常量处注释）
        &kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT,
        &kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT,
    };
    for (const GUID* g : catGuids) {
        std::string gStr = guidToString(*g);
        regSet(HKEY_LOCAL_MACHINE, tips + "\\Category\\Category\\" + gStr + "\\" + clsid, "", "");
        regSet(HKEY_LOCAL_MACHINE, tips + "\\Category\\Item\\" + gStr + "\\" + clsid, "", "");
    }

    std::string langProfile = tips + "\\LanguageProfile\\0x00000804\\" + profid;
    regSet(HKEY_LOCAL_MACHINE, langProfile, "Description", "Chaoset 拼音输入法");
    regSet(HKEY_LOCAL_MACHINE, langProfile, "IconFile", pathA);
    regSetDword(HKEY_LOCAL_MACHINE, langProfile, "IconIndex", 0);
    regSetDword(HKEY_LOCAL_MACHINE, langProfile, "Enable", 1);

    return hr;
}

STDAPI DllUnregisterServer() {
    std::string clsid = guidToString(CLSID_ChaosetTIP);

    HRESULT hrCo = CoInitialize(nullptr);

    ITfCategoryMgr* catMgr = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_CategoryMgr, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfCategoryMgr,
                                   reinterpret_cast<void**>(&catMgr)))) {
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIP_KEYBOARD, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER, CLSID_ChaosetTIP);
        // 注销旧版注册过的 UILess 类别（本版不再注册，但升级用户的注册表里
        // 仍残留，卸载必须清干净；GUID 直接内联，常量已随修复删除）。
        const GUID kLegacyUIElementEnabled = {
            0x49d2f9cf, 0x1f5e, 0x11d7, {0xa6, 0xd3, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}};
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kLegacyUIElementEnabled, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT, CLSID_ChaosetTIP);
        catMgr->Release();
    }

    ITfInputProcessorProfiles* profiles = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_InputProcessorProfiles, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfInputProcessorProfiles,
                                   reinterpret_cast<void**>(&profiles)))) {
        profiles->RemoveLanguageProfile(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile);
        profiles->Unregister(CLSID_ChaosetTIP);
        profiles->Release();
    }

    if (SUCCEEDED(hrCo)) {
        CoUninitialize();
    }

    RegDeleteTreeA(HKEY_LOCAL_MACHINE, ("Software\\Microsoft\\CTF\\TIPs\\" + clsid).c_str());
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, ("Software\\Classes\\CLSID\\" + clsid).c_str());
    return S_OK;
}

// 保持历史兼容
STDAPI DllUninstallServer() {
    return DllUnregisterServer();
}
