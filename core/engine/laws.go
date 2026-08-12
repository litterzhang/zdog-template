package engine

import (
	"bytes"
	"fmt"

	"github.com/huge-zhang/zdog-template/core/binding"
)

// LawViolation 描述一次 round-trip 定律的违反。
type LawViolation struct {
	Law      string // "A" 或 "B"
	Field    string // 定律 B 中出问题的绑定名；定律 A 为空
	Expected []byte
	Actual   []byte
	Detail   string
}

func (v *LawViolation) Error() string {
	if v.Field != "" {
		return fmt.Sprintf("定律 %s 违反 (字段 %q): %s\n  期望: %q\n  实际: %q",
			v.Law, v.Field, v.Detail, v.Expected, v.Actual)
	}
	return fmt.Sprintf("定律 %s 违反: %s\n  期望: %q\n  实际: %q",
		v.Law, v.Detail, v.Expected, v.Actual)
}

// VerifyLawA 校验源侧往返定律：format(parse(t)) == t。
//
// 它证明模板**完整覆盖**了源文，没有静默丢字符 —— 这是文本抽取里最容易
// 出错、又最难察觉的问题，这里把它变成一个自动断言（见 DESIGN.md §2）。
//
// 注意：这里刻意绕过 replay 快路径。走 replay 的话 format 直接返回源文，
// 定律就成了恒真的空断言，什么也证明不了。
func (e *Engine) VerifyLawA(src []byte) error {
	ctx, err := e.Parse(src)
	if err != nil {
		return err
	}
	out, err := e.formatFull(nil, ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(out, src) {
		return &LawViolation{
			Law:      "A",
			Expected: src,
			Actual:   out,
			Detail:   "模板未能从绑定完整还原源文" + firstDiff(src, out),
		}
	}
	return nil
}

// VerifyLawB 校验目标侧往返定律：parse(format(c)) == c。
//
// 它证明目标模板**能被读回来** —— 转换不是单程票。mapping 之后的字段没有
// 源文出处，只能靠 shape 序列化，这条定律就是那层序列化正确性的保证。
func (e *Engine) VerifyLawB(ctx *binding.Context) error {
	out, err := e.formatFull(nil, ctx)
	if err != nil {
		return err
	}
	back, err := e.Parse(out)
	if err != nil {
		return &LawViolation{
			Law:      "B",
			Expected: out,
			Detail:   "渲染结果无法被同一模板解析回来: " + err.Error(),
		}
	}
	for _, name := range e.Names() {
		want, okWant := ctx.Raw(name)
		got, okGot := back.Raw(name)
		if !okWant {
			continue // 未填充的槽位不参与比较
		}
		if !okGot {
			return &LawViolation{Law: "B", Field: name, Expected: want, Detail: "回读后该字段缺失"}
		}
		if !bytes.Equal(want, got) {
			return &LawViolation{
				Law: "B", Field: name, Expected: want, Actual: got,
				Detail: "回读后字段值发生变化（多半是缺少定界符导致边界漂移）",
			}
		}
	}
	return nil
}

// firstDiff 定位首个差异位置，便于排错。
func firstDiff(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("（首个差异在偏移 %d）", i)
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("（长度不同: %d vs %d，前 %d 字节相同）", len(a), len(b), n)
	}
	return ""
}
