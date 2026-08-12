package pipeline_test

import (
	"bytes"
	"encoding/json"
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

// shape 与 mapping 都支持路径限定键（`xs[].n`），用来区分跨层级的同名字段。
// 没有它时 `{"n": …}` 会同时命中外层与块内，且无法表达"只作用于外层"。
func TestPathScopedShape(t *testing.T) {
	const tmpl = "${n}|${each|name=xs,sep=;}${n}${end}"
	num := func(f string) json.RawMessage {
		return json.RawMessage(`{"type":"number","format":"` + f + `"}`)
	}
	for _, tc := range []struct {
		name  string
		shape map[string]json.RawMessage
		want  string
	}{
		{"裸名作用于所有层级（向后兼容）",
			map[string]json.RawMessage{"n": num("%.2f")}, "7.00|1.00;2.00\n"},
		{"路径限定 null 显式关掉块内",
			map[string]json.RawMessage{"n": num("%.2f"), "xs[].n": json.RawMessage("null")},
			"7.00|1;2\n"},
		{"两层各用各的格式",
			map[string]json.RawMessage{"n": num("%.2f"), "xs[].n": num("[%.0f]")},
			"7.00|[1];[2]\n"},
		{"只作用于块内",
			map[string]json.RawMessage{"xs[].n": num("<%.1f>")}, "7|<1.0>;<2.0>\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := pipeline.Compile(&pipeline.Config{
				Version: 1, Source: tmpl, Target: tmpl, Shape: tc.shape,
			})
			if err != nil {
				t.Fatal(err)
			}
			out, _, _ := p.Transform(nil, []byte("7|1;2"), p.NewScratch())
			if string(out) != tc.want {
				t.Errorf("out = %q, want %q", out, tc.want)
			}
		})
	}
}

func TestPathScopedMapping(t *testing.T) {
	p, err := pipeline.Compile(&pipeline.Config{
		Version: 1,
		Source:  "${v}|${each|name=xs,sep=;}${v}${end}",
		Target:  "${v}|${each|name=xs,sep=;}${v}${end}",
		// 外层 v 大写，块内 v 保持原样 —— 没有路径限定就没法分开
		Mapping: map[string]string{"v": "upper(v)", "xs[].v": "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ := p.Transform(nil, []byte("a|b;c"), p.NewScratch())
	if string(out) != "A|b;c\n" {
		t.Errorf("out = %q, want %q", out, "A|b;c\n")
	}
}

// 组不支持表达式，错误信息必须说清楚，而不是让人以为名字打错了。
func TestBlockExpressionErrorIsActionable(t *testing.T) {
	_, err := pipeline.Compile(&pipeline.Config{
		Version: 1,
		Source:  "${each|name=items,sep=;}${id}${end}",
		Target:  "${each|name=rows,sep=;}${id}${end}",
		Mapping: map[string]string{"rows": "reverse(items)"},
	})
	if err == nil {
		t.Fatal("组用表达式应当报错")
	}
	for _, want := range []string{"expressions are not supported on blocks", "items"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want substring %q", err, want)
		}
	}
}

// 不变量：parse | format ≡ transform
//
// 这是 blog 那张流程图的直接表述 —— 中间断开成两段，结果必须和一步到位相同。
// 早先 format 不过 mapping，输入的键是目标字段名而 parse 吐的是源字段名，
// 两者对不上，有 mapping 时 format(parse(x)) 会得到空结果。
func TestParseFormatEquivalentToTransform(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   pipeline.Config
		input string
	}{
		{"同名直通", pipeline.Config{
			Version: 1, Source: "[${ts}] ${lv}", Target: "${lv}/${ts}",
		}, "[T1] err\n[T2] warn"},
		{"重命名", pipeline.Config{
			Version: 1, Source: "[${ts}] ${lv}", Target: "${level}|${time}",
			Mapping: map[string]string{"level": "lv", "time": "ts"},
		}, "[T1] err"},
		{"表达式", pipeline.Config{
			Version: 1, Source: "[${ts}] ${lv}", Target: "${level}|${time}",
			Mapping: map[string]string{"level": "upper(lv)", "time": "ts"},
		}, "[T1] err\n[T2] warn"},
		{"结构化岛取值", pipeline.Config{
			Version: 1, Source: "[${ts}] ${json|name=p}", Target: "${host}",
			Mapping: map[string]string{"host": "p.host || 'unknown'"},
		}, `[T1] {"host":"web-1"}` + "\n" + `[T2] {"pct":9}`},
		{"重复块", pipeline.Config{
			Version: 1,
			Source:  "items=${each|name=xs,sep=;}${id}:${n}${end}",
			Target:  "${each|name=xs,sep=','}${id}x${n}${end}",
		}, "items=a:1;b:2"},
		{"shape 参与", pipeline.Config{
			Version: 1, Source: "cpu=${c}", Target: "${usage}",
			Mapping: map[string]string{"usage": "to_number(c)"},
			Shape:   map[string]json.RawMessage{"usage": json.RawMessage(`{"type":"number","format":"%.2f"}`)},
		}, "cpu=93.456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := pipeline.Compile(&tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			in := []byte(tc.input)

			direct, _, _ := p.Transform(nil, in, p.NewScratch())

			mid, pst := p.ParseJSON(nil, in, p.NewScratch())
			if pst.Failed != 0 {
				t.Fatalf("parse 报错: %v", pst.Errors)
			}
			viaJSON, fst := p.FormatJSON(nil, mid, p.NewScratch())
			if fst.Failed != 0 {
				t.Fatalf("format 报错: %v", fst.Errors)
			}

			if string(direct) != string(viaJSON) {
				t.Errorf("parse|format 与 transform 不等价\n  transform:    %q\n  parse|format: %q\n  中间 NDJSON:  %s",
					direct, viaJSON, mid)
			}
		})
	}
}

// 只有目标模板时是纯渲染：输入的键直接是目标字段名，不需要编一个用不上的源模板。
func TestFormatOnlyPipeline(t *testing.T) {
	p, err := pipeline.Compile(&pipeline.Config{Version: 1, Target: "${a}-${b}"})
	if err != nil {
		t.Fatal(err)
	}
	if p.HasSource() {
		t.Error("不该有源模板")
	}
	out, st := p.FormatJSON(nil, []byte(`{"a":"x","b":"y"}`), p.NewScratch())
	if string(out) != "x-y\n" || st.OK != 1 {
		t.Errorf("out=%q stats=%+v", out, st)
	}
}

func TestPipelineConfigCombinations(t *testing.T) {
	for _, tc := range []struct{ name, raw, wantErr string }{
		{"都没有", `{"version":1}`, "at least one of source or target"},
		{"无源模板却给了 mapping", `{"version":1,"target":"${a}","mapping":{"a":"b"}}`,
			"mapping needs a source template"},
	} {
		cfg, err := pipeline.ParseConfig([]byte(tc.raw))
		if err == nil {
			_, err = pipeline.Compile(cfg)
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error = %v, want substring %q", tc.name, err, tc.wantErr)
		}
	}
}
