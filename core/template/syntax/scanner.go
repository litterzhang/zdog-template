// Package syntax 负责模板文本的词法扫描与属性解析。
//
// 扫描器沿用旧原型 template/template.go 的三态状态机（flag 0/1/2），
// 补上转义支持 —— 旧版本无法表达属性值中的 `,` 与 `}`，
// 例如 `${re|name=x,expr=\d{1,3}}` 会被切坏。
package syntax

import (
	"fmt"
	"strings"
)

// Kind 区分扫描出的片段类型。
type Kind int

const (
	// KindText 是模板中的字面量文本。
	KindText Kind = iota
	// KindElement 是 ${...} 内部的表达式（不含花括号）。
	KindElement
)

// Piece 是扫描出的一个片段。
type Piece struct {
	Kind  Kind
	Value string
	// Pos 是该片段在模板源文中的起始偏移，用于错误定位。
	Pos int
}

// 转义规则：反斜杠只在其后紧跟"特殊字符"时才吞掉自身，
// 否则原样保留。这样 `\d+` `\s` 这类正则元字符不受影响，
// 而 `\}` `\,` `\$` `\\` 仍可用来转义。
func isEscapable(c byte) bool {
	switch c {
	case '}', ',', '$', '\\', '=':
		return true
	}
	return false
}

// unescape 还原转义序列。
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isEscapable(s[i+1]) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Scan 把模板文本切成字面量与元素表达式的交替序列。
//
// 状态机：
//
//	0 —— 普通文本，遇 '$' 转 1
//	1 —— 见过 '$'，遇 '{' 转 2；否则把 "$x" 退回文本
//	2 —— 在 ${...} 内，遇未转义的 '}' 收束回 0
func Scan(s string) ([]Piece, error) {
	var (
		pieces  []Piece
		buf     strings.Builder
		state   int
		start   int // 当前片段起点
		elStart int
	)

	flushText := func(at int) {
		if buf.Len() == 0 {
			return
		}
		pieces = append(pieces, Piece{Kind: KindText, Value: unescape(buf.String()), Pos: start})
		buf.Reset()
		start = at
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case 0:
			// 文本中的转义：`\$` 让 `${` 失去特殊含义
			if c == '\\' && i+1 < len(s) && isEscapable(s[i+1]) {
				buf.WriteByte(c)
				buf.WriteByte(s[i+1])
				i++
				continue
			}
			if c == '$' {
				state = 1
				continue
			}
			buf.WriteByte(c)
		case 1:
			if c == '{' {
				flushText(i - 1)
				elStart = i + 1
				state = 2
				continue
			}
			// 不是 ${，把 '$' 退回文本
			buf.WriteByte('$')
			i-- // 重新处理当前字符
			state = 0
		case 2:
			if c == '\\' && i+1 < len(s) && isEscapable(s[i+1]) {
				buf.WriteByte(c)
				buf.WriteByte(s[i+1])
				i++
				continue
			}
			if c == '}' {
				// 注意：元素体**不做**反转义。转义序列要留给 ParseAttrs ——
				// 它需要靠 `\,` 与未转义的 `,` 的区别来切分属性边界。
				// 在这里提前还原会导致双重反转义，`expr=a\,b` 会被切成两个属性。
				pieces = append(pieces, Piece{Kind: KindElement, Value: buf.String(), Pos: elStart})
				buf.Reset()
				start = i + 1
				state = 0
				continue
			}
			buf.WriteByte(c)
		}
	}

	switch state {
	case 1:
		buf.WriteByte('$')
		flushText(len(s))
	case 2:
		return nil, fmt.Errorf("syntax: unterminated ${...} starting at offset %d", elStart-2)
	default:
		flushText(len(s))
	}
	return pieces, nil
}

// EscapeText 把字面量文本转义成可安全嵌入模板的形式，用于 Dump。
func EscapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '$' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
