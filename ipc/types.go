package ipc

// 本文件是 daemon 响应数据的客户端 DTO：字段与 JSON 契约逐字对应。
//
// 为什么不再复用 engine 的类型：Go 引擎（engine/）已退役，ipc 客户端是
// daemon 的唯一 Go 调用方；把 DTO 收在协议包里，壳/工具/测试都只依赖
// ipc 而不依赖任何引擎实现，删除 engine/ 时不用再动调用方。

// Candidate 是一个候选项。Syllables 以 ' 连接，便于展示与传输。
type Candidate struct {
	Text      string   `json:"text"`
	Syllables string   `json:"syllables"`
	Score     int      `json:"score"`
	FromUser  bool     `json:"from_user,omitempty"` // 是否被用户数据（调频/词组/上下文）加权
	Segments  []string `json:"segments,omitempty"`  // 整句式候选的分段词文本（壳不使用）
}

// Composition 是当前组词状态：原始输入、预编辑串、当前页候选与分页信息。
type Composition struct {
	Input      string      `json:"input"`
	Preedit    string      `json:"preedit"`
	Candidates []Candidate `json:"candidates"`
	Page       int         `json:"page"`
	PageCount  int         `json:"page_count"`
	// Shuangpin 是当前会话启用的双拼方案名（zrm/mspy/flypy），全拼为空。
	Shuangpin string `json:"shuangpin,omitempty"`
	// Committed 是本次调用额外上屏、需要壳写入文档的文本（空 = 无）。
	Committed string `json:"committed,omitempty"`
}

// UserStats 是用户词典三类学习记录的条目数。
type UserStats struct {
	Words   int `json:"words"`   // 静态词调频记录
	Phrases int `json:"phrases"` // 用户合成词组
	Bigrams int `json:"bigrams"` // 前后词共现
}

// Controller 是 daemon 会话的统一操作接口（*Client 实现）。
// 语义与 daemon 的 19 个 IPC 方法一一对应；Go 侧只剩这一个实现。
type Controller interface {
	// Input 追加输入。halfWidth 是壳的全/半角标点状态，只对本次调用生效。
	Input(text string, halfWidth bool) (Composition, error)
	Backspace() (Composition, error)
	// BackspaceWord 逐词/逐音节回退：删除末尾的一个音节或词段。
	BackspaceWord() (Composition, error)
	// Reset 清空组词与学习上下文（prev/history），对应 Esc/失焦/新会话。
	Reset() (Composition, error)
	// ResetComposition 只丢弃当前组词、保留学习上下文。
	ResetComposition() (Composition, error)
	Select(index int) (string, error)
	// SelectPrefix 渐进上屏：选中候选并只消耗其音节对应的输入前缀。
	SelectPrefix(index int) (Composition, string, error)
	// Punct 提交一个标点键（组词中先上屏首选候选，再输出中文标点）。
	Punct(key string, halfWidth bool) (string, error)
	// NextPage / PrevPage 翻动候选页（边界钳制，不回绕）。
	NextPage() (Composition, error)
	PrevPage() (Composition, error)
	// CommitRaw 清空当前组词并返回用户输入的原始拼音。
	CommitRaw() (string, error)
}
