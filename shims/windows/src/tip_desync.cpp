// 异步引擎管线（issue #149）的本地回退原语：不依赖引擎的乐观编辑。
//
// 旧版本（issue #122）在这里实现了「超时后保留组词 + 重连整串重建」的失步
// 状态机——那是同步 IPC 截止时代的补救。异步管线把结果回投与乐观显示统一到
// tip_async.cpp：按键只入队并本地回放，结果按序应用；daemon 不可用时保留本地
// 组词、2s 宽限后按原始串落盘。本文件只保留与引擎无关的本地编辑函数
// （乐观预编辑回放复用，见 optimisticComp）。

#include "tip.hpp"

using namespace chaoset::win;

namespace chaoset::win {

// 本地回退一格（不依赖引擎）。pending 非空时退最近一键；否则退 comp.input 的
// 末尾：全拼逐字符（bySyllable 时按预编辑末段整段），双拼恒按末段（已解码
// 音节）。comp.preedit 同步裁掉对应部分，仅用于乐观显示。
void backspaceLocally(chaoset::Comp& comp, std::string& pending, bool shuangpin,
                      bool bySyllable) {
    if (!pending.empty()) {
        pending.pop_back();
        return;
    }
    if (comp.input.empty()) return;
    if (!shuangpin && !bySyllable) {
        const bool removedSeparator = comp.input.back() == '\'';
        comp.input.pop_back();
        if (comp.preedit.empty()) return;
        comp.preedit.pop_back();
        if (comp.input.empty()) {
            comp.preedit.clear();
            return;
        }
        // 末段被删空：连同引擎补的分隔符一起去掉（用户自己敲的分隔符保留）。
        if (!removedSeparator && comp.preedit.size() >= 2 && comp.preedit.back() == '\'' &&
            comp.input.back() != '\'') {
            comp.preedit.pop_back();
        }
        return;
    }
    const std::string::size_type sep = comp.preedit.rfind('\'');
    const std::string seg =
        sep == std::string::npos ? comp.preedit : comp.preedit.substr(sep + 1);
    if (seg.empty()) return;
    if (comp.input.size() >= seg.size()) comp.input.resize(comp.input.size() - seg.size());
    comp.preedit = sep == std::string::npos ? std::string() : comp.preedit.substr(0, sep);
}

}  // namespace chaoset::win
