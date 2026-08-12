package pipeline_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/core/pipeline"
)

const (
	benchLine = `[2026-08-12T01:00:00Z] ERROR disk full payload={"host":"web-1","pct":95}`
	benchN    = 5000
)

func benchPipeline(b *testing.B, cfg *pipeline.Config) {
	b.Helper()
	p, err := pipeline.Compile(cfg)
	if err != nil {
		b.Fatal(err)
	}
	in := []byte(strings.Repeat(benchLine+"\n", benchN))
	s := p.NewScratch()
	out := make([]byte, 0, len(in))

	// 先跑一次确认真的匹配上了，否则基准会在空转
	if _, matched, _ := p.Transform(out[:0], in, s); matched != benchN {
		b.Fatalf("only %d/%d lines matched", matched, benchN)
	}

	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _, _ = p.Transform(out[:0], in, s)
	}
	b.StopTimer()
	nsPerLine := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / benchN
	b.ReportMetric(nsPerLine, "ns/line")
	b.ReportMetric(1000/nsPerLine, "Mlines/s")
}

// 门禁：端到端 ≥ 10 M行/秒（即 ≤ 100 ns/行）。
func BenchmarkTransformLiteral(b *testing.B) {
	benchPipeline(b, &pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv} ${msg} payload=${payload}",
		Mapping: map[string]string{"level": "lv", "time": "ts", "text": "msg"},
		Target:  "${level}|${time}|${text}",
	})
}

func BenchmarkTransformIsland(b *testing.B) {
	benchPipeline(b, &pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv} ${msg} payload=${json|name=p}",
		Mapping: map[string]string{"level": "lv", "data": "p"},
		Target:  "${level} ${data}",
	})
}

// 重复块的代价：每次迭代都要跑一遍子计划，且组结果需要按迭代数增长。
func BenchmarkTransformEach(b *testing.B) {
	p, err := pipeline.Compile(&pipeline.Config{
		Version: 1,
		Source:  "[${ts}] items=${each|name=items,sep=;}${id}:${qty}${end}",
		Mapping: map[string]string{"when": "ts", "rows": "items", "sku": "id", "n": "qty"},
		Target:  "${when} ${each|name=rows,sep=','}${sku}x${n}${end}",
	})
	if err != nil {
		b.Fatal(err)
	}
	line := "[T1] items=a:1;b:2;c:3;d:4;e:5"
	in := []byte(strings.Repeat(line+"\n", benchN))
	s := p.NewScratch()
	out := make([]byte, 0, len(in)*2)
	if _, matched, _ := p.Transform(out[:0], in, s); matched != benchN {
		b.Fatalf("only %d/%d matched", matched, benchN)
	}
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _, _ = p.Transform(out[:0], in, s)
	}
	b.StopTimer()
	nsPerLine := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / benchN
	b.ReportMetric(nsPerLine, "ns/line")
	b.ReportMetric(nsPerLine/5, "ns/item")
}

// 表达式路径的代价：必须物化被引用字段的值（岛要解码），
// 并把结果序列化进 arena。对照 BenchmarkTransformLiteral 的零拷贝快路径。
func BenchmarkTransformExprScalar(b *testing.B) {
	benchPipeline(b, &pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv} ${msg} payload=${payload}",
		Mapping: map[string]string{"level": "upper(lv)", "time": "ts", "text": "msg"},
		Target:  "${level}|${time}|${text}",
	})
}

// 表达式钻进结构化岛：这是唯一会触发 JSON 解码的路径。
func BenchmarkTransformExprIsland(b *testing.B) {
	benchPipeline(b, &pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv} ${msg} payload=${json|name=p}",
		Mapping: map[string]string{"level": "lv", "host": "p.host || 'unknown'"},
		Target:  "${level}|${host}",
	})
}

func TestTransformBasics(t *testing.T) {
	p, err := pipeline.Compile(&pipeline.Config{
		Version: 1,
		Source:  "[${ts}] ${lv} ${msg}",
		Mapping: map[string]string{"level": "lv", "text": "msg"},
		Target:  "${level}|${text}",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := p.NewScratch()
	out, matched, total := p.Transform(nil, []byte("[T1] ERROR a\nbad line\n[T2] WARN b"), s)
	if string(out) != "ERROR|a\nWARN|b\n" {
		t.Errorf("out = %q", out)
	}
	if matched != 2 || total != 3 {
		t.Errorf("matched/total = %d/%d, want 2/3", matched, total)
	}
}

func TestTransformLineNoMatch(t *testing.T) {
	p, _ := pipeline.Compile(&pipeline.Config{
		Version: 1, Source: "[${a}]", Target: "${a}",
	})
	s := p.NewScratch()
	if _, ok := p.TransformLine(nil, []byte("nope"), s); ok {
		t.Error("TransformLine should fail on non-matching input")
	}
}

func TestConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"bad version", `{"version":99,"source":"${a}","target":"${a}"}`, "unsupported config version"},
		{"unknown field", `{"version":1,"source":"${a}","target":"${a}","zzz":1}`, "unknown field"},
		{"bad json", `not json`, "invalid config"},
	} {
		cfg, err := pipeline.ParseConfig([]byte(tc.raw))
		if err == nil {
			_, err = pipeline.Compile(cfg)
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want substring %q", tc.name, err, tc.want)
		}
	}
}

// Scratch 是每 goroutine 私有的；Pipeline 本身必须可并发共享。
func TestPipelineConcurrent(t *testing.T) {
	p, err := pipeline.Compile(&pipeline.Config{
		Version: 1, Source: "[${a}] ${b}", Target: "${b}-${a}",
	})
	if err != nil {
		t.Fatal(err)
	}
	in := []byte("[x] y\n[p] q\n")
	want := []byte("y-x\nq-p\n")
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() {
			s := p.NewScratch()
			for k := 0; k < 300; k++ {
				out, _, _ := p.Transform(nil, in, s)
				if !bytes.Equal(out, want) {
					done <- false
					return
				}
			}
			done <- true
		}()
	}
	for i := 0; i < 8; i++ {
		if !<-done {
			t.Fatal("concurrent transform produced wrong output")
		}
	}
}
