package shape

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 时间的解析与渲染。
//
// 用 **strftime 风格**（`%Y-%m-%d %H:%M:%S`）而不是 Go 的参考时间布局
// （`2006-01-02 15:04:05`）：core 是 Go 写的，但 SDK 面向 Python / shell /
// 其他语言，strftime 是这些人共同的母语。Go layout 作为逃生舱仍然可用 ——
// 模式里不含 '%' 时按 Go layout 处理。
//
// 命名别名覆盖最常见的几种，免得每次都拼 strftime。

// timeAliases 是命名格式。
var timeAliases = map[string]string{
	"iso8601":  time.RFC3339,
	"rfc3339":  time.RFC3339,
	"rfc1123":  time.RFC1123,
	"date":     "2006-01-02",
	"time":     "15:04:05",
	"datetime": "2006-01-02 15:04:05",
	"unix":     layoutUnix,
	"unix_ms":  layoutUnixMilli,
}

// 这两个不是布局串，是特殊标记：秒/毫秒时间戳。
const (
	layoutUnix      = "\x00unix"
	layoutUnixMilli = "\x00unix_ms"
)

// strftimeToGo 把 strftime 指示符翻译成 Go 的参考时间布局。
var strftimeToGo = map[byte]string{
	'Y': "2006", 'y': "06",
	'm': "01", 'd': "02", 'e': "_2",
	'H': "15", 'I': "03", 'M': "04", 'S': "05",
	'p': "PM", 'b': "Jan", 'B': "January", 'a': "Mon", 'A': "Monday",
	'j': "002", 'Z': "MST", 'z': "-0700",
	'f': "000000", 'L': "000", // 微秒 / 毫秒
	'%': "%",
}

// compileTimeLayout 把用户写的模式转成 Go 布局（或特殊标记）。
func compileTimeLayout(pattern string) (string, error) {
	if pattern == "" {
		return time.RFC3339, nil
	}
	if alias, ok := timeAliases[strings.ToLower(pattern)]; ok {
		return alias, nil
	}
	if !strings.ContainsRune(pattern, '%') {
		return pattern, nil // 逃生舱：直接当 Go layout 用
	}

	var b strings.Builder
	b.Grow(len(pattern) + 8)
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '%' {
			b.WriteByte(pattern[i])
			continue
		}
		if i+1 >= len(pattern) {
			return "", fmt.Errorf("shape: time pattern %q ends with a dangling %%", pattern)
		}
		i++
		got, ok := strftimeToGo[pattern[i]]
		if !ok {
			return "", fmt.Errorf(
				"shape: time pattern %q: unsupported directive %%%c (supported: %s)",
				pattern, pattern[i], supportedDirectives())
		}
		b.WriteString(got)
	}
	return b.String(), nil
}

func supportedDirectives() string {
	out := make([]string, 0, len(strftimeToGo))
	for _, c := range "YymdeHIMSpbBaAjZzfL" {
		out = append(out, "%"+string(c))
	}
	return strings.Join(out, " ")
}

// parseTime 按布局解析时间。
func parseTime(layout, s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch layout {
	case layoutUnix, layoutUnixMilli:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(s, 64)
			if ferr != nil {
				return time.Time{}, fmt.Errorf("shape: %q is not a unix timestamp", s)
			}
			n = int64(f)
		}
		if layout == layoutUnixMilli {
			return time.UnixMilli(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("shape: %q does not match the input time format: %w", s, err)
	}
	return t, nil
}

// formatTime 按布局渲染时间，追加到 dst。
func formatTime(dst []byte, layout string, t time.Time) []byte {
	switch layout {
	case layoutUnix:
		return strconv.AppendInt(dst, t.Unix(), 10)
	case layoutUnixMilli:
		return strconv.AppendInt(dst, t.UnixMilli(), 10)
	}
	return t.AppendFormat(dst, layout)
}
