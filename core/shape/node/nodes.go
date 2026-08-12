// Package node 提供 shape 各类型节点的实现与注册表。
// 复用自旧原型 shape/node，修复：注册表不再在 init 阶段打印日志、加并发保护。
package node

import (
	"fmt"
	"sync"

	"github.com/huge-zhang/zdog-template/core/shape/model"
)

var (
	parserMu   sync.RWMutex
	parserPool = map[model.Type]model.NodeParser{}
)

// putParser 注册某个类型的解析器。由各节点的 init 调用。
func putParser(t model.Type, p model.NodeParser) {
	parserMu.Lock()
	defer parserMu.Unlock()
	parserPool[t] = p
}

// getParser 取出类型对应的解析器。
func getParser(t model.Type) (model.NodeParser, error) {
	parserMu.RLock()
	defer parserMu.RUnlock()
	if p, ok := parserPool[t]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("shape: no parser for type %q", t)
}

// parseMap 按 type 字段分发到对应解析器。
func parseMap(v map[string]any) (model.Node, error) {
	t, err := model.ParseNodeType(v)
	if err != nil {
		return nil, err
	}
	p, err := getParser(t)
	if err != nil {
		return nil, err
	}
	return p.Parse(v)
}

// Parse 把一个已反序列化的 shape 定义转成节点树。
func Parse(v any) (model.Node, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("shape: invalid node, want object, got %T", v)
	}
	return parseMap(m)
}

// Dump 把节点树转回可序列化的 map。
func Dump(n model.Node) (map[string]any, error) {
	if n == nil {
		return nil, fmt.Errorf("shape: cannot dump nil node")
	}
	p, err := getParser(n.Type())
	if err != nil {
		return nil, err
	}
	return p.Dump(n)
}
