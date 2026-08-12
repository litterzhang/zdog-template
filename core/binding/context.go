package binding

import (
	"fmt"

	"github.com/litterzhang/zdog-template/core/plan"
)

// Context 承载一次 parse 的全部绑定，或一份待 format 的目标数据。
//
// 它按槽位（plan.Slot）而非 map 存储 —— 热路径上没有哈希查找。
// 重复块的每次迭代是一个子 Context，共享最外层源文，通过 Result.Base 定位。
//
// Context 不是并发安全的；每个 goroutine 应持有自己的实例。
type Context struct {
	p    *plan.Plan
	src  []byte // 最外层源文；scratch context 为 nil
	res  *plan.Result
	root *Context // nil 表示自己就是根

	overrides [][]byte
	values    []any
	decoded   []bool
	subs      [][]*Context // subs[groupSlot][i] —— 惰性构建

	dirty bool
}

func newContext(p *plan.Plan, src []byte, res *plan.Result, root *Context) *Context {
	n := p.NumSlots()
	c := &Context{
		p: p, src: src, res: res, root: root,
		overrides: make([][]byte, n),
		values:    make([]any, n),
		decoded:   make([]bool, n),
	}
	if g := p.NumGroups(); g > 0 {
		c.subs = make([][]*Context, g)
	}
	return c
}

// NewContext 建一个空的 context，所有槽位都无出处。
// 用于 mapping 之后的目标侧数据。
func NewContext(p *plan.Plan) *Context {
	c := newContext(p, nil, p.NewResult(), nil)
	c.dirty = true // 无源文，必然要走完整渲染
	return c
}

// FromParse 由一次成功的 parse 构造 context。src 与 res 的所有权移交给 context。
func FromParse(p *plan.Plan, src []byte, res *plan.Result) *Context {
	return newContext(p, src, res, nil)
}

// Plan 返回背后的执行计划。
func (c *Context) Plan() *plan.Plan { return c.p }

// Source 返回最外层源文，scratch context 返回 nil。
func (c *Context) Source() []byte { return c.src }

// Result 返回底层解析结果。
func (c *Context) Result() *plan.Result { return c.res }

// Dirty 报告是否有槽位被修改过。
//
// 这是定律 A 的性能红利（见 DESIGN.md §7）：未修改时 format 的正确答案
// 就是源文本身，直接 memcpy 即可，不必走一遍算子。
func (c *Context) Dirty() bool {
	if c.root != nil {
		return c.root.Dirty()
	}
	return c.dirty
}

func (c *Context) markDirty() {
	if c.root != nil {
		c.root.markDirty()
		return
	}
	c.dirty = true
}

// Names 返回全部标量绑定名，下标即槽位号。
func (c *Context) Names() []string { return c.p.Names() }

// GroupNames 返回全部重复块名。
func (c *Context) GroupNames() []string {
	gs := c.p.Groups()
	out := make([]string, len(gs))
	for i := range gs {
		out[i] = gs[i].Name
	}
	return out
}

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
	if slot < 0 || slot >= len(c.overrides) {
		return nil, false
	}
	if ov := c.overrides[slot]; ov != nil {
		return ov, true
	}
	if c.src == nil {
		return nil, false
	}
	s := c.res.Abs(c.res.Spans[slot])
	if !s.Valid() {
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
	if c.overrides[slot] == nil && c.src != nil {
		b.Span = c.res.Abs(c.res.Spans[slot])
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
	c.markDirty()
	return nil
}

// SetString 是 Set 的字符串便利版本。
func (c *Context) SetString(name, v string) error { return c.Set(name, []byte(v)) }

// ---- 重复块 ----

// GroupLen 返回某个重复块的迭代次数。
func (c *Context) GroupLen(name string) (int, bool) {
	slot, ok := c.p.GroupSlot(name)
	if !ok {
		return 0, false
	}
	return len(c.res.Groups[slot].Items), true
}

// GroupItem 返回重复块第 i 次迭代的子 context。
func (c *Context) GroupItem(name string, i int) (*Context, bool) {
	slot, ok := c.p.GroupSlot(name)
	if !ok {
		return nil, false
	}
	items := c.res.Groups[slot].Items
	if i < 0 || i >= len(items) {
		return nil, false
	}
	c.growSubs(slot, len(items))
	if c.subs[slot][i] == nil {
		root := c.root
		if root == nil {
			root = c
		}
		c.subs[slot][i] = newContext(c.p.Group(slot).Sub, c.src, &items[i], root)
	}
	return c.subs[slot][i], true
}

// AppendGroupItem 给重复块追加一次迭代，返回可写入的子 context。
func (c *Context) AppendGroupItem(name string) (*Context, error) {
	slot, ok := c.p.GroupSlot(name)
	if !ok {
		return nil, fmt.Errorf("binding: unknown group %q", name)
	}
	sub := c.p.Group(slot).Sub
	c.res.Groups[slot].Items = plan.GrowResults(c.res.Groups[slot].Items)
	items := c.res.Groups[slot].Items
	sub.ResetResult(&items[len(items)-1])
	c.growSubs(slot, len(items))

	root := c.root
	if root == nil {
		root = c
	}
	// 追加的迭代没有源文出处，全部走覆盖写入
	item := newContext(sub, nil, &items[len(items)-1], root)
	c.subs[slot][len(items)-1] = item
	c.markDirty()
	return item, nil
}

func (c *Context) growSubs(slot, n int) {
	for len(c.subs[slot]) < n {
		c.subs[slot] = append(c.subs[slot], nil)
	}
}

// ---- 渲染 ----

// Data 把 context 转成渲染输入。
func (c *Context) Data(dst *plan.Data) *plan.Data {
	if dst == nil {
		dst = c.p.NewData()
	}
	n := c.p.NumSlots()
	if cap(dst.Values) < n {
		dst.Values = make([][]byte, n)
	}
	dst.Values = dst.Values[:n]
	for i := 0; i < n; i++ {
		raw, ok := c.RawAt(i)
		if !ok {
			dst.Values[i] = nil
			continue
		}
		dst.Values[i] = raw
	}

	ng := c.p.NumGroups()
	if cap(dst.Groups) < ng {
		dst.Groups = make([]plan.GroupData, ng)
	}
	dst.Groups = dst.Groups[:ng]
	for g := 0; g < ng; g++ {
		name := c.p.Group(g).Name
		count := len(c.res.Groups[g].Items)
		dst.Groups[g].Items = dst.Groups[g].Items[:0]
		for i := 0; i < count; i++ {
			item, ok := c.GroupItem(name, i)
			if !ok {
				continue
			}
			dst.Groups[g].Items = plan.GrowData(dst.Groups[g].Items)
			item.Data(&dst.Groups[g].Items[len(dst.Groups[g].Items)-1])
		}
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
	for g := 0; g < c.p.NumGroups(); g++ {
		info := c.p.Group(g)
		for i := range c.res.Groups[g].Items {
			if item, ok := c.GroupItem(info.Name, i); ok {
				for _, m := range item.Missing() {
					out = append(out, fmt.Sprintf("%s[%d].%s", info.Name, i, m))
				}
			}
		}
	}
	return out
}

func (c *Context) String() string {
	return fmt.Sprintf("context{slots=%d groups=%d dirty=%v}",
		c.p.NumSlots(), c.p.NumGroups(), c.Dirty())
}
