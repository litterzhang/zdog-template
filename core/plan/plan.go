// Package plan 把 Element 序列编译成扁平算子序列，并执行它。
//
// 这是整个设计的性能枢纽（见 DESIGN.md §5、§7）。要点：
//
//  1. Element 只是纯数据；热路径上跑的是 []Op，没有接口分发、没有闭包、没有 goroutine。
//  2. 洞的定界符来自**后继元素** —— 所以编译必须做前瞻，不能由元素各自决定。
//  3. 能用字面量定界就绝不用正则：bytes.Index 在 Go 里是 SIMD 加速的，
//     实测 0.060 µs/行；而 Go 的 regexp 是 RE2，同样的活要 1.17 µs/行。
package plan

import (
	"fmt"
	"regexp"

	"github.com/huge-zhang/zdog-template/core/template/model"
)

// OpKind 是算子种类。
type OpKind uint8

const (
	// OpPrefix 是模板开头的字面量锚点。
	OpPrefix OpKind = iota
	// OpLiteral 是必须在当前位置原样出现的字面量。
	OpLiteral
	// OpFindByte 是由单字符定界的洞（memchr 快路径）。
	OpFindByte
	// OpFindLit 是由多字符字面量定界的洞（SIMD 快路径）。
	OpFindLit
	// OpRest 是吃到输入结尾的洞。
	OpRest
	// OpRegex 是有正则约束、且无后继字面量定界的洞。
	OpRegex
	// OpRegexUntil 是有正则约束、且由后继字面量定界的洞。
	// 语义：在后继字面量的各次出现位置中，取**最早的、且正则能完整覆盖该跨度**的那个。
	// 这正是 blog 里 `${re|expr=\d+}345` 对 "123345" 应匹配 "123" 的 best-match 语义。
	OpRegexUntil
	// OpIsland 是自定界的结构化块（只产生 0 或 1 个候选）。
	OpIsland
)

// Op 是一条扁平算子。
type Op struct {
	Kind   OpKind
	Lit    []byte         // OpPrefix / OpLiteral / OpFindLit / OpRegexUntil 的定界符
	Ch     byte           // OpFindByte
	Slot   int            // 绑定槽位；-1 表示不绑定
	Re     *regexp.Regexp // OpRegex / OpRegexUntil，已锚定到起点
	Island model.Island   // OpIsland
}

// Tier 是模板达到的执行层级，越低越快。
type Tier uint8

const (
	// TierLiteral 全部洞由字面量定界，纯 bytes.Index 链。
	TierLiteral Tier = iota
	// TierRegex 含正则约束的洞。
	TierRegex
	// TierIsland 含结构化岛。
	TierIsland
)

// String 返回层级名。
func (t Tier) String() string {
	switch t {
	case TierLiteral:
		return "T0/literal"
	case TierRegex:
		return "T1/regex"
	case TierIsland:
		return "T2/island"
	}
	return "unknown"
}

// Plan 是一份已编译的执行计划。它是不可变的，可被多 goroutine 并发使用。
type Plan struct {
	ops   []Op
	names []string
	index map[string]int
	tier  Tier
	// islands[slot] 非 nil 表示该槽位是结构化岛，读取值时需要解码。
	// 纯直通的字段永远不会走到解码。
	islands []model.Island
}

// Island 返回该槽位对应的结构化岛，非岛槽位返回 nil。
func (p *Plan) Island(slot int) model.Island {
	if slot < 0 || slot >= len(p.islands) {
		return nil
	}
	return p.islands[slot]
}

// Ops 返回算子序列。调用方不得修改。
func (p *Plan) Ops() []Op { return p.ops }

// Names 返回槽位名（下标即槽位号）。
func (p *Plan) Names() []string { return p.names }

// NumSlots 返回绑定槽位数量。
func (p *Plan) NumSlots() int { return len(p.names) }

// Slot 按名查槽位号。
func (p *Plan) Slot(name string) (int, bool) {
	i, ok := p.index[name]
	return i, ok
}

// Tier 返回执行层级。
func (p *Plan) Tier() Tier { return p.tier }

func (p *Plan) String() string {
	return fmt.Sprintf("plan{tier=%s, ops=%d, slots=%d}", p.tier, len(p.ops), len(p.names))
}

// Compile 把 Element 序列编译成执行计划。
func Compile(elements []model.Element) (*Plan, error) {
	p := &Plan{index: map[string]int{}}
	consumed := make([]bool, len(elements))

	slotFor := func(name string) int {
		if i, ok := p.index[name]; ok {
			return i
		}
		i := len(p.names)
		p.names = append(p.names, name)
		p.islands = append(p.islands, nil)
		p.index[name] = i
		return i
	}

	// nextLiteral 返回紧跟在 i 之后的字面量（若有）。
	nextLiteral := func(i int) (model.Literal, bool) {
		if i+1 >= len(elements) {
			return nil, false
		}
		lit, ok := elements[i+1].(model.Literal)
		return lit, ok
	}

	for i, el := range elements {
		if consumed[i] {
			continue
		}
		switch e := el.(type) {

		case model.Literal:
			kind := OpLiteral
			if len(p.ops) == 0 {
				kind = OpPrefix
			}
			p.ops = append(p.ops, Op{Kind: kind, Lit: []byte(e.Content()), Slot: -1})

		case model.Island:
			slot := slotFor(e.Name())
			p.islands[slot] = e
			p.ops = append(p.ops, Op{Kind: OpIsland, Island: e, Slot: slot})
			p.raise(TierIsland)

		case model.Hole:
			slot := slotFor(e.Name())
			lit, hasLit := nextLiteral(i)
			pattern := e.Pattern()

			switch {
			case hasLit:
				consumed[i+1] = true // 定界符由本算子一并消费
				delim := []byte(lit.Content())
				if pattern != "" {
					p.ops = append(p.ops, Op{Kind: OpRegexUntil, Lit: delim, Slot: slot, Re: regexpOf(e)})
					p.raise(TierRegex)
				} else if len(delim) == 1 {
					p.ops = append(p.ops, Op{Kind: OpFindByte, Ch: delim[0], Slot: slot})
				} else {
					p.ops = append(p.ops, Op{Kind: OpFindLit, Lit: delim, Slot: slot})
				}

			case pattern != "":
				p.ops = append(p.ops, Op{Kind: OpRegex, Slot: slot, Re: regexpOf(e)})
				p.raise(TierRegex)

			case i == len(elements)-1:
				p.ops = append(p.ops, Op{Kind: OpRest, Slot: slot})

			default:
				// 无约束的洞后面既没有字面量定界符、也不是模板结尾 ——
				// 边界无从确定，这是**歧义模板**，必须在编译期拒绝而不是运行期猜。
				return nil, fmt.Errorf(
					"plan: ambiguous template: hole %q has no delimiter before %s; "+
						"add a literal separator or give it an expr constraint",
					e.Name(), describe(elements[i+1]))
			}

		default:
			return nil, fmt.Errorf("plan: element %q (tag %s) is not compilable", el.Name(), el.Tag())
		}
	}
	return p, nil
}

// raise 把层级提升到至少 t。
func (p *Plan) raise(t Tier) {
	if t > p.tier {
		p.tier = t
	}
}

// regexpOf 取出洞已编译好的正则。
func regexpOf(h model.Hole) *regexp.Regexp {
	type hasRegexp interface{ Regexp() *regexp.Regexp }
	if r, ok := h.(hasRegexp); ok {
		return r.Regexp()
	}
	return nil
}

func describe(el model.Element) string {
	if el.Name() != "" {
		return fmt.Sprintf("%s %q", el.Tag(), el.Name())
	}
	return string(el.Tag())
}
