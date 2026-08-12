package mapping

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// function 是一个内置函数。
type function struct {
	minArgs int
	maxArgs int // -1 表示不限
	call    func(args []any) (any, error)
}

func (f *function) checkArity(name string, n int) error {
	if n < f.minArgs || (f.maxArgs >= 0 && n > f.maxArgs) {
		want := strconv.Itoa(f.minArgs)
		if f.maxArgs < 0 {
			want += "+"
		} else if f.maxArgs != f.minArgs {
			want += "-" + strconv.Itoa(f.maxArgs)
		}
		return fmt.Errorf("function %s takes %s argument(s), got %d", name, want, n)
	}
	return nil
}

// funcs 是内置函数表。
//
// 标注 [JMESPath] 的是规范内置函数；标注 [扩展] 的是本项目按 JMESPath 的
// 自定义函数机制加的 —— 规范本身没有大小写/去空白函数，但文本转换里太常用。
var funcs = map[string]*function{
	// —— JMESPath 内置 ——
	"length":      {1, 1, fnLength},
	"to_string":   {1, 1, fnToString},
	"to_number":   {1, 1, fnToNumber},
	"type":        {1, 1, fnType},
	"not_null":    {1, -1, fnNotNull},
	"join":        {2, 2, fnJoin},
	"keys":        {1, 1, fnKeys},
	"values":      {1, 1, fnValues},
	"starts_with": {2, 2, fnStartsWith},
	"ends_with":   {2, 2, fnEndsWith},
	"contains":    {2, 2, fnContains},
	"reverse":     {1, 1, fnReverse},
	"sort":        {1, 1, fnSort},
	// —— 扩展 ——
	"upper":   {1, 1, strFn(strings.ToUpper)},
	"lower":   {1, 1, strFn(strings.ToLower)},
	"trim":    {1, 1, strFn(strings.TrimSpace)},
	"replace": {3, 3, fnReplace},
	"split":   {2, 2, fnSplit},
	"mask":    {1, 2, fnMask},
}

func lookupFunc(name string) *function { return funcs[name] }

// FunctionNames 返回全部内置函数名（已排序），供错误提示使用。
func FunctionNames() []string {
	out := make([]string, 0, len(funcs))
	for k := range funcs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func strFn(f func(string) string) func([]any) (any, error) {
	return func(args []any) (any, error) {
		s, err := asString(args[0])
		if err != nil {
			return nil, err
		}
		return f(s), nil
	}
}

// asString 把值转成字符串。null 转成空串，便于 upper(missing) 这类写法不报错。
func asString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
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
	return "", fmt.Errorf("expected a string, got %T", v)
}

func asNumber(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", x)
		}
		return f, nil
	}
	return 0, fmt.Errorf("expected a number, got %T", v)
}

func fnLength(args []any) (any, error) {
	switch x := args[0].(type) {
	case nil:
		return float64(0), nil
	case string:
		return float64(len([]rune(x))), nil // 按字符而非字节
	case []any:
		return float64(len(x)), nil
	case map[string]any:
		return float64(len(x)), nil
	}
	return nil, fmt.Errorf("length() does not support %T", args[0])
}

func fnToString(args []any) (any, error) { return asString(args[0]) }

func fnToNumber(args []any) (any, error) {
	if args[0] == nil {
		return nil, nil
	}
	return asNumber(args[0])
}

func fnType(args []any) (any, error) {
	switch args[0].(type) {
	case nil:
		return "null", nil
	case bool:
		return "boolean", nil
	case string:
		return "string", nil
	case []any:
		return "array", nil
	case map[string]any:
		return "object", nil
	}
	return "number", nil
}

func fnNotNull(args []any) (any, error) {
	for _, a := range args {
		if a != nil {
			return a, nil
		}
	}
	return nil, nil
}

func fnJoin(args []any) (any, error) {
	sep, err := asString(args[0])
	if err != nil {
		return nil, err
	}
	arr, ok := args[1].([]any)
	if !ok {
		if args[1] == nil {
			return "", nil
		}
		return nil, fmt.Errorf("join() expects an array as its second argument, got %T", args[1])
	}
	parts := make([]string, len(arr))
	for i, v := range arr {
		if parts[i], err = asString(v); err != nil {
			return nil, err
		}
	}
	return strings.Join(parts, sep), nil
}

func fnKeys(args []any) (any, error) {
	m, ok := args[0].(map[string]any)
	if !ok {
		return []any{}, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 保证确定性输出，定律 B 需要
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out, nil
}

func fnValues(args []any) (any, error) {
	m, ok := args[0].(map[string]any)
	if !ok {
		return []any{}, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out, nil
}

func fnStartsWith(args []any) (any, error) { return strPair(args, strings.HasPrefix) }
func fnEndsWith(args []any) (any, error)   { return strPair(args, strings.HasSuffix) }

func strPair(args []any, f func(string, string) bool) (any, error) {
	a, err := asString(args[0])
	if err != nil {
		return nil, err
	}
	b, err := asString(args[1])
	if err != nil {
		return nil, err
	}
	return f(a, b), nil
}

func fnContains(args []any) (any, error) {
	switch hay := args[0].(type) {
	case string:
		needle, err := asString(args[1])
		if err != nil {
			return nil, err
		}
		return strings.Contains(hay, needle), nil
	case []any:
		for _, v := range hay {
			if v == args[1] {
				return true, nil
			}
		}
		return false, nil
	case nil:
		return false, nil
	}
	return nil, fmt.Errorf("contains() does not support %T", args[0])
}

func fnReverse(args []any) (any, error) {
	switch x := args[0].(type) {
	case string:
		r := []rune(x)
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return string(r), nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[len(x)-1-i] = v
		}
		return out, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("reverse() does not support %T", args[0])
}

func fnSort(args []any) (any, error) {
	arr, ok := args[0].([]any)
	if !ok {
		return args[0], nil
	}
	out := make([]any, len(arr))
	copy(out, arr)
	sort.SliceStable(out, func(i, j int) bool {
		si, ei := asString(out[i])
		sj, ej := asString(out[j])
		if ei != nil || ej != nil {
			return false
		}
		return si < sj
	})
	return out, nil
}

func fnReplace(args []any) (any, error) {
	s, err := asString(args[0])
	if err != nil {
		return nil, err
	}
	old, err := asString(args[1])
	if err != nil {
		return nil, err
	}
	nw, err := asString(args[2])
	if err != nil {
		return nil, err
	}
	return strings.ReplaceAll(s, old, nw), nil
}

func fnSplit(args []any) (any, error) {
	s, err := asString(args[0])
	if err != nil {
		return nil, err
	}
	sep, err := asString(args[1])
	if err != nil {
		return nil, err
	}
	if sep == "" {
		return nil, fmt.Errorf("split() separator must not be empty")
	}
	parts := strings.Split(s, sep)
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out, nil
}

// fnMask 把值打码：末 keepTail 个字符保留，其余替换成 '*'。
//
//	mask(phone)      13812345678 -> ***********
//	mask(phone, 4)   13812345678 -> *******5678
//
// 三个刻意的决定：
//
//  1. **按 rune 而非字节**，跟 length()/reverse() 一致。否则中文姓名会被切成
//     半个字符，产出非法 UTF-8。
//
//  2. **保长**。脱敏的产物通常要写回原格式（这正是双向模板的用武之处），
//     长度一变，定宽或对齐敏感的格式就废了。代价是泄漏长度——对手机号、
//     身份证这类定长字段无所谓，但别拿它打码密码。
//
//  3. **keepTail >= 长度时全部打码，而不是原样返回**。这是唯一一处安全相关
//     的选择：masking 函数在输入比预期短时把原值透出去，是典型的数据泄漏
//     bug，而且只在少数异常数据上触发，测不出来。这里 fail closed —— 宁可
//     多打码，不可漏。
func fnMask(args []any) (any, error) {
	s, err := asString(args[0])
	if err != nil {
		return nil, err
	}
	keep := 0
	if len(args) == 2 {
		n, err := asNumber(args[1])
		if err != nil {
			return nil, err
		}
		if n < 0 || n != float64(int64(n)) {
			return nil, fmt.Errorf("mask() keep-tail must be a non-negative whole number, got %v", args[1])
		}
		keep = int(n)
	}
	r := []rune(s)
	if keep >= len(r) {
		keep = 0 // fail closed，见上面第 3 点
	}
	cut := len(r) - keep
	for i := 0; i < cut; i++ {
		r[i] = '*'
	}
	return string(r), nil
}
