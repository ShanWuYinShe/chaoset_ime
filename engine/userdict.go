package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type userRecord struct {
	Count    int   `json:"count"`
	LastUsed int64 `json:"last_used"`
}

// UserDict 记录用户上屏行为用于自动调频。path 为空串时仅驻留内存
// （CLI 演示、测试用）。调频公式：score = 基础词频 + 上屏次数 × userBoostPerCount，
// 时间衰减与 bigram 属阶段 2。
type UserDict struct {
	path string
	recs map[string]*userRecord
}

// NewUserDict 加载用户词典；文件不存在视为空词典，不报错。
func NewUserDict(path string) (*UserDict, error) {
	u := &UserDict{path: path, recs: make(map[string]*userRecord)}
	if path == "" {
		return u, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return u, nil
		}
		return nil, fmt.Errorf("读取用户词典失败: %w", err)
	}
	if err := json.Unmarshal(data, &u.recs); err != nil {
		return nil, fmt.Errorf("解析用户词典 %s 失败: %w", path, err)
	}
	return u, nil
}

func userKey(word string, syllables []string) string {
	return word + "\x00" + strings.Join(syllables, " ")
}

// Bump 记录一次上屏并立即落盘（种子规模下整文件重写足够快，
// 记录量大后再做防抖）。
func (u *UserDict) Bump(word string, syllables []string) error {
	key := userKey(word, syllables)
	rec := u.recs[key]
	if rec == nil {
		rec = &userRecord{}
		u.recs[key] = rec
	}
	rec.Count++
	rec.LastUsed = time.Now().Unix()
	return u.Save()
}

// Count 返回某词条累计上屏次数。
func (u *UserDict) Count(word string, syllables []string) int {
	if rec := u.recs[userKey(word, syllables)]; rec != nil {
		return rec.Count
	}
	return 0
}

// Save 以「写临时文件 + rename」的原子方式落盘，避免写一半被读到。
func (u *UserDict) Save() error {
	if u.path == "" {
		return nil
	}
	if dir := filepath.Dir(u.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建用户词典目录失败: %w", err)
		}
	}
	data, err := json.MarshalIndent(u.recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := u.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入用户词典失败: %w", err)
	}
	if err := os.Rename(tmp, u.path); err != nil {
		return fmt.Errorf("替换用户词典失败: %w", err)
	}
	return nil
}
