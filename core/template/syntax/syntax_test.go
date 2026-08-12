package syntax

import (
	"reflect"
	"testing"
)

func el(v string) Piece { return Piece{Kind: KindElement, Value: v} }
func tx(v string) Piece { return Piece{Kind: KindText, Value: v} }

func values(ps []Piece) []Piece {
	if len(ps) == 0 {
		return nil
	}
	out := make([]Piece, len(ps))
	for i, p := range ps {
		out[i] = Piece{Kind: p.Kind, Value: p.Value} // 丢掉 Pos 便于比较
	}
	return out
}

func TestScan(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []Piece
	}{
		{"pure text", "hello world", []Piece{tx("hello world")}},
		{"empty", "", nil},
		{"single element", "${a}", []Piece{el("a")}},
		{"mixed", "[${ts}] ${lv}", []Piece{tx("["), el("ts"), tx("] "), el("lv")}},
		{"adjacent elements", "${a}${b}", []Piece{el("a"), el("b")}},
		{"trailing text", "${a}tail", []Piece{el("a"), tx("tail")}},
		{"lone dollar", "cost $5", []Piece{tx("cost $5")}},
		{"dollar at end", "abc$", []Piece{tx("abc$")}},
		{"dollar not brace", "a$b${c}", []Piece{tx("a$b"), el("c")}},
		{"full expr", "${re|name=lv,expr=\\w+}", []Piece{el("re|name=lv,expr=\\w+")}},
	} {
		got, err := Scan(tc.src)
		if err != nil {
			t.Errorf("%s: Scan(%q) error: %v", tc.name, tc.src, err)
			continue
		}
		if !reflect.DeepEqual(values(got), tc.want) {
			t.Errorf("%s: Scan(%q) = %v, want %v", tc.name, tc.src, values(got), tc.want)
		}
	}
}

// 旧原型无法表达属性值里的 } 与 , —— 这是本次新增的转义能力。
// 注意：Scan 只负责用转义定位结束的 }，元素体保持原样，
// 反转义由 ParseAttrs 完成（否则会双重反转义，切坏属性边界）。
func TestScanEscapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []Piece
	}{
		{"escaped brace in expr", `${re|name=x,expr=\d{1\,3\}}`, []Piece{el(`re|name=x,expr=\d{1\,3\}`)}},
		{"escaped dollar", `\${not an element}`, []Piece{tx("${not an element}")}},
		{"escaped backslash", `a\\b`, []Piece{tx(`a\b`)}},
		{"regex metachar survives", `${re|expr=\d+\s*}`, []Piece{el(`re|expr=\d+\s*`)}},
	} {
		got, err := Scan(tc.src)
		if err != nil {
			t.Errorf("%s: Scan(%q) error: %v", tc.name, tc.src, err)
			continue
		}
		if !reflect.DeepEqual(values(got), tc.want) {
			t.Errorf("%s: Scan(%q) = %v, want %v", tc.name, tc.src, values(got), tc.want)
		}
	}
}

func TestScanUnterminated(t *testing.T) {
	if _, err := Scan("abc${def"); err == nil {
		t.Error("expected error for unterminated ${")
	}
}

func TestSplitExpr(t *testing.T) {
	for _, tc := range []struct {
		expr, tag, args string
		sugar           bool
	}{
		{"re|name=x", "re", "name=x", false},
		{"text|content=hi", "text", "content=hi", false},
		{"ts", "", "name=ts", true},
		{"user.name", "", "name=user.name", true},
		{"ext|extension=echo,name=e", "ext", "extension=echo,name=e", false},
	} {
		tag, args, sugar := SplitExpr(tc.expr)
		if tag != tc.tag || args != tc.args || sugar != tc.sugar {
			t.Errorf("SplitExpr(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.expr, tag, args, sugar, tc.tag, tc.args, tc.sugar)
		}
	}
}

func TestParseAttrs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want Attrs
	}{
		{"simple", "name=x", Attrs{"name": "x"}},
		{"multi", "name=x,expr=y", Attrs{"name": "x", "expr": "y"}},
		{"empty", "", Attrs{}},
		{"flag without value", "greedy", Attrs{"greedy": ""}},
		{"empty value", "content=", Attrs{"content": ""}},
		// 修复 1：SplitN —— 旧版 Split(item,"=") 会把 a=b 截断成 a
		{"equals in value", "expr=a=b", Attrs{"expr": "a=b"}},
		// 修复 2：按未转义逗号切分 —— 旧版会把 \d{1,3} 切两段
		{"escaped comma", `expr=\d{1\,3},name=n`, Attrs{"expr": `\d{1,3}`, "name": "n"}},
		{"regex metachar", `expr=\w+`, Attrs{"expr": `\w+`}},
	} {
		got, err := ParseAttrs(tc.args)
		if err != nil {
			t.Errorf("%s: ParseAttrs(%q) error: %v", tc.name, tc.args, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ParseAttrs(%q) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestParseAttrsError(t *testing.T) {
	if _, err := ParseAttrs("=novalue"); err == nil {
		t.Error("expected error for empty attribute name")
	}
}

// 单引号包裹的值：`sep=','` 是最常见的分隔符写法，
// 若强制写成 `sep=\,` 太反直觉。
func TestParseAttrsQuoted(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want Attrs
	}{
		{"quoted comma", `name=xs,sep=','`, Attrs{"name": "xs", "sep": ","}},
		{"quoted comma first", `sep=',',name=xs`, Attrs{"name": "xs", "sep": ","}},
		{"quoted with space", `sep=', '`, Attrs{"sep": ", "}},
		{"quoted brace", `sep='}'`, Attrs{"sep": "}"}},
		{"escaped comma still works", `sep=\,`, Attrs{"sep": ","}},
		{"apostrophe mid-value is literal", `expr=it's`, Attrs{"expr": "it's"}},
		{"empty quotes", `sep=''`, Attrs{"sep": ""}},
	} {
		got, err := ParseAttrs(tc.args)
		if err != nil {
			t.Errorf("%s: ParseAttrs(%q) error: %v", tc.name, tc.args, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ParseAttrs(%q) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	for _, s := range []string{`a,b`, `a}b`, `a\b`, `plain`} {
		esc := EscapeAttrValue(s)
		got, err := ParseAttrs("v=" + esc)
		if err != nil {
			t.Fatalf("ParseAttrs error: %v", err)
		}
		if got["v"] != s {
			t.Errorf("EscapeAttrValue round trip: %q -> %q -> %q", s, esc, got["v"])
		}
	}
	for _, s := range []string{`a$b`, `a\b`, `${x}`, `plain`} {
		esc := EscapeText(s)
		ps, err := Scan(esc)
		if err != nil {
			t.Fatalf("Scan(%q) error: %v", esc, err)
		}
		if len(ps) != 1 || ps[0].Kind != KindText || ps[0].Value != s {
			t.Errorf("EscapeText round trip: %q -> %q -> %v", s, esc, values(ps))
		}
	}
}
