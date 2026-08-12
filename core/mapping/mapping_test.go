package mapping_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/litterzhang/zdog-template/core/mapping"
)

// mapEnv 是测试用的简单环境。
type mapEnv map[string]any

func (m mapEnv) Value(name string) (any, bool, error) {
	v, ok := m[name]
	return v, ok, nil
}

func evalStr(t *testing.T, expr string, env mapEnv) string {
	t.Helper()
	e, err := mapping.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	v, err := mapping.Eval(e, env)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	out, err := mapping.Serialize(nil, v)
	if err != nil {
		t.Fatalf("Serialize(%q): %v", expr, err)
	}
	return string(out)
}

func obj(s string) any {
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	_ = dec.Decode(&v)
	return v
}

func TestBareFieldIsFastPath(t *testing.T) {
	e, err := mapping.Compile("lv")
	if err != nil {
		t.Fatal(err)
	}
	name, bare := mapping.IsBareField(e)
	if !bare || name != "lv" {
		t.Errorf("IsBareField = (%q,%v), want (lv,true)", name, bare)
	}
	// 任何非裸字段都不能被误判成快路径
	for _, src := range []string{"a.b", "a[0]", "upper(a)", "a || 'x'", "'lit'"} {
		e, err := mapping.Compile(src)
		if err != nil {
			t.Fatalf("Compile(%q): %v", src, err)
		}
		if _, bare := mapping.IsBareField(e); bare {
			t.Errorf("%q 不应被判为裸字段", src)
		}
	}
}

func TestPaths(t *testing.T) {
	env := mapEnv{
		"lv": "error",
		"p":  obj(`{"host":"web-1","tags":["a","b","c"],"n":{"deep":42},"pct":95}`),
	}
	for _, tc := range []struct{ expr, want string }{
		{"lv", "error"},
		{"p.host", "web-1"},
		{"p.n.deep", "42"},
		{"p.tags[0]", "a"},
		{"p.tags[2]", "c"},
		{"p.tags[-1]", "c"},
		{"p.pct", "95"},
		{"p.missing", ""},
		{"p.missing.deeper", ""},
		{"nosuch", ""},
		{"p.tags[99]", ""},
		{"lv.notanobject", ""},
	} {
		if got := evalStr(t, tc.expr, env); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestLiterals(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{"'hello'", "hello"},
		{"'with space'", "with space"},
		{"42", "42"},
		{"-7", "-7"},
		{"3.5", "3.5"},
		{"true", "true"},
		{"false", "false"},
		{"null", ""},
		{`'it\'s'`, "it's"},
	} {
		if got := evalStr(t, tc.expr, mapEnv{}); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// JMESPath 的 || 是"取第一个非假值"，数字 0 不算假值。
func TestOrFallback(t *testing.T) {
	env := mapEnv{
		"a": "", "b": "val", "zero": float64(0),
		"p": obj(`{"host":null,"empty":[],"eobj":{}}`),
	}
	for _, tc := range []struct{ expr, want string }{
		{"a || 'fallback'", "fallback"},
		{"b || 'fallback'", "val"},
		{"nosuch || 'fallback'", "fallback"},
		{"p.host || 'unknown'", "unknown"},
		{"p.empty || 'none'", "none"},
		{"p.eobj || 'none'", "none"},
		{"zero || 'fallback'", "0"},
		{"a || b || 'last'", "val"},
		{"a || nosuch || 'last'", "last"},
	} {
		if got := evalStr(t, tc.expr, env); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestFunctions(t *testing.T) {
	env := mapEnv{
		"s": "  Hello World  ",
		"p": obj(`{"tags":["b","a","c"],"n":"42","m":{"x":1,"y":2}}`),
	}
	for _, tc := range []struct{ expr, want string }{
		{"upper('abc')", "ABC"},
		{"lower('ABC')", "abc"},
		{"trim(s)", "Hello World"},
		{"upper(trim(s))", "HELLO WORLD"},
		{"length('abc')", "3"},
		{"length('中国人')", "3"},
		{"length(p.tags)", "3"},
		{"length(p.m)", "2"},
		{"join('-', p.tags)", "b-a-c"},
		{"join('-', sort(p.tags))", "a-b-c"},
		{"join(',', keys(p.m))", "x,y"},
		{"to_number(p.n)", "42"},
		{"to_string(42)", "42"},
		{"type(p.tags)", "array"},
		{"type(p.m)", "object"},
		{"type(s)", "string"},
		{"type(nosuch)", "null"},
		{"starts_with('abcdef', 'abc')", "true"},
		{"ends_with('abcdef', 'def')", "true"},
		{"contains('abcdef', 'cd')", "true"},
		{"contains(p.tags, 'a')", "true"},
		{"reverse('abc')", "cba"},
		{"replace('a-b-c', '-', '+')", "a+b+c"},
		{"join('|', split('a-b-c', '-'))", "a|b|c"},
		{"not_null(nosuch, 'x')", "x"},
		{"upper(nosuch)", ""},
		{"mask('13812345678')", "***********"},
		{"mask('13812345678', 4)", "*******5678"},
		{"mask('13812345678', 0)", "***********"},
		{"mask('张三丰', 1)", "**丰"}, // 按 rune，不切碎多字节字符
		{"mask(nosuch)", ""},
	} {
		if got := evalStr(t, tc.expr, env); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// 复合值退回 JSON 序列化。
func TestSerializeComposite(t *testing.T) {
	env := mapEnv{"p": obj(`{"tags":["a","b"]}`)}
	if got := evalStr(t, "p.tags", env); got != `["a","b"]` {
		t.Errorf("p.tags = %q", got)
	}
}

// 整数不得经由 float64 变形成 1e+06。
func TestSerializeLargeInteger(t *testing.T) {
	if got := evalStr(t, "to_number('1000000')", mapEnv{}); got != "1000000" {
		t.Errorf("to_number('1000000') = %q, want 1000000", got)
	}
	env := mapEnv{"p": obj(`{"big":1000000}`)}
	if got := evalStr(t, "p.big", env); got != "1000000" {
		t.Errorf("p.big = %q, want 1000000", got)
	}
}

func TestRefs(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want []string
	}{
		{"lv", []string{"lv"}},
		{"p.host", []string{"p"}},
		{"upper(a)", []string{"a"}},
		{"a || b", []string{"a", "b"}},
		{"join('-', p.tags)", []string{"p"}},
		{"'literal'", nil},
		{"a.b || c[0]", []string{"a", "c"}},
	} {
		e, err := mapping.Compile(tc.expr)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.expr, err)
		}
		got := mapping.Refs(e)
		if len(got) != len(tc.want) {
			t.Errorf("Refs(%q) = %v, want %v", tc.expr, got, tc.want)
			continue
		}
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		for _, w := range tc.want {
			if !set[w] {
				t.Errorf("Refs(%q) = %v, missing %q", tc.expr, got, w)
			}
		}
	}
}

// TestMaskFailsClosed 锁住 mask() 唯一一处安全相关的行为。
//
// 输入比 keep-tail 短时必须全部打码，而不是把原值透出去。真实数据里总会有
// 异常短的值（测试号码、脏数据、被上游截断的字段），如果那时候 mask 返回原值，
// 泄漏就发生在最不可能被人工核对的那几行上。
func TestMaskFailsClosed(t *testing.T) {
	env := mapEnv{}
	for _, tc := range []struct{ expr, want string }{
		{"mask('123', 4)", "***"},     // keep 比长度大
		{"mask('1234', 4)", "****"},   // keep 恰好等于长度，同样不能原样返回
		{"mask('12345', 4)", "*2345"}, // 正常情况：只有这时才保留尾部
		{"mask('', 4)", ""},
	} {
		if got := evalStr(t, tc.expr, env); got != tc.want {
			t.Errorf("%s = %q, want %q（泄漏原值即为安全缺陷）", tc.expr, got, tc.want)
		}
	}

	// keep-tail 必须是非负整数，写错了要当场报错而不是静默取整
	for _, expr := range []string{"mask('abc', -1)", "mask('abc', 1.5)"} {
		e, err := mapping.Compile(expr)
		if err != nil {
			continue // 编译期就拦下也可以
		}
		if _, err := mapping.Eval(e, env); err == nil {
			t.Errorf("Eval(%q): expected an error", expr)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{"", "empty expression"},
		{"a.", "expected a property name"},
		{"a[", "index must be an integer"},
		{"a[*]", "projection"},
		{"a[?x]", "filter"},
		{"a | b", "pipe operator"},
		{"nosuchfn(a)", "unknown function"},
		{"upper()", "takes 1 argument"},
		{"upper(a, b)", "takes 1 argument"},
		{"join('-')", "takes 2 argument"},
		{"'unterminated", "unterminated"},
		{"a b", "unexpected trailing input"},
		{"@", "unexpected character"},
		{`a || "x"`, "single quotes"}, // 双引号是最容易踩的坑，错误必须点明
		{"a[0", "missing ']'"},
		{"upper(a", "missing ')'"},
	} {
		_, err := mapping.Compile(tc.expr)
		if err == nil {
			t.Errorf("Compile(%q): expected error", tc.expr)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Compile(%q): error = %q, want substring %q", tc.expr, err, tc.want)
		}
	}
}

// 错误信息必须列出可用函数，否则用户无从下手。
func TestUnknownFunctionListsAvailable(t *testing.T) {
	_, err := mapping.Compile("nope(a)")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, fn := range []string{"upper", "join", "to_number"} {
		if !strings.Contains(err.Error(), fn) {
			t.Errorf("错误信息未列出 %q: %v", fn, err)
		}
	}
}

func TestExprString(t *testing.T) {
	for _, src := range []string{"lv", "p.host", "p.tags[0]", "upper(lv)", "a || 'x'"} {
		e, err := mapping.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		if e.String() == "" {
			t.Errorf("String() for %q is empty", src)
		}
	}
}
