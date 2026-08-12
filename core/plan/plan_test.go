package plan_test

import (
	"strings"
	"testing"

	"github.com/litterzhang/zdog-template/core/plan"
	"github.com/litterzhang/zdog-template/core/template"
)

func compile(t *testing.T, src string) *plan.Plan {
	t.Helper()
	tpl, err := template.Load(src)
	if err != nil {
		t.Fatalf("Load(%q): %v", src, err)
	}
	p, err := plan.Compile(tpl.Elements())
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return p
}

// parse 返回 name -> 匹配到的原文。
func parse(t *testing.T, p *plan.Plan, input string) (map[string]string, bool) {
	t.Helper()
	res := p.NewResult()
	if !p.Parse([]byte(input), res) {
		return nil, false
	}
	out := map[string]string{}
	for i, name := range p.Names() {
		out[name] = input[res.Spans[i].Start:res.Spans[i].End]
	}
	return out, true
}

func TestTierSelection(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want plan.Tier
	}{
		{"[${ts}] ${lv} ${msg}", plan.TierLiteral},
		{"a${x}b", plan.TierLiteral},
		{`${re|name=n,expr=\d+}-`, plan.TierRegex},
		{"p=${json|name=j}", plan.TierIsland},
	} {
		if got := compile(t, tc.src).Tier(); got != tc.want {
			t.Errorf("%q: tier = %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestParseLiteralTier(t *testing.T) {
	p := compile(t, "[${ts}] ${lv} ${msg}")
	got, ok := parse(t, p, "[2026-08-12T01:00:00Z] ERROR disk full")
	if !ok {
		t.Fatal("parse failed")
	}
	for k, want := range map[string]string{
		"ts": "2026-08-12T01:00:00Z", "lv": "ERROR", "msg": "disk full",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// blog 里的规范例子：模板 ${re|expr=\d+}345 对 "123345" 应匹配 "123"，
// 而不是 "123345" 也不是 "1" —— 边界由后继元素裁决（best-match）。
func TestBestMatchSemantics(t *testing.T) {
	p := compile(t, `${re|name=n,expr=\d+}345`)
	got, ok := parse(t, p, "123345")
	if !ok {
		t.Fatal("parse failed")
	}
	if got["n"] != "123" {
		t.Errorf("best-match: n = %q, want %q", got["n"], "123")
	}
}

// 定界符首次出现处正则匹配不上时，必须继续试下一次出现。
// 这里 " " 第一次出现在 "my" 之后，但 `.+\.txt` 覆盖不了 "my"，
// 必须回溯到第二个空格，得到 "my file.txt"。
func TestRegexUntilBacktracksToNextDelimiter(t *testing.T) {
	p := compile(t, `${re|name=f,expr=.+\.txt} ${rest}`)
	got, ok := parse(t, p, "my file.txt done")
	if !ok {
		t.Fatal("parse failed: should backtrack to the second delimiter")
	}
	if got["f"] != "my file.txt" {
		t.Errorf("f = %q, want %q", got["f"], "my file.txt")
	}
	if got["rest"] != "done" {
		t.Errorf("rest = %q, want %q", got["rest"], "done")
	}
}

func TestParseMustConsumeWholeInput(t *testing.T) {
	p := compile(t, "[${ts}]")
	if _, ok := parse(t, p, "[abc] trailing"); ok {
		t.Error("parse should fail when input has an unconsumed tail (定律 A 前提)")
	}
}

func TestParseFailures(t *testing.T) {
	p := compile(t, "[${ts}] ${lv}")
	for _, input := range []string{"", "no bracket", "[unclosed", "[a] "} {
		if _, ok := parse(t, p, input); ok && input != "[a] " {
			t.Errorf("parse(%q) should fail", input)
		}
	}
}

func TestJSONIslandInPlan(t *testing.T) {
	p := compile(t, "payload=${json|name=p} end")
	got, ok := parse(t, p, `payload={"host":"web-1","n":[1,2]} end`)
	if !ok {
		t.Fatal("parse failed")
	}
	if got["p"] != `{"host":"web-1","n":[1,2]}` {
		t.Errorf("island raw = %q", got["p"])
	}
}

func TestAmbiguousTemplateRejected(t *testing.T) {
	tpl, err := template.Load("${a}${b}")
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Compile(tpl.Elements())
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguity error, got %v", err)
	}
}

// 定律 A：format(parse(t)) == t
func TestLawARoundTrip(t *testing.T) {
	cases := []struct{ tmpl, input string }{
		{"[${ts}] ${lv} ${msg}", "[2026-08-12T01:00:00Z] ERROR disk full"},
		{"${a}-${b}", "x-y"},
		{"prefix ${x} suffix", "prefix mid suffix"},
		{"${only}", "everything"},
		{"a${x}b${y}c", "a1b2c"},
		{`${re|name=n,expr=\d+}345`, "123345"},
		{"payload=${json|name=p} end", `payload={"a":[1,{"b":"}"}]} end`},
		{"${x} 中国人 ${y}", "前 中国人 后"},
	}
	for _, tc := range cases {
		p := compile(t, tc.tmpl)
		src := []byte(tc.input)
		res := p.NewResult()
		if !p.Parse(src, res) {
			t.Errorf("%q: parse(%q) failed", tc.tmpl, tc.input)
			continue
		}
		data := p.NewData()
		for i, s := range res.Spans {
			data.Values[i] = src[s.Start:s.End]
		}
		out, ok := p.Format(nil, data)
		if !ok {
			t.Errorf("%q: format failed", tc.tmpl)
			continue
		}
		if string(out) != tc.input {
			t.Errorf("定律 A 违反\n template: %q\n input:    %q\n output:   %q", tc.tmpl, tc.input, out)
		}
	}
}

func TestPlanConcurrentSafe(t *testing.T) {
	p := compile(t, "[${ts}] ${lv}")
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() {
			res := p.NewResult()
			for k := 0; k < 500; k++ {
				if !p.Parse([]byte("[t] L"), res) {
					done <- false
					return
				}
			}
			done <- true
		}()
	}
	for i := 0; i < 8; i++ {
		if !<-done {
			t.Fatal("concurrent parse failed")
		}
	}
}
