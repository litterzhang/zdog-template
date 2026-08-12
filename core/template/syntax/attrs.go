package syntax

import (
	"fmt"
	"strings"
)

// Attrs 是元素表达式里 `k=v,k=v` 形式的属性表。
type Attrs map[string]string

// Get 返回属性值，不存在时返回 def。
func (a Attrs) Get(key, def string) string {
	if v, ok := a[key]; ok {
		return v
	}
	return def
}

// Has 报告属性是否存在。
func (a Attrs) Has(key string) bool {
	_, ok := a[key]
	return ok
}

// SplitExpr 把 `tag|args` 拆成 tag 与 args。
//
// 无 '|' 时视为语法糖 `${name}`，等价于 `${re|name=name}`：
// 返回 tag 为空、args 为 "name=<原文>"。
func SplitExpr(expr string) (tag, args string, sugar bool) {
	if i := strings.IndexByte(expr, '|'); i >= 0 {
		return expr[:i], expr[i+1:], false
	}
	return "", "name=" + expr, true
}

// ParseAttrs 解析 `k=v,k=v` 属性串。
//
// 相对旧原型 template/util.StringToMap 的修复：
//  1. 用 SplitN(item, "=", 2) 而非 Split —— 否则 `expr=a=b` 会被截断成 `a`。
//  2. 按未转义的 ',' 切分 —— 否则 `expr=\d{1,3}` 会被切成两段。
func ParseAttrs(args string) (Attrs, error) {
	attrs := Attrs{}
	if strings.TrimSpace(args) == "" {
		return attrs, nil
	}
	for _, item := range splitUnescaped(args, ',') {
		if item == "" {
			continue
		}
		k, v, found := strings.Cut(item, "=")
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, fmt.Errorf("syntax: empty attribute name in %q", args)
		}
		if !found {
			attrs[k] = ""
			continue
		}
		attrs[k] = v
	}
	return attrs, nil
}

// splitUnescaped 按未被反斜杠转义的 sep 切分，并还原转义。
func splitUnescaped(s string, sep byte) []string {
	var (
		out []string
		b   strings.Builder
	)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isEscapable(s[i+1]) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if s[i] == sep {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(s[i])
	}
	out = append(out, b.String())
	return out
}

// EscapeAttrValue 把属性值转义成可安全嵌入模板的形式，用于 Dump。
func EscapeAttrValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ',', '}', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
