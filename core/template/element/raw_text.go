// Package element 提供内置模板元素的实现。
package element

import (
	"github.com/huge-zhang/zdog-template/core/template/model"
	"github.com/huge-zhang/zdog-template/core/template/syntax"
)

// RawText 是字面量锚点。它是确定性的：要么在当前位置原样出现，要么失败。
// plan 编译器用它作为洞的定界符，走 bytes.Index 快路径。
type RawText struct {
	content string
}

// NewRawText 构造一个字面量元素。
func NewRawText(content string) *RawText { return &RawText{content: content} }

// Tag 返回 TagText。
func (RawText) Tag() model.Tag { return model.TagText }

// Name 返回空串 —— 字面量不绑定到 context。
func (RawText) Name() string { return "" }

// Content 返回字面量内容。
func (t RawText) Content() string { return t.content }

// Dump 输出模板文本。字面量的 ${} 可省略。
func (t RawText) Dump() string { return syntax.EscapeText(t.content) }

func (t RawText) String() string { return "{text|content=" + t.content + "}" }
