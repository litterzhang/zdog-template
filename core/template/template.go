// Package template 把模板文本加载成元素树。
//
// 加载流程（见 DESIGN.md §5）：
//
//	模板文本 --syntax.Scan--> Piece 序列 --loader--> Element 树
//
// Element 只是纯数据描述；真正的执行计划由 core/plan 编译产生。
// 热路径上不会出现 Element。
package template

import (
	"fmt"
	"strings"

	"github.com/huge-zhang/zdog-template/core/template/element"
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

// Elements 返回顶层元素序列。调用方不得修改。
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

// frame 是嵌套 ${each} 的构建栈帧。
type frame struct {
	name       string // 组名；顶层为空
	sep        string
	allowEmpty bool
	pos        int // ${each} 在源文中的偏移，供未闭合报错定位
	elements   []model.Element
	seen       map[string]bool // 该层的绑定名，各层独立命名空间
}

func newFrame() *frame { return &frame{seen: map[string]bool{}} }

// Load 把模板文本加载成 Template。
func Load(src string) (*Template, error) {
	pieces, err := syntax.Scan(src)
	if err != nil {
		return nil, err
	}
	nameFunc := defaultNameFunc()
	stack := []*frame{newFrame()}

	for _, p := range pieces {
		top := stack[len(stack)-1]

		if p.Kind == syntax.KindElement {
			tag, args, sugar := syntax.SplitExpr(p.Value)
			// ${end} 没有属性，会被 SplitExpr 当作 ${name} 语法糖，
			// 因此结构标记要在 sugar 分支里也认一次。
			if sugar && model.Tag(p.Value) == model.TagEnd {
				tag, sugar = string(model.TagEnd), false
			}
			if !sugar {
				switch model.Tag(tag) {
				case model.TagEach:
					f, err := openEach(args, p.Pos, nameFunc)
					if err != nil {
						return nil, err
					}
					if err := claim(top, f.name, p.Pos); err != nil {
						return nil, err
					}
					stack = append(stack, f)
					continue
				case model.TagEnd:
					if len(stack) == 1 {
						return nil, fmt.Errorf(
							"template: ${end} at offset %d has no matching ${each}", p.Pos)
					}
					blk, err := element.NewEachBlock(top.name, top.sep, top.allowEmpty, top.elements)
					if err != nil {
						return nil, err
					}
					stack = stack[:len(stack)-1]
					parent := stack[len(stack)-1]
					parent.elements = append(parent.elements, blk)
					continue
				}
			}
		}

		el, err := loadPiece(p, nameFunc)
		if err != nil {
			return nil, err
		}
		if el == nil {
			continue
		}
		if err := claim(top, el.Name(), p.Pos); err != nil {
			return nil, err
		}
		top.elements = append(top.elements, el)
	}

	if len(stack) > 1 {
		return nil, fmt.Errorf("template: ${each|name=%s} at offset %d is never closed by ${end}",
			stack[len(stack)-1].name, stack[len(stack)-1].pos)
	}
	return &Template{source: src, elements: stack[0].elements}, nil
}

// claim 在当前层登记一个绑定名，重名报错。每个 ${each} 层是独立命名空间。
func claim(f *frame, name string, pos int) error {
	if name == "" {
		return nil
	}
	if f.seen[name] {
		return fmt.Errorf("template: duplicate binding name %q at offset %d", name, pos)
	}
	f.seen[name] = true
	return nil
}

// openEach 解析 ${each|...} 的属性并开一个新栈帧。
func openEach(args string, pos int, nameFunc func(model.Tag) string) (*frame, error) {
	attrs, err := syntax.ParseAttrs(args)
	if err != nil {
		return nil, err
	}
	name := attrs.Get("name", "")
	if name == "" {
		name = nameFunc(model.TagEach)
	}
	sep := attrs.Get("sep", "")
	if sep == "" {
		return nil, fmt.Errorf(
			"template: ${each|name=%s} at offset %d requires a non-empty sep attribute; "+
				"without a separator the iteration boundary is ambiguous "+
				"(note: a literal comma separator must be written sep=',' or sep=\\, "+
				"because ',' also separates attributes)", name, pos)
	}
	allowEmpty := false
	if raw, ok := attrs["empty"]; ok {
		allowEmpty = raw == "" || raw == "true"
	}
	f := newFrame()
	f.name, f.sep, f.allowEmpty, f.pos = name, sep, allowEmpty, pos
	return f, nil
}

// loadPiece 把单个片段构造成元素。
func loadPiece(p syntax.Piece, nameFunc func(model.Tag) string) (model.Element, error) {
	if p.Kind == syntax.KindText {
		l := loader.Get(model.TagText)
		return l.Load("content="+syntax.EscapeAttrValue(p.Value), nameFunc)
	}

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
