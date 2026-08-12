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
		return fmt.Sprintf("law %s violated (field %q): %s\n  want: %q\n  got:  %q",
			v.Law, v.Field, v.Detail, v.Expected, v.Actual)
	}
	return fmt.Sprintf("law %s violated: %s\n  want: %q\n  got:  %q",
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
			Detail:   "template could not reconstruct the source from its bindings" + firstDiff(src, out),
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
			Detail:   "rendered output cannot be parsed back by the same template: " + err.Error(),
		}
	}
	for _, name := range e.Names() {
		want, okWant := ctx.Raw(name)
		got, okGot := back.Raw(name)
		if !okWant {
			continue // 未填充的槽位不参与比较
		}
		if !okGot {
			return &LawViolation{Law: "B", Field: name, Expected: want, Detail: "field is missing after re-parsing"}
		}
		if !bytes.Equal(want, got) {
			return &LawViolation{
				Law: "B", Field: name, Expected: want, Actual: got,
				Detail: "field value changed after re-parsing (usually a missing delimiter causing boundary drift)",
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
			return fmt.Sprintf(" (first difference at offset %d)", i)
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf(" (lengths differ: %d vs %d; first %d bytes match)", len(a), len(b), n)
	}
	return ""
}

// ErrAmbiguous 表示模板对该输入有多于一个解。
type ErrAmbiguous struct {
	Input []byte
	Count int
}

func (e *ErrAmbiguous) Error() string {
	s := e.Input
	if len(s) > 120 {
		s = s[:120]
	}
	return fmt.Sprintf(
		"engine: template has at least %d parses for input %q — the chosen one depends on "+
			"operator order, so a different input may yield a different answer. "+
			"Add a literal delimiter, or constrain a hole with expr.",
		s, e.Count)
}

// VerifyUnambiguous 校验模板对该输入只有唯一解。
//
// 歧义是模板设计的 bug，而不是运行期的偶然：`${a}.${b}!` 对 "x.y.z!" 既可以
// 解成 a="x" 也可以解成 a="x.y"，引擎只能挑一个。这类问题在开发期用本方法
// 暴露，好过上线后靠数据暴露。
func (e *Engine) VerifyUnambiguous(src []byte) error {
	n := e.pl.CountParses(src, 2)
	switch n {
	case 0:
		return &ErrNoMatch{Input: src}
	case 1:
		return nil
	default:
		return &ErrAmbiguous{Input: src, Count: n}
	}
}

// CountParses 统计最多 limit 个解，供调试与歧义分析使用。
func (e *Engine) CountParses(src []byte, limit int) int {
	return e.pl.CountParses(src, limit)
}
