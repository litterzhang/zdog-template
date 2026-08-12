// Package engine 把模板、执行计划与绑定串起来，提供 parse / format 与定律校验。
package engine

import (
	"fmt"

	"github.com/huge-zhang/zdog-template/core/binding"
	"github.com/huge-zhang/zdog-template/core/plan"
	"github.com/huge-zhang/zdog-template/core/template"
)

// Engine 是一份编译好的双向模板。不可变，可被多 goroutine 并发使用
// （每个 goroutine 各自持有 Context）。
type Engine struct {
	tpl *template.Template
	pl  *plan.Plan
}

// New 从模板文本编译出 Engine。
func New(src string) (*Engine, error) {
	tpl, err := template.Load(src)
	if err != nil {
		return nil, err
	}
	pl, err := plan.Compile(tpl.Elements())
	if err != nil {
		return nil, err
	}
	return &Engine{tpl: tpl, pl: pl}, nil
}

// Template 返回底层模板。
func (e *Engine) Template() *template.Template { return e.tpl }

// Plan 返回执行计划。
func (e *Engine) Plan() *plan.Plan { return e.pl }

// Tier 返回该模板达到的执行层级。
func (e *Engine) Tier() plan.Tier { return e.pl.Tier() }

// Names 返回全部绑定名。
func (e *Engine) Names() []string { return e.pl.Names() }

// NewResult 分配一个可复用的解析结果容器，供 ParseInto 使用。
func (e *Engine) NewResult() *plan.Result { return e.pl.NewResult() }

// ErrNoMatch 表示输入不匹配模板。
type ErrNoMatch struct{ Input []byte }

func (e *ErrNoMatch) Error() string {
	s := e.Input
	if len(s) > 120 {
		s = s[:120]
	}
	return fmt.Sprintf("engine: input does not match template: %q", s)
}

// Parse 解析输入并返回 context。src 的所有权移交给 context（不会被拷贝）。
func (e *Engine) Parse(src []byte) (*binding.Context, error) {
	res := e.NewResult()
	if !e.pl.Parse(src, res) {
		return nil, &ErrNoMatch{Input: src}
	}
	return binding.FromParse(e.pl, src, res), nil
}

// ParseInto 是 Parse 的零分配版本，结果容器由调用方复用。
func (e *Engine) ParseInto(src []byte, res *plan.Result) bool {
	return e.pl.Parse(src, res)
}

// NewContext 建一个空 context，用于 mapping 之后的目标侧渲染。
func (e *Engine) NewContext() *binding.Context { return binding.NewContext(e.pl) }

// Format 把 context 渲染成文本。
//
// 未修改的 context 走 replay 快路径：定律 A 保证此时正确答案就是源文本身，
// 一次 memcpy 即可，不必走一遍算子（见 DESIGN.md §7）。
func (e *Engine) Format(ctx *binding.Context) ([]byte, error) {
	return e.FormatTo(nil, ctx)
}

// FormatTo 把渲染结果追加到 dst。
func (e *Engine) FormatTo(dst []byte, ctx *binding.Context) ([]byte, error) {
	if !ctx.Dirty() {
		if src := ctx.Source(); src != nil {
			return append(dst, src...), nil // replay
		}
	}
	return e.formatFull(dst, ctx)
}

// formatFull 无条件走完整算子渲染，不使用 replay。
// 定律校验必须用它 —— 否则 replay 会让 format(parse(t)) == t 变成恒真的空断言。
func (e *Engine) formatFull(dst []byte, ctx *binding.Context) ([]byte, error) {
	data := ctx.Data(nil)
	out, ok := e.pl.Format(dst, data)
	if !ok {
		if missing := ctx.Missing(); len(missing) > 0 {
			return dst, fmt.Errorf("engine: cannot format, unset bindings: %v", missing)
		}
		return dst, fmt.Errorf("engine: format failed")
	}
	return out, nil
}
