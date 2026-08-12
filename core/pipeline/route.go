package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/huge-zhang/zdog-template/core/mapping"
	"github.com/huge-zhang/zdog-template/core/plan"
	"github.com/huge-zhang/zdog-template/core/shape"
)

// slotSource 描述目标某个标量槽位的取值方式。
//
// 关键的性能分叉（见 DESIGN.md §6）：
//
//	srcSlot >= 0 —— **快路径**，直接引用源槽位的字节，零拷贝
//	srcSlot <  0 —— 慢路径，求值表达式并序列化到 arena
//
// 裸字段名（`lv`）总是走快路径；只有真正含路径/函数的表达式才付物化代价。
type slotSource struct {
	srcSlot int
	expr    mapping.Expr
	// codec 非 nil 时按 shape 序列化，而不是用 mapping 的默认规则。
	codec *shape.Codec
}

// route 描述目标计划的每个槽位从源计划的哪个槽位取值。
// 重复块使得路由必须递归。
type route struct {
	scalars []slotSource
	groups  []groupRoute
	// fast 为 true 表示本层全部走快路径，可跳过 env 构建。
	fast bool
}

type groupRoute struct {
	srcGroup int
	sub      route
}

// buildRoute 按名字（经 mapping 重映射）把目标计划接到源计划上。
// codecTable 的键既可以是裸字段名，也可以是路径限定名（`xs[].n`）。
// 值为 nil 表示"该路径上显式不套 shape"（配置里写 null）。
type codecTable map[string]*shape.Codec

func compileCodecs(defs map[string]json.RawMessage) (codecTable, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	out := make(codecTable, len(defs))
	for name, raw := range defs {
		// 显式 null：在这条路径上关掉 shape。
		// 有了它才能表达"只格式化外层的 n，块内的 n 保持原样"。
		if string(bytes.TrimSpace(raw)) == "null" {
			out[name] = nil
			continue
		}
		c, err := shape.LoadCodec(raw)
		if err != nil {
			return nil, fmt.Errorf("pipeline: invalid shape for field %q: %w", name, err)
		}
		out[name] = c
	}
	return out, nil
}

// lookup 先试路径限定名，再回退裸名。
//
// 于是 `{"n": …, "xs[].n": …}` 能分别作用于外层与块内；只写 `n` 时
// 仍作用于所有层级（向后兼容，也是大多数场景想要的）。
func (t codecTable) lookup(path, name string) (*shape.Codec, bool) {
	if t == nil {
		return nil, false
	}
	if c, ok := t[path+name]; ok {
		return c, true
	}
	c, ok := t[name]
	return c, ok
}

func buildRoute(src, tgt *plan.Plan, m map[string]string,
	codecs codecTable, path string) (route, error) {
	r := route{scalars: make([]slotSource, tgt.NumSlots()), fast: true}

	for i, tName := range tgt.Names() {
		// 与 shape 同理：路径限定名优先，再回退裸名。
		raw, mapped := m[path+tName]
		if !mapped {
			raw, mapped = m[tName]
		}
		if !mapped {
			raw = tName // 同名直通
		}

		expr, err := mapping.Compile(raw)
		if err != nil {
			return route{}, fmt.Errorf("pipeline: invalid mapping %[3]q for target field %[1]s%[2]q: %[4]w",
				path, tName, raw, err)
		}

		cod, _ := codecs.lookup(path, tName)

		// 裸字段名 -> 零拷贝快路径。声明了 shape 的字段不走这里：
		// 按类型格式化意味着输出不再等于源文字节。
		if name, bare := mapping.IsBareField(expr); bare && cod == nil {
			slot, ok := src.Slot(name)
			if !ok {
				return route{}, fmt.Errorf(
					"pipeline: target field %s%q maps to unknown source field %q (available: %v)",
					path, tName, name, src.Names())
			}
			r.scalars[i] = slotSource{srcSlot: slot}
			continue
		}

		// 表达式 -> 慢路径。引用的字段在编译期就校验存在性，
		// 免得每一行都在运行期才发现拼错了字段名。
		if err := validateRefs(src, expr, path, tName, raw); err != nil {
			return route{}, err
		}
		r.scalars[i] = slotSource{srcSlot: -1, expr: expr, codec: cod}
		r.fast = false
	}

	for g := 0; g < tgt.NumGroups(); g++ {
		info := tgt.Group(g)
		sName := info.Name
		if mm, ok := m[path+info.Name]; ok {
			sName = mm
		} else if mm, ok := m[info.Name]; ok {
			sName = mm
		}
		sSlot, ok := src.GroupSlot(sName)
		if !ok {
			// 组只支持同名/重命名，不支持表达式。把这一点说清楚，
			// 否则用户看到 "unknown source block" 会以为是名字打错了。
			if _, err := mapping.Compile(sName); err == nil && !isPlainName(sName) {
				return route{}, fmt.Errorf(
					"pipeline: target block %s%q maps to %q, but repeat blocks only "+
						"support a plain source block name (expressions are not supported "+
						"on blocks; available: %v)",
					path, info.Name, sName, blockNames(src))
			}
			return route{}, fmt.Errorf(
				"pipeline: target block %s%q maps to unknown source block %q (available: %v)",
				path, info.Name, sName, blockNames(src))
		}
		sub, err := buildRoute(src.Group(sSlot).Sub, info.Sub, m, codecs, path+info.Name+"[].")
		if err != nil {
			return route{}, err
		}
		if !sub.fast {
			r.fast = false
		}
		r.groups = append(r.groups, groupRoute{srcGroup: sSlot, sub: sub})
	}
	return r, nil
}

// validateRefs 在编译期校验表达式引用的字段都存在。
func validateRefs(src *plan.Plan, e mapping.Expr, path, tName, raw string) error {
	for _, name := range mapping.Refs(e) {
		if _, ok := src.Slot(name); !ok {
			return fmt.Errorf(
				"pipeline: expression %q for target field %s%q references unknown source field %q (available: %v)",
				path, tName, raw, name, src.Names())
		}
	}
	return nil
}

// env 是表达式求值的字段访问环境。它是惰性的：只有被表达式引用到的槽位
// 才会被读取，结构化岛也只在此时才解码。
type env struct {
	p     *plan.Plan
	res   *plan.Result
	src   []byte
	cache []any
	done  []bool
}

func (e *env) reset(p *plan.Plan, res *plan.Result, src []byte) {
	e.p, e.res, e.src = p, res, src
	n := p.NumSlots()
	if cap(e.cache) < n {
		e.cache = make([]any, n)
		e.done = make([]bool, n)
	}
	e.cache, e.done = e.cache[:n], e.done[:n]
	for i := range e.done {
		e.done[i] = false
	}
}

// Value 实现 mapping.Env。
func (e *env) Value(name string) (any, bool, error) {
	slot, ok := e.p.Slot(name)
	if !ok {
		return nil, false, nil
	}
	if e.done[slot] {
		return e.cache[slot], true, nil
	}
	sp := e.res.Abs(e.res.Spans[slot])
	if !sp.Valid() {
		return nil, false, nil
	}
	raw := e.src[sp.Start:sp.End]

	var v any
	if island := e.p.Island(slot); island != nil {
		decoded, err := island.Decode(raw)
		if err != nil {
			return nil, false, err
		}
		v = decoded
	} else {
		v = bytesAsString(raw)
	}
	e.cache[slot], e.done[slot] = v, true
	return v, true, nil
}

// bytesAsString 返回共享底层数组的字符串视图，避免每个被引用字段都拷一份。
//
// 安全性论证：raw 指向调用方传入的输入缓冲，在整个 Transform 调用期间不被修改；
// 由此产生的字符串只存活于本行的求值过程 —— 它要么被 mapping.Serialize 拷进
// arena，要么随 env 的每行复位一起丢弃，不会逃逸出调用。
func bytesAsString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

var _ mapping.Env = (*env)(nil)

// fill 按路由把源解析结果搬进目标渲染输入。
//
// 快路径上目标的每个值都是源文的子切片，直到最后写出才发生一次拷贝。
// 表达式的结果写进 arena（每行复位），因此稳态下同样零分配。
func (r *route) fill(d *plan.Data, res *plan.Result, src []byte, s *Scratch, p *plan.Plan) bool {
	if cap(d.Values) < len(r.scalars) {
		d.Values = make([][]byte, len(r.scalars))
	}
	d.Values = d.Values[:len(r.scalars)]

	if !r.fast {
		s.env.reset(p, res, src)
	}

	for i := range r.scalars {
		ss := &r.scalars[i]
		if ss.srcSlot >= 0 {
			sp := res.Abs(res.Spans[ss.srcSlot])
			if !sp.Valid() {
				return false
			}
			d.Values[i] = src[sp.Start:sp.End]
			continue
		}
		v, err := s.eval.Eval(ss.expr, &s.env)
		if err != nil {
			return false
		}
		start := len(s.arena)
		var out []byte
		if ss.codec != nil {
			out, err = ss.codec.Encode(s.arena, v)
		} else {
			out, err = mapping.Serialize(s.arena, v)
		}
		if err != nil {
			return false
		}
		s.arena = out
		d.Values[i] = s.arena[start:len(s.arena):len(s.arena)]
	}

	if cap(d.Groups) < len(r.groups) {
		d.Groups = make([]plan.GroupData, len(r.groups))
	}
	d.Groups = d.Groups[:len(r.groups)]
	for g := range r.groups {
		gr := &r.groups[g]
		items := res.Groups[gr.srcGroup].Items
		d.Groups[g].Items = d.Groups[g].Items[:0]
		subPlan := p.Group(gr.srcGroup).Sub
		for i := range items {
			d.Groups[g].Items = plan.GrowData(d.Groups[g].Items)
			if !gr.sub.fill(&d.Groups[g].Items[len(d.Groups[g].Items)-1],
				&items[i], src, s, subPlan) {
				return false
			}
		}
	}
	return true
}

// isPlainName 报告 s 是否就是一个裸标识符（没有路径、下标、函数调用）。
func isPlainName(s string) bool {
	e, err := mapping.Compile(s)
	if err != nil {
		return false
	}
	_, bare := mapping.IsBareField(e)
	return bare
}

func blockNames(p *plan.Plan) []string {
	gs := p.Groups()
	out := make([]string, len(gs))
	for i := range gs {
		out[i] = gs[i].Name
	}
	return out
}

// mapEnv 让映射表达式能在一个已解码的 JSON 对象上求值。
//
// parse 侧的 env 是惰性的（按需从源文切片、按需解码岛）；这里的输入已经是
// 物化好的值，所以直接查表即可。format 不在热路径上，简单优先。
type mapEnv struct{ obj map[string]any }

func (e mapEnv) Value(name string) (any, bool, error) {
	v, ok := e.obj[name]
	return v, ok, nil
}

var _ mapping.Env = mapEnv{}

// fillFromMap 按路由把一个 JSON 对象搬进目标渲染输入。
//
// 与 fill 的差别只在数据来源：fill 从源文切片（零拷贝），这里从已解码的
// map 取值。**路由本身是同一套** —— 于是 format 与 transform 走的是同一条
// mapping 语义，`parse | format` 才能等价于 `transform`。
func (r *route) fillFromMap(d *plan.Data, obj map[string]any, s *Scratch, srcPlan *plan.Plan) error {
	if cap(d.Values) < len(r.scalars) {
		d.Values = make([][]byte, len(r.scalars))
	}
	d.Values = d.Values[:len(r.scalars)]
	env := mapEnv{obj: obj}

	for i := range r.scalars {
		ss := &r.scalars[i]

		var v any
		if ss.srcSlot >= 0 {
			// 直连源字段：从 map 里按源字段名取
			name := srcPlan.Names()[ss.srcSlot]
			raw, ok := obj[name]
			if !ok {
				return fmt.Errorf("missing source field %q", name)
			}
			v = raw
		} else {
			var err error
			if v, err = s.eval.Eval(ss.expr, env); err != nil {
				return err
			}
		}

		start := len(s.arena)
		var out []byte
		var err error
		if ss.codec != nil {
			out, err = ss.codec.Encode(s.arena, v)
		} else {
			out, err = mapping.Serialize(s.arena, v)
		}
		if err != nil {
			return err
		}
		s.arena = out
		d.Values[i] = s.arena[start:len(s.arena):len(s.arena)]
	}

	if cap(d.Groups) < len(r.groups) {
		d.Groups = make([]plan.GroupData, len(r.groups))
	}
	d.Groups = d.Groups[:len(r.groups)]
	for g := range r.groups {
		gr := &r.groups[g]
		name := srcPlan.Group(gr.srcGroup).Name
		d.Groups[g].Items = d.Groups[g].Items[:0]

		raw, ok := obj[name]
		if !ok {
			continue // 缺失的块渲染成零次迭代；AllowEmpty 会在 Format 里裁决
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("block %q expects an array, got %T", name, raw)
		}
		subPlan := srcPlan.Group(gr.srcGroup).Sub
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("block %q items must be objects, got %T", name, it)
			}
			d.Groups[g].Items = plan.GrowData(d.Groups[g].Items)
			if err := gr.sub.fillFromMap(
				&d.Groups[g].Items[len(d.Groups[g].Items)-1], m, s, subPlan); err != nil {
				return err
			}
		}
	}
	return nil
}
