package plan_test

import (
	"testing"

	"github.com/huge-zhang/zdog-template/core/plan"
	"github.com/huge-zhang/zdog-template/core/template"
)

const benchLine = `[2026-08-12T01:00:00Z] ERROR disk full payload={"host":"web-1","pct":95}`

func mustPlan(b *testing.B, src string) *plan.Plan {
	b.Helper()
	tpl, err := template.Load(src)
	if err != nil {
		b.Fatal(err)
	}
	p, err := plan.Compile(tpl.Elements())
	if err != nil {
		b.Fatal(err)
	}
	return p
}

// 性能门禁（见 DESIGN.md §11）：T0 层 ≤ 0.10 µs/行。
// 整个架构都押在"字面量定界比正则快一个数量级"这个结论上，这里守住它。
func BenchmarkParseTierLiteral(b *testing.B) {
	p := mustPlan(b, "[${ts}] ${lv} ${msg} payload=${payload}")
	src := []byte(benchLine)
	spans := make([]plan.Span, p.NumSlots())
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Parse(src, spans) {
			b.Fatal("parse failed")
		}
	}
}

// 对照：同一个模板改用正则约束，应显著变慢 —— 这正是"能用字面量就别用正则"的依据。
func BenchmarkParseTierRegex(b *testing.B) {
	p := mustPlan(b, `[${re|name=ts,expr=[^\]]+}] ${re|name=lv,expr=\w+} ${msg}`)
	src := []byte(`[2026-08-12T01:00:00Z] ERROR disk full`)
	spans := make([]plan.Span, p.NumSlots())
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Parse(src, spans) {
			b.Fatal("parse failed")
		}
	}
}

func BenchmarkParseTierIsland(b *testing.B) {
	p := mustPlan(b, "[${ts}] ${lv} ${msg} payload=${json|name=payload}")
	src := []byte(benchLine)
	spans := make([]plan.Span, p.NumSlots())
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Parse(src, spans) {
			b.Fatal("parse failed")
		}
	}
}

// 定律 A 的完整往返成本。
func BenchmarkParseFormatRoundTrip(b *testing.B) {
	p := mustPlan(b, "[${ts}] ${lv} ${msg} payload=${payload}")
	src := []byte(benchLine)
	spans := make([]plan.Span, p.NumSlots())
	values := make([][]byte, p.NumSlots())
	out := make([]byte, 0, len(src))
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Parse(src, spans) {
			b.Fatal("parse failed")
		}
		for k, s := range spans {
			values[k] = src[s.Start:s.End]
		}
		out, _ = p.Format(out[:0], values)
	}
}
