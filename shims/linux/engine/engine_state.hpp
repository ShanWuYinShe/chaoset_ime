// Linux ibus 引擎状态机的声明与公共小工具（异步管线版，issue #149 三端统一）。
//
// main.cpp 只做入口/按键分发/生命周期（含预编辑与候选呈现）；
// async_pipeline.cpp 放乐观组词态、操作入队、结果按 seq 应用、失败宽限与整串重建。
// 工作线程（EnginePump）绝不触碰 ibus：结果只在主循环的 g_idle 回调里应用。
#pragma once

#include <ibus.h>
#include <glib.h>

#include <atomic>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <deque>
#include <memory>
#include <string>
#include <utility>
#include <vector>

#include "core.hpp"
#include "daemon.hpp"
#include "engine_pump.hpp"

namespace ibus_shell {

using chaoset::EngineOp;
using chaoset::EngineOpKind;
using chaoset::EnginePump;
using chaoset::EngineResult;


struct ShiftTracker {
    int shiftDown = 0;
    bool sinceShift = false;
    void reset() { shiftDown = 0; sinceShift = false; }
    bool feed(guint keyval, guint state);
};

struct EngineOptions {
    std::vector<std::string> dicts;
    std::string importedDict;
    std::string userdict;
    std::string socket;
    std::string config;
    std::string daemonPath;
    std::string log;
    bool fuzzy = false;
    bool traditional = false;
    std::string shuangpin;
    bool associate = true;
    bool associateFlagSet = false;
};

struct UserConfig {
    bool fuzzy = false;
    bool traditional = false;
    std::string shuangpin;
    bool associate = true;
};

extern EngineOptions gOptions;

void logLine(const std::string& text);
bool readUserConfig(const std::string& path, UserConfig* out);
std::string executableDir();
bool knownDpScheme(const std::string& scheme);
std::vector<std::string> splitSyllables(const std::string& s);
chaoset::Daemon::Options makeDaemonOptions(EngineOptions options);

std::int64_t monotonicMs();
void backspaceLocally(chaoset::Comp& comp, std::string& pending, bool shuangpin, bool bySyllable);

struct ResultWake;
void scheduleResultsIdle(const std::shared_ptr<ResultWake>& wake);

class IbusEngineState {
public:
    explicit IbusEngineState(EngineOptions options, IBusEngine* engine);
    ~IbusEngineState();
    // 结果只能在主循环线程应用：工作线程绝不触碰 ibus API。
    void drainResults();
    bool processKeyEvent(guint keyval, guint keycode, guint state);
    void focusIn();
    void focusOut();
    void reset();
    void pageUp();
    void pageDown();
    void candidateClicked(guint index);

private:
    void ensurePump();
    bool engineAvailable();
    // 失联时按节流投一个探测请求（无会话副作用）；恢复后下一键即可正常组词。
    void probeIfDown();
    std::uint64_t submit(EngineOp op);
    void submitInput(const std::string& text);
    void submitPunct(const std::string& key);
    void submitPage(bool next);
    // 纪元递增：一切在飞结果作废；清空乐观项与引擎线程积压，再投一次复位。
    void resetPipeline(bool newSession);
    // 会话收尾入口（异步：纪元递增 + 清空壳侧乐观项 + 引擎复位）。名字与旧同步
    // 实现一致，调用方语义不变：sessionReset 清组词与学习上下文，resetComposition
    // 只丢当前组词、保留 prev/history。
    void sessionReset();
    void resetComposition();
    void sessionResetComposition();
    // 按音节回退（Ctrl+Backspace）：入队 + 乐观渲染，绝不等待 daemon。
    void backspaceWord();
    chaoset::Comp optimisticComp() const;
    std::string optimisticInput() const;
    // 只更新显示，不改 last_（last_ 是已应用状态，候选表保持最后一次已应用候选）。
    void display(const chaoset::Comp& disp);
    void refresh(chaoset::Comp comp, int cursor = 0);
    void renderOptimistic();
    // 上屏后若用户已经继续输入，立即用乐观预编辑重建新合成。
    void renderPendingAfterCommit();
    void applyResult(const EngineResult& r);
    void markEngineLost();
    void scheduleRebuild();
    // 宽限期到（daemon 一直不可用）：本地内容按原始串落盘，一个键都不丢。
    void giveUpEngineState();
    // 显式上屏/标点失败时已在本地写盘，引擎状态不可信：按本地剩余输入整串重建。
    void restartFromLocalState();
    void updatePreedit(const std::string& text, bool visible);
    void hidePreedit();
    void updateLookupTable(const chaoset::Comp& comp);
    void showLookup(const std::vector<chaoset::Candidate>& cands, int page, int pageCount, int cursor);
    void hideLookupTable();
    void commit(const std::string& text);
    void flushConfirmedAndReset();
    bool associateEnabled();
    // 联想查询只入队；结果回来（kAssociate）再展示候选。
    void enterAssociate(const std::vector<std::string>& sylls);
    void enterAssociateForPrev(const std::string& syllsOrWord);
    void showAssociateTable();
    bool flipAssociate(bool next);
    // 方向键：上/下翻页、左/右页内预选移动；页边界保持不动（仍消费）。
    void moveLookup(int delta);
    // 组词态翻页：上=上一页、下=下一页；边界页保持不动（仍消费按键）。
    void flipPage(bool next);
    void exitAssociate();
    bool selectCandidate(int idx);
    void showToast(const std::string& text);
    bool isComposing() const;
    IBusEngine* engine_ = nullptr;
    EngineOptions options_;
    std::string configPath_;
    std::unique_ptr<EnginePump> pump_;
    std::shared_ptr<ResultWake> wake_;
    std::deque<EngineOp> pendingOps_;
    std::uint64_t seq_ = 0;
    std::uint32_t epoch_ = 0;
    bool engineLost_ = false;
    std::int64_t engineLostMs_ = 0;
    std::int64_t lastRebuildMs_ = 0;
    std::int64_t lastProbeMs_ = 0;
    bool enMode_ = false;
    bool halfWidth_ = false;
    bool assocOn_ = true;
    bool assocFlagSet_ = false;
    std::string assocConfigErr_;
    ShiftTracker shift_;
    std::string dpScheme_;
    std::vector<chaoset::Candidate> assoc_;
    int assocPage_ = 0;
    int lookupIndex_ = 0;  // 页内预选序号（lookup cursor，0 起）
    std::string confirmed_;
    std::vector<std::string> selectedSylls_;
    chaoset::Comp last_;
    guint toastId_ = 0;
    guint64 toastSeq_ = 0;
};

// 工作线程只置 scheduled 并投一个 idle 源；主循环取结果。state 在析构时置空，
// 迟到 idle 只会看到 nullptr，绝不访问已销毁对象。
struct ResultWake {
    std::atomic<IbusEngineState*> state{nullptr};
    std::atomic<bool> scheduled{false};
};

}  // namespace ibus_shell
