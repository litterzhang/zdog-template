package element

import (
	"bytes"
	"encoding/json"
	"testing"
)

const islandSrc = `{"host":"web-1","pct":95,"tags":["a","b"],"meta":{"x":1,"s":"}]"}}`

// A：当前实现 —— Decode 物化整棵值树。
func decodeExtent(src []byte, pos int) (int, bool) {
	dec := json.NewDecoder(bytes.NewReader(src[pos:]))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, false
	}
	return pos + int(dec.InputOffset()), true
}

// B：手写边界扫描，不物化值。
// 在 Python 里这么做是负收益（解释级字符循环，实测 3.73 µs vs C 的 1.29 µs），
// 在 Go 里是编译代码 —— 与 bytes.Index 打败 regexp 是同一个道理的反转。
func BenchmarkIslandDecodeMaterialize(b *testing.B) {
	src := []byte(islandSrc)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := decodeExtent(src, 0); !ok {
			b.Fatal("failed")
		}
	}
}

func BenchmarkIslandScanExtentOnly(b *testing.B) {
	src := []byte(islandSrc)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := scanValue(src, 0); !ok {
			b.Fatal("failed")
		}
	}
}

func BenchmarkIslandScanExtentPlusValidate(b *testing.B) {
	src := []byte(islandSrc)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		end, ok := scanValue(src, 0)
		if !ok || !json.Valid(src[0:end]) {
			b.Fatal("failed")
		}
	}
}
