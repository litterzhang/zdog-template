package plan

// Result 是一次 parse 的结果容器，可复用以避免热路径分配。
//
// 无重复块的模板 Groups 恒为 nil，因此 T0 的零分配特性不受影响。
type Result struct {
	// Base 是本结果对应的字节片段在**最外层源文**中的起点。
	// 重复块内各次迭代的 Spans 相对该迭代自身的片段，读取时须加上 Base。
	Base   int32
	Spans  []Span
	Groups []GroupResult
}

// Abs 把本层的相对跨度换算成最外层源文的绝对跨度。
func (r *Result) Abs(s Span) Span {
	if !s.Valid() {
		return NoSpan
	}
	return Span{s.Start + r.Base, s.End + r.Base}
}

// GroupResult 是一个重复块的解析结果。
type GroupResult struct {
	Items []Result
}

// NewResult 为该计划分配一个结果容器。
func (p *Plan) NewResult() *Result {
	r := &Result{Spans: make([]Span, len(p.names))}
	if n := len(p.groups); n > 0 {
		r.Groups = make([]GroupResult, n)
	}
	return r
}

// ensure 保证容器与计划匹配，并复位。
func (p *Plan) ensure(r *Result) {
	if cap(r.Spans) < len(p.names) {
		r.Spans = make([]Span, len(p.names))
	}
	r.Spans = r.Spans[:len(p.names)]
	for i := range r.Spans {
		r.Spans[i] = NoSpan
	}
	if len(p.groups) == 0 {
		r.Groups = r.Groups[:0]
		return
	}
	if cap(r.Groups) < len(p.groups) {
		r.Groups = make([]GroupResult, len(p.groups))
	}
	r.Groups = r.Groups[:len(p.groups)]
}

// Data 是一次 format 的输入。
type Data struct {
	Values [][]byte
	Groups []GroupData
}

// GroupData 是一个重复块的渲染输入。
type GroupData struct {
	Items []Data
}

// NewData 为该计划分配一个渲染输入容器。
func (p *Plan) NewData() *Data {
	d := &Data{Values: make([][]byte, len(p.names))}
	if n := len(p.groups); n > 0 {
		d.Groups = make([]GroupData, n)
	}
	return d
}

// ResetResult 把结果容器复位到与本计划匹配的状态，尽量复用已分配容量。
func (p *Plan) ResetResult(r *Result) { p.ensure(r) }

// GrowResults 在**保留已分配容量**的前提下把切片延长一个元素。
//
// 不能直接写 append(s, Result{})：那会用零值覆盖掉复用元素里已经分配好的
// Spans/Groups 切片，于是每次迭代都要重新分配。实测这是重复块热路径上
// 最大的分配来源（每行 10 次分配 → 0 次）。
func GrowResults(s []Result) []Result {
	if len(s) < cap(s) {
		return s[:len(s)+1]
	}
	return append(s, Result{})
}

// GrowData 是 GrowResults 的渲染侧对应物。
func GrowData(s []Data) []Data {
	if len(s) < cap(s) {
		return s[:len(s)+1]
	}
	return append(s, Data{})
}
