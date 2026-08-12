package plan

import "bytes"

// 回溯引擎。
//
// 扁平引擎（exec.go 的 Parse）在每个定界点取**首次匹配**。绝大多数模板下
// 这就是正确答案，且零分配、无函数调用。但它会漏掉这类情况：
//
//	模板 ${a}.${re|name=n,expr=\d+}   输入 "x.y.42"
//	  首个 '.' 在 index 1 -> a="x"，剩下 "y.42" 匹配不上 \d+ -> 失败
//	  正解是 a="x.y"，n="42"
//
//	模板 ${a}/${json|name=p}          输入 "a/b/{\"x\":1}"
//	  首个 '/' -> 剩下 "b/{...}" 不是 JSON -> 失败
//	  正解是 a="a/b"
//
// 所以需要跨算子回溯。代价由**编译期分析**限定：只有"选择点之后还跟着可能
// 失败的算子"的计划才启用，其余仍走扁平快路径（见 needsBacktrack）。

// choiceProducing 报告该算子是否会产生多个候选。
func choiceProducing(k OpKind) bool {
	switch k {
	case OpFindByte, OpFindLit, OpRegexUntil, OpEach:
		return true
	}
	return false
}

// hardFallible 报告该算子的失败**无法**靠"把前面的边界推后"来修复。
//
// Find 类算子刻意不算在内，理由是严谨的：候选按位置**递增**枚举，因此所有
// 备选都让起点更靠后、剩余输入更短。若 Find 因"剩下的输入里没有定界符"而失败，
// 更短的输入同样没有 —— 推后不可能把失败变成成功。据此可以安全地把
// `${a} ${b} ${c}` 这类纯字面量链排除在回溯之外，保住扁平快路径。
func hardFallible(k OpKind) bool {
	switch k {
	case OpLiteral, OpRegex, OpRegexUntil, OpIsland, OpEach:
		return true
	}
	return false
}

// needsBacktrack 判断计划是否需要回溯引擎。
func needsBacktrack(ops []Op) bool {
	if len(ops) == 0 {
		return false
	}
	// 末尾若不是 OpRest，"必须吃满输入"这个终检本身就是可失败点。
	// 例：模板 `${a}!` 输入 "x!y!" —— 取首个 '!' 会剩下 "y!" 没吃完，
	// 必须回溯到第二个 '!' 才有解。
	endFallible := ops[len(ops)-1].Kind != OpRest

	for i := range ops {
		if !choiceProducing(ops[i].Kind) {
			continue
		}
		if endFallible {
			return true
		}
		for j := i + 1; j < len(ops); j++ {
			if hardFallible(ops[j].Kind) {
				return true
			}
		}
	}
	return false
}

// NeedsBacktrack 报告该计划是否启用了回溯引擎。
func (p *Plan) NeedsBacktrack() bool { return p.backtrack }

// visitor 在每找到一个完整解时被调用。返回 false 表示停止搜索。
type visitor func() bool

// memoThreshold 是启用记忆化的步数门槛。
//
// 记忆化表本身要分配，而绝大多数搜索几步就结束了（尤其是批量日志里
// "这行压根不匹配"的情形）。所以先裸跑，确认真的在thrash了才建表 ——
// 常见路径一分钱不花，病态路径才付这笔管理费。
const memoThreshold = 64

// searcher 是一次回溯搜索的状态。
type searcher struct {
	p     *Plan
	src   []byte
	r     *Result
	visit visitor

	// visit 为 nil 表示"找到第一个解即停"。
	// 这是最常见的用法（Parse），单独走一条路避免闭包分配 ——
	// 批量日志里每一行不匹配的输入都要走回溯，那点分配会累积。
	found bool

	hits  int // visit 被调用的次数，用于判断某个子树是否真的无解
	steps int
	// memo 记录**已确认无解**的 (opIdx, pos)。惰性创建。
	//
	// 只记失败、不记成功：成功需要连同写入的 span 一起复现，而失败不需要 ——
	// 从 (opIdx, pos) 出发能否成功，只取决于 opIdx 之后的算子与 src[pos:]，
	// 与"怎么走到这里的"无关（后续算子只写 Spans，从不读）。
	memo map[uint64]struct{}
}

func memoKey(opIdx, pos int) uint64 {
	return uint64(uint32(opIdx))<<32 | uint64(uint32(pos))
}

// run 是带记忆化的 search 入口。
func (s *searcher) run(opIdx, pos int) bool {
	s.steps++
	if s.memo == nil && s.steps >= memoThreshold {
		s.memo = make(map[uint64]struct{}, 256)
	}
	var key uint64
	if s.memo != nil {
		key = memoKey(opIdx, pos)
		if _, dead := s.memo[key]; dead {
			return true // 已知无解，直接换下一条路
		}
	}

	before := s.hits
	cont := s.step(opIdx, pos)

	// cont 为 true 表示这棵子树被**完整**探索过（不是被 visit 叫停的）；
	// hits 没涨说明一个解也没找到 —— 这才能安全地记为死路。
	if cont && s.hits == before && s.memo != nil {
		s.memo[key] = struct{}{}
	}
	return cont
}

// step 从 opIdx/pos 开始深度优先搜索，每找到一个解就调用 visit。
// 返回 false 表示 visit 要求提前停止。
func (s *searcher) step(opIdx, pos int) bool {
	if opIdx == len(s.p.ops) {
		if pos != len(s.src) {
			return true // 不是解，继续找
		}
		s.hits++
		if s.visit == nil {
			s.found = true
			return false // 找到即停
		}
		return s.visit()
	}
	op := &s.p.ops[opIdx]
	src, r := s.src, s.r

	switch op.Kind {
	case OpPrefix, OpLiteral:
		if !bytes.HasPrefix(src[pos:], op.Lit) {
			return true
		}
		return s.run(opIdx+1, pos+len(op.Lit))

	case OpRest:
		r.Spans[op.Slot] = Span{int32(pos), int32(len(src))}
		return s.run(opIdx+1, len(src))

	case OpRegex:
		loc := op.Re.FindIndex(src[pos:])
		if loc == nil {
			return true
		}
		r.Spans[op.Slot] = Span{int32(pos), int32(pos + loc[1])}
		return s.run(opIdx+1, pos+loc[1])

	case OpIsland:
		end, ok := op.Island.Scan(src, pos)
		if !ok {
			return true
		}
		r.Spans[op.Slot] = Span{int32(pos), int32(end)}
		return s.run(opIdx+1, end)

	case OpFindByte:
		off := 0
		for {
			j := bytes.IndexByte(src[pos+off:], op.Ch)
			if j < 0 {
				return true
			}
			end := pos + off + j
			r.Spans[op.Slot] = Span{int32(pos), int32(end)}
			if !s.run(opIdx+1, end+1) {
				return false
			}
			off = end - pos + 1
		}

	case OpFindLit:
		off := 0
		for {
			j := bytes.Index(src[pos+off:], op.Lit)
			if j < 0 {
				return true
			}
			end := pos + off + j
			r.Spans[op.Slot] = Span{int32(pos), int32(end)}
			if !s.run(opIdx+1, end+len(op.Lit)) {
				return false
			}
			off = end - pos + 1
		}

	case OpRegexUntil:
		off := 0
		for {
			j := bytes.Index(src[pos+off:], op.Lit)
			if j < 0 {
				return true
			}
			cut := pos + off + j
			if loc := op.Re.FindIndex(src[pos:cut]); loc != nil && loc[0] == 0 && loc[1] == cut-pos {
				r.Spans[op.Slot] = Span{int32(pos), int32(cut)}
				if !s.run(opIdx+1, cut+len(op.Lit)) {
					return false
				}
			}
			off = cut - pos + 1
		}

	case OpEach:
		g := &s.p.groups[op.Slot]
		gr := &r.Groups[op.Slot]
		// 两层都要枚举：块的终止字面量可能在内容里出现多次，
		// 块内部的迭代切分也可能有多种分法。
		if len(op.Lit) == 0 {
			return s.searchGroup(g, src[pos:], gr, r.Base+int32(pos), func() bool {
				return s.run(opIdx+1, len(src))
			})
		}
		off := 0
		for {
			j := bytes.Index(src[pos+off:], op.Lit)
			if j < 0 {
				return true
			}
			regionEnd := pos + off + j
			cont := func() bool {
				return s.run(opIdx+1, regionEnd+len(op.Lit))
			}
			if !s.searchGroup(g, src[pos:regionEnd], gr, r.Base+int32(pos), cont) {
				return false
			}
			off = regionEnd - pos + 1
		}
	}
	return true
}

// searchGroup 枚举 region 的**所有**合法切分，每得到一种完整切分就调用 k。
//
// 扁平的 parseGroup 对每个迭代取"能被子计划完整消费的最短前缀"，一旦选定
// 就不再回头。于是这类输入会失败：
//
//	模板 ${each|name=xs,sep=;}${k}:${v}${end}   输入 "a:1;b;c"
//	  迭代 1 取最短的 "a:1"（能解析），剩下 "b;c" 怎么切都不成立 -> 失败
//	  正解是让迭代 1 退让，整串吞掉：k="a", v="1;b;c"
//
// 所以块内切分也必须参与回溯。与外层同理，只在扁平引擎失败后才走到这里。
func (s *searcher) searchGroup(g *GroupInfo, region []byte, out *GroupResult, base int32, k func() bool) bool {
	out.Items = out.Items[:0]
	if len(region) == 0 {
		if g.AllowEmpty {
			return k()
		}
		return true
	}
	return s.splitFrom(g, region, out, base, 0, k)
}

// splitFrom 从 region[pos:] 起枚举「下一个迭代」的所有可能边界。
func (s *searcher) splitFrom(g *GroupInfo, region []byte, out *GroupResult, base int32, pos int, k func() bool) bool {
	searchFrom := pos
	for {
		s.steps++
		j := bytes.Index(region[searchFrom:], g.Sep)
		last := j < 0

		var item []byte
		next := -1
		if last {
			item = region[pos:]
		} else {
			item = region[pos : searchFrom+j]
			next = searchFrom + j + len(g.Sep)
		}

		out.Items = GrowResults(out.Items)
		ir := &out.Items[len(out.Items)-1]
		ir.Base = base + int32(pos)
		if g.Sub.Parse(item, ir) {
			ok := true
			if last {
				ok = k() // 吃到区间结尾，这是一种完整切分
			} else {
				ok = s.splitFrom(g, region, out, base, next, k)
			}
			if !ok {
				return false // 调用方要求停止，保留现场
			}
		}
		out.Items = out.Items[:len(out.Items)-1] // 回退这个迭代，试更长的边界

		if last {
			return true
		}
		searchFrom = searchFrom + j + 1
		if searchFrom > len(region) {
			return true
		}
	}
}

// parseBacktrack 找第一个解。
func (p *Plan) parseBacktrack(src []byte, r *Result) bool {
	s := searcher{p: p, src: src, r: r} // visit 为 nil：找到即停
	s.run(0, 0)
	return s.found
}

// CountParses 统计最多 limit 个解，用于歧义检测。
//
// 返回值 >= 2 就意味着模板对该输入是**歧义的** —— 首个解依赖算子顺序，
// 换个实现或换个输入就可能给出不同答案。这是模板设计的 bug，
// 应该在开发期发现而不是上线后靠数据暴露。
func (p *Plan) CountParses(src []byte, limit int) int {
	if limit <= 0 {
		limit = 2
	}
	r := p.NewResult()
	p.ensure(r)
	n := 0
	s := searcher{p: p, src: src, r: r, visit: func() bool {
		n++
		return n < limit
	}}
	s.run(0, 0)
	return n
}
