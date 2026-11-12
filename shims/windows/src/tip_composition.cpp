// ChaosetTIP 文本合成：EditSession + 预编辑/光标/提交/结束合成 + 会话收尾
// （Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 拆出：EditSession（TSF 同步编辑会话，原类前整段）与类内定义
// focusContext/can*/render/上下文探测/updateCaretFromRange/applyPreedit/
// applyCommit/syncDpScheme/reset*/flushOrCancel/endComposition/applyEndComposition/
// releaseCompositionContext/splitSyllables。方法体逐字迁移（单合成模型、
// 引用所有权与 cookie 兜底逻辑全部保留）。
//
// 模块边界：只依赖 tip.hpp 与 core.hpp 的 IPC 纯数据；不 include win32.cpp。

#include "tip.hpp"

using namespace chaoset::win;

namespace {

// ---- 同步编辑会话：TSF 规定文本操作必须经 RequestEditSession ----

class EditSession : public ITfEditSession {
public:
    using Fn = void (*)(void* user, ITfContext* ctx, TfEditCookie ec);
    EditSession(ITfContext* ctx, void* user, Fn fn)
        : ctx_(ctx), user_(user), fn_(fn) {}

    HRESULT run(ITfThreadMgr* tm, TfClientId tid) {
        (void)tm;
        HRESULT hrSession = E_FAIL;
        HRESULT hr = ctx_->RequestEditSession(tid, this, TF_ES_SYNC | TF_ES_READWRITE, &hrSession);
        if (FAILED(hr)) return hr;
        return hrSession;
    }

    // IUnknown（栈上使用：外层在 RequestEditSession 返回后 Release）
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_ITfEditSession) {
            *ppv = static_cast<ITfEditSession*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    // 栈上生命周期：TSF 在同步调用内 AddRef/Release 配对，
    // 由外层作用域析构，Release 绝不 delete this。
    ULONG STDMETHODCALLTYPE AddRef() override { return InterlockedIncrement(&ref_); }
    ULONG STDMETHODCALLTYPE Release() override { return InterlockedDecrement(&ref_); }
    HRESULT STDMETHODCALLTYPE DoEditSession(TfEditCookie ec) override {
        fn_(user_, ctx_, ec);
        return S_OK;
    }

private:
    ITfContext* ctx_;
    void* user_;
    Fn fn_;
    LONG ref_ = 1;
};

}  // namespace


ITfContext* ChaosetTIP::focusContext() {
    if (tm_ == nullptr) return nullptr;
    ITfDocumentMgr* doc = nullptr;
    if (FAILED(tm_->GetFocus(&doc)) || doc == nullptr) return nullptr;
    ITfContext* ctx = nullptr;
    doc->GetBase(&ctx);
    doc->Release();
    return ctx;  // 调用方必须用 ContextRef 释放
}


// 上下文能力探测：只做 QI，不改文档、不产生副作用，可安全用于
// OnTestKeyDown。Windows 11 开始菜单等 XAML 宿主可能不提供合成接口。
bool ChaosetTIP::canInsert(ITfContext* ctx) {
    if (ctx == nullptr) return false;
    ITfInsertAtSelection* insert = nullptr;
    if (FAILED(ctx->QueryInterface(IID_ITfInsertAtSelection,
                                   reinterpret_cast<void**>(&insert))) ||
        insert == nullptr) {
        return false;
    }
    insert->Release();
    return true;
}


bool ChaosetTIP::canCompose(ITfContext* ctx) {
    if (!canInsert(ctx)) return false;
    ITfContextComposition* cc = nullptr;
    if (FAILED(ctx->QueryInterface(IID_ITfContextComposition,
                                   reinterpret_cast<void**>(&cc))) ||
        cc == nullptr) {
        return false;
    }
    cc->Release();
    return true;
}


// StartComposition/InsertTextAtSelection 语义上要求可写文档：只读上下文
// 上调用属于 TS_E_READONLY，且部分宿主对只读文档的回调和出参处理不可靠。
// 探测顺序（与 SampleIME 一致）：GetStatus 查 TF_SD_READONLY → QueryInterface
// ITfInsertAtSelection → ITfContextComposition。全程零文档副作用，可安全
// 用于 OnTestKeyDown 预检（2026-10-29 实机反馈：Windows 11 开始菜单
// 搜索框等宿主的上下文需要放行）。
bool ChaosetTIP::canStartComposition(ITfContext* ctx) {
    if (ctx == nullptr) return false;
    TF_STATUS st{};
    if (FAILED(ctx->GetStatus(&st))) return false;
    if (st.dwStaticFlags & TF_SD_READONLY) return false;
    return canCompose(ctx);  // canInsert + ITfContextComposition
}


// 「该键现在确实处理不了」→ 撤掉引擎未决状态并放行原键。渲染后仍无
// 合成对象（接口探测放过了但宿主仍拒绝建合成）即属此类：键必须还给
// 宿主，否则就是「键被吃掉、字也没出来」（2026-10-29 实机反馈）。
// OnKeyDown 返回 false 是两阶段按键模型的最终裁决，TSF 会把原键交还。
bool ChaosetTIP::bailOutNoComposition() {
    if (diagBudget()) {
        char host[260];
        diagHostName(host, static_cast<int>(sizeof(host)));
        diagPrintf(L"tsf.log", "no composition after render host=%s preedit_len=%lu\n",
                   host, static_cast<unsigned long>(comp_.preedit.size()));
    }
    resetComposition();
    comp_ = chaoset::Comp();
    confirmedPreedit_.clear(); // 上下文已不可写：缓冲一并放弃，绝不串进下轮组词
    candidateWnd_.hide();
    // 走到这里时合成对象必然已不存在（未建出或 SetText 失败已当场结束），
    // 上下文记录一并清掉，避免过期引用跨越焦点切换存活。
    releaseCompositionContext();
    return false;
}


// 把引擎候选状态渲染为预编辑合成与候选悬浮窗
void ChaosetTIP::render(ITfContext* pic) {
    g_lastOp = kOpRender;
    // 防御：非激活时绝不渲染任何 UI（候选窗/预编辑都不该出现在别人的应用里）。
    // 按键入口已有 bailOutIfInactive 闸门，这里再兜一层——render 还有别的
    // 调用点（联想、翻页、候选删除后的刷新），不能指望每个都先过闸门。
    // 用 inputActive_ 而不是 isActiveTip：后者在后台预激活实例仍可能为 true。
    if (!inputActive_) {
        candidateWnd_.hide();
        toast_.hide();
        return;
    }
    // P2（issue #1）：本帧请求编辑会话前先清「预编辑已写入」标志。旧值可能是上一次
    // 成功渲染留下的 true；若本次 RequestEditSession/SetText 失败，字母键与删除刷新
    // 路径的兜底判据（composition_ == nullptr || !preeditWritten_）会漏判，
    // 表现为「键被吃掉、字没出来 / 候选窗有候选而文档无预编辑」。
    preeditWritten_ = false;
    ContextRef owned(nullptr);
    ITfContext* ctx = pic;
    if (ctx == nullptr) {
        owned.ctx = focusContext();
        ctx = owned.ctx;
    }
    if (ctx == nullptr) return;
    // 记录合成所属上下文：此后所有对 composition_ 的清理必须对它做——
    // 此前一律 focusContext()，焦点已切走时拿到的是新文档的上下文，
    // 用它的 cookie 清旧合成必然失败，拼音文本永久残留（2026-10-29 审查）。
    if (compCtx_ != ctx) {
        if (compCtx_ != nullptr) compCtx_->Release();
        compCtx_ = ctx;
        ctx->AddRef();
    }
    caretFound_ = false;
    caretFromMouse_ = false;
    EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
        static_cast<ChaosetTIP*>(user)->applyPreedit(c, ec);
    });
    const HRESULT hr = es.run(tm_, clientId_);
    es.Release();
    if (FAILED(hr)) diagTsfFail("RequestEditSession(render)", hr);

    // 缓存承载本合成的宿主窗口（供前台事件回调做无 COM 的比较）。
    composeHostWnd_ = contextHostWindow(ctx);

    // 预编辑没写进文档（编辑会话失败/上下文只读/合成被拒）：绝不能显示候选窗，
    // 否则出现「候选窗有候选、文档无预编辑」的不一致状态。收口交给调用方按各自
    // 语义处理（字母键/删除刷新路径会走 bailOutNoComposition）。
    if (!comp_.input.empty() && (composition_ == nullptr || !preeditWritten_)) {
        candidateWnd_.hide();
        if (diagBudget()) {
            diagPrintf(L"tsf.log", "render: preedit not written composition=%d, hide candidate wnd\n",
                       composition_ != nullptr ? 1 : 0);
        }
        return;
    }

    if (comp_.candidates.empty()) {
        candidateWnd_.hide();
        return;
    }

    if (!caretFound_ || (caretPos_.x == 0 && caretPos_.y == 0)) {
        // 锚点兜底链：真实 GDI 光标 → 鼠标位置。rcCaret 只在 hwndCaret
        // 非空时有意义；浏览器等自绘应用没有 GDI 光标，借 hwndFocus 的
        // rcCaret 只会拿到 (0,0)，候选窗飞到屏幕左上角（2026-10-29 实机
        // 反馈）——该分支已删，直接退回鼠标位置（用户点哪儿鼠标在哪儿）。
        GUITHREADINFO gti{};
        gti.cbSize = sizeof(gti);
        if (GetGUIThreadInfo(0, &gti) && gti.hwndCaret != nullptr &&
            (gti.rcCaret.right > gti.rcCaret.left || gti.rcCaret.bottom > gti.rcCaret.top)) {
            POINT pt{gti.rcCaret.left, gti.rcCaret.bottom};
            ClientToScreen(gti.hwndCaret, &pt);
            caretPos_ = pt;
            caretLineHeight_ = (gti.rcCaret.bottom > gti.rcCaret.top) ? (gti.rcCaret.bottom - gti.rcCaret.top) : 20;
            caretFound_ = true;
            caretFromMouse_ = false;
        } else {
            // 鼠标兜底：**锚点要下移一行**。
            //
            // 鼠标通常正停在用户正在输入的那一行上，而候选窗是从锚点
            // 再往下 6pt 起画的——直接用鼠标坐标会把候选窗画在输入行
            // 上面，正好挡住正在打的字（Firefox 装到任务栏后的独立窗口
            // 实测「候选框遮挡输入」，2026-11-05）。下移一行后候选窗
            // 落在输入行下方，与有真实光标时的观感一致。
            GetCursorPos(&caretPos_);
            caretLineHeight_ = 20;
            caretPos_.y += caretLineHeight_;
            caretFromMouse_ = true;
        }
    }
    // 候选窗 owner = 宿主窗口（2026-10-30 开始菜单排障）。
    // ITfContextView::GetWnd 返回承载本上下文的宿主 HWND；拿不到时退回
    // 前台窗口。owner 关系让候选窗恒在其宿主之上，可跨 Windows 11
    // 开始菜单/搜索的沉浸式 z 序 band——无主 topmost 窗口会被压住。
    // owner 用**当前前台窗口**（且限定本进程），不要用 hostWindowOf(ctx)——
    // 后者可能是另一个窗口的上下文，带 owner 显示时会把它抬到前面，
    // 表现为「候选窗一出现就把别的窗口顶上来」（2026-11-05 用户反馈）。
    candidateWnd_.setOwner(popupOwner());
    candidateWnd_.show(g_hInst, caretPos_, caretLineHeight_, comp_, this);
}


// 取上下文宿主窗口（**只在 TSF 线程调用**）：与 hostWindowOf 的区别是
// 不回退到前台窗口，供前台事件回调缓存后做纯 Win32 比较。
HWND ChaosetTIP::contextHostWindow(ITfContext* ctx) {
    if (ctx == nullptr) return nullptr;
    ITfContextView* view = nullptr;
    if (FAILED(ctx->GetActiveView(&view)) || view == nullptr) return nullptr;
    HWND wnd = nullptr;
    const HRESULT hr = view->GetWnd(&wnd);
    view->Release();
    if (FAILED(hr) || wnd == nullptr || !IsWindow(wnd)) return nullptr;
    return wnd;
}


// 取上下文宿主窗口：优先 ITfContextView::GetWnd（TSF 标准途径，
// 直接给出承载该文档的 HWND），失败退回前台窗口。
HWND ChaosetTIP::hostWindowOf(ITfContext* ctx) {
    if (ctx != nullptr) {
        ITfContextView* view = nullptr;
        if (SUCCEEDED(ctx->GetActiveView(&view)) && view != nullptr) {
            HWND wnd = nullptr;
            const HRESULT hr = view->GetWnd(&wnd);
            view->Release();
            if (SUCCEEDED(hr) && wnd != nullptr && IsWindow(wnd)) return wnd;
        }
    }
    return GetForegroundWindow();
}


// 用合成范围更新候选窗锚点；View 不可用或拿到零矩形时保留旧位置——
// Chromium 系浏览器对刚建立的合成文本还没完成布局，GetTextExt 会返回
// S_OK 但矩形全零（2026-10-29 实机反馈：候选窗因此飞到屏幕左上角），
// 必须按失败处理，等下一次渲染布局就绪后自然拿到真实位置。
void ChaosetTIP::updateCaretFromRange(ITfContext* ctx, TfEditCookie ec, ITfRange* range) {
    ITfContextView* view = nullptr;
    if (FAILED(ctx->GetActiveView(&view)) || view == nullptr) return;
    RECT rc{};
    BOOL fClipped = FALSE;
    if (SUCCEEDED(view->GetTextExt(ec, range, &rc, &fClipped)) &&
        (rc.right > rc.left || rc.bottom > rc.top)) {
        caretPos_.x = rc.left;
        caretPos_.y = rc.bottom;
        caretLineHeight_ = (rc.bottom > rc.top) ? (rc.bottom - rc.top) : 20;
        caretFound_ = true;
        caretFromMouse_ = false;
    }
    view->Release();
}


void ChaosetTIP::applyPreedit(ITfContext* ctx, TfEditCookie ec) {
    g_lastOp = kOpPreedit;
    // 预编辑 = 已确认段（渐进上屏的已选字）+ 剩余拼音：二者同属**同一条
    // 合成**，整条组词结束才落盘（见 confirmedPreedit_ 注释）。壳对文档
    // 只做两类操作——同一合成内 SetText 更新（与逐键输入同原语）与
    // 落盘提交（见 applyCommit），不存在「中途结束再重建合成」的易碎操作。
    const std::string combinedPreedit = confirmedPreedit_ + comp_.preedit;
    // 「预编辑是否真的写进了文档」：字符串为空视作已写（无需合成）；
    // SetText 失败时保持 false，字母分支据此判定该上下文不可用并放行。
    preeditWritten_ = combinedPreedit.empty();
    const std::wstring wide = toWide(combinedPreedit);
    if (composition_ != nullptr) {
        ITfRange* range = nullptr;
        const HRESULT hrRange = composition_->GetRange(&range);
        if (FAILED(hrRange) || range == nullptr) {
            diagTsfFail("ITfComposition::GetRange(preedit)",
                        FAILED(hrRange) ? hrRange : E_POINTER);
            composition_->Release();
            composition_ = nullptr;
            return;
        }
        // 先按上一轮布局查光标再改文本：Firefox(Gecko) 对「本次会话刚
        // 修改过的范围」布局未刷新，SetText 之后再查会失败或拿到旧值；
        // 改文本前合成起点不变、文档布局有效，这一查才是可靠锚点
        // （Chrome 同步布局，两次都能成功，后一次覆盖前一次）。
        updateCaretFromRange(ctx, ec, range);
        const HRESULT hrText =
            range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
        if (FAILED(hrText)) {
            diagTsfFail("ITfRange::SetText(preedit)", hrText);
            // 写不进（只读上下文 / 会话已被终止）：合成已无意义。
            // EndComposition 交还引用，本地所有权一并清空——保留一个
            // 写不进字的合成只会让后续兜底永远认为「合成还在」。
            preeditWritten_ = false;
            composition_->EndComposition(ec);
            composition_->Release();
            composition_ = nullptr;
            updateCaretFromRange(ctx, ec, range);
            range->Release();
            return;
        }
        preeditWritten_ = true;
        updateCaretFromRange(ctx, ec, range);
        range->Release();
        return;
    }
    ITfInsertAtSelection* insert = nullptr;
    HRESULT hr = ctx->QueryInterface(IID_ITfInsertAtSelection, reinterpret_cast<void**>(&insert));
    if (FAILED(hr) || insert == nullptr) {
        diagTsfFail("ITfInsertAtSelection(preedit)", FAILED(hr) ? hr : E_NOINTERFACE);
        return;
    }
    ITfRange* range = nullptr;
    // QUERYONLY：只取插入点位置的范围，不插文本
    hr = insert->InsertTextAtSelection(ec, TF_IAS_QUERYONLY, nullptr, 0, &range);
    if (FAILED(hr) || range == nullptr) {
        diagTsfFail("InsertTextAtSelection(QUERYONLY)", FAILED(hr) ? hr : E_POINTER);
        insert->Release();
        return;
    }
    // 首次建合成前先锚定插入点：此处文档已布局完成（Firefox 对新合成
    // 文本的布局要等会话结束后才刷新，合成后再查会失败），即便首键
    // 也能贴住光标；合成建立后再查一次，成功则覆盖为更精确的值。
    updateCaretFromRange(ctx, ec, range);
    ITfContextComposition* cc = nullptr;
    hr = ctx->QueryInterface(IID_ITfContextComposition, reinterpret_cast<void**>(&cc));
    if (FAILED(hr) || cc == nullptr) {
        diagTsfFail("ITfContextComposition", FAILED(hr) ? hr : E_NOINTERFACE);
    } else {
        ITfComposition* comp = nullptr;
        hr = cc->StartComposition(ec, range, compositionSink(), &comp);
        if (FAILED(hr) || comp == nullptr) {
            diagTsfFail("StartComposition", FAILED(hr) ? hr : E_POINTER);
            if (comp != nullptr) comp->Release();
        } else {
            composition_ = comp;
            const HRESULT hrText =
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
            if (FAILED(hrText)) {
                diagTsfFail("ITfRange::SetText(preedit)", hrText);
                // StartComposition 成功但文本写不进：与上面同理，
                // 必须立刻结束合成并清空所有权，否则字母分支的
                // 「渲染后仍无合成」兜底永远不触发，键照样被吃。
                preeditWritten_ = false;
                composition_->EndComposition(ec);
                composition_->Release();
                composition_ = nullptr;
            } else {
                preeditWritten_ = true;
            }
        }
        cc->Release();
    }
    // 合成建立失败也尝试取一次光标位置：候选窗仍能贴住插入点显示
    updateCaretFromRange(ctx, ec, range);
    range->Release();
    insert->Release();
}


// 上屏；返回是否确实写进了文档（调用方据此决定失败时是否放行原键）
bool ChaosetTIP::commit(ITfContext* pic, const std::string& text) {
    g_lastOp = kOpCommit;
    candidateWnd_.hide();
    ContextRef owned(nullptr);
    ITfContext* ctx = pic;
    if (ctx == nullptr) {
        owned.ctx = focusContext();
        ctx = owned.ctx;
    }
    if (ctx == nullptr) return false;
    committed_ = text;
    commitWritten_ = false;
    EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
        static_cast<ChaosetTIP*>(user)->applyCommit(c, ec);
    });
    const HRESULT hr = es.run(tm_, clientId_);
    es.Release();
    if (FAILED(hr)) diagTsfFail("RequestEditSession(commit)", hr);
    comp_ = chaoset::Comp();
    // 提交即整条组词结束：确认段缓冲必须清空（调用方已先行取出拼接，
    // 这里幂等兜底，防止异常路径把旧段串进下一轮组词的预编辑）。
    confirmedPreedit_.clear();
    return SUCCEEDED(hr) && commitWritten_;
}


void ChaosetTIP::applyCommit(ITfContext* ctx, TfEditCookie ec) {
    g_lastOp = kOpCommit;
    commitWritten_ = false;
    if (committed_.empty()) return;
    const std::wstring wide = toWide(committed_);

    // 先摘成员再用局部指针：EndComposition 之后成员值不再可信，且
    // 「我方结束」与「外部终止」两条路径的引用所有权必须唯一
    // （见 OnCompositionTerminated 的注释）。
    ITfComposition* comp = composition_;
    composition_ = nullptr;
    if (comp != nullptr) {
        ITfRange* range = nullptr;
        if (SUCCEEDED(comp->GetRange(&range)) && range != nullptr) {
            const HRESULT hrText =
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
            if (FAILED(hrText)) diagTsfFail("ITfRange::SetText(commit)", hrText);
            else commitWritten_ = true;
            range->Release();
        }
        const HRESULT hrEnd = comp->EndComposition(ec);
        if (FAILED(hrEnd)) diagTsfFail("ITfComposition::EndComposition(commit)", hrEnd);
        comp->Release();
        releaseCompositionContext();
    } else {
        // 无合成（直接打标点 / 合成被外部终止）：必须自己插入文本。
        // 此前这里什么都不做，标点会凭空消失。
        ITfInsertAtSelection* insert = nullptr;
        HRESULT hr = ctx->QueryInterface(IID_ITfInsertAtSelection,
                                         reinterpret_cast<void**>(&insert));
        if (FAILED(hr) || insert == nullptr) {
            diagTsfFail("ITfInsertAtSelection(commit)", FAILED(hr) ? hr : E_NOINTERFACE);
        } else {
            ITfRange* range = nullptr;
            hr = insert->InsertTextAtSelection(ec, 0, wide.c_str(),
                                               static_cast<LONG>(wide.size()), &range);
            if (FAILED(hr)) {
                diagTsfFail("InsertTextAtSelection(commit)", hr);
            } else {
                commitWritten_ = true;
                if (range != nullptr) range->Release();
            }
            insert->Release();
        }
    }
    committed_.clear();
}


// 缓存引擎的双拼方案名（IPC Composition.shuangpin）。引擎契约：启用
// 双拼时**每次 Compose 都回填**方案名；方案在 daemon 生命周期内固定，
// 壳只需要任意一次响应就能判定 ';' 路由（见 handleKey），因此只在
// 非空时覆盖，空值不清除（兼容未实现该字段的旧 daemon）。
void ChaosetTIP::syncDpScheme(const chaoset::Comp& comp) {
    if (!comp.shuangpin.empty()) dpScheme_ = comp.shuangpin;
}


// 结束组词并重置引擎会话。reset 响应同样是 Composition（也带 shuangpin），
// 这里统一缓存方案名——所有调用点替换 session_.reset() 以免漏掉。
void ChaosetTIP::resetSession() { syncDpScheme(session_.reset()); }


// 只丢弃当前组词、保留学习上下文（prev/history）的收尾。
//
// 用途判定（2026-11-05）：**用户还会继续打字**的场景一律走这里——
// 切中英模式、退格删空、Esc 取消、回车/空格顶屏、宿主拒建合成兜底、
// 传给宿主后清本地状态。这些情况下用户只是「打断了这一轮组词」，
// 上一轮上屏的 prev/bigram 依然有效；用 reset 会让引擎忘记刚打的字，
// 紧接着的候选失去上下文加成（实测：学过「我们→时」后输入 shi，
// 带 prev 首候选是「时」，清掉后变成「是」）。
//
// reset 只保留给**会话真的结束**的场景：失焦（OnSetFocus(FALSE)）、
// TIP 停用（Deactivate）、合成被宿主终止（OnCompositionTerminated）。
void ChaosetTIP::resetComposition() { syncDpScheme(session_.resetComposition()); }


// 透传/故障型收尾（对齐 macOS 壳 passThrough）：已确认段先落盘——用户
// 确认过的字不能随透传（Ctrl+C 等）或 daemon 故障消失；纯拼音维持
// 「清空」旧语义。显式取消（Esc / 全部删除）不走这里，直接 endComposition。
//
// 用 resetComposition（不是 reset）：这条路径的全部调用方（切中英、
// 方向键/功能键透传、daemon 故障降级、翻页失败）都意味着**用户还会
// 继续输入**，不该把刚上屏的 prev/bigram 一起忘掉（2026-11-05）。
void ChaosetTIP::flushOrCancel(ITfContext* pic) {
    resetComposition();
    comp_ = chaoset::Comp();
    if (!confirmedPreedit_.empty()) {
        const std::string all = confirmedPreedit_;
        confirmedPreedit_.clear();
        commit(pic, all);
    } else {
        endComposition();
    }
}


// 取消/清理当前合成：必须经真实编辑会话拿到有效 cookie。旧实现用
// cookie 0 在编辑会话外调用 SetText/EndComposition，TSF 只会返回失败：
// 预编辑文本留在文档里、合成对象也泄漏（2026-10-29 审查）。
void ChaosetTIP::endComposition() {
    candidateWnd_.hide();
    // 取消整条组词：未落盘的已确认段一并丢弃（与 macOS 壳 Esc 语义一致；
    // 幂等兜底——落盘型收尾走 commit() 且已先行取出拼接）。
    confirmedPreedit_.clear();
    ITfComposition* comp = composition_;
    if (comp == nullptr) return;
    g_lastOp = kOpEndComposition;
    composition_ = nullptr;  // 先摘成员：结束路径的引用所有权唯一（见 sink）
    // 必须对合成所属的上下文做（render 时记录），不能用 focusContext()：
    // 焦点切走后现查到的是新文档的上下文，RequestEditSession 会失败或
    // 拿到无效 cookie，拼音文本永久残留（2026-10-29 审查）。
    // 注意所有权：compCtx_ 的引用归 releaseCompositionContext() 释放，
    // 这里绝不能把它包进 owning 的 ContextRef（会双重 Release）。
    ITfContext* target = compCtx_;
    ContextRef owned(nullptr);
    if (target == nullptr) {
        owned.ctx = focusContext();
        target = owned.ctx;
    }
    if (target != nullptr) {
        endTarget_ = comp;
        EditSession es(target, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyEndComposition(c, ec);
        });
        HRESULT hrSession = S_OK;
        const HRESULT hr = target->RequestEditSession(
            clientId_, &es, TF_ES_SYNC | TF_ES_READWRITE, &hrSession);
        if (FAILED(hr)) {
            diagTsfFail("RequestEditSession(endComposition)", hr);
        } else if (FAILED(hrSession)) {
            // TF_ES_SYNC 在后台线程等场景会失败；保留最后一次成功的
            // cookie 收尾——比换 cookie 0 强（0 必失败，旧值多半仍有效）。
            diagTsfFail("RequestEditSession(endComposition).session", hrSession);
            if (compEditCookie_ > 0) applyEndComposition(nullptr, compEditCookie_);
        }
        es.Release();
        endTarget_ = nullptr;
    }
    comp->Release();
    releaseCompositionContext();
}


void ChaosetTIP::applyEndComposition(ITfContext* /*ctx*/, TfEditCookie ec) {
    ITfComposition* comp = endTarget_;
    if (comp == nullptr) return;
    if (ec > 0) compEditCookie_ = ec;  // 记录成功 cookie（endComposition 兜底用）
    ITfRange* range = nullptr;
    if (SUCCEEDED(comp->GetRange(&range)) && range != nullptr) {
        const HRESULT hrText = range->SetText(ec, 0, L"", 0);
        if (FAILED(hrText)) diagTsfFail("ITfRange::SetText(clear)", hrText);
        range->Release();
    }
    const HRESULT hrEnd = comp->EndComposition(ec);
    if (FAILED(hrEnd)) diagTsfFail("ITfComposition::EndComposition(clear)", hrEnd);
}


// 合成结束（commit / endComposition / 外部终止）后调用：清空合成所属
// 上下文记录。composition_ 为空时 compCtx_ 必须为空，避免持有过期引用。
void ChaosetTIP::releaseCompositionContext() {
    if (compCtx_ != nullptr) {
        compCtx_->Release();
        compCtx_ = nullptr;
    }
    composeHostWnd_ = nullptr;
    preeditWritten_ = false;
}


// 撇号分隔的音节串 → 音节数组（"xian'zai" → ["xian","zai"]）。
std::vector<std::string> ChaosetTIP::splitSyllables(const std::string& syllables) {
    std::vector<std::string> out;
    std::size_t start = 0;
    while (start <= syllables.size()) {
        std::size_t pos = syllables.find('\'', start);
        if (pos == std::string::npos) {
            if (start < syllables.size()) out.push_back(syllables.substr(start));
            break;
        }
        if (pos > start) out.push_back(syllables.substr(start, pos - start));
        start = pos + 1;
    }
    return out;
}
