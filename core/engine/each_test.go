package engine_test

import (
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/core/engine"
	"github.com/huge-zhang/zdog-template/core/plan"
)

// items 把某个重复块的字段抽成便于断言的形状。
func items(t *testing.T, e *engine.Engine, input, group string, fields ...string) [][]string {
	t.Helper()
	ctx, err := e.Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse(%q): %v", input, err)
	}
	n, ok := ctx.GroupLen(group)
	if !ok {
		t.Fatalf("unknown group %q", group)
	}
	out := make([][]string, n)
	for i := 0; i < n; i++ {
		item, ok := ctx.GroupItem(group, i)
		if !ok {
			t.Fatalf("item %d missing", i)
		}
		row := make([]string, len(fields))
		for k, f := range fields {
			raw, ok := item.Raw(f)
			if !ok {
				t.Fatalf("item %d field %q not bound", i, f)
			}
			row[k] = string(raw)
		}
		out[i] = row
	}
	return out
}

func eq(t *testing.T, got, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d items %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if strings.Join(got[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Errorf("item %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEachParse(t *testing.T) {
	e := newEngine(t, "items=${each|name=items,sep=;}${id}:${qty}${end}")
	eq(t, items(t, e, "items=a:1;b:2;c:3", "items", "id", "qty"),
		[][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}})
}

func TestEachSingleItem(t *testing.T) {
	e := newEngine(t, "items=${each|name=items,sep=;}${id}:${qty}${end}")
	eq(t, items(t, e, "items=solo:9", "items", "id", "qty"), [][]string{{"solo", "9"}})
}

func TestEachWithTrailingLiteral(t *testing.T) {
	e := newEngine(t, "[${each|name=xs,sep=','}${k}=${v}${end}] tail")
	eq(t, items(t, e, "[a=1,b=2] tail", "xs", "k", "v"),
		[][]string{{"a", "1"}, {"b", "2"}})
}

// 分隔符出现在迭代内容里时，边界必须由"能否被子计划完整消费"裁决，
// 而不是无脑按分隔符切分。
func TestEachSeparatorInsideItem(t *testing.T) {
	e := newEngine(t, "${each|name=xs,sep=;}[${v}]${end}")
	eq(t, items(t, e, "[a;b];[c]", "xs", "v"), [][]string{{"a;b"}, {"c"}})
}

func TestEachEmptyRejectedByDefault(t *testing.T) {
	e := newEngine(t, "items=${each|name=items,sep=;}${id}${end}")
	if _, err := e.Parse([]byte("items=")); err == nil {
		t.Error("零次迭代默认应当不匹配")
	}
	e2 := newEngine(t, "items=${each|name=items,sep=;,empty=true}${id}${end}")
	ctx, err := e2.Parse([]byte("items="))
	if err != nil {
		t.Fatalf("empty=true 应当允许零次迭代: %v", err)
	}
	if n, _ := ctx.GroupLen("items"); n != 0 {
		t.Errorf("GroupLen = %d, want 0", n)
	}
}

func TestNestedEach(t *testing.T) {
	e := newEngine(t, "${each|name=rows,sep=;}${name}(${each|name=cells,sep=','}${c}${end})${end}")
	ctx, err := e.Parse([]byte("r1(a,b);r2(c)"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := ctx.GroupLen("rows")
	if n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	row0, _ := ctx.GroupItem("rows", 0)
	name, _ := row0.Raw("name")
	if string(name) != "r1" {
		t.Errorf("row0.name = %q", name)
	}
	cells, _ := row0.GroupLen("cells")
	if cells != 2 {
		t.Errorf("row0 cells = %d, want 2", cells)
	}
	c1, _ := row0.GroupItem("cells", 1)
	v, _ := c1.Raw("c")
	if string(v) != "b" {
		t.Errorf("row0.cells[1] = %q, want b", v)
	}
}

// 定律 A 必须覆盖重复块 —— 这是最容易出边界错的地方。
func TestEachLawA(t *testing.T) {
	for _, tc := range []struct{ tmpl, input string }{
		{"items=${each|name=xs,sep=;}${id}:${qty}${end}", "items=a:1;b:2;c:3"},
		{"items=${each|name=xs,sep=;}${id}:${qty}${end}", "items=solo:9"},
		{"[${each|name=xs,sep=','}${k}=${v}${end}] tail", "[a=1,b=2] tail"},
		{"${each|name=xs,sep=;}[${v}]${end}", "[a;b];[c]"},
		{"${each|name=rows,sep=;}${n}(${each|name=cs,sep=','}${c}${end})${end}", "r1(a,b);r2(c)"},
		{"pre ${each|name=xs,sep=|}${a}-${b}${end} post", "pre 1-2|3-4 post"},
		{"${each|name=xs,sep=、}${w}${end}", "甲、乙、丙"},
	} {
		e := newEngine(t, tc.tmpl)
		if err := e.VerifyLawA([]byte(tc.input)); err != nil {
			t.Errorf("template %q: %v", tc.tmpl, err)
		}
	}
}

// 定律 B：从空 context 构造重复块并渲染，必须能原样读回。
func TestEachLawB(t *testing.T) {
	e := newEngine(t, "items=${each|name=xs,sep=;}${id}:${qty}${end}")
	ctx := e.NewContext()
	for _, row := range [][2]string{{"a", "1"}, {"b", "2"}} {
		item, err := ctx.AppendGroupItem("xs")
		if err != nil {
			t.Fatal(err)
		}
		_ = item.SetString("id", row[0])
		_ = item.SetString("qty", row[1])
	}
	out, err := e.Format(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "items=a:1;b:2" {
		t.Fatalf("format = %q", out)
	}
	if err := e.VerifyLawB(ctx); err != nil {
		t.Error(err)
	}
}

func TestEachSyntaxErrors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"${each|name=x}${a}${end}", "requires a non-empty sep"},
		{"${each|name=x,sep=;}${a}", "never closed"},
		{"${end}", "no matching ${each}"},
		{"${each|name=x,sep=;}${end}", "empty body"},
		{"${each|name=x,sep=;}${a}${end}${b}", "must be followed by a literal"},
	} {
		_, err := engine.New(tc.src)
		if err == nil {
			t.Errorf("New(%q): expected error", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("New(%q): error = %q, want substring %q", tc.src, err, tc.want)
		}
	}
}

// 每个 ${each} 层是独立命名空间，内外同名不冲突。
func TestEachScopedNames(t *testing.T) {
	e := newEngine(t, "${v}|${each|name=xs,sep=','}${v}${end}")
	ctx, err := e.Parse([]byte("outer|a,b"))
	if err != nil {
		t.Fatal(err)
	}
	outer, _ := ctx.Raw("v")
	if string(outer) != "outer" {
		t.Errorf("outer v = %q", outer)
	}
	item, _ := ctx.GroupItem("xs", 1)
	inner, _ := item.Raw("v")
	if string(inner) != "b" {
		t.Errorf("inner v = %q", inner)
	}
}

func TestEachTier(t *testing.T) {
	e := newEngine(t, "${each|name=xs,sep=','}${a}=${b}${end}")
	if got := e.Tier(); got != plan.TierEach {
		t.Errorf("tier = %v, want %v", got, plan.TierEach)
	}
}
