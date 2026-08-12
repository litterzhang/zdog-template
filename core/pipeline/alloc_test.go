//go:build !race

// 竞态检测器自己会插入分配（实测同一用例 1 次/行变成 4.7 次/行），
// 所以分配计数在 race 模式下没有意义 —— 用构建标签把这组测试排除掉。
// `make test`（带 -race）跳过它们，`make alloc`（不带）专门跑。

package pipeline_test

import (
	"strings"
	"testing"

	"github.com/litterzhang/zdog-template/core/pipeline"
)

// 零分配回归测试。
//
// 为什么用分配次数而不是墙钟时间做门禁：**分配次数是确定性的**，在共享 CI
// runner 上也不会抖；墙钟时间会因邻居负载差 2~3 倍，拿它设阈值必然假失败。
// 而真正会被意外引入的性能退化——多一次分配、少一次缓冲复用——恰恰都体现在
// 分配次数上。
//
// 这些断言守住的是设计承诺：**快路径全程零分配**。

func mustCompile(t *testing.T, cfg *pipeline.Config) *pipeline.Pipeline {
	t.Helper()
	p, err := pipeline.Compile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// allocsFor 返回稳态下每次调用的分配次数。
//
// 先跑几轮让缓冲长到位 —— 第一次调用必然要分配，那不是回归。
func allocsFor(t *testing.T, fn func()) float64 {
	t.Helper()
	for i := 0; i < 8; i++ {
		fn()
	}
	return testing.AllocsPerRun(30, fn)
}

func TestTransformFastPathIsZeroAlloc(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *pipeline.Config
		line string
	}{
		{"字面量定界", &pipeline.Config{
			Version: 1, Source: "[${ts}] ${lv} ${msg}", Target: "${lv}|${ts}|${msg}",
		}, "[T1] ERROR disk full"},

		{"结构化岛直通", &pipeline.Config{
			Version: 1, Source: "[${ts}] p=${json|name=p}", Target: "${ts} ${p}",
		}, `[T1] p={"host":"web-1","n":[1,2]}`},

		{"重复块", &pipeline.Config{
			Version: 1,
			Source:  "items=${each|name=xs,sep=;}${id}:${n}${end}",
			Target:  "${each|name=xs,sep=','}${id}x${n}${end}",
		}, "items=a:1;b:2;c:3"},

		{"重命名映射", &pipeline.Config{
			Version: 1, Source: "[${ts}] ${lv}", Target: "${level}/${time}",
			Mapping: map[string]string{"level": "lv", "time": "ts"},
		}, "[T1] ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mustCompile(t, tc.cfg)
			in := []byte(strings.Repeat(tc.line+"\n", 200))
			s := p.NewScratch()
			out := make([]byte, 0, len(in)*2)

			if _, matched, total := p.Transform(out[:0], in, s); matched != total || matched == 0 {
				t.Fatalf("matched %d/%d —— 基准跑在空转上", matched, total)
			}
			if n := allocsFor(t, func() { out, _, _ = p.Transform(out[:0], in, s) }); n != 0 {
				t.Errorf("快路径出现 %v 次分配/调用，应为 0", n)
			}
		})
	}
}

// 扁平引擎失败后走回溯：分配次数应当有界且很小，不随输入线性增长。
func TestBacktrackAllocationsAreBounded(t *testing.T) {
	p := mustCompile(t, &pipeline.Config{
		Version: 1, Source: `${a}.${re|name=n,expr=\d+}`, Target: "${a}-${n}",
	})
	in := []byte(strings.Repeat("x.y.42\n", 200))
	s := p.NewScratch()
	out := make([]byte, 0, len(in)*2)
	if _, matched, _ := p.Transform(out[:0], in, s); matched != 200 {
		t.Fatalf("only %d matched", matched)
	}
	n := allocsFor(t, func() { out, _, _ = p.Transform(out[:0], in, s) })
	// 每行走一次回溯，searcher 因 OpEach 分支的闭包逃逸，约 1 次/行。
	// 定成 2 倍余量：真出现"每行多好几次分配"的退化就会被抓住。
	if limit := float64(200 * 2); n > limit {
		t.Errorf("回溯路径 %v 次分配/调用，超过上限 %v", n, limit)
	}
}

// 表达式路径不可能零分配（any 装箱是固有的），但要盯住"每个被引用字段 1 次"
// 这个量级，防止悄悄变成每行好几次。
func TestExpressionAllocationsStayBounded(t *testing.T) {
	p := mustCompile(t, &pipeline.Config{
		Version: 1, Source: "[${ts}] ${lv} ${msg}", Target: "${a}|${b}|${c}",
		Mapping: map[string]string{"a": "ts", "b": "upper(lv)", "c": "msg"},
	})
	const lines = 200
	in := []byte(strings.Repeat("[T1] error disk full\n", lines))
	s := p.NewScratch()
	out := make([]byte, 0, len(in)*2)
	if _, matched, _ := p.Transform(out[:0], in, s); matched != lines {
		t.Fatalf("only %d matched", matched)
	}
	n := allocsFor(t, func() { out, _, _ = p.Transform(out[:0], in, s) })
	// 实测每行 3 次（取值装箱 + ToUpper + 结果装箱）。留一倍余量。
	if limit := float64(lines * 6); n > limit {
		t.Errorf("表达式路径 %v 次分配/调用（%d 行），超过上限 %v", n, lines, limit)
	}
}
