package model

import (
	"errors"
	"fmt"
)

// Node 是 shape 树上的一个类型节点。
type Node interface {
	Type() Type
	Desc() string
	// Meta 返回 codec 所需的元信息。
	Meta() Meta
}

// Meta 承载与序列化相关的元信息。
//
// 设计要点（见 DESIGN.md §3）：经过 mapping 之后的字段没有 Raw 出处，
// format 必须靠 Meta 决定如何把值写成文本。所以 Meta 不是可选的装饰，
// 而是定律 B（parse(format(c)) == c）成立的前提。
type Meta struct {
	Desc     string `json:"desc,omitempty"`
	Format   string `json:"format,omitempty"`   // 如 number 的 "%d"、时间的布局
	Default  any    `json:"default,omitempty"`  // 缺失时的填充值
	Nullable bool   `json:"nullable,omitempty"` // 允许空值
	Required bool   `json:"required,omitempty"` // 对象属性是否必需
}

// AbstractNode 提供 Node 的公共字段，供各具体节点内嵌。
type AbstractNode struct {
	meta Meta
}

// Desc 返回节点描述。
func (n AbstractNode) Desc() string { return n.meta.Desc }

// Meta 返回节点元信息。
func (n AbstractNode) Meta() Meta { return n.meta }

// NodeParser 负责某个 Type 的双向转换。
type NodeParser interface {
	Parse(v map[string]any) (Node, error)
	Dump(node Node) (map[string]any, error)
}

// ParseAbstractNode 从原始 map 中提取公共元信息。
func ParseAbstractNode(v map[string]any) *AbstractNode {
	var m Meta
	if s, ok := v["desc"].(string); ok {
		m.Desc = s
	}
	if s, ok := v["format"].(string); ok {
		m.Format = s
	}
	if b, ok := v["nullable"].(bool); ok {
		m.Nullable = b
	}
	if b, ok := v["required"].(bool); ok {
		m.Required = b
	}
	if d, ok := v["default"]; ok {
		m.Default = d
	}
	return &AbstractNode{meta: m}
}

// NewAbstractNode 由 Meta 直接构造，供程序化建树使用。
func NewAbstractNode(m Meta) *AbstractNode { return &AbstractNode{meta: m} }

// DumpAbstractNode 把公共元信息写回 map。
func DumpAbstractNode(n AbstractNode) map[string]any {
	v := make(map[string]any)
	if n.meta.Desc != "" {
		v["desc"] = n.meta.Desc
	}
	if n.meta.Format != "" {
		v["format"] = n.meta.Format
	}
	if n.meta.Nullable {
		v["nullable"] = true
	}
	if n.meta.Required {
		v["required"] = true
	}
	if n.meta.Default != nil {
		v["default"] = n.meta.Default
	}
	return v
}

// ErrMissingType 表示 shape 定义缺少 type 字段。
var ErrMissingType = errors.New("shape: missing type field")

// ParseNodeType 读取并校验 type 字段。
func ParseNodeType(v map[string]any) (Type, error) {
	raw, ok := v["type"]
	if !ok {
		return "", ErrMissingType
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("shape: invalid type field %v", raw)
	}
	t := Type(s)
	if !Supported(t) {
		return "", fmt.Errorf("shape: unsupported type %q", t)
	}
	return t, nil
}
