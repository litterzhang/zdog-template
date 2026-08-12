package plan

import (
	"bytes"
)

// Span 是一段绑定在源文中的字节区间。
type Span struct{ Start, End int32 }

// Len 返回跨度长度。
func (s Span) Len() int { return int(s.End - s.Start) }

// Valid 报告该槽位是否被填充过。
func (s Span) Valid() bool { return s.End >= s.Start && s.Start >= 0 }

// NoSpan 表示未填充。
var NoSpan = Span{Start: -1, End: -1}

// Parse 在 src 上执行计划，把结果写进 r。
//
// r 由调用方复用以避免热路径分配。无重复块的模板全程零分配。
// 无全局状态，同一个 Plan 可被多 goroutine 并发调用。
//
// 注意跨度是**相对 src 起点**的偏移；重复块内各次迭代的跨度相对该迭代自身的
// 字节片段，因此读取时必须逐层带上父级的基址（见 binding.Context）。
func (p *Plan) Parse(src []byte, r *Result) bool {
	p.ensure(r)
	if p.parseFlat(src, r) {
		return true
	}
	if !p.backtrack {
		return false
	}
	// 扁平引擎在每个定界点取首次匹配；它成功时回溯引擎的第一条路径与之相同，
	// 所以只有扁平引擎失败的输入才需要付回溯代价。
	p.ensure(r)
	return p.parseBacktrack(src, r)
}

// parseFlat 是扁平快路径：每个定界点取首次匹配，零分配、无函数调用。
func (p *Plan) parseFlat(src []byte, r *Result) bool {
	pos := 0
	for k := range p.ops {
		op := &p.ops[k]
		switch op.Kind {

		case OpPrefix, OpLiteral:
			if !bytes.HasPrefix(src[pos:], op.Lit) {
				return false
			}
			pos += len(op.Lit)

		case OpFindByte:
			j := bytes.IndexByte(src[pos:], op.Ch) // memchr
			if j < 0 {
				return false
			}
			r.Spans[op.Slot] = Span{int32(pos), int32(pos + j)}
			pos += j + 1

		case OpFindLit:
			j := bytes.Index(src[pos:], op.Lit) // SIMD
			if j < 0 {
				return false
			}
			r.Spans[op.Slot] = Span{int32(pos), int32(pos + j)}
			pos += j + len(op.Lit)

		case OpRest:
			r.Spans[op.Slot] = Span{int32(pos), int32(len(src))}
			pos = len(src)

		case OpRegex:
			loc := op.Re.FindIndex(src[pos:])
			if loc == nil {
				return false
			}
			r.Spans[op.Slot] = Span{int32(pos), int32(pos + loc[1])}
			pos += loc[1]

		case OpRegexUntil:
			// best-match：在定界符的各次出现中取**最早的、且正则能完整覆盖跨度**的那个。
			// 例：模板 ${re|expr=\d+}345 对 "123345" 应得到 "123" 而非 "123345"。
			end, ok := matchUntil(src, pos, op)
			if !ok {
				return false
			}
			r.Spans[op.Slot] = Span{int32(pos), int32(end)}
			pos = end + len(op.Lit)

		case OpIsland:
			end, ok := op.Island.Scan(src, pos)
			if !ok {
				return false
			}
			r.Spans[op.Slot] = Span{int32(pos), int32(end)}
			pos = end

		case OpEach:
			g := &p.groups[op.Slot]
			// 块的范围由后继字面量划定，与洞的定界同理，只是抬高了一层。
			regionEnd := len(src)
			next := len(src)
			if len(op.Lit) > 0 {
				j := bytes.Index(src[pos:], op.Lit)
				if j < 0 {
					return false
				}
				regionEnd = pos + j
				next = regionEnd + len(op.Lit)
			}
			if !parseGroup(g, src[pos:regionEnd], &r.Groups[op.Slot], r.Base+int32(pos)) {
				return false
			}
			pos = next
		}
	}
	// 模板必须完整覆盖输入 —— 这是定律 A 的前提：
	// 留下未消费的尾巴就意味着 format 无法还原原文。
	return pos == len(src)
}

// parseGroup 把 region 按分隔符切成若干次迭代，每次迭代必须被子计划完整消费。
//
// 切分不是无脑 Split：分隔符可能出现在某次迭代的内容里，因此对每个候选边界
// 都尝试用子计划解析，失败则延伸到下一个分隔符。这与 OpRegexUntil 的 best-match
// 是同一个思路 —— 边界由"能否被后续消费"来裁决。
func parseGroup(g *GroupInfo, region []byte, out *GroupResult, base int32) bool {
	out.Items = out.Items[:0]
	if len(region) == 0 {
		return g.AllowEmpty
	}
	pos := 0
	for {
		searchFrom := pos
		matched := false
		for {
			j := bytes.Index(region[searchFrom:], g.Sep)
			last := j < 0
			var item []byte
			var nextPos int
			if last {
				item, nextPos = region[pos:], -1
			} else {
				item, nextPos = region[pos:searchFrom+j], searchFrom+j+len(g.Sep)
			}

			out.Items = GrowResults(out.Items)
			ir := &out.Items[len(out.Items)-1]
			ir.Base = base + int32(pos)
			if g.Sub.Parse(item, ir) {
				matched = true
				if last {
					return true
				}
				pos = nextPos
				break
			}
			out.Items = out.Items[:len(out.Items)-1]
			if last {
				return false
			}
			searchFrom = searchFrom + j + 1
			if searchFrom > len(region) {
				return false
			}
		}
		if !matched {
			return false
		}
	}
}

// matchUntil 实现 OpRegexUntil 的 best-match 搜索。
func matchUntil(src []byte, pos int, op *Op) (end int, ok bool) {
	rest := src[pos:]
	off := 0
	for {
		j := bytes.Index(rest[off:], op.Lit)
		if j < 0 {
			return 0, false
		}
		cut := off + j
		if loc := op.Re.FindIndex(rest[:cut]); loc != nil && loc[0] == 0 && loc[1] == cut {
			return pos + cut, true
		}
		off = cut + 1 // 试下一次出现
		if off > len(rest) {
			return 0, false
		}
	}
}

// Format 按计划把数据渲染成文本，追加到 dst。
//
// 定律 A 的 replay 快路径由上层（engine）负责：未修改时直接返回原文，
// 根本不会走到这里。
func (p *Plan) Format(dst []byte, d *Data) ([]byte, bool) {
	if len(d.Values) < len(p.names) || len(d.Groups) < len(p.groups) {
		return dst, false
	}
	for k := range p.ops {
		op := &p.ops[k]
		switch op.Kind {
		case OpPrefix, OpLiteral:
			dst = append(dst, op.Lit...)

		case OpEach:
			g := &p.groups[op.Slot]
			items := d.Groups[op.Slot].Items
			if len(items) == 0 && !g.AllowEmpty {
				return dst, false
			}
			for i := range items {
				if i > 0 {
					dst = append(dst, g.Sep...)
				}
				var ok bool
				if dst, ok = g.Sub.Format(dst, &items[i]); !ok {
					return dst, false
				}
			}
			dst = append(dst, op.Lit...) // 块的终止字面量

		default:
			v := d.Values[op.Slot]
			if v == nil {
				return dst, false
			}
			dst = append(dst, v...)
			// 洞的定界符是模板的一部分，必须写回
			if op.Kind == OpFindByte {
				dst = append(dst, op.Ch)
			} else if op.Kind == OpFindLit || op.Kind == OpRegexUntil {
				dst = append(dst, op.Lit...)
			}
		}
	}
	return dst, true
}
