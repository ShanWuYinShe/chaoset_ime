# dp_data：简繁转换数据

本目录数据来自 [OpenCC](https://github.com/BYVoid/OpenCC)
（`data/dictionary/STCharacters.txt` 与 `STPhrases.txt`，简→繁方向），
经 `go:embed` 内嵌供 `engine.ToTraditional` 使用。

- 许可：Apache License 2.0（遵循上游 LICENSE）
- 来源：https://github.com/BYVoid/OpenCC/tree/master/data/dictionary
- 取用日期：2026-09-24
- 格式：`简体\t繁体`（繁体多候选项以空格分隔，取首个为 OpenCC 推荐）

更新数据时直接替换两个 txt 并 rerun `go test ./engine/`。
