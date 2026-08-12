package node

import (
	"fmt"

	"github.com/huge-zhang/zdog-template/core/shape/model"
)

// Basic 表示标量类型：bool / number / string。
type Basic struct {
	*model.AbstractNode
	t model.Type
}

// NewBasic 构造一个标量节点。
func NewBasic(t model.Type, m model.Meta) *Basic {
	return &Basic{AbstractNode: model.NewAbstractNode(m), t: t}
}

// Type 返回节点类型。
func (b Basic) Type() model.Type { return b.t }

func (b Basic) String() string {
	return fmt.Sprintf("{%s|desc=%s}", b.t, b.Desc())
}

func init() {
	p := &basicParser{}
	putParser(model.TypeBool, p)
	putParser(model.TypeNumber, p)
	putParser(model.TypeString, p)
}

type basicParser struct{}

func (basicParser) Parse(v map[string]any) (model.Node, error) {
	t, err := model.ParseNodeType(v)
	if err != nil {
		return nil, err
	}
	return &Basic{AbstractNode: model.ParseAbstractNode(v), t: t}, nil
}

func (basicParser) Dump(n model.Node) (map[string]any, error) {
	b, ok := n.(*Basic)
	if !ok {
		return nil, fmt.Errorf("shape: basicParser got %T", n)
	}
	v := model.DumpAbstractNode(*b.AbstractNode)
	v["type"] = string(b.t)
	return v, nil
}
