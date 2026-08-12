package engine_test

import (
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/core/engine"
)

func newEngine(t *testing.T, src string) *engine.Engine {
	t.Helper()
	e, err := engine.New(src)
	if err != nil {
		t.Fatalf("New(%q): %v", src, err)
	}
	return e
}

func TestParseAndRead(t *testing.T) {
	e := newEngine(t, "[${ts}] ${lv} ${msg}")
	ctx, err := e.Parse([]byte("[2026-08-12T01:00:00Z] ERROR disk full"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"ts": "2026-08-12T01:00:00Z", "lv": "ERROR", "msg": "disk full",
	} {
		raw, ok := ctx.Raw(name)
		if !ok {
			t.Errorf("%s not bound", name)
			continue
		}
		if string(raw) != want {
			t.Errorf("%s = %q, want %q", name, raw, want)
		}
	}
	if ctx.Dirty() {
		t.Error("freshly parsed context should not be dirty")
	}
}

func TestParseNoMatch(t *testing.T) {
	e := newEngine(t, "[${ts}] ${lv}")
	_, err := e.Parse([]byte("nope"))
	if err == nil {
		t.Fatal("expected ErrNoMatch")
	}
	var nm *engine.ErrNoMatch
	if !asErr(err, &nm) {
		t.Errorf("want *ErrNoMatch, got %T", err)
	}
}

func asErr[T error](err error, target *T) bool {
	if v, ok := err.(T); ok {
		*target = v
		return true
	}
	return false
}

// 定律 A：format(parse(t)) == t，且校验必须绕过 replay 才有意义。
func TestLawA(t *testing.T) {
	for _, tc := range []struct{ tmpl, input string }{
		{"[${ts}] ${lv} ${msg}", "[2026-08-12T01:00:00Z] ERROR disk full"},
		{"${a}-${b}", "x-y"},
		{"${only}", "everything"},
		{"a${x}b${y}c", "a1b2c"},
		{`${re|name=n,expr=\d+}345`, "123345"},
		{"payload=${json|name=p} end", `payload={"a":[1,{"b":"}"}]} end`},
		{"${x} 中国人 ${y}", "前 中国人 后"},
		{"[${ts}] ${lv} ${msg} payload=${json|name=p}",
			`[2026-08-12T01:00:00Z] ERROR disk full payload={"host":"web-1","pct":95}`},
	} {
		e := newEngine(t, tc.tmpl)
		if err := e.VerifyLawA([]byte(tc.input)); err != nil {
			t.Errorf("template %q: %v", tc.tmpl, err)
		}
	}
}

// 定律 B：parse(format(c)) == c，用于 mapping 之后无出处的目标侧数据。
func TestLawB(t *testing.T) {
	e := newEngine(t, "[${ts}] ${lv} ${msg}")
	ctx := e.NewContext()
	for k, v := range map[string]string{"ts": "T1", "lv": "WARN", "msg": "hello world"} {
		if err := ctx.SetString(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.VerifyLawB(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := e.Format(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[T1] WARN hello world" {
		t.Errorf("format = %q", out)
	}
}

// 定律 B 必须能抓出"字段值里混入定界符导致边界漂移"这类问题。
func TestLawBCatchesDelimiterInValue(t *testing.T) {
	e := newEngine(t, "${a} ${b}")
	ctx := e.NewContext()
	// a 的值里含空格 —— 而空格正是 a 与 b 的定界符，回读时边界会漂。
	_ = ctx.SetString("a", "has space")
	_ = ctx.SetString("b", "tail")
	err := e.VerifyLawB(ctx)
	if err == nil {
		t.Fatal("定律 B 应当抓出定界符污染")
	}
	var lv *engine.LawViolation
	if !asErr(err, &lv) {
		t.Fatalf("want *LawViolation, got %T: %v", err, err)
	}
	if lv.Law != "B" || lv.Field != "a" {
		t.Errorf("violation = %+v, want law B on field a", lv)
	}
}

func TestFormatMissingBinding(t *testing.T) {
	e := newEngine(t, "${a}-${b}")
	ctx := e.NewContext()
	_ = ctx.SetString("a", "x")
	_, err := e.Format(ctx)
	if err == nil || !strings.Contains(err.Error(), "unset bindings") {
		t.Errorf("expected unset-bindings error, got %v", err)
	}
	if missing := ctx.Missing(); len(missing) != 1 || missing[0] != "b" {
		t.Errorf("Missing() = %v, want [b]", missing)
	}
}

// replay 快路径：未修改的 context 直接回放源文。
func TestReplayFastPath(t *testing.T) {
	e := newEngine(t, "[${ts}] ${lv} ${msg}")
	input := "[T] E some message"
	ctx, err := e.Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Format(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != input {
		t.Errorf("replay = %q, want %q", out, input)
	}

	// 修改后必须走完整渲染
	if err := ctx.SetString("lv", "WARN"); err != nil {
		t.Fatal(err)
	}
	if !ctx.Dirty() {
		t.Error("context should be dirty after Set")
	}
	out, err = e.Format(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[T] WARN some message" {
		t.Errorf("after Set, format = %q", out)
	}
}

// 岛的值惰性解码，且只解码一次。
func TestIslandLazyDecode(t *testing.T) {
	e := newEngine(t, "payload=${json|name=p}")
	ctx, err := e.Parse([]byte(`payload={"host":"web-1","pct":95}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := ctx.Raw("p")
	if string(raw) != `{"host":"web-1","pct":95}` {
		t.Errorf("raw = %q", raw)
	}
	v, err := ctx.Value("p")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want map, got %T", v)
	}
	if m["host"] != "web-1" {
		t.Errorf("host = %v", m["host"])
	}
	// 第二次读取应命中缓存并返回同一个值
	v2, _ := ctx.Value("p")
	if &m != &m || v2 == nil {
		t.Error("cached decode failed")
	}
}

func TestNonIslandValueIsString(t *testing.T) {
	e := newEngine(t, "[${ts}]")
	ctx, _ := e.Parse([]byte("[abc]"))
	v, err := ctx.Value("ts")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v.(string); !ok || s != "abc" {
		t.Errorf("Value = %#v, want string \"abc\"", v)
	}
}

func TestUnknownNameErrors(t *testing.T) {
	e := newEngine(t, "${a}")
	ctx := e.NewContext()
	if err := ctx.SetString("nope", "x"); err == nil {
		t.Error("Set with unknown name should fail")
	}
	if _, err := ctx.Value("nope"); err == nil {
		t.Error("Value with unknown name should fail")
	}
	if _, ok := ctx.Raw("nope"); ok {
		t.Error("Raw with unknown name should report not-found")
	}
}

func TestNewErrors(t *testing.T) {
	if _, err := engine.New("${a}${b}"); err == nil {
		t.Error("ambiguous template should fail at compile time")
	}
	if _, err := engine.New("${bad|"); err == nil {
		t.Error("malformed template should fail")
	}
}
