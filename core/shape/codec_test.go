package shape_test

import (
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/core/shape"
)

func codec(t *testing.T, def string) *shape.Codec {
	t.Helper()
	c, err := shape.LoadCodec([]byte(def))
	if err != nil {
		t.Fatalf("LoadCodec(%s): %v", def, err)
	}
	return c
}

func enc(t *testing.T, c *shape.Codec, v any) string {
	t.Helper()
	out, err := c.Encode(nil, v)
	if err != nil {
		t.Fatalf("Encode(%v): %v", v, err)
	}
	return string(out)
}

func TestCodecEncodeScalars(t *testing.T) {
	for _, tc := range []struct {
		def  string
		in   any
		want string
	}{
		{`{"type":"string"}`, "abc", "abc"},
		{`{"type":"string"}`, 42.0, "42"},
		{`{"type":"string"}`, true, "true"},
		{`{"type":"number"}`, 42.0, "42"},
		{`{"type":"number"}`, 3.5, "3.5"},
		{`{"type":"number"}`, "42", "42"},
		{`{"type":"number"}`, 1000000.0, "1000000"},
		{`{"type":"bool"}`, true, "true"},
		{`{"type":"bool"}`, "false", "false"},
		{`{"type":"any"}`, map[string]any{"a": 1.0}, `{"a":1}`},
		{`{"type":"array","items":{"type":"string"}}`, []any{"a", "b"}, `["a","b"]`},
	} {
		if got := enc(t, codec(t, tc.def), tc.in); got != tc.want {
			t.Errorf("%s Encode(%v) = %q, want %q", tc.def, tc.in, got, tc.want)
		}
	}
}

// format 是 shape 的核心增值：无 Raw 出处的字段靠它决定长什么样。
func TestCodecFormat(t *testing.T) {
	for _, tc := range []struct {
		def  string
		in   any
		want string
	}{
		{`{"type":"number","format":"%.2f"}`, 3.14159, "3.14"},
		{`{"type":"number","format":"%05.1f"}`, 3.14159, "003.1"},
		{`{"type":"number","format":"%d"}`, 42.0, "%!d(float64=42)"}, // 类型不匹配会被 fmt 标出来
		{`{"type":"string","format":"%-6s|"}`, "ab", "ab    |"},
		{`{"type":"string","format":"[%s]"}`, "x", "[x]"},
	} {
		if got := enc(t, codec(t, tc.def), tc.in); got != tc.want {
			t.Errorf("%s Encode(%v) = %q, want %q", tc.def, tc.in, got, tc.want)
		}
	}
}

func TestCodecNullHandling(t *testing.T) {
	// 无 default、非必填 -> 空
	if got := enc(t, codec(t, `{"type":"string"}`), nil); got != "" {
		t.Errorf("null -> %q, want empty", got)
	}
	// 有 default -> 用 default
	if got := enc(t, codec(t, `{"type":"string","default":"N/A"}`), nil); got != "N/A" {
		t.Errorf("null with default -> %q, want N/A", got)
	}
	// 必填且无值 -> 报错
	c := codec(t, `{"type":"string","required":true}`)
	if _, err := c.Encode(nil, nil); err == nil {
		t.Error("必填字段值为空时应当报错")
	}
	// 必填但有 default -> 用 default，不报错
	if got := enc(t, codec(t, `{"type":"number","required":true,"default":0}`), nil); got != "0" {
		t.Errorf("required with default -> %q, want 0", got)
	}
}

// 定律 B 在 codec 层的表述：Encode 与 Decode 必须互逆。
func TestCodecEncodeDecodeInverse(t *testing.T) {
	for _, tc := range []struct {
		def string
		v   any
	}{
		{`{"type":"string"}`, "hello world"},
		{`{"type":"string"}`, "中国人"},
		{`{"type":"number"}`, 42.0},
		{`{"type":"number"}`, -3.5},
		{`{"type":"number"}`, 1000000.0},
		{`{"type":"bool"}`, true},
		{`{"type":"bool"}`, false},
		{`{"type":"number","format":"%.2f"}`, 3.14},
	} {
		c := codec(t, tc.def)
		encoded, err := c.Encode(nil, tc.v)
		if err != nil {
			t.Fatalf("%s Encode: %v", tc.def, err)
		}
		back, err := c.Decode(encoded)
		if err != nil {
			t.Fatalf("%s Decode(%q): %v", tc.def, encoded, err)
		}
		reencoded, err := c.Encode(nil, back)
		if err != nil {
			t.Fatalf("%s re-Encode: %v", tc.def, err)
		}
		if string(reencoded) != string(encoded) {
			t.Errorf("定律 B 违反（codec 层）%s\n  值:     %v\n  编码:   %q\n  回读:   %v\n  再编码: %q",
				tc.def, tc.v, encoded, back, reencoded)
		}
	}
}

func TestCodecDecodeErrors(t *testing.T) {
	for _, tc := range []struct{ def, raw, want string }{
		{`{"type":"number"}`, "notanumber", "不是数字"},
		{`{"type":"bool"}`, "maybe", "不是布尔值"},
		{`{"type":"any"}`, "{bad json", "不是合法 JSON"},
		{`{"type":"string","required":true}`, "", "必填"},
	} {
		c := codec(t, tc.def)
		if _, err := c.Decode([]byte(tc.raw)); err == nil {
			t.Errorf("%s Decode(%q): expected error", tc.def, tc.raw)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s Decode(%q): error = %q, want substring %q", tc.def, tc.raw, err, tc.want)
		}
	}
}

func TestCodecEncodeTypeErrors(t *testing.T) {
	c := codec(t, `{"type":"number"}`)
	if _, err := c.Encode(nil, []any{1, 2}); err == nil {
		t.Error("把数组编码成 number 应当报错")
	}
	c2 := codec(t, `{"type":"bool"}`)
	if _, err := c2.Encode(nil, 1.0); err == nil {
		t.Error("把数字编码成 bool 应当报错")
	}
}

// format 与类型不匹配必须在编译期发现，而不是每行才报。
func TestCodecFormatValidatedAtCompileTime(t *testing.T) {
	for _, tc := range []struct{ def, want string }{
		{`{"type":"array","items":{"type":"string"},"format":"%s"}`, "不支持 format"},
		{`{"type":"any","format":"%s"}`, "不支持 format"},
		{`{"type":"number","format":"no verb"}`, "必须恰好含一个动词"},
		{`{"type":"number","format":"%d %d"}`, "必须恰好含一个动词"},
	} {
		_, err := shape.LoadCodec([]byte(tc.def))
		if err == nil {
			t.Errorf("%s: expected compile-time error", tc.def)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want substring %q", tc.def, err, tc.want)
		}
	}
}

func TestNewCodecNil(t *testing.T) {
	if _, err := shape.NewCodec(nil); err == nil {
		t.Error("NewCodec(nil): expected error")
	}
}
