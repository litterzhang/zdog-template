package model

// Element 是模板中的一个元素。
//
// 与旧原型的关键差异（见 DESIGN.md §5、§9）：
// 旧接口是 Parse(src, i, ctx) (<-chan ParseResult, chan Void) —— 每个元素在解析时
// 自己产候选、自己起 goroutine、自己维护 Sync/Undo 闭包。实测这种"边解析边产候选"
// 的设计在 Go 里比单条正则还慢。
//
// 新设计把 Element 降级为**纯数据**：它只描述自己是什么，由 plan 编译器统一做
// 前瞻（洞的定界符来自后继元素）并生成扁平算子序列。热路径上不再有 Element 的踪影。
type Element interface {
	Tag() Tag
	// Name 返回该元素在 context 中的绑定名。字面量返回空串。
	Name() string
	// Dump 输出可重新解析的模板文本，满足 Load(Dump(e)) == e。
	Dump() string
}

// Literal 是确定性的字面量锚点。
type Literal interface {
	Element
	Content() string
}

// Hole 是一个待填充的洞。
//
// Pattern 为空表示"无正则约束" —— 此时 plan 编译器会用后继字面量作为定界符，
// 走 bytes.Index 快路径（T0 层，实测 0.060 µs/行）。只有 Pattern 非空时才会
// 退化到 regexp（T1 层，~1.1 µs/行）。
type Hole interface {
	Element
	Pattern() string
	// Greedy 为 false 表示最短匹配（默认），由后继元素裁决边界。
	Greedy() bool
}

// Island 是自定界的结构化块。
//
// 关键性质：从 pos 开始要么扫出唯一一个完整值、结束位置确定，要么失败 ——
// **只产生 0 或 1 个候选**。因此岛是回溯的天然屏障（见 DESIGN.md §5）。
//
// Scan 与 Decode 分离是刻意的：parse 阶段只需要边界，实测只扫边界 67 ns、
// 零分配，而物化整棵值树要 1838 ns、28 次分配（27 倍差距）。纯直通的字段
// 永远不会被解码。
type Island interface {
	Element
	// Scan 从 src[pos:] 扫出一个完整值的结束偏移，不物化值。
	Scan(src []byte, pos int) (end int, ok bool)
	// Decode 把 raw（即 src[pos:end]）解码成值。调用方按需调用。
	Decode(raw []byte) (any, error)
}

// ElementLoader 把 ${tag|args} 中的 args 构造成 Element。
type ElementLoader interface {
	Tag() Tag
	Load(args string, nameFunc func(Tag) string) (Element, error)
}

// Block 是一个重复块：把 Body 重复若干次，迭代之间用 Separator 分隔。
//
// 边界确定方式与洞同理，只是抬高了一层：整个块的范围由**块之后的后继字面量**
// 划定，块内部再按 Separator 切分成若干次迭代，每次迭代必须被 Body 完整消费。
// 于是 T0 的思路在这里依然成立 —— 一次 bytes.Index 定终点，一次切分定迭代。
type Block interface {
	Element
	// Body 返回块内的子元素序列。
	Body() []Element
	// Separator 是迭代之间的分隔符，不能为空。
	Separator() string
	// AllowEmpty 报告是否允许零次迭代。
	AllowEmpty() bool
}

// ExtensionLoader 是 ${ext|extension=NAME,...} 的扩展注册接口。
type ExtensionLoader interface {
	Name() string
	Load(args string, nameFunc func(Tag) string) (Element, error)
}
