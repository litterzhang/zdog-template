package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/huge-zhang/zdog-template/core/binding"
	"github.com/huge-zhang/zdog-template/core/engine"
	"github.com/huge-zhang/zdog-template/core/plan"
)

// 本文件提供以 JSON 为交换格式的入口，服务于 SDK 与 CLI。
//
// 用 JSON 而不是给每种数据结构设计一套跨界表示，是「配置即数据」这条铁律的
// 延续（见 DESIGN.md §8）：所有宿主语言拿到的是同一串字节，
// 不需要各自维护一套绑定结构的编解码。

// MaxReportedErrors 是每次批量操作最多回报的错误条数。
// 只是为了不让错误信息本身撑爆缓冲；计数不受此限制。
const MaxReportedErrors = 10

// BatchStats 是一次批量操作的统计与诊断。
//
// Failed 与 (Total - OK - Failed) 是**两回事**：前者是"这行本该处理却出错了"
// （岛不是合法 JSON、渲染失败……），后者是"这行本来就不匹配模板"。
// 早先的实现把两者都静默跳过，用户看到 8/10 无从判断是数据坏了还是模板不对。
type BatchStats struct {
	OK     int
	Total  int
	Failed int
	Errors []string
}

func (b *BatchStats) fail(line int, err error) {
	b.Failed++
	if len(b.Errors) < MaxReportedErrors {
		b.Errors = append(b.Errors, fmt.Sprintf("line %d: %v", line, err))
	}
}

// ParseJSON 把每行解析成一个 JSON 对象，按 NDJSON 输出（每行一个对象）。
//
// 结构化岛的原文**原样嵌入**，不做「解码成 map 再序列化回去」的往返 ——
// 岛的 raw 本来就是合法 JSON。实测每行 5047 -> 796 ns（6.3 倍），
// 顺带保住了原文的数字写法与键序。
//
// 剩下的成本大头是 json.Valid（约 450 ns）：非 strict 的岛在 Scan 阶段
// 只查了括号配对，直接拼进输出会毁掉整行 NDJSON，所以这一道省不掉。
// 无岛的模板约 350 ns/行。
func (p *Pipeline) ParseJSON(dst, in []byte, s *Scratch) ([]byte, BatchStats) {
	var st BatchStats
	out := dst
	forEachLine(in, func(line []byte) {
		st.Total++
		if !p.src.ParseInto(line, s.res) {
			return // 不匹配，不是错误
		}
		ctx := binding.FromParse(p.src.Plan(), line, s.res)
		mark := len(out)
		next, err := appendRecordJSON(out, ctx)
		if err != nil {
			out = out[:mark] // 回滚半截输出
			st.fail(st.Total, err)
			return
		}
		out = append(next, '\n')
		st.OK++
	})
	return out, st
}

// appendRecordJSON 把一条绑定直接写成 JSON，不经过 map。
func appendRecordJSON(dst []byte, ctx *binding.Context) ([]byte, error) {
	pl := ctx.Plan()
	dst = append(dst, '{')
	first := true

	for i, name := range ctx.Names() {
		raw, ok := ctx.RawAt(i)
		if !ok {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = appendJSONString(dst, name)
		dst = append(dst, ':')

		if island := pl.Island(i); island != nil {
			// 岛的原文就是 JSON，原样嵌入 —— 不做「解码成 map 再序列化回去」
			// 的往返，顺带保住原文的数字写法与键序。
			//
			// 但必须确认它真的合法：非 strict 的岛在 Scan 阶段只查了括号配对，
			// 把 {"a":} 这种直接拼进输出会毁掉整行 NDJSON。
			// strict 的岛在 Scan 时已经校验过，这里跳过，免得白验两遍
			// （实测双重校验让 strict 比非 strict 还慢 27%）。
			if !alreadyValidated(island) && !json.Valid(raw) {
				return dst, fmt.Errorf("field %q is not valid JSON: %s", name, truncate(raw))
			}
			dst = append(dst, raw...)
			continue
		}
		dst = appendJSONString(dst, string(raw))
	}

	for g := 0; g < pl.NumGroups(); g++ {
		info := pl.Group(g)
		n, ok := ctx.GroupLen(info.Name)
		if !ok {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = appendJSONString(dst, info.Name)
		dst = append(dst, ':', '[')
		for i := 0; i < n; i++ {
			if i > 0 {
				dst = append(dst, ',')
			}
			item, ok := ctx.GroupItem(info.Name, i)
			if !ok {
				continue
			}
			var err error
			if dst, err = appendRecordJSON(dst, item); err != nil {
				return dst, err
			}
		}
		dst = append(dst, ']')
	}
	return append(dst, '}'), nil
}

// appendJSONString 写一个 JSON 字符串。
//
// 快路径：纯 ASCII 且无需转义时直接拷贝。其余交给标准库 —— 多字节序列的
// 合法性与转义规则不值得自己重写一遍。
func appendJSONString(dst []byte, s string) []byte {
	if !needsJSONEscape(s) {
		dst = append(dst, '"')
		dst = append(dst, s...)
		return append(dst, '"')
	}
	b, err := json.Marshal(s)
	if err != nil { // json.Marshal 对 string 不会失败，兜底而已
		return append(dst, `""`...)
	}
	return append(dst, b...)
}

// alreadyValidated 报告该岛是否在 Scan 阶段就做过完整合法性校验。
// 用可选接口而不是往 model.Island 里加方法 —— 这只是一处优化，
// 不该让每个岛的实现都被迫关心。
func alreadyValidated(island any) bool {
	v, ok := island.(interface{ Strict() bool })
	return ok && v.Strict()
}

func needsJSONEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '"' || c == '\\' || c >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

func truncate(b []byte) string {
	if len(b) > 60 {
		return string(b[:60]) + "…"
	}
	return string(b)
}

// FormatJSON 把 NDJSON 的每一行（一个对象）按目标模板渲染成文本。
func (p *Pipeline) FormatJSON(dst, in []byte, _ *Scratch) ([]byte, BatchStats) {
	var st BatchStats
	out := dst
	if p.tgt == nil {
		return out, st
	}
	forEachLine(in, func(line []byte) {
		st.Total++
		var obj map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			st.fail(st.Total, fmt.Errorf("input is not valid JSON: %w", err))
			return
		}
		ctx := p.tgt.NewContext()
		if err := mapToContext(ctx, obj); err != nil {
			st.fail(st.Total, err)
			return
		}
		rendered, err := p.tgt.Format(ctx)
		if err != nil {
			st.fail(st.Total, err)
			return
		}
		out = append(append(out, rendered...), '\n')
		st.OK++
	})
	return out, st
}

// VerifyJSON 逐行校验定律 A 与歧义，输出 NDJSON 报告。
func (p *Pipeline) VerifyJSON(dst, in []byte) ([]byte, BatchStats) {
	var st BatchStats
	out := dst
	forEachLine(in, func(line []byte) {
		st.Total++
		rep := map[string]any{"line": st.Total}
		problems := []string{}

		if err := p.src.VerifyLawA(line); err != nil {
			problems = append(problems, "law A: "+err.Error())
		}
		switch n := p.src.CountParses(line, 2); {
		case n == 0:
			problems = append(problems, "does not match the source template")
		case n > 1:
			problems = append(problems, fmt.Sprintf("ambiguous: at least %d parses", n))
		}

		rep["ok"] = len(problems) == 0
		if len(problems) > 0 {
			rep["problems"] = problems
			rep["input"] = string(line)
			st.Failed++
		} else {
			st.OK++
		}
		b, _ := json.Marshal(rep)
		out = append(append(out, b...), '\n')
	})
	return out, st
}

// Inspect 返回模板结构的 JSON 描述，供 CLI 展示与调试。
func (p *Pipeline) Inspect() ([]byte, error) {
	info := map[string]any{"source": describeEngine(p.src)}
	if p.tgt != nil {
		info["target"] = describeEngine(p.tgt)
	}
	return json.MarshalIndent(info, "", "  ")
}

func describeEngine(e *engine.Engine) map[string]any {
	pl := e.Plan()
	d := map[string]any{
		"template":  e.Template().Source(),
		"tier":      pl.Tier().String(),
		"backtrack": pl.NeedsBacktrack(),
		"fields":    fieldInfo(pl),
	}
	if blocks := blockInfo(pl); len(blocks) > 0 {
		d["blocks"] = blocks
	}
	return d
}

func fieldInfo(pl *plan.Plan) []map[string]any {
	out := make([]map[string]any, 0, pl.NumSlots())
	for i, name := range pl.Names() {
		f := map[string]any{"name": name}
		if pl.Island(i) != nil {
			f["kind"] = "json-island"
		} else {
			f["kind"] = "hole"
		}
		out = append(out, f)
	}
	return out
}

func blockInfo(pl *plan.Plan) []map[string]any {
	var out []map[string]any
	for g := 0; g < pl.NumGroups(); g++ {
		info := pl.Group(g)
		b := map[string]any{
			"name":   info.Name,
			"sep":    string(info.Sep),
			"fields": fieldInfo(info.Sub),
		}
		if nested := blockInfo(info.Sub); len(nested) > 0 {
			b["blocks"] = nested
		}
		out = append(out, b)
	}
	return out
}

// mapToContext 把 JSON 对象写进目标 context。
func mapToContext(ctx *binding.Context, obj map[string]any) error {
	for _, name := range ctx.Names() {
		v, ok := obj[name]
		if !ok {
			continue
		}
		b, err := scalarBytes(v)
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		if err := ctx.Set(name, b); err != nil {
			return err
		}
	}
	for _, gname := range ctx.GroupNames() {
		raw, ok := obj[gname]
		if !ok {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("block %q expects an array, got %T", gname, raw)
		}
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("block %q items must be objects, got %T", gname, it)
			}
			sub, err := ctx.AppendGroupItem(gname)
			if err != nil {
				return err
			}
			if err := mapToContext(sub, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func scalarBytes(v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return []byte{}, nil
	case string:
		return []byte(x), nil
	case json.Number:
		return []byte(x.String()), nil
	case bool:
		return strconv.AppendBool(nil, x), nil
	default:
		return json.Marshal(v) // 复合值按 JSON 写回
	}
}

// forEachLine 按 \n 切分并对每个非空行调用 fn。
func forEachLine(in []byte, fn func(line []byte)) {
	for start := 0; start < len(in); {
		line := in[start:]
		if j := bytes.IndexByte(line, '\n'); j >= 0 {
			line, start = line[:j], start+j+1
		} else {
			start = len(in)
		}
		if len(line) > 0 {
			fn(line)
		}
	}
}
