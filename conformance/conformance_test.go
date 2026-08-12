package conformance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/conformance"
	"github.com/huge-zhang/zdog-template/core/engine"
	"github.com/huge-zhang/zdog-template/core/pipeline"
)

func loadCases(t *testing.T) []conformance.Case {
	t.Helper()
	paths, err := filepath.Glob("cases/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no conformance cases found: %v", err)
	}
	cases := make([]conformance.Case, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var c conformance.Case
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if c.Name == "" {
			t.Fatalf("%s: case has no name", p)
		}
		cases = append(cases, c)
	}
	return cases
}

func compilePipeline(c *conformance.Case) (*pipeline.Pipeline, error) {
	raw, err := json.Marshal(c.Config)
	if err != nil {
		return nil, err
	}
	cfg, err := pipeline.ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	return pipeline.Compile(cfg)
}

func TestConformance(t *testing.T) {
	cases := loadCases(t)
	t.Logf("running %d conformance cases", len(cases))

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			p, err := compilePipeline(&c)

			if c.IsError() {
				if err == nil {
					t.Fatalf("expected compile error containing %q, got success", c.CompileError)
				}
				if !strings.Contains(err.Error(), c.CompileError) {
					t.Fatalf("error = %q, want substring %q", err, c.CompileError)
				}
				return
			}
			if err != nil {
				t.Fatalf("compile failed: %v", err)
			}

			// —— 转换结果 ——
			s := p.NewScratch()
			out, matched, total := p.Transform(nil, []byte(c.Input), s)
			if string(out) != c.Output {
				t.Errorf("output mismatch\n  want: %q\n  got:  %q", c.Output, out)
			}
			if c.Matched != nil && matched != *c.Matched {
				t.Errorf("matched = %d, want %d", matched, *c.Matched)
			}
			if c.Total != nil && total != *c.Total {
				t.Errorf("total = %d, want %d", total, *c.Total)
			}

			// —— 源侧绑定 ——
			if len(c.Bindings) > 0 {
				ctx, err := p.Source().Parse([]byte(c.Input))
				if err != nil {
					t.Fatalf("source parse failed: %v", err)
				}
				for name, want := range c.Bindings {
					got, ok := ctx.Raw(name)
					if !ok {
						t.Errorf("binding %q not set", name)
						continue
					}
					if string(got) != want {
						t.Errorf("binding %q = %q, want %q", name, got, want)
					}
				}
			}

			// —— 执行层级 ——
			if c.Tier != "" {
				if got := p.Source().Tier().String(); got != c.Tier {
					t.Errorf("tier = %q, want %q", got, c.Tier)
				}
			}

			// —— round-trip 定律 ——
			for _, law := range c.Laws {
				switch law {
				case "A":
					if err := p.Source().VerifyLawA([]byte(c.Input)); err != nil {
						t.Errorf("%v", err)
					}
				case "B":
					ctx, err := p.Source().Parse([]byte(c.Input))
					if err != nil {
						t.Fatalf("parse for law B: %v", err)
					}
					if err := p.Source().VerifyLawB(ctx); err != nil {
						t.Errorf("%v", err)
					}
				default:
					t.Fatalf("unknown law %q", law)
				}
			}
		})
	}
}

// 每条用例的 description 都必须写清楚它在守护什么 —— 用例是跨语言的语义真源，
// 没有说明的用例在别的语言里出错时无从判断是实现错还是用例错。
func TestEveryCaseIsDocumented(t *testing.T) {
	for _, c := range loadCases(t) {
		if len(c.Description) < 10 {
			t.Errorf("case %q: description too short: %q", c.Name, c.Description)
		}
	}
}

var _ = engine.New // 保持 engine 依赖显式，便于后续补充定律用例
