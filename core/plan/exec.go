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

// Parse 在 src 上执行计划，把各槽位的跨度写进 spans。
//
// spans 必须至少有 NumSlots() 个元素，由调用方复用以避免热路径分配。
// 无全局状态，同一个 Plan 可被多 goroutine 并发调用。
func (p *Plan) Parse(src []byte, spans []Span) bool {
	if len(spans) < len(p.names) {
		return false
	}
	for i := range spans[:len(p.names)] {
		spans[i] = NoSpan
	}

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
			spans[op.Slot] = Span{int32(pos), int32(pos + j)}
			pos += j + 1

		case OpFindLit:
			j := bytes.Index(src[pos:], op.Lit) // SIMD
			if j < 0 {
				return false
			}
			spans[op.Slot] = Span{int32(pos), int32(pos + j)}
			pos += j + len(op.Lit)

		case OpRest:
			spans[op.Slot] = Span{int32(pos), int32(len(src))}
			pos = len(src)

		case OpRegex:
			loc := op.Re.FindIndex(src[pos:])
			if loc == nil {
				return false
			}
			spans[op.Slot] = Span{int32(pos), int32(pos + loc[1])}
			pos += loc[1]

		case OpRegexUntil:
			// best-match：在定界符的各次出现中取**最早的、且正则能完整覆盖跨度**的那个。
			// 例：模板 ${re|expr=\d+}345 对 "123345" 应得到 "123" 而非 "123345"。
			end, ok := p.matchUntil(src, pos, op)
			if !ok {
				return false
			}
			spans[op.Slot] = Span{int32(pos), int32(end)}
			pos = end + len(op.Lit)

		case OpIsland:
			end, ok := op.Island.Scan(src, pos)
			if !ok {
				return false
			}
			spans[op.Slot] = Span{int32(pos), int32(end)}
			pos = end
		}
	}
	// 模板必须完整覆盖输入 —— 这是定律 A 的前提：
	// 留下未消费的尾巴就意味着 format 无法还原原文。
	return pos == len(src)
}

// matchUntil 实现 OpRegexUntil 的 best-match 搜索。
func (p *Plan) matchUntil(src []byte, pos int, op *Op) (end int, ok bool) {
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

// Format 按计划把各槽位的值渲染成文本，追加到 dst。
//
// values[slot] 为该槽位要写出的字节。定律 A 的 replay 快路径由上层
// （engine）负责：未修改时直接返回原文，根本不会走到这里。
func (p *Plan) Format(dst []byte, values [][]byte) ([]byte, bool) {
	if len(values) < len(p.names) {
		return dst, false
	}
	for k := range p.ops {
		op := &p.ops[k]
		switch op.Kind {
		case OpPrefix, OpLiteral:
			dst = append(dst, op.Lit...)
		default:
			v := values[op.Slot]
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
