// Package shape 提供 Z-Template 的类型系统。
//
// 职责（见 DESIGN.md §3）：经过 mapping 之后的字段没有 Raw 出处，
// format 必须靠 shape 决定如何把值写成文本。shape 不是可有可无的校验器，
// 而是定律 B（parse(format(c)) == c）成立的前提。
package shape

import (
	"encoding/json"
	"fmt"

	"github.com/huge-zhang/zdog-template/core/shape/model"
	"github.com/huge-zhang/zdog-template/core/shape/node"
)

// Shape 是一棵带标题的类型树。
type Shape struct {
	node  model.Node
	title string
}

// Node 返回根节点。
func (s Shape) Node() model.Node { return s.node }

// Title 返回 shape 标题。
func (s Shape) Title() string { return s.title }

func (s Shape) String() string {
	return fmt.Sprintf("shape{node: %v, title: %s}", s.node, s.title)
}

// New 由节点树直接构造 Shape。
func New(n model.Node, title string) *Shape {
	return &Shape{node: n, title: title}
}

// Load 从 JSON 定义解析出 Shape。
func Load(data []byte) (*Shape, error) {
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("shape: invalid json: %w", err)
	}
	n, err := node.Parse(v)
	if err != nil {
		return nil, err
	}
	title, _ := v["title"].(string)
	return &Shape{node: n, title: title}, nil
}

// LoadString 是 Load 的字符串便利版本。
func LoadString(s string) (*Shape, error) { return Load([]byte(s)) }

// Dump 把 Shape 序列化回 JSON。
func Dump(s *Shape) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("shape: cannot dump nil shape")
	}
	v, err := node.Dump(s.node)
	if err != nil {
		return nil, err
	}
	if s.title != "" {
		v["title"] = s.title
	}
	return json.Marshal(v)
}
