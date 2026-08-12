package element

import (
	"encoding/json"
	"testing"

	"github.com/litterzhang/zdog-template/core/template/syntax"
)

// 验证 json.Decoder.InputOffset() 能精确给出嵌入 JSON 值的结束位置 ——
// 这是替换旧原型 555 行手写状态机的前提。
func TestJSONIslandScan(t *testing.T) {
	island := NewJSONIsland("payload")
	for _, tc := range []struct {
		name    string
		src     string
		pos     int
		wantRaw string
	}{
		{"object", `{"a":1}`, 0, `{"a":1}`},
		{"object with trailing text", `{"a":1} trailing`, 0, `{"a":1}`},
		{"nested", `{"a":[1,2,{"b":"}"}],"c":{"d":[]}} tail`, 0, `{"a":[1,2,{"b":"}"}],"c":{"d":[]}}`},
		{"array", `[1,"two",null] rest`, 0, `[1,"two",null]`},
		{"string with escapes", `"a\"b\\" rest`, 0, `"a\"b\\"`},
		{"number", `95, more`, 0, `95`},
		{"true", `true}`, 0, `true`},
		{"null", `null `, 0, `null`},
		{"offset start", `payload={"a":1} x`, 8, `{"a":1}`},
		{"leading whitespace", `   {"a":1}`, 0, `   {"a":1}`},
		{"unicode", `{"k":"中国人"} tail`, 0, `{"k":"中国人"}`},
	} {
		end, ok := island.Scan([]byte(tc.src), tc.pos)
		if !ok {
			t.Errorf("%s: Scan(%q,%d) failed", tc.name, tc.src, tc.pos)
			continue
		}
		if got := tc.src[tc.pos:end]; got != tc.wantRaw {
			t.Errorf("%s: raw = %q, want %q", tc.name, got, tc.wantRaw)
		}
		val, err := island.Decode([]byte(tc.src[tc.pos:end]))
		if err != nil {
			t.Errorf("%s: Decode error: %v", tc.name, err)
		}
		if val == nil && tc.name != "null" {
			t.Errorf("%s: decoded value is nil", tc.name)
		}
	}
}

func TestJSONIslandScanFailure(t *testing.T) {
	island := NewJSONIsland("p")
	for _, src := range []string{``, `not json`, `{`, `[1,`} {
		if _, ok := island.Scan([]byte(src), 0); ok {
			t.Errorf("Scan(%q) unexpectedly succeeded", src)
		}
	}
	if _, ok := island.Scan([]byte(`{"a":1}`), 99); ok {
		t.Error("Scan with out-of-range pos unexpectedly succeeded")
	}
	if _, ok := island.Scan([]byte(`{"a":1}`), -1); ok {
		t.Error("Scan with negative pos unexpectedly succeeded")
	}
}

// 非 strict 模式只求边界，结构平衡但内容非法的 JSON 会通过 Scan ——
// 这是刻意的取舍（67 ns vs 360 ns），错误留给 Decode 暴露。
func TestJSONIslandStrictMode(t *testing.T) {
	const malformed = `{"a":}`
	if _, ok := NewJSONIsland("p").Scan([]byte(malformed), 0); !ok {
		t.Error("non-strict island should accept structurally balanced input")
	}
	if _, ok := NewStrictJSONIsland("p").Scan([]byte(malformed), 0); ok {
		t.Error("strict island should reject malformed json")
	}
	if _, err := NewJSONIsland("p").Decode([]byte(malformed)); err == nil {
		t.Error("Decode should reject malformed json")
	}
}

// UseNumber 保证整数不会经由 float64 变形（1000000 -> 1e+06）。
func TestJSONIslandPreservesIntegers(t *testing.T) {
	island := NewJSONIsland("p")
	val, err := island.Decode([]byte(`{"n":1000000}`))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	m := val.(map[string]any)
	num, isNumber := m["n"].(json.Number)
	if !isNumber {
		t.Fatalf("want json.Number, got %T", m["n"])
	}
	if num.String() != "1000000" {
		t.Errorf("number = %s, want 1000000", num)
	}
}

func TestRawTextAndHoleDump(t *testing.T) {
	if got := NewRawText("a$b").Dump(); got != `a\$b` {
		t.Errorf("RawText.Dump() = %q, want %q", got, `a\$b`)
	}
	h, err := NewHole("ts", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Dump(); got != "${ts}" {
		t.Errorf("Hole.Dump() = %q, want ${ts}", got)
	}
	h2, err := NewHole("lv", `\w+`, true)
	if err != nil {
		t.Fatal(err)
	}
	// 反斜杠必须转义：否则模式 `\,` 会在 unescape 时退化成 `,` 而丢字符。
	if got := h2.Dump(); got != `${re|name=lv,expr=\\w+,greedy=true}` {
		t.Errorf("Hole.Dump() = %q", got)
	}
	if _, err := NewHole("bad", `[unclosed`, false); err == nil {
		t.Error("expected error for invalid regex")
	}
}

// Dump 出来的文本必须能被 syntax 重新解析回同样的属性。
func TestHoleDumpReparsable(t *testing.T) {
	for _, pattern := range []string{`\w+`, `\d{1,3}`, `a,b`, `x}y`, `\,`, `[^\]]*`} {
		h, err := NewHole("n", pattern, false)
		if err != nil {
			t.Fatalf("NewHole(%q): %v", pattern, err)
		}
		pieces, err := syntax.Scan(h.Dump())
		if err != nil {
			t.Fatalf("Scan(%q): %v", h.Dump(), err)
		}
		if len(pieces) != 1 || pieces[0].Kind != syntax.KindElement {
			t.Fatalf("pattern %q: Dump()=%q scanned to %v", pattern, h.Dump(), pieces)
		}
		_, args, _ := syntax.SplitExpr(pieces[0].Value)
		attrs, err := syntax.ParseAttrs(args)
		if err != nil {
			t.Fatalf("ParseAttrs(%q): %v", args, err)
		}
		if attrs["expr"] != pattern {
			t.Errorf("pattern %q: round trip via %q gave %q", pattern, h.Dump(), attrs["expr"])
		}
	}
}

func TestHoleRegexpAnchored(t *testing.T) {
	h, err := NewHole("n", `\d+`, false)
	if err != nil {
		t.Fatal(err)
	}
	re := h.Regexp()
	if loc := re.FindIndex([]byte("abc123")); loc != nil {
		t.Errorf("anchored regex matched at non-zero offset: %v", loc)
	}
	if loc := re.FindIndex([]byte("123abc")); loc == nil || loc[0] != 0 {
		t.Errorf("anchored regex failed to match at 0: %v", loc)
	}
}
