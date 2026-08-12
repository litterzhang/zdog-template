package pipeline

import (
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
func compileCodecs(defs map[string]json.RawMessage) (map[string]*shape.Codec, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	out := make(map[string]*shape.Codec, len(defs))
	for name, raw := range defs {
		c, err := shape.LoadCodec(raw)
		if err != nil {
			return nil, fmt.Errorf("pipeline: 字段 %q 的 shape 无效: %w", name, err)
		}
		out[name] = c
	}
	return out, nil
}

func buildRoute(src, tgt *plan.Plan, m map[string]string,
	codecs map[string]*shape.Codec, path string) (route, error) {
	r := route{scalars: make([]slotSource, tgt.NumSlots()), fast: true}

	for i, tName := range tgt.Names() {
		raw, mapped := m[tName]
		if !mapped {
			raw = tName // 同名直通
		}

		expr, err := mapping.Compile(raw)
		if err != nil {
			return route{}, fmt.Errorf("pipeline: 目标字段 %s%q 的映射 %q 无效: %w",
				path, tName, raw, err)
		}

		cod := codecs[tName]

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
		if mm, ok := m[info.Name]; ok {
			sName = mm
		}
		sSlot, ok := src.GroupSlot(sName)
		if !ok {
			return route{}, fmt.Errorf(
				"pipeline: target block %s%q maps to unknown source block %q",
				path, info.Name, sName)
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
				"pipeline: 目标字段 %s%q 的表达式 %q 引用了未知源字段 %q (available: %v)",
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
		v, err := mapping.Eval(ss.expr, &s.env)
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
