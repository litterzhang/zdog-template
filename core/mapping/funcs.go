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
		return fmt.Errorf("函数 %s 需要 %s 个参数，实际 %d 个", name, want, n)
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
	return "", fmt.Errorf("需要字符串，得到 %T", v)
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
			return 0, fmt.Errorf("%q 不是数字", x)
		}
		return f, nil
	}
	return 0, fmt.Errorf("需要数字，得到 %T", v)
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
	return nil, fmt.Errorf("length 不支持 %T", args[0])
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
		return nil, fmt.Errorf("join 的第二个参数需要数组，得到 %T", args[1])
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
	return nil, fmt.Errorf("contains 不支持 %T", args[0])
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
	return nil, fmt.Errorf("reverse 不支持 %T", args[0])
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
		return nil, fmt.Errorf("split 的分隔符不能为空")
	}
	parts := strings.Split(s, sep)
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out, nil
}
