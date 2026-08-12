package pipeline_test

import (
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/core/pipeline"
)

// 量清楚一个表达式字段的**边际成本**，用来判断 any 装箱值不值得改掉。
//
// 三个基准的目标模板完全相同，只有 mapping 的写法不同：
//   fast  三个字段全是裸字段名 -> 全走零拷贝快路径
//   one   其中一个换成 upper(...) -> 一个表达式
//   three 三个全换成表达式
// 差值就是每个表达式字段的边际成本。

const exprLine = `[2026-08-12T01:00:00Z] error disk full`
const exprN = 5000

func benchExpr(b *testing.B, mapping map[string]string) {
	b.Helper()
	p, err := pipeline.Compile(&pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv} ${msg}",
		Target:  "${a}|${b}|${c}",
		Mapping: mapping,
	})
	if err != nil {
		b.Fatal(err)
	}
	in := []byte(strings.Repeat(exprLine+"\n", exprN))
	s := p.NewScratch()
	out := make([]byte, 0, len(in))
	if _, m, _ := p.Transform(out[:0], in, s); m != exprN {
		b.Fatalf("only %d matched", m)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _, _ = p.Transform(out[:0], in, s)
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/exprN, "ns/line")
}

func BenchmarkExprNone(b *testing.B) {
	benchExpr(b, map[string]string{"a": "ts", "b": "lv", "c": "msg"})
}

func BenchmarkExprOne(b *testing.B) {
	benchExpr(b, map[string]string{"a": "ts", "b": "upper(lv)", "c": "msg"})
}

func BenchmarkExprThree(b *testing.B) {
	benchExpr(b, map[string]string{"a": "upper(ts)", "b": "upper(lv)", "c": "upper(msg)"})
}

// 对照：不做字符串加工的表达式（只是取值），隔离出"装箱"与"加工"两部分。
func BenchmarkExprPassthroughViaOr(b *testing.B) {
	benchExpr(b, map[string]string{"a": "ts || 'x'", "b": "lv || 'x'", "c": "msg || 'x'"})
}
