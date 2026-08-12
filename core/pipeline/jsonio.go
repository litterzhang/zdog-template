package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/huge-zhang/zdog-template/core/binding"
	"github.com/huge-zhang/zdog-template/core/engine"
	"github.com/huge-zhang/zdog-template/core/plan"
)

// 本文件提供以 JSON 为交换格式的入口，服务于 SDK 与 CLI。
//
// 用 JSON 而不是给每种数据结构设计一套跨界表示，是「配置即数据」这条铁律的
// 延续（见 DESIGN.md §8）：所有宿主语言拿到的是同一串字节，
// 不需要各自维护一套绑定结构的编解码。
//
// 这些入口**不在热路径上** —— 热路径是 Transform。这里可以从容地用 map。

// ParseJSON 把每行解析成一个 JSON 对象，按 NDJSON 输出（每行一个对象）。
// 不匹配的行被跳过。
func (p *Pipeline) ParseJSON(dst, in []byte, s *Scratch) (out []byte, matched, total int) {
	out = dst
	forEachLine(in, func(line []byte) {
		total++
		if !p.src.ParseInto(line, s.res) {
			return
		}
		ctx := binding.FromParse(p.src.Plan(), line, s.res)
		obj, err := contextToMap(ctx)
		if err != nil {
			return
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return
		}
		out = append(append(out, b...), '\n')
		matched++
	})
	return out, matched, total
}

// FormatJSON 把 NDJSON 的每一行（一个对象）按目标模板渲染成文本。
func (p *Pipeline) FormatJSON(dst, in []byte, _ *Scratch) (out []byte, rendered, total int) {
	out = dst
	if p.tgt == nil {
		return out, 0, 0
	}
	forEachLine(in, func(line []byte) {
		total++
		var obj map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			return
		}
		ctx := p.tgt.NewContext()
		if err := mapToContext(ctx, obj); err != nil {
			return
		}
		rendered0, err := p.tgt.Format(ctx)
		if err != nil {
			return
		}
		out = append(append(out, rendered0...), '\n')
		rendered++
	})
	return out, rendered, total
}

// VerifyJSON 逐行校验定律 A 与歧义，输出 NDJSON 报告。
func (p *Pipeline) VerifyJSON(dst, in []byte) (out []byte, bad, total int) {
	out = dst
	forEachLine(in, func(line []byte) {
		total++
		rep := map[string]any{"line": total}
		problems := []string{}

		if err := p.src.VerifyLawA(line); err != nil {
			problems = append(problems, "定律A: "+err.Error())
		}
		switch n := p.src.CountParses(line, 2); {
		case n == 0:
			problems = append(problems, "不匹配源模板")
		case n > 1:
			problems = append(problems, fmt.Sprintf("歧义: 至少 %d 个解", n))
		}

		rep["ok"] = len(problems) == 0
		if len(problems) > 0 {
			rep["problems"] = problems
			rep["input"] = string(line)
			bad++
		}
		b, _ := json.Marshal(rep)
		out = append(append(out, b...), '\n')
	})
	return out, bad, total
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

// contextToMap 把绑定摊成可 JSON 化的 map。岛在此处解码。
func contextToMap(ctx *binding.Context) (map[string]any, error) {
	obj := make(map[string]any, len(ctx.Names()))
	for _, name := range ctx.Names() {
		v, err := ctx.Value(name)
		if err != nil {
			return nil, err
		}
		obj[name] = v
	}
	for _, gname := range ctx.GroupNames() {
		n, ok := ctx.GroupLen(gname)
		if !ok {
			continue
		}
		items := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, ok := ctx.GroupItem(gname, i)
			if !ok {
				continue
			}
			sub, err := contextToMap(item)
			if err != nil {
				return nil, err
			}
			items = append(items, sub)
		}
		obj[gname] = items
	}
	return obj, nil
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
			return fmt.Errorf("字段 %q: %w", name, err)
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
			return fmt.Errorf("重复块 %q 需要一个数组，得到 %T", gname, raw)
		}
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("重复块 %q 的元素需要是对象，得到 %T", gname, it)
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
		if x {
			return []byte("true"), nil
		}
		return []byte("false"), nil
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
