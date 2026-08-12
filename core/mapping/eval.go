package mapping

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Env 提供表达式求值所需的字段访问。
//
// 它是惰性的：Value 只在表达式真正引用到某个字段时才被调用，
// 因此没被引用的结构化岛永远不会被解码。
type Env interface {
	// Value 返回字段的解码值。字段不存在时返回 ok=false。
	Value(name string) (v any, ok bool, err error)
}

// Eval 求值一条表达式。
func Eval(e Expr, env Env) (any, error) { return e.eval(env) }

func (f *fieldExpr) eval(env Env) (any, error) {
	v, ok, err := env.Value(f.name)
	if err != nil {
		return nil, err
	}
	if !ok {
		// JMESPath 语义：找不到的字段求值为 null，而非报错。
		// 这让 `a.b || 'default'` 能正常工作。
		return nil, nil
	}
	return v, nil
}

func (p *propExpr) eval(env Env) (any, error) {
	base, err := p.base.eval(env)
	if err != nil {
		return nil, err
	}
	switch m := base.(type) {
	case map[string]any:
		return m[p.name], nil // 缺失即 null
	case nil:
		return nil, nil
	default:
		return nil, nil // 对非对象取属性 -> null（JMESPath 语义）
	}
}

func (x *indexExpr) eval(env Env) (any, error) {
	base, err := x.base.eval(env)
	if err != nil {
		return nil, err
	}
	arr, ok := base.([]any)
	if !ok {
		return nil, nil
	}
	i := x.i
	if i < 0 {
		i += len(arr) // JMESPath 支持负下标
	}
	if i < 0 || i >= len(arr) {
		return nil, nil
	}
	return arr[i], nil
}

func (l *literalExpr) eval(Env) (any, error) { return l.v, nil }

func (o *orExpr) eval(env Env) (any, error) {
	lhs, err := o.lhs.eval(env)
	if err != nil {
		return nil, err
	}
	if !isFalsy(lhs) {
		return lhs, nil
	}
	return o.rhs.eval(env)
}

func (c *callExpr) eval(env Env) (any, error) {
	args := make([]any, len(c.args))
	for i, a := range c.args {
		v, err := a.eval(env)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	v, err := c.fn.call(args)
	if err != nil {
		return nil, fmt.Errorf("mapping: %s: %w", c.name, err)
	}
	return v, nil
}

// isFalsy 实现 JMESPath 的假值定义：null、空字符串、空数组、空对象、false。
// 注意数字 0 **不是**假值。
func isFalsy(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// Serialize 把求值结果写成字节，追加到 dst。
//
// 这是「无 Raw 出处的字段」的默认序列化规则。P3 的 Shape codec 会在此之上
// 提供按类型定制的格式（见 DESIGN.md §3）。
func Serialize(dst []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return dst, nil // null 渲染成空
	case string:
		return append(dst, x...), nil
	case []byte:
		return append(dst, x...), nil
	case bool:
		return strconv.AppendBool(dst, x), nil
	case float64:
		// 整数值不要写成 1e+06
		if x == float64(int64(x)) && x < 1e15 && x > -1e15 {
			return strconv.AppendInt(dst, int64(x), 10), nil
		}
		return strconv.AppendFloat(dst, x, 'g', -1, 64), nil
	case int:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case int64:
		return strconv.AppendInt(dst, x, 10), nil
	case json.Number:
		return append(dst, x.String()...), nil
	default:
		// 复合值退回 JSON
		b, err := json.Marshal(v)
		if err != nil {
			return dst, fmt.Errorf("mapping: 无法序列化 %T: %w", v, err)
		}
		return append(dst, b...), nil
	}
}
