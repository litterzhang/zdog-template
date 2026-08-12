package element

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/litterzhang/zdog-template/core/template/model"
	"github.com/litterzhang/zdog-template/core/template/syntax"
)

// JSONIsland 是嵌在非结构化文本中的一段 JSON 值。
//
// 这是 Z-Template 相对 Grok / python-parse 的真正差异点：正则无法匹配
// 嵌套/配对结构（不是正则语言），而 JSON 值是**自定界**的 —— 从 pos 开始
// 要么扫出唯一一个完整值、结束位置确定，要么失败。**只产生 0 或 1 个候选**，
// 因此岛是回溯的天然屏障（见 DESIGN.md §5）。
//
// Scan/Decode 分离见 model.Island 的说明与 island_bench_test.go 的实测。
type JSONIsland struct {
	name string
	// strict 为 true 时，Scan 额外做一次完整合法性校验（约 5 倍开销）。
	// 默认关闭：纯直通字段不需要合法性，读取值时 Decode 自然会报错。
	strict bool
}

// NewJSONIsland 构造一个 JSON 岛。
func NewJSONIsland(name string) *JSONIsland { return &JSONIsland{name: name} }

// NewStrictJSONIsland 构造一个在 Scan 阶段就校验合法性的 JSON 岛。
func NewStrictJSONIsland(name string) *JSONIsland {
	return &JSONIsland{name: name, strict: true}
}

// Tag 返回 TagJSON。
func (JSONIsland) Tag() model.Tag { return model.TagJSON }

// Name 返回绑定名。
func (j JSONIsland) Name() string { return j.name }

// Strict 报告是否开启扫描期校验。
func (j JSONIsland) Strict() bool { return j.strict }

// Dump 输出模板文本。
func (j JSONIsland) Dump() string {
	s := "${json|name=" + syntax.EscapeAttrValue(j.name)
	if j.strict {
		s += ",strict=true"
	}
	return s + "}"
}

func (j JSONIsland) String() string { return fmt.Sprintf("{json|name=%s}", j.name) }

// Scan 从 src[pos:] 扫出一个完整 JSON 值的结束偏移。
//
// src[pos:end] 即该岛的 raw span —— 定律 A 要求保留它，因为 json.Marshal
// 无法还原原文的空白与键序。
func (j JSONIsland) Scan(src []byte, pos int) (end int, ok bool) {
	if pos < 0 || pos > len(src) {
		return 0, false
	}
	end, ok = scanValue(src, pos)
	if !ok {
		return 0, false
	}
	if j.strict && !json.Valid(src[pos:end]) {
		return 0, false
	}
	return end, true
}

// Decode 把 raw 解码成值。使用 json.Number 保号，避免整数经由 float64
// 变形（1000000 -> 1e+06）而破坏定律 B。
func (j JSONIsland) Decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("template: island %q: %w", j.name, err)
	}
	return v, nil
}
