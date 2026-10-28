package main

// 用户配置：从 XDG 配置目录读取 JSON，与命令行 flag 合并。
//
// 为什么需要（2026-10-26 审查发现）：引擎支持模糊音/繁体/双拼三项能力
// （-fuzzy / -traditional / -shuangpin），但 ibus 按 component XML 拉起
// 引擎时只传 --ibus，这三个 flag **在发行包里永远不可达**——等于 Linux 端
// 这三项能力不存在，而 README 的阶段 10/12/13 在 Linux 上成了空头承诺。
//
// 这里采用「配置文件优先、flag 覆盖」的合并策略：
//   - 默认读 $XDG_CONFIG_HOME/chaoset/config.json（缺省 ~/.config/chaoset/）；
//   - 文件里能配的项：fuzzy / traditional / shuangpin（词库路径暂不可配，
//     走 --dict 与默认词库；曾在此宣称可配 dict/userdict 但结构体并无该
//     字段，用户照写静默无效果——2026-10-27 审查修正注释）；
//   - 命令行显式传的 flag 优先（便于排障时临时覆盖）。
//
// 为什么不用 ibus 的 <setup> 弹窗：那需要一个图形设置程序，Linux 端暂无条件
// 做 GUI。用配置文件是最小可用方案，用户也可以直接编辑。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// userConfig 是 config.json 的结构。
type userConfig struct {
	Fuzzy       bool   `json:"fuzzy"`       // 模糊音
	Traditional bool   `json:"traditional"` // 繁体输出
	Shuangpin   string `json:"shuangpin"`   // 双拼方案：zrm / mspy / flypy / 空
	// Associate 控制词组联想（选词上屏后展示续接词组）。nil（配置未写）
	// = 开启；显式 false 关闭。指针类型区分「未配置」与「显式关闭」。
	Associate *bool `json:"associate"`
}

// AssociateEnabled 返回联想开关（未配置时默认开启）。
func (c userConfig) AssociateEnabled() bool {
	return c.Associate == nil || *c.Associate
}

// defaultConfigPath 返回 XDG 规范下的配置文件路径。
func defaultConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "chaoset", "config.json")
}

// loadUserConfig 读取配置文件。文件不存在视为全部默认（不报错）；
// 文件存在但 JSON 非法时返回错误——静默忽略会让用户以为配置生效了。
func loadUserConfig(path string) (userConfig, error) {
	var cfg userConfig
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("读取配置 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("解析配置 %s 失败: %w", path, err)
	}
	return cfg, nil
}
