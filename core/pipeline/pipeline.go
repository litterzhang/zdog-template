// Package pipeline 把源模板、字段映射、目标模板串成一条完整的文本转换流水线。
//
// 这是 blog 里那张流程图的实现：
//
//	source text --parse--> source context --mapping--> target context --format--> target text
//
// 模块边界是实测定死的（见 DESIGN.md §7 否决记录 2）：整条流水线必须留在
// 同一侧完成。把 parse 放在 Go、mapping/format 放在宿主语言，实测只有 1.09x
// —— 等于白写；全链路在 Go 内则是 12.09x。
package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/litterzhang/zdog-template/core/engine"
	"github.com/litterzhang/zdog-template/core/mapping"
	"github.com/litterzhang/zdog-template/core/plan"
)

// ConfigVersion 是当前配置格式版本。
const ConfigVersion = 1

// Config 是一条流水线的完整描述。
//
// 「配置即数据」是多语言 SDK 的第一条铁律（见 DESIGN.md §8）：
// 所有语言传同样的 JSON 字节，不需要每个语言各自维护一套构造 API。
type Config struct {
	Version int `json:"version"`
	// Source 可省略。只做 format（数据不来自文本，只差渲染）时不需要源模板；
	// 此时输入 JSON 的键直接就是目标字段名，mapping 不参与。
	Source string `json:"source,omitempty"`
	// Target 可省略。只做 parse / verify / inspect 时不需要目标模板。
	Target string `json:"target,omitempty"`
	// Mapping 是 目标字段 -> 源字段（或映射表达式）。缺省时按同名直通。
	Mapping map[string]string `json:"mapping,omitempty"`
	// Shape 是 目标字段 -> shape 定义，为**无 Raw 出处**的字段提供序列化规则。
	//
	// 声明了 shape 的字段会被移出零拷贝快路径 —— 因为"按类型格式化"本身
	// 就意味着输出不再等于源文字节。这是刻意的显式取舍。
	Shape map[string]json.RawMessage `json:"shape,omitempty"`
}

// ParseConfig 从 JSON 解析配置。
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("pipeline: invalid config: %w", err)
	}
	return &cfg, nil
}

// Pipeline 是一条已编译的流水线。不可变，可被多 goroutine 并发使用。
type Pipeline struct {
	src   *engine.Engine
	tgt   *engine.Engine
	route route
}

// Compile 编译一条流水线。
func Compile(cfg *Config) (*Pipeline, error) {
	if cfg.Version != ConfigVersion {
		return nil, fmt.Errorf("pipeline: unsupported config version %d (want %d)",
			cfg.Version, ConfigVersion)
	}
	if cfg.Source == "" && cfg.Target == "" {
		return nil, fmt.Errorf("pipeline: at least one of source or target is required")
	}
	codecs, err := compileCodecs(cfg.Shape)
	if err != nil {
		return nil, err
	}

	// 只有目标模板：纯渲染。输入 JSON 的键直接是目标字段名，没有 mapping 可谈。
	if cfg.Source == "" {
		tgt, err := engine.New(cfg.Target)
		if err != nil {
			return nil, fmt.Errorf("pipeline: target template: %w", err)
		}
		if len(cfg.Mapping) > 0 {
			return nil, fmt.Errorf(
				"pipeline: mapping needs a source template — without one there is " +
					"nothing to map from; the input keys are target field names directly")
		}
		return &Pipeline{tgt: tgt}, nil
	}

	src, err := engine.New(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("pipeline: source template: %w", err)
	}

	// 只有源模板：parse / verify / inspect。
	if cfg.Target == "" {
		return &Pipeline{src: src}, nil
	}
	tgt, err := engine.New(cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("pipeline: target template: %w", err)
	}
	r, err := buildRoute(src.Plan(), tgt.Plan(), cfg.Mapping, codecs, "")
	if err != nil {
		return nil, err
	}
	return &Pipeline{src: src, tgt: tgt, route: r}, nil
}

// HasSource 报告该流水线是否配置了源模板。
func (p *Pipeline) HasSource() bool { return p.src != nil }

// HasTarget 报告该流水线是否配置了目标模板。
func (p *Pipeline) HasTarget() bool { return p.tgt != nil }

// Source 返回源模板引擎。
func (p *Pipeline) Source() *engine.Engine { return p.src }

// Target 返回目标模板引擎。
func (p *Pipeline) Target() *engine.Engine { return p.tgt }

// Scratch 是可复用的每次调用工作区，避免热路径分配。
type Scratch struct {
	res  *plan.Result
	data *plan.Data
	env  env
	eval mapping.Evaluator
	// arena 存放表达式求值结果的字节。每行复位，稳态下不再增长。
	// 快路径（裸字段名）根本不碰它 —— 那些值是源文的子切片。
	arena []byte
}

// Result 暴露底层解析结果容器，供基准测试与需要细粒度控制的调用方使用。
func (s *Scratch) Result() *plan.Result { return s.res }

// NewScratch 为该流水线分配工作区。每个 goroutine 应持有自己的实例。
func (p *Pipeline) NewScratch() *Scratch {
	s := &Scratch{}
	if p.src != nil {
		s.res = p.src.Plan().NewResult()
	}
	if p.tgt != nil {
		s.data = p.tgt.Plan().NewData()
	}
	return s
}

// TransformLine 转换单行，结果追加到 dst。不匹配时返回 ok=false。
func (p *Pipeline) TransformLine(dst, line []byte, s *Scratch) ([]byte, bool) {
	if p.tgt == nil || p.src == nil {
		return dst, false
	}
	if !p.src.ParseInto(line, s.res) {
		return dst, false
	}
	s.arena = s.arena[:0]
	if !p.route.fill(s.data, s.res, line, s, p.src.Plan()) {
		return dst, false
	}
	return p.tgt.Plan().Format(dst, s.data)
}

// Transform 批量转换：输入按 \n 分行，每行转换后以 \n 结尾写入 dst。
// 返回结果、匹配行数、总行数。不匹配的行被跳过。
func (p *Pipeline) Transform(dst, in []byte, s *Scratch) (out []byte, matched, total int) {
	out = dst
	for start := 0; start < len(in); {
		line := in[start:]
		if j := bytes.IndexByte(line, '\n'); j >= 0 {
			line, start = line[:j], start+j+1
		} else {
			start = len(in)
		}
		if len(line) == 0 {
			continue
		}
		total++
		next, ok := p.TransformLine(out, line, s)
		if !ok {
			continue
		}
		out = append(next, '\n')
		matched++
	}
	return out, matched, total
}
