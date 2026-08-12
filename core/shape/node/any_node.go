package node

import (
	"fmt"

	"github.com/huge-zhang/zdog-template/core/shape/model"
)

// Any 表示不约束结构的任意值。
type Any struct {
	*model.AbstractNode
}

// NewAny 构造一个 any 节点。
func NewAny(m model.Meta) *Any {
	return &Any{AbstractNode: model.NewAbstractNode(m)}
}

// Type 返回节点类型。
func (Any) Type() model.Type { return model.TypeAny }

func (a Any) String() string { return fmt.Sprintf("{any|desc=%s}", a.Desc()) }

func init() { putParser(model.TypeAny, &anyParser{}) }

type anyParser struct{}

func (anyParser) Parse(v map[string]any) (model.Node, error) {
	return &Any{AbstractNode: model.ParseAbstractNode(v)}, nil
}

func (anyParser) Dump(n model.Node) (map[string]any, error) {
	a, ok := n.(*Any)
	if !ok {
		return nil, fmt.Errorf("shape: anyParser got %T", n)
	}
	v := model.DumpAbstractNode(*a.AbstractNode)
	v["type"] = string(model.TypeAny)
	return v, nil
}
