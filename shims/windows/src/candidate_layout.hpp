// 候选窗多行折行布局（纯数学、无 Win32 依赖）。
//
// 与 macOS CandidatePanel.layoutRows 同算法（2026-10-31 三端一致性收尾）：
// 按内容宽度 maxContentW 折行，最多 maxRows 行；第 maxRows 行之后的候选不参与
// 布局（与 mac 一致，数字键仍可能选中——文档记录为已知边角）。放在头文件里是
// 为了让 win32.cpp 与 tests/core_test.cpp 共用，并能在 macOS 上单测布局数学。
#pragma once

#include <vector>

namespace chaoset::layout {

struct Box {
    int padX = 0;         // 左右内边距（首项 firstPadLeft，其余 itemPadX）
    int chipW = 0;        // 序号键帽宽
    int chipTextGap = 0;  // 键帽与文本间距
    int textW = 0;        // 候选文本宽（首项超宽时已是截断后宽度）
    int markW = 0;        // 学习标记宽（0 = 无）
};

struct Rect {
    int x = 0;
    int y = 0;
    int w = 0;
    int h = 0;
};

struct Row {
    std::vector<Rect> items;  // x 相对内容区左缘；y/h 由调用方填
    int width = 0;            // 含项间距的行宽
};

struct Result {
    std::vector<Row> rows;
};

inline int boxWidth(const Box& b) {
    return b.padX * 2 + b.chipW + b.chipTextGap + b.textW + b.markW;
}

inline Result wrapRows(const std::vector<Box>& boxes, int maxContentW, int gap,
                       int itemH, int maxRows) {
    Result out;
    Row row;
    int rowW = 0;
    for (const Box& b : boxes) {
        int w = boxWidth(b);
        if (w > maxContentW) w = maxContentW;  // 防御：单条绝不超上限
        int add = (row.items.empty() ? 0 : gap) + w;
        if (!row.items.empty() && rowW + add > maxContentW) {
            row.width = rowW;
            out.rows.push_back(row);
            row = Row{};
            rowW = 0;
            if (static_cast<int>(out.rows.size()) >= maxRows) break;
            add = w;
        }
        Rect r;
        r.x = rowW;
        r.w = w;
        r.h = itemH;
        row.items.push_back(r);
        rowW += add;
    }
    if (!row.items.empty() && static_cast<int>(out.rows.size()) < maxRows) {
        row.width = rowW;
        out.rows.push_back(row);
    }
    return out;
}

}  // namespace chaoset::layout