package binding

import (
	"fmt"

	"github.com/huge-zhang/zdog-template/core/plan"
)

// Context 承载一次 parse 的全部绑定，或一份待 format 的目标数据。
//
// 它按槽位（plan.Slot）而非 map 存储 —— 热路径上没有哈希查找。
// Context 不是并发安全的；每个 goroutine 应持有自己的实例。
type Context struct {
	p   *plan.Plan
	src []byte // parse 得来的源文；scratch context 为 nil

	spans     []plan.Span
	overrides [][]byte // 非 nil 表示该槽位被显式覆盖
	values    []any    // 惰性解码结果
	decoded   []bool

	dirty bool
}

// NewContext 建一个空的 context，所有槽位都无出处。
// 用于 mapping 之后的目标侧数据。
func NewContext(p *plan.Plan) *Context {
	n := p.NumSlots()
	return &Context{
		p:         p,
		spans:     newNoSpans(n),
		overrides: make([][]byte, n),
		values:    make([]any, n),
		decoded:   make([]bool, n),
		dirty:     true, // 无源文，必然要走完整渲染
	}
}

// FromParse 由一次成功的 parse 构造 context。src 与 spans 的所有权移交给 context。
func FromParse(p *plan.Plan, src []byte, spans []plan.Span) *Context {
	n := p.NumSlots()
	return &Context{
		p:         p,
		src:       src,
		spans:     spans,
		overrides: make([][]byte, n),
		values:    make([]any, n),
		decoded:   make([]bool, n),
	}
}

func newNoSpans(n int) []plan.Span {
	s := make([]plan.Span, n)
	for i := range s {
		s[i] = plan.NoSpan
	}
	return s
}

// Plan 返回背后的执行计划。
func (c *Context) Plan() *plan.Plan { return c.p }

// Source 返回源文，scratch context 返回 nil。
func (c *Context) Source() []byte { return c.src }

// Dirty 报告是否有槽位被修改过。
//
// 这是定律 A 的性能红利（见 DESIGN.md §7）：未修改时 format 的正确答案
// 就是源文本身，直接 memcpy 即可，不必走一遍算子。
func (c *Context) Dirty() bool { return c.dirty }

// Names 返回全部绑定名，下标即槽位号。
func (c *Context) Names() []string { return c.p.Names() }

// Raw 返回槽位的原始字节。第二个返回值报告该槽位是否已填充。
func (c *Context) Raw(name string) ([]byte, bool) {
	slot, ok := c.p.Slot(name)
	if !ok {
		return nil, false
	}
	return c.RawAt(slot)
}

// RawAt 按槽位号取原始字节。
func (c *Context) RawAt(slot int) ([]byte, bool) {
	if slot < 0 || slot >= len(c.spans) {
		return nil, false
	}
	if ov := c.overrides[slot]; ov != nil {
		return ov, true
	}
	s := c.spans[slot]
	if !s.Valid() || c.src == nil {
		return nil, false
	}
	return c.src[s.Start:s.End], true
}

// Binding 返回一个完整绑定（值 + 出处）。
func (c *Context) Binding(name string) (Binding, bool) {
	slot, ok := c.p.Slot(name)
	if !ok {
		return Binding{}, false
	}
	raw, ok := c.RawAt(slot)
	if !ok {
		return Binding{}, false
	}
	b := Binding{Name: name, Raw: raw, Span: plan.NoSpan}
	// 只有未被覆盖的槽位才保留源文出处
	if c.overrides[slot] == nil && c.src != nil {
		b.Span = c.spans[slot]
	} else {
		b.Raw = raw
	}
	return b, true
}

// Value 返回槽位的解码值。
//
// 结构化岛在此处才真正解码，且只解码一次 —— 纯直通的字段永远不会付这个代价
// （实测解码 1838 ns vs 只扫边界 67 ns）。非岛槽位返回字符串。
func (c *Context) Value(name string) (any, error) {
	slot, ok := c.p.Slot(name)
	if !ok {
		return nil, fmt.Errorf("binding: unknown name %q", name)
	}
	if c.decoded[slot] {
		return c.values[slot], nil
	}
	raw, ok := c.RawAt(slot)
	if !ok {
		return nil, fmt.Errorf("binding: %q is not set", name)
	}
	var v any
	if island := c.p.Island(slot); island != nil {
		decoded, err := island.Decode(raw)
		if err != nil {
			return nil, err
		}
		v = decoded
	} else {
		v = string(raw)
	}
	c.values[slot] = v
	c.decoded[slot] = true
	return v, nil
}

// Set 覆盖一个槽位的字节值，并把 context 标记为 dirty。
func (c *Context) Set(name string, raw []byte) error {
	slot, ok := c.p.Slot(name)
	if !ok {
		return fmt.Errorf("binding: unknown name %q", name)
	}
	if raw == nil {
		raw = []byte{}
	}
	c.overrides[slot] = raw
	c.decoded[slot] = false
	c.values[slot] = nil
	c.dirty = true
	return nil
}

// SetString 是 Set 的字符串便利版本。
func (c *Context) SetString(name, v string) error { return c.Set(name, []byte(v)) }

// Values 按槽位顺序收集用于 format 的字节切片。
// 未填充的槽位为 nil，format 会因此失败。
func (c *Context) Values(dst [][]byte) [][]byte {
	n := c.p.NumSlots()
	if cap(dst) < n {
		dst = make([][]byte, n)
	}
	dst = dst[:n]
	for i := 0; i < n; i++ {
		raw, ok := c.RawAt(i)
		if !ok {
			dst[i] = nil
			continue
		}
		dst[i] = raw
	}
	return dst
}

// Missing 返回尚未填充的绑定名，供错误提示使用。
func (c *Context) Missing() []string {
	var out []string
	for i, name := range c.p.Names() {
		if _, ok := c.RawAt(i); !ok {
			out = append(out, name)
		}
	}
	return out
}

func (c *Context) String() string {
	return fmt.Sprintf("context{slots=%d dirty=%v}", c.p.NumSlots(), c.dirty)
}
