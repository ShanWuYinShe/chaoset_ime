# 内置词库数据许可与署名（data/dict_common.tsv）

本项目**源代码**（含 `cmd/chaoset-dictgen` 等）以 **MIT** 许可发布，见仓库根 `LICENSE`。

由 `cmd/chaoset-dictgen` 生成的**内置词库** `data/dict_common.tsv` 是数据文件：
当前默认方案（`-freq wf`）下的词库数据按 **CC BY-SA 4.0** 分发，须保留以下署名；
修改后重新分发时，数据部分须继续以 CC BY-SA 4.0 提供。

## 数据来源与许可

| 来源 | 用途 | 许可 | 署名/说明 |
|---|---|---|---|
| wordfreq（Robyn Speer / Luminoso）<https://github.com/rspeer/wordfreq> | 词频（方案 A 主源） | 代码 Apache-2.0；**数据 CC BY-SA 4.0** | 署名 wordfreq / Luminoso |
| wordfreq 的语料来源（FrequencyWords / OpenSubtitles 等公开字幕/文本语料） | wordfreq 中文词频的底层语料 | 内容 CC BY-SA 4.0 | 随 wordfreq 一并署名 |
| THUOCL（清华大学自然语言处理与社会人文计算实验室）<https://github.com/THUNLP/THUOCL> | 领域词频兜底 | MIT | 署名 THUNLP |
| phrase-pinyin-data <https://github.com/mozillazg/phrase-pinyin-data> | 词语拼音 | MIT | 上游由汉典 zdic、CC-CEDICT（CC BY-SA 4.0）等合并，再分发保留说明 |
| pinyin-data <https://github.com/mozillazg/pinyin-data> | 单字读音 | MIT | 上游主要来自 Unicode Unihan（Unicode License） |
| 通用规范汉字表 | 单字字表（8105） | 国家标准文本 | — |
| HSK 3.0 词表 <https://github.com/elkmovie/hsk30> | 常用词门槛 | MIT | Pleco Inc. |
| 《现代汉语常用词表》 | 常用词门槛 | 正式发布前需确认授权来源 | 本仓库不内置原始数据文件 |
| HanLP <https://github.com/hankcs/HanLP> | 方案 B 词频 / 高频词校验 / 补拼音 | Apache-2.0 | 产品说明保留 HanLP 链接与协议 |
| Jun Da 现代汉语字频 <https://github.com/ruddfawcett/hanziDB.csv> | 单字词频排名 | **原作者声明仅限研究/教学；商业使用需书面许可** | 已知风险：发布前应替换为 Unihan kHanyuPinlu/kFrequency 或取得授权 |

## 方案 B（`-freq hanlp`）

词库数据来源为 HanLP CoreNatureDictionary（Apache-2.0）+ THUOCL（MIT），
不包含 wordfreq 数据；Jun Da 单字字频风险与方案 A 相同。

## 说明

- 上游原始数据文件不随仓库分发，生成时按 README「内置词库与再生成」自行下载；
- 词库文件头注释同步记录了来源、许可与再生成命令，以生成产物为准。

