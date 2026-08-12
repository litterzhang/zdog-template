package element

// scanValue 返回 src[pos:] 中第一个完整 JSON 值的结束偏移。
//
// 它只求**边界**，不物化值 —— parse 阶段唯一需要的就是边界。
// 值的解码推迟到调用方真正读取该绑定时（见 Decode）。
//
// 为什么这里可以手写扫描，而 DESIGN.md §7 否决记录 3 又说"别手写"？
// 因为那条结论是针对 **Python** 的：解释级字符循环 3.73 µs，输给 C 实现的
// raw_decode 1.29 µs。Go 里这是编译代码，情况正好反过来 ——
// 与 bytes.Index 打败 regexp 是同一个道理。见 island_bench_test.go 的实测。
func scanValue(src []byte, pos int) (end int, ok bool) {
	i := skipWS(src, pos)
	if i >= len(src) {
		return 0, false
	}
	switch c := src[i]; c {
	case '{':
		return scanContainer(src, i, '{', '}')
	case '[':
		return scanContainer(src, i, '[', ']')
	case '"':
		return scanString(src, i)
	case 't':
		return scanLit(src, i, "true")
	case 'f':
		return scanLit(src, i, "false")
	case 'n':
		return scanLit(src, i, "null")
	default:
		if c == '-' || (c >= '0' && c <= '9') {
			return scanNumber(src, i)
		}
		return 0, false
	}
}

func skipWS(src []byte, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// scanContainer 按深度配对扫描 {} 或 []，正确跳过字符串内的括号。
func scanContainer(src []byte, i int, open, close byte) (int, bool) {
	depth := 0
	for i < len(src) {
		switch src[i] {
		case '"':
			e, ok := scanString(src, i)
			if !ok {
				return 0, false
			}
			i = e
			continue
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1, true
			}
			if depth < 0 {
				return 0, false
			}
		}
		i++
	}
	return 0, false
}

// scanString 扫描一个 JSON 字符串字面量，处理反斜杠转义。
func scanString(src []byte, i int) (int, bool) {
	if i >= len(src) || src[i] != '"' {
		return 0, false
	}
	i++
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2 // 跳过被转义的字符；\uXXXX 的后续字符不含 " 与 \，无需特判
			continue
		case '"':
			return i + 1, true
		}
		i++
	}
	return 0, false
}

func scanLit(src []byte, i int, lit string) (int, bool) {
	if i+len(lit) > len(src) || string(src[i:i+len(lit)]) != lit {
		return 0, false
	}
	return i + len(lit), true
}

func scanNumber(src []byte, i int) (int, bool) {
	start := i
	if i < len(src) && (src[i] == '-' || src[i] == '+') {
		i++
	}
	for i < len(src) {
		c := src[i]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			i++
			continue
		}
		break
	}
	if i == start {
		return 0, false
	}
	return i, true
}
