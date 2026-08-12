package element

import (
	"fmt"
	"strings"

	"github.com/huge-zhang/zdog-template/core/template/model"
	"github.com/huge-zhang/zdog-template/core/template/syntax"
)

// EachBlock 是重复块 `${each|name=X,sep=S}...${end}`。
//
// 它是第一个让模板 AST 真正成为树的元素。语义：
//
//	parse  —— 块的范围由后继字面量划定，块内按 sep 切分，每段必须被 Body 完整消费
//	format —— 把组内每次迭代渲染一遍，用 sep 连接
//
// 分隔符不能为空：没有分隔符就无法确定迭代边界，这与"两个相邻无约束洞"是
// 同一类歧义，必须在编译期拒绝。
type EachBlock struct {
	name       string
	sep        string
	body       []model.Element
	allowEmpty bool
}

// NewEachBlock 构造一个重复块。
func NewEachBlock(name, sep string, allowEmpty bool, body []model.Element) (*EachBlock, error) {
	if sep == "" {
		return nil, fmt.Errorf(
			"template: each block %q requires a non-empty sep attribute; "+
				"without a separator the iteration boundary is ambiguous", name)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("template: each block %q has an empty body", name)
	}
	return &EachBlock{name: name, sep: sep, body: body, allowEmpty: allowEmpty}, nil
}

// Tag 返回 TagEach。
func (EachBlock) Tag() model.Tag { return model.TagEach }

// Name 返回组名。
func (b EachBlock) Name() string { return b.name }

// Body 返回块内元素。
func (b EachBlock) Body() []model.Element { return b.body }

// Separator 返回迭代分隔符。
func (b EachBlock) Separator() string { return b.sep }

// AllowEmpty 报告是否允许零次迭代。
func (b EachBlock) AllowEmpty() bool { return b.allowEmpty }

// Dump 输出可重新加载的模板文本。
func (b EachBlock) Dump() string {
	var sb strings.Builder
	sb.WriteString("${each|name=")
	sb.WriteString(syntax.EscapeAttrValue(b.name))
	sb.WriteString(",sep=")
	sb.WriteString(syntax.EscapeAttrValue(b.sep))
	if b.allowEmpty {
		sb.WriteString(",empty=true")
	}
	sb.WriteString("}")
	for _, e := range b.body {
		sb.WriteString(e.Dump())
	}
	sb.WriteString("${end}")
	return sb.String()
}

func (b EachBlock) String() string {
	return fmt.Sprintf("{each|name=%s,sep=%q,body=%d}", b.name, b.sep, len(b.body))
}
