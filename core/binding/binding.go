// Package binding 提供解析结果的承载结构：Binding 与 Context。
//
// 核心设计（见 DESIGN.md §3）：每个绑定同时持有**值**与**出处**。
//
//	Raw != nil  -> 有出处，format 时原样回放（定律 A）
//	Raw == nil  -> 无出处（来自 mapping），format 时靠 Shape 序列化（定律 B）
//
// 旧原型的 context 是扁平 sync.Map + 自动生成的 text0/regex1 键，
// 既无法表达嵌套也无法回放原文，因此定律 A 根本无从谈起。
package binding

import (
	"fmt"

	"github.com/huge-zhang/zdog-template/core/plan"
)

// Binding 是一个绑定：值 + 出处。
type Binding struct {
	// Name 是绑定名。
	Name string
	// Raw 是该绑定在源文中的原始字节；nil 表示无出处。
	Raw []byte
	// Span 是在源文中的偏移；无出处时为 plan.NoSpan。
	Span plan.Span
}

// HasOrigin 报告该绑定是否有源文出处。
func (b Binding) HasOrigin() bool { return b.Raw != nil }

func (b Binding) String() string {
	return fmt.Sprintf("binding{%s=%q origin=%v}", b.Name, b.Raw, b.HasOrigin())
}
