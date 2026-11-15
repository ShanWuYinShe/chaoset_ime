// UILess 宿主官方协商（微软《UI less mode》/《UILess Mode Overview》）：
// ITfCandidateListUIElement 的 TIP 侧实现与 mingw 缺失接口补声明。
//
// 官方协议（微软文档 + Windows SDK 示例 SampleIME CCandidateListUIPresenter）：
//   1. UILess 线程（TF_TMAE_UIELEMENTENABLEDONLY）激活后，TIP 通过
//      ITfUIElementMgr::BeginUIElement 提交 ITfUIElement（候选列表即
//      ITfCandidateListUIElement）；
//   2. 宿主在 BeginUIElement 出参 pbShow 里表态：TRUE = 宿主渲染（TIP 不画
//      浮窗）；FALSE = 宿主不渲染，按官方语义 TIP 自行显示自己的 UI；
//   3. 数据变化时 TIP 设 TF_CLUIE_* 更新标志并调 UpdateUIElement；
//   4. 候选关闭时 EndUIElement。
// Dota 2/CS2 等 SDL2 宿主对 BeginUIElement 一律回 pbShow=FALSE 且不画
// 非白名单输入法的候选 → 本壳按官方语义回退 GDI 自绘浮窗（render 兜底）；
// 愿意渲染的 UILess 宿主（部分 WinUI/触摸键盘宿主）则由宿主画原生候选。
//
// 本文件是叶子模块（docs/architecture.md §3.1）：不得 include/引用 TIP TU
// 的类型；对 TIP 的回呼只经 UiElementHost 抽象接口。
//
// 接口声明的常量来源（mingw 的 msctf.h 缺 ITfCandidateListUIElement，本文件
// 按 Windows SDK msctf.idl 原样补声明；IID/标志值经三源交叉验证一致）：
//   * deploymenttheory/go-bindings-win32（自微软 SDK 元数据生成）；
//   * libsdl-org/SDL src/video/windows/SDL_msctf.h 的 vtable（Dota 2 同款
//     宿主库，为 MinGW 补的同一批声明）；
//   * microsoft/Windows-classic-samples 官方示例 SampleIME 的用法。
//   IID_ITfCandidateListUIElement = ea1ea138-19df-11d7-a6d2-00065b84435c
//   （与 mingw 已声明的 IID_ITfUIElement ea1ea137 / IID_ITfUIElementMgr
//   ea1ea135 同批连续段）。

#ifndef CHAOSET_WIN_UI_ELEMENT_HPP_
#define CHAOSET_WIN_UI_ELEMENT_HPP_

#include <windows.h>
#include <msctf.h>

#include <string>
#include <vector>

namespace chaoset::win {

// ITfCandidateListUIElement 的 IID（见文件头三源验证说明）。
inline const GUID kIID_ITfCandidateListUIElement = {
    0xea1ea138, 0x19df, 0x11d7, {0xa6, 0xd2, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}};

// GetUpdatedFlags 的官方更新标志（Windows SDK msctf.h；值自 SDK 元数据绑定）。
inline const DWORD kTfCluieSelection = 0x00000004;
inline const DWORD kTfCluieString = 0x00000008;
inline const DWORD kTfCluieCount = 0x00000002;
inline const DWORD kTfCluiePageindex = 0x00000010;
inline const DWORD kTfCluieCurrentpage = 0x00000020;

// mingw 的 msctf.h 缺 ITfCandidateListUIElement 接口声明（只有 ITfUIElement /
// ITfUIElementMgr）。按 Windows SDK msctf.idl 原样补：继承 ITfUIElement，
// 虚表顺序 = ITfUIElement 四方法 + 下列八字节方法（SDL_msctf.h vtable 与
// SDK 元数据绑定双源一致）。
struct ITfCandidateListUIElement : public ITfUIElement {
// 微软 SDK 接口的惯用保护析构：防止经基类指针 delete（COM 释放只经 Release）。
protected:
    ~ITfCandidateListUIElement() {}
public:
    virtual HRESULT STDMETHODCALLTYPE GetUpdatedFlags(DWORD* pdwFlags) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetDocumentMgr(ITfDocumentMgr** ppdim) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetCount(UINT* puCount) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetSelection(UINT* puIndex) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetString(UINT nIndex, BSTR* pbstr) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetPageIndex(UINT** ppIndex, UINT uCount,
                                                   UINT* puPageCnt) = 0;
    virtual HRESULT STDMETHODCALLTYPE SetPageIndex(UINT* pIndex, UINT uCount) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetCurrentPage(UINT* puPage) = 0;
};

// 宿主经 ITfUIElement::Show 通知显示/隐藏时的回呼（TIP 据此同步自绘浮窗）。
class UiElementHost {
public:
    virtual ~UiElementHost() = default;
    virtual void onUiElementShow(BOOL shown) = 0;
};

// 本壳的候选列表 UI 元素：官方 ITfCandidateListUIElement 的最小正确实现。
// 数据模型与壳的分页候选一致：GetString(0..count) 为**当前页**候选文本，
// GetSelection 为页内预选，GetCurrentPage/GetPageIndex 反映引擎页码（单页
// 表）；宿主要翻页时 SetPageIndex 返回 E_NOTIMPL（翻页由用户方向键驱动，
// 本壳更新数据后 UpdateUIElement 通知）。
class ChaosetCandidateListUIElement final : public ITfCandidateListUIElement {
public:
    static HRESULT CreateInstance(UiElementHost* host,
                                  ChaosetCandidateListUIElement** ppobj);

    // IUnknown
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override;
    ULONG STDMETHODCALLTYPE AddRef() override;
    ULONG STDMETHODCALLTYPE Release() override;

    // ITfUIElement
    HRESULT STDMETHODCALLTYPE GetDescription(BSTR* pbstr) override;
    HRESULT STDMETHODCALLTYPE GetGUID(GUID* pguid) override;
    HRESULT STDMETHODCALLTYPE Show(BOOL shown) override;
    HRESULT STDMETHODCALLTYPE IsShown(BOOL* pbShown) override;

    // ITfCandidateListUIElement
    HRESULT STDMETHODCALLTYPE GetUpdatedFlags(DWORD* pdwFlags) override;
    HRESULT STDMETHODCALLTYPE GetDocumentMgr(ITfDocumentMgr** ppdim) override;
    HRESULT STDMETHODCALLTYPE GetCount(UINT* puCount) override;
    HRESULT STDMETHODCALLTYPE GetSelection(UINT* puIndex) override;
    HRESULT STDMETHODCALLTYPE GetString(UINT nIndex, BSTR* pbstr) override;
    HRESULT STDMETHODCALLTYPE GetPageIndex(UINT** ppIndex, UINT uCount,
                                           UINT* puPageCnt) override;
    HRESULT STDMETHODCALLTYPE SetPageIndex(UINT* pIndex, UINT uCount) override;
    HRESULT STDMETHODCALLTYPE GetCurrentPage(UINT* puPage) override;

    // TIP 每次 render 灌入当前页数据；updatedFlags 为本次变化的 TF_CLUIE_*
    // 组合，GetUpdatedFlags 据此应答（官方协议：宿主按标志做差量渲染）。
    void snapshot(const std::vector<std::wstring>& strings, UINT selection,
                  UINT page, DWORD updatedFlags);

private:
    explicit ChaosetCandidateListUIElement(UiElementHost* host);
    // COM 接口无虚析构：不经基类指针 delete（释放只经本类 Release），
    // 故析构不标 override。
    ~ChaosetCandidateListUIElement();

    UiElementHost* host_;
    LONG ref_ = 1;
    bool shown_ = true;
    DWORD updatedFlags_ = 0;
    UINT selection_ = 0;
    UINT page_ = 0;
    std::vector<std::wstring> strings_;
};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_UI_ELEMENT_HPP_
