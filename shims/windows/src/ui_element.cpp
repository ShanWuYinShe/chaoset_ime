// UILess 官方协商的候选列表 UI 元素实现（叶子模块，见 ui_element.hpp 文件头）。
//
// 方法语义逐条对照 Windows SDK 示例 SampleIME 的 CCandidateListUIPresenter
// （官方 UILess 参考实现）与本壳数据模型；元素 GUID 为本壳自定义标识
// （ITfUIElement::GetGUID 官方语义：本元素的唯一 id，非接口 IID）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <msctf.h>

#include "ui_element.hpp"

#include <objbase.h>
#include <oleauto.h>

namespace chaoset::win {

// 本元素唯一 id（GetGUID 应答；官方语义为「该 UI 元素的标识」，SampleIME
// 同样使用自定义 GUID）。随机生成后固化，不参与注册表/协议匹配。
inline const GUID kGUID_ChaosetCandListUIElement = {
    0x7f4a9c52, 0x83d1, 0x4be7, {0x9a, 0x25, 0x3e, 0x61, 0xc8, 0xf0, 0x2d, 0xb4}};

HRESULT ChaosetCandidateListUIElement::CreateInstance(UiElementHost* host,
                                                      ChaosetCandidateListUIElement** ppobj) {
    if (ppobj == nullptr) return E_POINTER;
    *ppobj = nullptr;
    auto* obj = new (std::nothrow) ChaosetCandidateListUIElement(host);
    if (obj == nullptr) return E_OUTOFMEMORY;
    *ppobj = obj;
    return S_OK;
}

ChaosetCandidateListUIElement::ChaosetCandidateListUIElement(UiElementHost* host)
    : host_(host) {}

ChaosetCandidateListUIElement::~ChaosetCandidateListUIElement() = default;

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::QueryInterface(REFIID riid,
                                                                        void** ppv) {
    if (ppv == nullptr) return E_POINTER;
    *ppv = nullptr;
    if (IsEqualIID(riid, IID_IUnknown) || IsEqualIID(riid, IID_ITfUIElement)) {
        *ppv = static_cast<ITfUIElement*>(this);
    } else if (IsEqualIID(riid, IID_ITfCandidateListUIElement)) {
        *ppv = static_cast<ITfCandidateListUIElement*>(this);
    }
    if (*ppv == nullptr) return E_NOINTERFACE;
    AddRef();
    return S_OK;
}

ULONG STDMETHODCALLTYPE ChaosetCandidateListUIElement::AddRef() {
    return InterlockedIncrement(&ref_);
}

ULONG STDMETHODCALLTYPE ChaosetCandidateListUIElement::Release() {
    const LONG cr = InterlockedDecrement(&ref_);
    if (cr == 0) delete this;
    return static_cast<ULONG>(cr);
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetDescription(BSTR* pbstr) {
    if (pbstr == nullptr) return E_POINTER;
    *pbstr = SysAllocString(L"Chaoset candidate list");
    return *pbstr != nullptr ? S_OK : E_OUTOFMEMORY;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetGUID(GUID* pguid) {
    if (pguid == nullptr) return E_POINTER;
    *pguid = kGUID_ChaosetCandListUIElement;
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::Show(BOOL shown) {
    shown_ = shown != FALSE;
    if (host_ != nullptr) host_->onUiElementShow(shown_);
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::IsShown(BOOL* pbShown) {
    if (pbShown == nullptr) return E_POINTER;
    *pbShown = shown_ ? TRUE : FALSE;
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetUpdatedFlags(DWORD* pdwFlags) {
    if (pdwFlags == nullptr) return E_POINTER;
    *pdwFlags = updatedFlags_;
    return S_OK;
}

// 官方模型里元素可回指承载文档；本壳的 UILess 数据快照不持有文档引用
// （文档指针跨 COM 协商传递的生命周期成本大于收益），明确返回未实现。
// 愿意渲染的宿主不依赖它画候选；SDL2 类宿主对 BeginUIElement 一律
// pbShow=FALSE，本壳按官方语义回退自绘，无候选丢失风险。
HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetDocumentMgr(ITfDocumentMgr** ppdim) {
    if (ppdim == nullptr) return E_POINTER;
    *ppdim = nullptr;
    return E_NOTIMPL;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetCount(UINT* puCount) {
    if (puCount == nullptr) return E_POINTER;
    *puCount = static_cast<UINT>(strings_.size());
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetSelection(UINT* puIndex) {
    if (puIndex == nullptr) return E_POINTER;
    *puIndex = selection_;
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetString(UINT nIndex, BSTR* pbstr) {
    if (pbstr == nullptr) return E_POINTER;
    *pbstr = nullptr;
    if (nIndex >= strings_.size()) return E_INVALIDARG;
    *pbstr = SysAllocStringLen(strings_[nIndex].c_str(),
                               static_cast<UINT>(strings_[nIndex].size()));
    return *pbstr != nullptr ? S_OK : E_OUTOFMEMORY;
}

// 单页表：返回 CoTaskMemAlloc 分配的当前页码数组（COM [out] 数组标准语义，
// 调用方 CoTaskMemFree 释放）；宿主端不参与翻页，翻页由用户方向键驱动、
// 本壳更新数据后经 UpdateUIElement 通知。
HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetPageIndex(UINT** ppIndex,
                                                                      UINT uCount,
                                                                      UINT* puPageCnt) {
    if (ppIndex == nullptr || puPageCnt == nullptr) return E_POINTER;
    *ppIndex = nullptr;
    *puPageCnt = 0;
    if (uCount == 0) return E_INVALIDARG;
    auto* pages = static_cast<UINT*>(CoTaskMemAlloc(sizeof(UINT)));
    if (pages == nullptr) return E_OUTOFMEMORY;
    pages[0] = page_;
    *ppIndex = pages;
    *puPageCnt = 1;
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::SetPageIndex(UINT* /*pIndex*/,
                                                                      UINT /*uCount*/) {
    // 翻页由用户方向键驱动（数据经 snapshot + UpdateUIElement 刷新），
    // 宿主端换页请求明确不支持。
    return E_NOTIMPL;
}

HRESULT STDMETHODCALLTYPE ChaosetCandidateListUIElement::GetCurrentPage(UINT* puPage) {
    if (puPage == nullptr) return E_POINTER;
    *puPage = page_;
    return S_OK;
}

void ChaosetCandidateListUIElement::snapshot(const std::vector<std::wstring>& strings,
                                             UINT selection, UINT page,
                                             DWORD updatedFlags) {
    strings_ = strings;
    selection_ = selection;
    page_ = page;
    updatedFlags_ = updatedFlags;
}

}  // namespace chaoset::win
