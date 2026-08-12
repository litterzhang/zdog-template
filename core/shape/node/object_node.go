package node

import (
	"fmt"
	"sort"

	"github.com/huge-zhang/zdog-template/core/shape/model"
)

// Object 表示带命名属性的对象类型。
type Object struct {
	*model.AbstractNode
	properties map[string]model.Node
}

// NewObject 构造一个对象节点。
func NewObject(props map[string]model.Node, m model.Meta) *Object {
	if props == nil {
		props = map[string]model.Node{}
	}
	return &Object{AbstractNode: model.NewAbstractNode(m), properties: props}
}

// Type 返回节点类型。
func (Object) Type() model.Type { return model.TypeObject }

// Properties 返回属性表。调用方不得修改返回的 map。
func (o Object) Properties() map[string]model.Node { return o.properties }

// Property 按名取属性。
func (o Object) Property(name string) (model.Node, bool) {
	n, ok := o.properties[name]
	return n, ok
}

// Keys 返回排序后的属性名，保证遍历顺序稳定（定律 B 需要确定性输出）。
func (o Object) Keys() []string {
	keys := make([]string, 0, len(o.properties))
	for k := range o.properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (o Object) String() string {
	return fmt.Sprintf("{object|desc=%s,properties=%v}", o.Desc(), o.properties)
}

func init() { putParser(model.TypeObject, &objectParser{}) }

type objectParser struct{}

func (objectParser) Parse(v map[string]any) (model.Node, error) {
	raw, ok := v["properties"]
	if !ok {
		return nil, fmt.Errorf("shape: object missing properties field")
	}
	rawProps, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("shape: object properties must be an object, got %T", raw)
	}
	props := make(map[string]model.Node, len(rawProps))
	for name, rawProp := range rawProps {
		child, err := Parse(rawProp)
		if err != nil {
			return nil, fmt.Errorf("shape: property %q: %w", name, err)
		}
		props[name] = child
	}
	return &Object{AbstractNode: model.ParseAbstractNode(v), properties: props}, nil
}

func (objectParser) Dump(n model.Node) (map[string]any, error) {
	o, ok := n.(*Object)
	if !ok {
		return nil, fmt.Errorf("shape: objectParser got %T", n)
	}
	props := make(map[string]any, len(o.properties))
	for name, child := range o.properties {
		dumped, err := Dump(child)
		if err != nil {
			return nil, fmt.Errorf("shape: property %q: %w", name, err)
		}
		props[name] = dumped
	}
	v := model.DumpAbstractNode(*o.AbstractNode)
	v["type"] = string(model.TypeObject)
	v["properties"] = props
	return v, nil
}
