package setupctl

import (
	"fmt"
	"io"

	"chaoset.ime/engine"
	"chaoset.ime/internal/appdirs"
)

// UserDictPath 返回 Linux 用户词典路径。
func UserDictPath() (string, error) {
	return appdirs.LinuxUserDictPath()
}

func openUserDict() (*engine.UserDict, error) {
	path, err := UserDictPath()
	if err != nil {
		return nil, err
	}
	u, err := engine.NewUserDict(path)
	if err != nil {
		return nil, fmt.Errorf("加载用户词典失败: %w", err)
	}
	return u, nil
}

// Stats 返回学习数据统计。
func Stats() (engine.UserStats, error) {
	u, err := openUserDict()
	if err != nil {
		return engine.UserStats{}, err
	}
	return u.Stats(), nil
}

// Clear 清空学习数据（本地文件模式；调用方负责先停 ibus 引擎，避免其内存态
// 在延迟落盘时把旧数据写回）。
func Clear() error {
	u, err := openUserDict()
	if err != nil {
		return err
	}
	if err := u.Clear(); err != nil {
		return fmt.Errorf("清空学习数据失败: %w", err)
	}
	if err := u.Save(); err != nil {
		return fmt.Errorf("保存清空结果失败: %w", err)
	}
	return nil
}

// ExportTSV 把学习数据写成 TSV；phrasesOnly 只导出用户词组。
func ExportTSV(w io.Writer, phrasesOnly bool) error {
	u, err := openUserDict()
	if err != nil {
		return err
	}
	if err := u.ExportTSV(w, phrasesOnly); err != nil {
		return fmt.Errorf("导出学习数据失败: %w", err)
	}
	return nil
}
