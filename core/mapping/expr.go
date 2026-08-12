// Package mapping 提供源上下文到目标上下文的字段映射表达式。
//
// 语言是 **JMESPath 的一个子集**，不是新发明的语言（见 DESIGN.md §6）。
// 支持：
//
//	字段          lv
//	属性路径      payload.host
//	下标          payload.tags[0]
//	字面量        'literal'  42  true  null
//	或（回退）    payload.host || 'unknown'
//	函数          upper(lv)  join('-', payload.tags)
//
// 不支持（刻意留白）：投影 `a[*].b`、过滤 `[?x=='y']`、多选哈希/列表、管道 `|`。
// 重复块的展开由模板的 ${each} 负责，因此投影在这里没有必要。
//
// 性能约定：**裸字段名（如 `lv`）不走这里**。pipeline 在编译期识别出这种情况
// 并直连源槽位，保持零拷贝。只有真正含路径/函数的表达式才会物化值。
package mapping

import (
	"fmt"
	"strconv"
	"strings"
)

// Expr 是一条已编译的映射表达式。
type Expr interface {
	eval(env Env) (any, error)
	// refs 把本表达式引用到的根字段名收集进 set。
	refs(set map[string]struct{})
	String() string
}

// Refs 返回表达式引用到的根字段名。
// pipeline 用它决定只物化哪些字段 —— 没被引用的字段不会被解码。
func Refs(e Expr) []string {
	set := map[string]struct{}{}
	e.refs(set)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// ---- AST ----

type fieldExpr struct{ name string }

func (f *fieldExpr) String() string               { return f.name }
func (f *fieldExpr) refs(set map[string]struct{}) { set[f.name] = struct{}{} }

type propExpr struct {
	base Expr
	name string
}

func (p *propExpr) String() string             { return p.base.String() + "." + p.name }
func (p *propExpr) refs(s map[string]struct{}) { p.base.refs(s) }

type indexExpr struct {
	base Expr
	i    int
}

func (x *indexExpr) String() string             { return fmt.Sprintf("%s[%d]", x.base, x.i) }
func (x *indexExpr) refs(s map[string]struct{}) { x.base.refs(s) }

type literalExpr struct{ v any }

func (l *literalExpr) String() string {
	if s, ok := l.v.(string); ok {
		return "'" + s + "'"
	}
	return fmt.Sprintf("%v", l.v)
}
func (l *literalExpr) refs(map[string]struct{}) {}

type orExpr struct{ lhs, rhs Expr }

func (o *orExpr) String() string             { return o.lhs.String() + " || " + o.rhs.String() }
func (o *orExpr) refs(s map[string]struct{}) { o.lhs.refs(s); o.rhs.refs(s) }

type callExpr struct {
	name string
	fn   *function
	args []Expr
}

func (c *callExpr) String() string {
	parts := make([]string, len(c.args))
	for i, a := range c.args {
		parts[i] = a.String()
	}
	return c.name + "(" + strings.Join(parts, ", ") + ")"
}
func (c *callExpr) refs(s map[string]struct{}) {
	for _, a := range c.args {
		a.refs(s)
	}
}

// ---- 词法 ----

type tokKind uint8

const (
	tkEOF tokKind = iota
	tkIdent
	tkString
	tkNumber
	tkDot
	tkLBracket
	tkRBracket
	tkLParen
	tkRParen
	tkComma
	tkOr
)

type token struct {
	kind tokKind
	text string
	pos  int
}

type lexer struct {
	src string
	pos int
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.src) && (l.src[l.pos] == ' ' || l.src[l.pos] == '\t') {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tkEOF, pos: l.pos}, nil
	}
	start := l.pos
	c := l.src[l.pos]

	switch c {
	case '.':
		l.pos++
		return token{tkDot, ".", start}, nil
	case '[':
		l.pos++
		return token{tkLBracket, "[", start}, nil
	case ']':
		l.pos++
		return token{tkRBracket, "]", start}, nil
	case '(':
		l.pos++
		return token{tkLParen, "(", start}, nil
	case ')':
		l.pos++
		return token{tkRParen, ")", start}, nil
	case ',':
		l.pos++
		return token{tkComma, ",", start}, nil
	case '|':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '|' {
			l.pos += 2
			return token{tkOr, "||", start}, nil
		}
		return token{}, fmt.Errorf("mapping: 位置 %d：单个 '|'（管道）不在支持的子集内", start)
	case '"':
		return token{}, fmt.Errorf(
			"mapping: 位置 %d：字符串字面量请用**单引号**，如 'text'（双引号在这里没有含义）", start)
	case '*':
		return token{}, fmt.Errorf(
			"mapping: 位置 %d：投影 [*] 不在支持的子集内 —— 重复结构请用模板的 ${each} 块展开", start)
	case '?':
		return token{}, fmt.Errorf(
			"mapping: 位置 %d：过滤 [?...] 不在支持的子集内", start)
	case '\'':
		l.pos++
		var b strings.Builder
		for l.pos < len(l.src) {
			ch := l.src[l.pos]
			if ch == '\\' && l.pos+1 < len(l.src) {
				b.WriteByte(l.src[l.pos+1])
				l.pos += 2
				continue
			}
			if ch == '\'' {
				l.pos++
				return token{tkString, b.String(), start}, nil
			}
			b.WriteByte(ch)
			l.pos++
		}
		return token{}, fmt.Errorf("mapping: 位置 %d：字符串字面量未闭合", start)
	}

	if c == '-' || (c >= '0' && c <= '9') {
		l.pos++
		for l.pos < len(l.src) {
			ch := l.src[l.pos]
			if (ch >= '0' && ch <= '9') || ch == '.' || ch == 'e' || ch == 'E' || ch == '-' || ch == '+' {
				l.pos++
				continue
			}
			break
		}
		return token{tkNumber, l.src[start:l.pos], start}, nil
	}

	if isIdentStart(c) {
		l.pos++
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		return token{tkIdent, l.src[start:l.pos], start}, nil
	}
	return token{}, fmt.Errorf("mapping: 位置 %d：无法识别的字符 %q", start, string(c))
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// ---- 语法 ----

type parser struct {
	lex  *lexer
	cur  token
	expr string
}

// Compile 把表达式文本编译成 Expr。
func Compile(src string) (Expr, error) {
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("mapping: 表达式为空")
	}
	p := &parser{lex: &lexer{src: src}, expr: src}
	if err := p.advance(); err != nil {
		return nil, err
	}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur.kind != tkEOF {
		return nil, fmt.Errorf("mapping: %q 位置 %d：表达式后有多余内容 %q",
			src, p.cur.pos, p.cur.text)
	}
	return e, nil
}

// IsBareField 报告表达式是否就是一个裸字段名。
// pipeline 用它判断能否走零拷贝快路径。
func IsBareField(e Expr) (string, bool) {
	f, ok := e.(*fieldExpr)
	if !ok {
		return "", false
	}
	return f.name, true
}

func (p *parser) advance() error {
	t, err := p.lex.next()
	if err != nil {
		return err
	}
	p.cur = t
	return nil
}

func (p *parser) parseOr() (Expr, error) {
	lhs, err := p.parseChain()
	if err != nil {
		return nil, err
	}
	for p.cur.kind == tkOr {
		if err := p.advance(); err != nil {
			return nil, err
		}
		rhs, err := p.parseChain()
		if err != nil {
			return nil, err
		}
		lhs = &orExpr{lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseChain() (Expr, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.cur.kind {
		case tkDot:
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.cur.kind != tkIdent {
				return nil, fmt.Errorf("mapping: %q 位置 %d：'.' 之后需要属性名",
					p.expr, p.cur.pos)
			}
			base = &propExpr{base: base, name: p.cur.text}
			if err := p.advance(); err != nil {
				return nil, err
			}
		case tkLBracket:
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.cur.kind != tkNumber {
				return nil, fmt.Errorf(
					"mapping: %q 位置 %d：下标必须是整数（投影 [*] 与过滤 [?] 不在支持的子集内）",
					p.expr, p.cur.pos)
			}
			n, err := strconv.Atoi(p.cur.text)
			if err != nil {
				return nil, fmt.Errorf("mapping: %q 位置 %d：无效下标 %q",
					p.expr, p.cur.pos, p.cur.text)
			}
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.cur.kind != tkRBracket {
				return nil, fmt.Errorf("mapping: %q 位置 %d：缺少 ']'", p.expr, p.cur.pos)
			}
			base = &indexExpr{base: base, i: n}
			if err := p.advance(); err != nil {
				return nil, err
			}
		default:
			return base, nil
		}
	}
}

func (p *parser) parsePrimary() (Expr, error) {
	switch p.cur.kind {
	case tkString:
		v := p.cur.text
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &literalExpr{v: v}, nil

	case tkNumber:
		f, err := strconv.ParseFloat(p.cur.text, 64)
		if err != nil {
			return nil, fmt.Errorf("mapping: %q 位置 %d：无效数字 %q",
				p.expr, p.cur.pos, p.cur.text)
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &literalExpr{v: f}, nil

	case tkLParen:
		if err := p.advance(); err != nil {
			return nil, err
		}
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.cur.kind != tkRParen {
			return nil, fmt.Errorf("mapping: %q 位置 %d：缺少 ')'", p.expr, p.cur.pos)
		}
		return e, p.advance()

	case tkIdent:
		name := p.cur.text
		pos := p.cur.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		switch name {
		case "true":
			return &literalExpr{v: true}, nil
		case "false":
			return &literalExpr{v: false}, nil
		case "null":
			return &literalExpr{v: nil}, nil
		}
		if p.cur.kind != tkLParen {
			return &fieldExpr{name: name}, nil
		}
		return p.parseCall(name, pos)
	}
	return nil, fmt.Errorf("mapping: %q 位置 %d：需要一个字段、字面量或函数调用",
		p.expr, p.cur.pos)
}

func (p *parser) parseCall(name string, pos int) (Expr, error) {
	fn := lookupFunc(name)
	if fn == nil {
		return nil, fmt.Errorf("mapping: %q 位置 %d：未知函数 %q（可用：%s）",
			p.expr, pos, name, strings.Join(FunctionNames(), ", "))
	}
	if err := p.advance(); err != nil { // 吃掉 '('
		return nil, err
	}
	var args []Expr
	if p.cur.kind != tkRParen {
		for {
			a, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.cur.kind != tkComma {
				break
			}
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
	}
	if p.cur.kind != tkRParen {
		return nil, fmt.Errorf("mapping: %q 位置 %d：函数 %s 缺少 ')'", p.expr, p.cur.pos, name)
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	if err := fn.checkArity(name, len(args)); err != nil {
		return nil, fmt.Errorf("mapping: %q：%w", p.expr, err)
	}
	return &callExpr{name: name, fn: fn, args: args}, nil
}
