// Package template 把模板文本加载成元素序列。
//
// 加载流程（见 DESIGN.md §5）：
//
//	模板文本 --syntax.Scan--> Piece 序列 --loader--> Element 序列
//
// Element 只是纯数据描述；真正的执行计划由 core/plan 编译产生。
// 热路径上不会出现 Element。
package template

import (
	"fmt"
	"strings"

	"github.com/huge-zhang/zdog-template/core/template/loader"
	"github.com/huge-zhang/zdog-template/core/template/model"
	"github.com/huge-zhang/zdog-template/core/template/syntax"
)

// Template 是一份已加载的模板。它是双向的 —— 同一份模板既能 parse 也能 format。
type Template struct {
	source   string
	elements []model.Element
}

// Source 返回原始模板文本。
func (t *Template) Source() string { return t.source }

// Elements 返回元素序列。调用方不得修改。
func (t *Template) Elements() []model.Element { return t.elements }

func (t *Template) String() string {
	return fmt.Sprintf("template{%d elements: %s}", len(t.elements), t.Dump())
}

// Dump 输出可重新加载的模板文本。
func (t *Template) Dump() string {
	var b strings.Builder
	for _, e := range t.elements {
		b.WriteString(e.Dump())
	}
	return b.String()
}

// defaultNameFunc 为匿名元素生成稳定的内部名。
// 复用自旧原型 template/util.GenDefaultNameFunc。
func defaultNameFunc() func(model.Tag) string {
	n := 0
	return func(tag model.Tag) string {
		n++
		return fmt.Sprintf("%s-%d", tag, n)
	}
}

// Load 把模板文本加载成 Template。
func Load(src string) (*Template, error) {
	pieces, err := syntax.Scan(src)
	if err != nil {
		return nil, err
	}
	nameFunc := defaultNameFunc()
	elements := make([]model.Element, 0, len(pieces))
	seen := map[string]bool{}

	for _, p := range pieces {
		var el model.Element
		if p.Kind == syntax.KindText {
			l := loader.Get(model.TagText)
			if el, err = l.Load("content="+syntax.EscapeAttrValue(p.Value), nameFunc); err != nil {
				return nil, err
			}
		} else if el, err = loadElement(p, nameFunc); err != nil {
			return nil, err
		}
		if el == nil {
			continue
		}
		if name := el.Name(); name != "" {
			if seen[name] {
				return nil, fmt.Errorf("template: duplicate binding name %q at offset %d", name, p.Pos)
			}
			seen[name] = true
		}
		elements = append(elements, el)
	}
	return &Template{source: src, elements: elements}, nil
}

// loadElement 解析单个 ${...} 表达式。
func loadElement(p syntax.Piece, nameFunc func(model.Tag) string) (model.Element, error) {
	tagStr, args, sugar := syntax.SplitExpr(p.Value)

	// 语法糖 ${name} 等价于 ${re|name=name}，是最快的写法：
	// 无正则约束的洞由后继字面量定界，走 bytes.Index 快路径。
	tag := model.TagRegex
	if !sugar {
		tag = model.Tag(tagStr)
		if !model.Supported(tag) {
			return nil, fmt.Errorf("template: unsupported tag %q at offset %d (supported: %v)",
				tag, p.Pos, loader.Tags())
		}
	}

	l := loader.Get(tag)
	if l == nil {
		return nil, fmt.Errorf("template: no loader for tag %q at offset %d", tag, p.Pos)
	}
	el, err := l.Load(args, nameFunc)
	if err != nil {
		return nil, fmt.Errorf("%w (at offset %d)", err, p.Pos)
	}
	return el, nil
}
