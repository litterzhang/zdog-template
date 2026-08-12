package element

import (
	"fmt"
	"regexp"

	"github.com/litterzhang/zdog-template/core/template/model"
	"github.com/litterzhang/zdog-template/core/template/syntax"
)

// Hole 是一个待填充的洞。
//
// pattern 为空时表示无正则约束 —— plan 编译器会用后继字面量定界，
// 走 bytes.Index 快路径（T0，实测 0.060 µs/行）。只有 pattern 非空
// 才退化到 regexp（T1，~1.1 µs/行）。所以「不写 expr」是最快的写法。
type Hole struct {
	name    string
	pattern string
	greedy  bool
	// compiled 在构造时校验并缓存，避免热路径上重复编译。
	compiled *regexp.Regexp
}

// NewHole 构造一个洞。pattern 为空表示无约束。
func NewHole(name, pattern string, greedy bool) (*Hole, error) {
	h := &Hole{name: name, pattern: pattern, greedy: greedy}
	if pattern == "" {
		return h, nil
	}
	// 锚定到当前位置：洞总是从 startIndex 开始匹配。
	anchored := pattern
	if anchored[0] != '^' {
		anchored = "^(?:" + pattern + ")"
	}
	re, err := regexp.Compile(anchored)
	if err != nil {
		return nil, fmt.Errorf("template: hole %q has invalid expr %q: %w", name, pattern, err)
	}
	h.compiled = re
	return h, nil
}

// Tag 返回 TagRegex。
func (Hole) Tag() model.Tag { return model.TagRegex }

// Name 返回绑定名。
func (h Hole) Name() string { return h.name }

// Pattern 返回正则约束，空串表示无约束。
func (h Hole) Pattern() string { return h.pattern }

// Greedy 报告是否贪婪匹配。默认 false（最短匹配，由后继元素裁决）。
func (h Hole) Greedy() bool { return h.greedy }

// Regexp 返回已锚定编译的正则，无约束时为 nil。
func (h Hole) Regexp() *regexp.Regexp { return h.compiled }

// Dump 输出模板文本。无约束且非贪婪时用 ${name} 语法糖。
func (h Hole) Dump() string {
	if h.pattern == "" && !h.greedy {
		return "${" + h.name + "}"
	}
	s := "${re|name=" + syntax.EscapeAttrValue(h.name)
	if h.pattern != "" {
		s += ",expr=" + syntax.EscapeAttrValue(h.pattern)
	}
	if h.greedy {
		s += ",greedy=true"
	}
	return s + "}"
}

func (h Hole) String() string {
	return fmt.Sprintf("{re|name=%s,expr=%s,greedy=%v}", h.name, h.pattern, h.greedy)
}
