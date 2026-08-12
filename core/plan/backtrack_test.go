package plan_test

import (
	"testing"

	"github.com/huge-zhang/zdog-template/core/plan"
)

// 扁平引擎在每个定界点取首次匹配，下面这些模板因此会失败。
// 回溯引擎必须救回它们 —— 这些都是完全合法的模板 + 输入。
func TestBacktrackRescuesFirstMatchFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tmpl  string
		input string
		want  map[string]string
	}{
		{
			// 首个 '.' 之后是 "y.42"，匹配不上 \d+；正解是 a="x.y"
			name: "regex after delimiter", tmpl: `${a}.${re|name=n,expr=\d+}`,
			input: "x.y.42",
			want:  map[string]string{"a": "x.y", "n": "42"},
		},
		{
			// 首个 '/' 之后是 "b/{...}"，不是 JSON；正解是 a="a/b"
			name: "island after delimiter", tmpl: `${a}/${json|name=p}`,
			input: `a/b/{"x":1}`,
			want:  map[string]string{"a": "a/b", "p": `{"x":1}`},
		},
		{
			// 首个 ':' 之后凑不出后面的字面量
			name: "literal after delimiter", tmpl: `${k}:${v}!END`,
			input: "a:b:c!END",
			want:  map[string]string{"k": "a", "v": "b:c"},
		},
		{
			// 多字符定界符的多次出现
			name: "multi-char delimiter", tmpl: `${a} -> ${re|name=n,expr=\d+}`,
			input: "x -> y -> 42",
			want:  map[string]string{"a": "x -> y", "n": "42"},
		},
		{
			// 需要连续两次回溯
			name: "double backtrack", tmpl: `${a}.${b}.${re|name=n,expr=\d+}`,
			input: "p.q.r.s.7",
			want:  map[string]string{"a": "p", "b": "q.r.s", "n": "7"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compile(t, tc.tmpl)
			if !p.NeedsBacktrack() {
				t.Fatalf("模板 %q 应当被判定为需要回溯", tc.tmpl)
			}
			got, ok := parse(t, p, tc.input)
			if !ok {
				t.Fatalf("parse(%q) 失败 —— 回溯引擎没生效", tc.input)
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("%s = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

// 不需要回溯的模板必须保持扁平快路径 —— 这是性能的前提。
func TestNoBacktrackForSimpleTemplates(t *testing.T) {
	for _, tmpl := range []string{
		"[${ts}] ${lv} ${msg}",
		"${a}-${b}",
		"${only}",
		"a${x}b${y}",
		"prefix ${x}",
	} {
		if compile(t, tmpl).NeedsBacktrack() {
			t.Errorf("模板 %q 不该需要回溯（会拖慢快路径）", tmpl)
		}
	}
}

// 回溯不能改变已经能匹配的结果：扁平引擎成功时，
// 回溯引擎的第一条路径与它相同。
func TestBacktrackAgreesWithFlatOnSuccess(t *testing.T) {
	for _, tc := range []struct{ tmpl, input, field, want string }{
		{`${a}.${re|name=n,expr=\d+}`, "x.42", "a", "x"},
		{`${a}/${json|name=p}`, `a/{"x":1}`, "a", "a"},
		{`${k}:${v}!END`, "a:b!END", "v", "b"},
	} {
		p := compile(t, tc.tmpl)
		got, ok := parse(t, p, tc.input)
		if !ok {
			t.Fatalf("parse(%q) failed", tc.input)
		}
		if got[tc.field] != tc.want {
			t.Errorf("%s: %s = %q, want %q", tc.tmpl, tc.field, got[tc.field], tc.want)
		}
	}
}

func TestBacktrackStillFailsOnGenuineMismatch(t *testing.T) {
	p := compile(t, `${a}.${re|name=n,expr=\d+}`)
	for _, input := range []string{"nodot", "x.notanumber", "x.12y", ""} {
		if _, ok := parse(t, p, input); ok {
			t.Errorf("parse(%q) 应当失败", input)
		}
	}
}

// 歧义检测：同一输入有多个解意味着模板设计有 bug ——
// 首个解依赖算子顺序，换个输入就可能给出不同答案。
func TestCountParsesDetectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tmpl  string
		input string
		want  int // 期望的解数（上限 2）
	}{
		{"unambiguous simple", "[${ts}] ${lv}", "[a] b", 1},
		{"unambiguous anchored", `${a}.${re|name=n,expr=\d+}`, "x.42", 1},
		// a 可以是 "x"（v="y.z"）也可以是 "x.y"（v="z"）—— 两个解
		{"ambiguous two holes", `${a}.${b}!`, "x.y.z!", 2},
		{"no parse", "[${ts}]", "nope", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compile(t, tc.tmpl)
			if got := p.CountParses([]byte(tc.input), 2); got != tc.want {
				t.Errorf("CountParses(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestCountParsesLimit(t *testing.T) {
	p := compile(t, `${a}.${b}!`)
	// 输入里有 4 个 '.'，理论上 4 个解；limit 必须生效
	input := []byte("a.b.c.d.e!")
	if got := p.CountParses(input, 2); got != 2 {
		t.Errorf("CountParses(limit=2) = %d, want 2", got)
	}
	if got := p.CountParses(input, 10); got != 4 {
		t.Errorf("CountParses(limit=10) = %d, want 4", got)
	}
	// limit <= 0 视为 2
	if got := p.CountParses(input, 0); got != 2 {
		t.Errorf("CountParses(limit=0) = %d, want 2", got)
	}
}

// 回溯路径下定律 A 仍须成立。
func TestBacktrackPreservesLawA(t *testing.T) {
	for _, tc := range []struct{ tmpl, input string }{
		{`${a}.${re|name=n,expr=\d+}`, "x.y.42"},
		{`${a}/${json|name=p}`, `a/b/{"x":1}`},
		{`${k}:${v}!END`, "a:b:c!END"},
		{`${a}.${b}.${re|name=n,expr=\d+}`, "p.q.r.s.7"},
	} {
		p := compile(t, tc.tmpl)
		src := []byte(tc.input)
		res := p.NewResult()
		if !p.Parse(src, res) {
			t.Errorf("%q: parse failed", tc.tmpl)
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
			t.Errorf("定律 A 违反（回溯路径）\n template: %q\n input:  %q\n output: %q",
				tc.tmpl, tc.input, out)
		}
	}
}

func TestBacktrackConcurrentSafe(t *testing.T) {
	p := compile(t, `${a}.${re|name=n,expr=\d+}`)
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() {
			res := p.NewResult()
			for k := 0; k < 300; k++ {
				if !p.Parse([]byte("x.y.42"), res) {
					done <- false
					return
				}
			}
			done <- true
		}()
	}
	for i := 0; i < 8; i++ {
		if !<-done {
			t.Fatal("并发回溯失败")
		}
	}
}

var _ = plan.NoSpan

// 末尾不是 OpRest 时，"必须吃满输入"这个终检本身就是可失败点。
func TestBacktrackForTrailingLiteral(t *testing.T) {
	p := compile(t, `${a}!`)
	if !p.NeedsBacktrack() {
		t.Fatal("以字面量结尾的模板需要回溯")
	}
	got, ok := parse(t, p, "x!y!")
	if !ok {
		t.Fatal(`parse("x!y!") 失败 —— 应回溯到第二个 '!'`)
	}
	if got["a"] != "x!y" {
		t.Errorf("a = %q, want %q", got["a"], "x!y")
	}
}

// 纯字面量链不需要回溯：候选按位置递增枚举，
// 推后边界只会让剩余输入更短，不可能把失败变成成功。
func TestFindChainNeedsNoBacktrack(t *testing.T) {
	for _, tmpl := range []string{
		"${a} ${b} ${c}",
		"${a},${b};${c}",
		"[${a}] [${b}] ${c}",
	} {
		if compile(t, tmpl).NeedsBacktrack() {
			t.Errorf("%q 是纯 Find 链，不该需要回溯", tmpl)
		}
	}
}
