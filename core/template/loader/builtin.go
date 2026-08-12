package loader

import (
	"fmt"
	"strconv"

	"github.com/huge-zhang/zdog-template/core/template/element"
	"github.com/huge-zhang/zdog-template/core/template/extension"
	"github.com/huge-zhang/zdog-template/core/template/model"
	"github.com/huge-zhang/zdog-template/core/template/syntax"
)

func init() {
	Put(textLoader{})
	Put(regexLoader{})
	Put(jsonLoader{})
	Put(extLoader{})
}

// resolveName 取 name 属性，缺失时用 nameFunc 自动生成。
//
// 自动命名不是可有可无的：定律 A（format(parse(t)) == t）要求每个洞的
// 原文都被绑定住才能原样回放，所以匿名洞也必须有一个内部名。
func resolveName(attrs syntax.Attrs, tag model.Tag, nameFunc func(model.Tag) string) string {
	if n := attrs.Get("name", ""); n != "" {
		return n
	}
	return nameFunc(tag)
}

func parseBool(attrs syntax.Attrs, key string) (bool, error) {
	if !attrs.Has(key) {
		return false, nil
	}
	raw := attrs[key]
	if raw == "" { // `greedy` 等价于 `greedy=true`
		return true, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("template: attribute %s=%q is not a boolean", key, raw)
	}
	return v, nil
}

// ---- text ----

type textLoader struct{}

func (textLoader) Tag() model.Tag { return model.TagText }

func (textLoader) Load(args string, _ func(model.Tag) string) (model.Element, error) {
	attrs, err := syntax.ParseAttrs(args)
	if err != nil {
		return nil, err
	}
	if !attrs.Has("content") {
		return nil, fmt.Errorf("template: ${text|...} requires a content attribute")
	}
	content := attrs["content"]
	if content == "" {
		return nil, nil // 空字面量无意义，直接丢弃
	}
	return element.NewRawText(content), nil
}

// ---- re ----

type regexLoader struct{}

func (regexLoader) Tag() model.Tag { return model.TagRegex }

func (regexLoader) Load(args string, nameFunc func(model.Tag) string) (model.Element, error) {
	attrs, err := syntax.ParseAttrs(args)
	if err != nil {
		return nil, err
	}
	greedy, err := parseBool(attrs, "greedy")
	if err != nil {
		return nil, err
	}
	// 兼容旧原型的 express 拼写。
	pattern := attrs.Get("expr", attrs.Get("express", ""))
	return element.NewHole(resolveName(attrs, model.TagRegex, nameFunc), pattern, greedy)
}

// ---- json ----

type jsonLoader struct{}

func (jsonLoader) Tag() model.Tag { return model.TagJSON }

func (jsonLoader) Load(args string, nameFunc func(model.Tag) string) (model.Element, error) {
	attrs, err := syntax.ParseAttrs(args)
	if err != nil {
		return nil, err
	}
	strict, err := parseBool(attrs, "strict")
	if err != nil {
		return nil, err
	}
	name := resolveName(attrs, model.TagJSON, nameFunc)
	if strict {
		return element.NewStrictJSONIsland(name), nil
	}
	return element.NewJSONIsland(name), nil
}

// ---- ext ----

type extLoader struct{}

func (extLoader) Tag() model.Tag { return model.TagExt }

func (extLoader) Load(args string, nameFunc func(model.Tag) string) (model.Element, error) {
	attrs, err := syntax.ParseAttrs(args)
	if err != nil {
		return nil, err
	}
	name := attrs.Get("extension", "")
	if name == "" {
		return nil, fmt.Errorf("template: ${ext|...} requires an extension attribute")
	}
	ext := extension.Get(name)
	if ext == nil {
		return nil, fmt.Errorf("template: unknown extension %q (registered: %v)", name, extension.Names())
	}
	el, err := ext.Load(args, nameFunc)
	if err != nil {
		return nil, fmt.Errorf("template: extension %q: %w", name, err)
	}
	if el == nil {
		return nil, fmt.Errorf("template: extension %q returned no element", name)
	}
	return el, nil
}
