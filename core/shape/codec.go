package shape

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/litterzhang/zdog-template/core/shape/model"
)

// Codec 按 shape 在「值」与「字节」之间双向转换。
//
// 这是 shape 的真正职责（见 DESIGN.md §3）：经过 mapping 之后的字段没有 Raw
// 出处，format 不知道该怎么把值写成文本。有 Raw 的字段走 replay（定律 A），
// 无 Raw 的字段走 Codec（定律 B）。
//
// Encode 与 Decode 必须互逆 —— 这正是定律 B 在 codec 层的表述。
type Codec struct {
	node model.Node
	meta model.Meta
	// time 类型的两个已编译布局：怎么读进来、怎么写出去。
	inLayout  string
	outLayout string
}

// NewCodec 由 shape 节点构造 codec。
func NewCodec(n model.Node) (*Codec, error) {
	if n == nil {
		return nil, fmt.Errorf("shape: codec requires a non-nil node")
	}
	c := &Codec{node: n, meta: n.Meta()}
	if n.Type() == model.TypeTime {
		var err error
		if c.inLayout, err = compileTimeLayout(c.meta.Input); err != nil {
			return nil, err
		}
		if c.outLayout, err = compileTimeLayout(c.meta.Format); err != nil {
			return nil, err
		}
		return c, nil
	}
	if err := c.validateFormat(); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadCodec 从 JSON shape 定义构造 codec。
func LoadCodec(data []byte) (*Codec, error) {
	s, err := Load(data)
	if err != nil {
		return nil, err
	}
	return NewCodec(s.Node())
}

// Node 返回底层 shape 节点。
func (c *Codec) Node() model.Node { return c.node }

// validateFormat 在编译期校验 format 与类型匹配，避免每行才发现写错。
func (c *Codec) validateFormat() error {
	f := c.meta.Format
	if f == "" {
		return nil
	}
	switch c.node.Type() {
	case model.TypeNumber, model.TypeString:
		// %% 是转义的字面百分号，不是动词 —— "%.1f%%" 这种写法完全合法。
		verbs := strings.Count(strings.ReplaceAll(f, "%%", ""), "%")
		if verbs != 1 {
			return fmt.Errorf(
				"shape: format %q needs exactly one verb (e.g. %%.2f, %%-10s); "+
					"write %%%% for a literal percent, e.g. %%.1f%%%%", f)
		}
		return nil
	default:
		return fmt.Errorf("shape: type %s does not support format", c.node.Type())
	}
}

// Encode 把值按 shape 写成字节，追加到 dst。
func (c *Codec) Encode(dst []byte, v any) ([]byte, error) {
	if v == nil {
		if c.meta.Default != nil {
			v = c.meta.Default
		} else if c.meta.Required {
			return dst, fmt.Errorf("shape: required field has no value")
		} else {
			return dst, nil
		}
	}

	switch c.node.Type() {
	case model.TypeTime:
		// 值已经是时间就直接写出去 —— 再"转成字符串又按 input 解析一遍"
		// 不仅多余，而且在 input != format 时必然失败（Decode 的产物用的是
		// 输出格式）。这一条是 Encode(Decode(x)) == x 成立的前提。
		if t, ok := v.(time.Time); ok {
			return formatTime(dst, c.outLayout, t), nil
		}
		s, err := coerceString(v)
		if err != nil {
			return dst, err
		}
		t, err := parseTime(c.inLayout, s)
		if err != nil {
			return dst, err
		}
		return formatTime(dst, c.outLayout, t), nil

	case model.TypeString:
		s, err := coerceString(v)
		if err != nil {
			return dst, err
		}
		if c.meta.Format != "" {
			return fmt.Appendf(dst, c.meta.Format, s), nil
		}
		return append(dst, s...), nil

	case model.TypeNumber:
		f, err := coerceNumber(v)
		if err != nil {
			return dst, err
		}
		if c.meta.Format != "" {
			return fmt.Appendf(dst, c.meta.Format, f), nil
		}
		// 整数值不要写成 1e+06
		if f == float64(int64(f)) && f < 1e15 && f > -1e15 {
			return strconv.AppendInt(dst, int64(f), 10), nil
		}
		return strconv.AppendFloat(dst, f, 'g', -1, 64), nil

	case model.TypeBool:
		b, err := coerceBool(v)
		if err != nil {
			return dst, err
		}
		return strconv.AppendBool(dst, b), nil

	default: // object / array / dict / any -> JSON
		b, err := json.Marshal(v)
		if err != nil {
			return dst, fmt.Errorf("shape: cannot serialize %T: %w", v, err)
		}
		return append(dst, b...), nil
	}
}

// Decode 把字节按 shape 读回值。与 Encode 互逆（定律 B）。
func (c *Codec) Decode(raw []byte) (any, error) {
	s := string(raw)
	if s == "" {
		if c.meta.Required {
			return nil, fmt.Errorf("shape: required field has no value")
		}
		return nil, nil
	}
	switch c.node.Type() {
	case model.TypeTime:
		// 回读用**输出**格式解析 —— Decode 是 Encode 的逆运算（定律 B）。
		t, err := parseTime(c.outLayout, s)
		if err != nil {
			return nil, err
		}
		return t, nil
	case model.TypeString:
		return s, nil
	case model.TypeNumber:
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("shape: %q is not a number", s)
		}
		return f, nil
	case model.TypeBool:
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("shape: %q is not a boolean", s)
		}
		return b, nil
	default:
		var v any
		dec := json.NewDecoder(strings.NewReader(s))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("shape: %q is not valid JSON: %w", s, err)
		}
		return v, nil
	}
}

func coerceString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case time.Time:
		return x.Format(time.RFC3339Nano), nil
	case []byte:
		return string(x), nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	}
	return "", fmt.Errorf("shape: expected string, got %T", v)
}

func coerceNumber(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, fmt.Errorf("shape: %q is not a number", x)
		}
		return f, nil
	}
	return 0, fmt.Errorf("shape: expected number, got %T", v)
}

func coerceBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		if err != nil {
			return false, fmt.Errorf("shape: %q is not a boolean", x)
		}
		return b, nil
	}
	return false, fmt.Errorf("shape: expected bool, got %T", v)
}
