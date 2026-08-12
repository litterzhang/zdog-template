package pipeline_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/huge-zhang/zdog-template/core/pipeline"
)

// 这组基准回答一个问题：transform 凭什么比 format 快？
//
// 答案不是"transform 被优化过"，而是**两者做的工作量根本不同**。
// 下面把 format 的成本逐层剥开，看它到底花在哪。

const (
	cmpText = `[T1] error`               // transform 的输入：源文本
	cmpJSON = `{"ts":"T1","lv":"error"}` // format 的输入：JSON
	cmpN    = 5000
)

func cmpConfig() *pipeline.Config {
	return &pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv}",
		Target:  "${lv}/${ts}",
	}
}

func perLine(b *testing.B, n int) {
	b.Helper()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/line")
}

// ① transform 全程：源文本 -> 目标文本
func BenchmarkCmpTransform(b *testing.B) {
	p, err := pipeline.Compile(cmpConfig())
	if err != nil {
		b.Fatal(err)
	}
	in := []byte(repeatLines(cmpText, cmpN))
	s := p.NewScratch()
	out := make([]byte, 0, len(in))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _, _ = p.Transform(out[:0], in, s)
	}
	b.StopTimer()
	perLine(b, cmpN)
}

// ② 只做 JSON 解码，别的什么都不干。
//
// 这是 format 绕不开的第一步。如果它本身就比整个 transform 还贵，
// 那"format 慢"就与模板引擎无关了 —— 是 JSON 这个输入格式的固有代价。
func BenchmarkCmpJSONDecodeOnly(b *testing.B) {
	in := []byte(repeatLines(cmpJSON, cmpN))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range bytes.Split(in, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var obj map[string]any
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.UseNumber()
			if err := dec.Decode(&obj); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	perLine(b, cmpN)
}

// ③ format 全程：JSON -> 目标文本
func BenchmarkCmpFormat(b *testing.B) {
	p, err := pipeline.Compile(cmpConfig())
	if err != nil {
		b.Fatal(err)
	}
	in := []byte(repeatLines(cmpJSON, cmpN))
	s := p.NewScratch()
	out := make([]byte, 0, len(in))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _ = p.FormatJSON(out[:0], in, s)
	}
	b.StopTimer()
	perLine(b, cmpN)
}

// ④ 对照：transform 的 parse 那一半（只找定界符，不渲染）
func BenchmarkCmpParseOnly(b *testing.B) {
	p, err := pipeline.Compile(cmpConfig())
	if err != nil {
		b.Fatal(err)
	}
	line := []byte(cmpText)
	s := p.NewScratch()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for k := 0; k < cmpN; k++ {
			if !p.Source().ParseInto(line, s.Result()) {
				b.Fatal("no match")
			}
		}
	}
	b.StopTimer()
	perLine(b, cmpN)
}

func repeatLines(line string, n int) string {
	var sb bytes.Buffer
	for i := 0; i < n; i++ {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}
