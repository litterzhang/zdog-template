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
		// %% 是字面百分号，不是动词 —— 百分比格式很常用
		{`{"type":"number","format":"%.1f%%"}`, 93.456, "93.5%"},
		{`{"type":"number","format":"%d%%"}`, 42.0, "%!d(float64=42)%"},
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
		{`{"type":"number"}`, "notanumber", "is not a number"},
		{`{"type":"bool"}`, "maybe", "is not a boolean"},
		{`{"type":"any"}`, "{bad json", "is not valid JSON"},
		{`{"type":"string","required":true}`, "", "required field"},
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
		{`{"type":"array","items":{"type":"string"},"format":"%s"}`, "does not support format"},
		{`{"type":"any","format":"%s"}`, "does not support format"},
		{`{"type":"number","format":"no verb"}`, "needs exactly one verb"},
		{`{"type":"number","format":"%d %d"}`, "needs exactly one verb"},
		{`{"type":"number","format":"%%"}`, "needs exactly one verb"}, // 只有转义百分号，没有动词
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

// time 类型：日志转换里时间戳换格式是刚需，其它类型的 format 做不到。
func TestCodecTime(t *testing.T) {
	for _, tc := range []struct {
		name, def string
		in        any
		want      string
	}{
		{"strftime 输出", `{"type":"time","format":"%Y-%m-%d %H:%M:%S"}`,
			"2026-08-12T01:02:03Z", "2026-08-12 01:02:03"},
		{"只要日期", `{"type":"time","format":"%Y/%m/%d"}`,
			"2026-08-12T01:02:03Z", "2026/08/12"},
		{"命名别名 date", `{"type":"time","format":"date"}`,
			"2026-08-12T01:02:03Z", "2026-08-12"},
		{"命名别名 datetime", `{"type":"time","format":"datetime"}`,
			"2026-08-12T01:02:03Z", "2026-08-12 01:02:03"},
		{"转 unix 秒", `{"type":"time","format":"unix"}`,
			"2026-08-12T00:00:00Z", "1786492800"},
		{"从 unix 秒读入", `{"type":"time","input":"unix","format":"date"}`,
			"1786492800", "2026-08-12"},
		{"从 unix 毫秒读入", `{"type":"time","input":"unix_ms","format":"%H:%M:%S"}`,
			"1786492800000", "00:00:00"},
		{"自定义输入格式", `{"type":"time","input":"%d/%b/%Y:%H:%M:%S","format":"iso8601"}`,
			"12/Aug/2026:01:02:03", "2026-08-12T01:02:03Z"},
		{"月份英文名", `{"type":"time","format":"%d %B %Y"}`,
			"2026-08-12T00:00:00Z", "12 August 2026"},
		{"Go layout 逃生舱", `{"type":"time","format":"2006年01月02日"}`,
			"2026-08-12T00:00:00Z", "2026年08月12日"},
		{"缺省输入输出都是 RFC3339", `{"type":"time"}`,
			"2026-08-12T01:02:03Z", "2026-08-12T01:02:03Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := enc(t, codec(t, tc.def), tc.in); got != tc.want {
				t.Errorf("Encode(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// 定律 B 在 time 上同样要成立：Decode 用**输出**格式解析，是 Encode 的逆。
func TestCodecTimeEncodeDecodeInverse(t *testing.T) {
	for _, tc := range []struct{ def, in string }{
		{`{"type":"time","format":"%Y-%m-%d %H:%M:%S"}`, "2026-08-12T01:02:03Z"},
		{`{"type":"time","format":"iso8601"}`, "2026-08-12T01:02:03Z"},
		{`{"type":"time","format":"unix"}`, "2026-08-12T01:02:03Z"},
		// input 与 format 不同时，Decode 用的是 **format**（它是 Encode 的逆），
		// 所以往返闭合的是输出侧的格式。
		{`{"type":"time","input":"unix","format":"iso8601"}`, "1786492800"},
	} {
		def := tc.def
		c := codec(t, def)
		encoded, err := c.Encode(nil, tc.in)
		if err != nil {
			t.Fatalf("%s Encode: %v", def, err)
		}
		back, err := c.Decode(encoded)
		if err != nil {
			t.Fatalf("%s Decode(%q): %v", def, encoded, err)
		}
		again, err := c.Encode(nil, back)
		if err != nil {
			t.Fatalf("%s re-Encode: %v", def, err)
		}
		if string(again) != string(encoded) {
			t.Errorf("定律 B 违反 %s: %q -> %v -> %q", def, encoded, back, again)
		}
	}
}

func TestCodecTimeErrors(t *testing.T) {
	for _, tc := range []struct{ def, want string }{
		{`{"type":"time","format":"%Q"}`, "unsupported directive %Q"},
		{`{"type":"time","format":"%Y-%m-%"}`, "dangling %"},
		{`{"type":"time","input":"%Q"}`, "unsupported directive"},
	} {
		if _, err := shape.LoadCodec([]byte(tc.def)); err == nil {
			t.Errorf("%s: 应当在编译期报错", tc.def)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want %q", tc.def, err, tc.want)
		}
	}
	c := codec(t, `{"type":"time","format":"date"}`)
	if _, err := c.Encode(nil, "not a timestamp"); err == nil {
		t.Error("无法解析的时间应当报错")
	}
}
