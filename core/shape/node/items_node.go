package node

import (
	"fmt"

	"github.com/litterzhang/zdog-template/core/shape/model"
)

// Array 表示同构列表类型。
type Array struct {
	*model.AbstractNode
	items model.Node
}

// NewArray 构造一个数组节点。
func NewArray(items model.Node, m model.Meta) *Array {
	return &Array{AbstractNode: model.NewAbstractNode(m), items: items}
}

// Type 返回节点类型。
func (Array) Type() model.Type { return model.TypeArray }

// Items 返回元素类型。
func (a Array) Items() model.Node { return a.items }

func (a Array) String() string {
	return fmt.Sprintf("{array|desc=%s,items=%v}", a.Desc(), a.items)
}

// Dict 表示键为字符串、值同构的映射类型。
type Dict struct {
	*model.AbstractNode
	items model.Node
}

// NewDict 构造一个字典节点。
func NewDict(items model.Node, m model.Meta) *Dict {
	return &Dict{AbstractNode: model.NewAbstractNode(m), items: items}
}

// Type 返回节点类型。
func (Dict) Type() model.Type { return model.TypeDict }

// Items 返回值类型。
func (d Dict) Items() model.Node { return d.items }

func (d Dict) String() string {
	return fmt.Sprintf("{dict|desc=%s,items=%v}", d.Desc(), d.items)
}

func init() {
	putParser(model.TypeArray, &itemsParser{t: model.TypeArray})
	putParser(model.TypeDict, &itemsParser{t: model.TypeDict})
}

// itemsParser 处理 array 与 dict —— 两者的定义结构完全相同（都只有一个 items），
// 旧原型为此写了两份几乎逐字重复的代码，且 dict 版本的 Dump 有吞错误的 bug。
type itemsParser struct{ t model.Type }

func (p itemsParser) Parse(v map[string]any) (model.Node, error) {
	raw, ok := v["items"]
	if !ok {
		return nil, fmt.Errorf("shape: %s missing items field", p.t)
	}
	items, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("shape: %s items: %w", p.t, err)
	}
	abs := model.ParseAbstractNode(v)
	if p.t == model.TypeArray {
		return &Array{AbstractNode: abs, items: items}, nil
	}
	return &Dict{AbstractNode: abs, items: items}, nil
}

func (p itemsParser) Dump(n model.Node) (map[string]any, error) {
	var abs model.AbstractNode
	var items model.Node
	switch x := n.(type) {
	case *Array:
		abs, items = *x.AbstractNode, x.items
	case *Dict:
		abs, items = *x.AbstractNode, x.items
	default:
		return nil, fmt.Errorf("shape: itemsParser got %T", n)
	}
	dumped, err := Dump(items)
	if err != nil {
		return nil, fmt.Errorf("shape: %s items: %w", p.t, err)
	}
	v := model.DumpAbstractNode(abs)
	v["type"] = string(n.Type())
	v["items"] = dumped
	return v, nil
}
