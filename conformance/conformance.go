// Package conformance 定义并运行跨语言一致性用例。
//
// 这是多语言 SDK 的核心资产（见 DESIGN.md §8）：**语义的唯一真源是用例，
// 不是某个实现**。因此不必维护第二套完整实现 —— 每个语言的 SDK 跑同一套
// cases/*.json，通过即视为语义一致。
//
// 用例格式（cases/*.json）：
//
//	{
//	  "name":        "简短标识",
//	  "description": "这个用例在守护什么",
//	  "config":      { "version":1, "source":"...", "target":"...", "mapping":{...} },
//
//	  // —— 成功用例 ——
//	  "input":    "输入文本，按 \n 分行",
//	  "output":   "期望输出（每行以 \n 结尾）",
//	  "matched":  3,            // 可选：匹配的行数
//	  "total":    4,            // 可选：总行数
//	  "bindings": {"名":"值"},   // 可选：单行输入时源侧各绑定的原文
//	  "tier":     "T0/literal", // 可选：期望的执行层级
//	  "laws":     ["A","B"],    // 可选：需要校验的 round-trip 定律
//
//	  // —— 失败用例（与上面互斥）——
//	  "compile_error": "错误信息应包含的子串"
//	}
//
// 只有 config / input / output / matched / total / compile_error 是跨语言必须支持的；
// bindings / tier / laws 需要更丰富的 ABI，目前只有 Go 侧校验。
package conformance

// Case 是一条一致性用例。
type Case struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	Config map[string]any `json:"config"`

	Input   string `json:"input"`
	Output  string `json:"output"`
	Matched *int   `json:"matched,omitempty"`
	Total   *int   `json:"total,omitempty"`

	Bindings map[string]string `json:"bindings,omitempty"`
	Tier     string            `json:"tier,omitempty"`
	Laws     []string          `json:"laws,omitempty"`

	CompileError string `json:"compile_error,omitempty"`
}

// IsError 报告这是否是一条期望编译失败的用例。
func (c *Case) IsError() bool { return c.CompileError != "" }
