// ChaosetIME TSF 壳（Windows 专用胶水层）。
//
// 职责：COM TIP 注册与激活、按键拦截（ITfKeyEventSink + ITfKeystrokeMgr）、
// 预编辑合成（ITfContextComposition）、候选上屏；引擎逻辑全部经
// core::Session 走 TCP 回环（127.0.0.1，默认 7949）与 chaosetd 通信。
// daemon 不可达时所有按键原样透传（降级策略，见 README）。
//
// 验证等级：mingw-w64 编译级 + 核心层行为级（macOS 上对真实 daemon，
// 见 tests/core_test.cpp）。COM 注册表细节与 TSF 运行时行为需在
// Windows 真机上完成联调（已知限制，README 有说明）。
//
// 构建见 build.sh（x86_64-w64-mingw32-g++）。

#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <msctf.h>
#include <objbase.h>


#include <string>

#include "core.hpp"

namespace {

// 本壳 TIP 的 COM 类标识（固定值；注册表以它为键）
const GUID CLSID_ChaosetTIP = {
    0x8a5c7b31, 0x4e2d, 0x4c6a, {0x9f, 0x41, 0x77, 0xd0, 0xb3, 0xe5, 0xc1, 0xa8}};

constexpr char kDefaultTcpAddr[] = "127.0.0.1:7949";
constexpr DWORD kVkBackspace = 0x08;
constexpr DWORD kVkEscape = 0x1B;
constexpr DWORD kVkReturn = 0x0D;
constexpr DWORD kVkSpace = 0x20;
constexpr DWORD kVkFirstLetter = 0x41;  // 'A'
constexpr DWORD kVkLastLetter = 0x5A;   // 'Z'
constexpr DWORD kVkFirstDigit = 0x31;   // '1'
constexpr DWORD kVkLastDigit = 0x39;    // '9'
// OEM 标点虚键（US 键盘布局假设；真机联调时按实际布局核验）
constexpr DWORD kVkOemSemicolon = 0xBA;      // ';'
constexpr DWORD kVkOemComma = 0xBC;          // ','
constexpr DWORD kVkOemPeriod = 0xBE;         // '.'
constexpr DWORD kVkOemOpenBrackets = 0xDB;   // '['
constexpr DWORD kVkOemBackslash = 0xDC;      // '\\'
constexpr DWORD kVkOemCloseBrackets = 0xDD;  // ']'
constexpr DWORD kVkOemQuotes = 0xDE;         // '\''

 volatile LONG g_liveObjects = 0;

std::string envOr(const char* name, const std::string& fallback) {
    char buf[256] = {0};
    DWORD n = GetEnvironmentVariableA(name, buf, sizeof(buf));
    if (n > 0 && n < sizeof(buf)) return std::string(buf);
    return fallback;
}

std::wstring toWide(const std::string& utf8) {
    int n = MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), static_cast<int>(utf8.size()), nullptr, 0);
    std::wstring w;
    if (n > 0) {
        w.resize(static_cast<std::size_t>(n));
        MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), static_cast<int>(utf8.size()), &w[0], n);
    }
    return w;
}

// ---- 传输：TCP 回环（与 daemon -tcp 127.0.0.1:7949 配对） ----

class TcpTransport : public chaoset::Transport {
public:
    TcpTransport() {
        WSADATA data{};
        WSAStartup(MAKEWORD(2, 2), &data);
    }
    ~TcpTransport() override {
        closeConn();
        WSACleanup();
    }

    bool writeAll(const std::uint8_t* data, std::size_t len) override {
        if (!ensureConnected()) return false;
        std::size_t off = 0;
        while (off < len) {
            int n = ::send(sock_, reinterpret_cast<const char*>(data + off),
                           static_cast<int>(len - off), 0);
            if (n <= 0) {
                closeConn();
                return false;
            }
            off += static_cast<std::size_t>(n);
        }
        return true;
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
        std::size_t off = 0;
        while (off < len) {
            int n = ::recv(sock_, reinterpret_cast<char*>(buf + off),
                           static_cast<int>(len - off), 0);
            if (n <= 0) {
                closeConn();
                return false;
            }
            off += static_cast<std::size_t>(n);
        }
        return true;
    }

    void closeConn() override {
        if (sock_ != INVALID_SOCKET) {
            ::closesocket(sock_);
            sock_ = INVALID_SOCKET;
        }
        lastFail_ = 0;  // 主动断开（非连接失败）不进入冷却
    }

private:
    bool ensureConnected() {
        if (sock_ != INVALID_SOCKET) return true;
        DWORD now = GetTickCount();
        if (now - lastFail_ < 1000) return false;  // 失败冷却 1 秒
        addrinfoW hints{};
        hints.ai_family = AF_INET;
        hints.ai_socktype = SOCK_STREAM;
        addrinfoW* res = nullptr;
        if (GetAddrInfoW(hostWide_.c_str(), portWide_.c_str(), &hints, &res) != 0) {
            lastFail_ = now;
            return false;
        }
        SOCKET s = INVALID_SOCKET;
        for (addrinfoW* p = res; p != nullptr; p = p->ai_next) {
            s = ::socket(p->ai_family, p->ai_socktype, p->ai_protocol);
            if (s == INVALID_SOCKET) continue;
            if (::connect(s, p->ai_addr, static_cast<int>(p->ai_addrlen)) == 0) break;
            ::closesocket(s);
            s = INVALID_SOCKET;
        }
        FreeAddrInfoW(res);
        if (s == INVALID_SOCKET) {
            lastFail_ = now;
            return false;
        }
        sock_ = s;
        return true;
    }

    SOCKET sock_ = INVALID_SOCKET;
    DWORD lastFail_ = 0;
    std::string addr_ = envOr("CHAOSET_TCP", kDefaultTcpAddr);
    std::wstring hostWide_ = toWide(addr_.substr(0, addr_.rfind(':')));
    std::wstring portWide_ = toWide(addr_.substr(addr_.rfind(':') + 1));
};

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

// ---- TIP 主体 ----

class ChaosetTIP : public ITfTextInputProcessor, public ITfKeyEventSink, public ITfCompositionSink {
public:
    ChaosetTIP() { InterlockedIncrement(&g_liveObjects); }
    virtual ~ChaosetTIP() {
        if (tm_ != nullptr) tm_->Release();
        InterlockedDecrement(&g_liveObjects);
    }

    // IUnknown
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown) {
            *ppv = static_cast<IUnknown*>(static_cast<ITfTextInputProcessor*>(this));
        } else if (riid == IID_ITfTextInputProcessor) {
            *ppv = static_cast<ITfTextInputProcessor*>(this);
        } else if (riid == IID_ITfKeyEventSink) {
            *ppv = static_cast<ITfKeyEventSink*>(this);
        } else if (riid == IID_ITfCompositionSink) {
            *ppv = static_cast<ITfCompositionSink*>(this);
        } else {
            *ppv = nullptr;
            return E_NOINTERFACE;
        }
        AddRef();
        return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return InterlockedIncrement(&ref_); }
    ULONG STDMETHODCALLTYPE Release() override {
        ULONG r = InterlockedDecrement(&ref_);
        if (r == 0) delete this;
        return r;
    }

    // ITfTextInputProcessor
    HRESULT STDMETHODCALLTYPE Activate(ITfThreadMgr* ptim, TfClientId tid) override {
        tm_ = ptim;
        if (tm_ != nullptr) tm_->AddRef();
        clientId_ = tid;

        ITfKeystrokeMgr* mgr = nullptr;
        if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
            keySinkAdvised_ = SUCCEEDED(mgr->AdviseKeyEventSink(clientId_, keySink(), TRUE));
            mgr->Release();
        }
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE Deactivate() override {
        endComposition();
        session_.reset();
        comp_ = chaoset::Comp();
        if (keySinkAdvised_ && tm_ != nullptr) {
            ITfKeystrokeMgr* mgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
                mgr->UnadviseKeyEventSink(clientId_);
                mgr->Release();
            }
            keySinkAdvised_ = false;
        }
        if (tm_ != nullptr) {
            tm_->Release();
            tm_ = nullptr;
        }
        return S_OK;
    }

    // ITfKeyEventSink
    HRESULT STDMETHODCALLTYPE OnSetFocus(BOOL /*fForeground*/) override { return S_OK; }

    HRESULT STDMETHODCALLTYPE OnTestKeyDown(ITfContext* /*pic*/, WPARAM wParam, LPARAM /*lParam*/,
                                            BOOL* pfEaten) override {
        *pfEaten = wouldHandle(wParam) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                        BOOL* pfEaten) override {
        *pfEaten = handleKey(wParam, pic) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyUp(ITfContext* /*pic*/, WPARAM /*wParam*/, LPARAM /*lParam*/,
                                          BOOL* pfEaten) override {
        *pfEaten = FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyUp(ITfContext* /*pic*/, WPARAM /*wParam*/, LPARAM /*lParam*/,
                                      BOOL* pfEaten) override {
        *pfEaten = FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnPreservedKey(ITfContext* /*pic*/, REFGUID /*rguid*/, BOOL* pfEaten) override {
        *pfEaten = FALSE;
        return S_OK;
    }

    // ITfCompositionSink：合成被外部终止（焦点切换等）
    HRESULT STDMETHODCALLTYPE OnCompositionTerminated(TfEditCookie /*ec*/, ITfComposition* /*pComposition*/) override {
        composition_ = nullptr;
        session_.reset();
        comp_ = chaoset::Comp();
        return S_OK;
    }

private:
    ITfKeyEventSink* keySink() { return static_cast<ITfKeyEventSink*>(this); }
    ITfCompositionSink* compositionSink() { return static_cast<ITfCompositionSink*>(this); }

    bool isComposing() const { return !comp_.input.empty(); }
    bool hasCandidates() const { return !comp_.candidates.empty(); }

    // OEM 虚键 → 标点字符（与 engine.IsPunctKey 的直映子集对应；
    // shift+键产生的 ？!: 等需字符级识别，待真机联调时接 ToAscii）
    static const char* oemPunctChar(WPARAM vk) {
        switch (vk) {
            case kVkOemComma: return ",";
            case kVkOemPeriod: return ".";
            case kVkOemSemicolon: return ";";
            case kVkOemOpenBrackets: return "[";
            case kVkOemBackslash: return "\\";
            case kVkOemCloseBrackets: return "]";
            case kVkOemQuotes: return "'";
        }
        return nullptr;
    }

    // 是否会吃掉该键：字母与标点键任意时刻都吃（标点由 daemon 映射，
    // daemon 不可达时在 handleKey 里放行）；组词中再吃数字/空格/回车/
    // 退格/Esc。
    bool wouldHandle(WPARAM vk) {
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return true;
        if (oemPunctChar(vk) != nullptr) return true;
        if (!isComposing()) return false;
        return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
               (vk >= kVkFirstDigit && vk <= kVkLastDigit);
    }

    bool handleKey(WPARAM vk, ITfContext* pic) {
        // 字母：追加组词
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
            comp_ = session_.input(std::string(1, static_cast<char>('a' + (vk - kVkFirstLetter))));
            if (!comp_.ok) {
                // daemon 不可达：清状态放行，绝不让宿主应用卡住
                endComposition();
                return false;
            }
            render(pic);
            return true;
        }

        // 标点键（US 布局 OEM 映射）：组词中先上屏首选再出中文标点，
        // 空组词直接出中文标点；映射规则在 daemon 侧统一完成。
        if (const char* key = oemPunctChar(vk)) {
            if (vk == kVkOemQuotes && isComposing()) {
                // 组词中的撇号是音节分隔符，走组词流程
                comp_ = session_.input("'");
                if (!comp_.ok) {
                    endComposition();
                    return false;
                }
                render(pic);
                return true;
            }
            const bool composing = isComposing();
            std::string committed;
            if (session_.punct(key, &committed)) {
                commit(pic, committed);
                return true;
            }
            // daemon 不可达：清状态放行原键，不卡宿主应用
            if (composing) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
            return false;
        }

        if (!isComposing()) return false;

        if (vk == kVkBackspace) {
            comp_ = session_.backspace();
            if (!comp_.ok) {
                endComposition();
                return true;
            }
            if (comp_.input.empty()) endComposition();
            else render(pic);
            return true;
        }

        if (vk == kVkEscape) {
            session_.reset();
            endComposition();
            comp_ = chaoset::Comp();
            return true;
        }

        if (vk >= kVkFirstDigit && vk <= kVkLastDigit && hasCandidates()) {
            int index = static_cast<int>(vk - kVkFirstDigit);
            std::string committed;
            if (session_.select(index, &committed)) {
                commit(pic, committed);
            }
            return true;
        }

        if ((vk == kVkSpace || vk == kVkReturn) && hasCandidates()) {
            std::string committed;
            if (session_.select(0, &committed)) {
                commit(pic, committed);
            }
            return true;
        }

        return false;
    }

    ITfContext* focusContext() {
        if (tm_ == nullptr) return nullptr;
        ITfDocumentMgr* doc = nullptr;
        if (FAILED(tm_->GetFocus(&doc)) || doc == nullptr) return nullptr;
        ITfContext* ctx = nullptr;
        doc->GetBase(&ctx);
        doc->Release();
        return ctx;
    }

    // 把引擎候选状态渲染为预编辑合成（无合成则从插入点启动）
    void render(ITfContext* pic) {
        ITfContext* ctx = pic != nullptr ? pic : focusContext();
        if (ctx == nullptr) return;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyPreedit(c, ec);
        });
        es.run(tm_, clientId_);
        es.Release();
    }

    void applyPreedit(ITfContext* ctx, TfEditCookie ec) {
        std::wstring wide = toWide(comp_.preedit);
        if (composition_ != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(composition_->GetRange(&range)) && range != nullptr) {
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                range->Release();
            }
            return;
        }
        ITfInsertAtSelection* insert = nullptr;
        if (FAILED(ctx->QueryInterface(IID_ITfInsertAtSelection, reinterpret_cast<void**>(&insert)))) return;
        ITfRange* range = nullptr;
        // QUERYONLY：只取插入点位置的范围，不插文本
        if (SUCCEEDED(insert->InsertTextAtSelection(ec, TF_IAS_QUERYONLY, nullptr, 0, &range)) &&
            range != nullptr) {
            ITfContextComposition* cc = nullptr;
            if (SUCCEEDED(ctx->QueryInterface(IID_ITfContextComposition, reinterpret_cast<void**>(&cc)))) {
                if (SUCCEEDED(cc->StartComposition(ec, range, compositionSink(), &composition_))) {
                    if (composition_ != nullptr) {
                        range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                    }
                }
                cc->Release();
            }
            range->Release();
        }
        insert->Release();
    }

    void commit(ITfContext* pic, const std::string& text) {
        ITfContext* ctx = pic != nullptr ? pic : focusContext();
        if (ctx == nullptr) return;
        committed_ = text;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyCommit(c, ec);
        });
        es.run(tm_, clientId_);
        es.Release();
        comp_ = chaoset::Comp();
    }

    void applyCommit(ITfContext* /*ctx*/, TfEditCookie ec) {
        std::wstring wide = toWide(committed_);
        if (composition_ != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(composition_->GetRange(&range)) && range != nullptr) {
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                range->Release();
            }
            composition_->EndComposition(ec);
            composition_->Release();
            composition_ = nullptr;
        }
        committed_.clear();
    }

    void endComposition() {
        if (composition_ == nullptr) return;
        ITfContext* ctx = focusContext();
        if (ctx != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(composition_->GetRange(&range)) && range != nullptr) {
                range->SetText(ecScratch(ctx), 0, L"", 0);
                range->Release();
            }
            composition_->EndComposition(ecScratch(ctx));
        }
        composition_->Release();
        composition_ = nullptr;
    }

    // endComposition 无 pic 可用时的占位 cookie：
    // 清空预编辑属于残留清理，失败也不影响宿主（合成由 TSF 回收）。
    static TfEditCookie ecScratch(ITfContext* /*ctx*/) { return 0; }

    LONG ref_ = 1;
    ITfThreadMgr* tm_ = nullptr;
    TfClientId clientId_ = 0;
    bool keySinkAdvised_ = false;

    TcpTransport transport_;
    chaoset::Session session_{transport_};
    chaoset::Comp comp_;
    ITfComposition* composition_ = nullptr;
    std::string committed_;
};

// ---- 类厂 ----

class ChaosetFactory : public IClassFactory {
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_IClassFactory) {
            *ppv = static_cast<IClassFactory*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return 2; }
    ULONG STDMETHODCALLTYPE Release() override { return 1; }
    HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown* outer, REFIID riid, void** ppv) override {
        if (outer != nullptr) return CLASS_E_NOAGGREGATION;
        ChaosetTIP* tip = new ChaosetTIP();
        HRESULT hr = tip->QueryInterface(riid, ppv);
        tip->Release();
        return hr;
    }
    HRESULT STDMETHODCALLTYPE LockServer(BOOL /*fLock*/) override { return S_OK; }
};

}  // namespace

// ---- 导出 ----

BOOL WINAPI DllMain(HINSTANCE, DWORD, LPVOID) { return TRUE; }

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (!IsEqualCLSID(rclsid, CLSID_ChaosetTIP)) return CLASS_E_CLASSNOTAVAILABLE;
    static ChaosetFactory factory;  // 无状态，静态实例即可
    return factory.QueryInterface(riid, ppv);
}

STDAPI DllCanUnloadNow() {
    return g_liveObjects == 0 ? S_OK : S_FALSE;  // 常驻：S_FALSE 最安全
}

static std::string dllPath() {
    char path[MAX_PATH] = {0};
    GetModuleFileNameA(nullptr, path, MAX_PATH);
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

static std::string guidToString(const GUID& g) {
    wchar_t wide[64] = {0};
    StringFromGUID2(g, wide, 64);
    char narrow[64] = {0};
    WideCharToMultiByte(CP_ACP, 0, wide, -1, narrow, sizeof(narrow), nullptr, nullptr);
    return narrow;
}

STDAPI DllRegisterServer() {
    std::string clsid = guidToString(CLSID_ChaosetTIP);
    std::string base = "Software\\Classes\\CLSID\\" + clsid;
    HRESULT hr = regSet(HKEY_LOCAL_MACHINE, base, "", "ChaosetIME");
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, base + "\\InprocServer32", "", dllPath());
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, base + "\\InprocServer32", "ThreadingModel", "Apartment");

    // CTF 输入法注册；分类暂只登记键盘类（GUID_TFCAT_CATEGORY_OF_TIP 的
    // 值不在 mingw uuid 库中，注册完备性留待 Windows 真机验证）
    std::string tips = "Software\\Microsoft\\CTF\\TIPs\\" + clsid;
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, tips, "", "ChaosetIME");
    if (SUCCEEDED(hr)) {
        hr = regSet(HKEY_LOCAL_MACHINE, tips + "\\Category\\" + guidToString(GUID_TFCAT_TIP_KEYBOARD),
                    "", "");
    }
    return hr;
}

STDAPI DllUninstallServer() {
    std::string clsid = guidToString(CLSID_ChaosetTIP);
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, ("Software\\Microsoft\\CTF\\TIPs\\" + clsid).c_str());
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, ("Software\\Classes\\CLSID\\" + clsid).c_str());
    return S_OK;
}
