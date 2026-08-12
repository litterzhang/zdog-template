# Z-Template 设计文档

> 源起：<https://blog.942295.xyz/2022/11/22/z-template-设计/>
> 前身原型：`gospace/src/huge-zhang/ztemplate`（Go，2022）
> 状态：v1 设计冻结，实现中

---

## 1. 核心 idea

**一份模板，两个方向。**

```
source text --parse(源模板)--> source context
                                     |
                                MappingConfig
                                     v
target text <--format(目标模板)-- target context
```

普通模板引擎（Jinja/Handlebars）只有 `format`；日志抽取工具（Grok）只有 `parse`。
Z-Template 让**同一份模板同时具备两个方向**，再加 mapping 层把两端接起来 —— 于是"文本 A → 文本 B 的转换"变成**声明式**的：写两份模板 + 一份字段映射，不写代码。

不存在"ParseTemplate 语法"和"FormatTemplate 语法"之分。流程图里的两个模板只是**两份各自双向的模板**（源模板、目标模板）。

### 定位

对比先验技术，能看清独特性：

| 方案 | parse | format | 结构化岛 | mapping |
|---|:---:|:---:|:---:|:---:|
| Python `parse` 库 | ✅ | ✅ | ❌ | ❌ |
| Grok / logstash | ✅ | ❌ | ❌ | ❌ |
| Jinja / Handlebars | ❌ | ✅ | ❌ | ❌ |
| Boomerang (lens) | ✅ | ✅ | ❌ | ❌ |
| **Z-Template** | ✅ | ✅ | ✅ | ✅ |

**双向 + 结构化岛 + mapping，三者缺一就退化成上面某个已有工具。**

---

## 2. 两条 round-trip 定律（硬约束）

"硬约束"不能一刀切，因为 mapping 之后目标侧**没有原文可参照**。两条定律分管两段：

| 定律 | 表述 | 靠什么保证 | 作用 |
|---|---|---|---|
| **A（源侧往返）** | `format(parse(t)) == t` | 每个绑定保留 `raw` span，format 走 replay | 证明模板**完整覆盖**了源文，没有静默丢字符 |
| **B（目标侧往返）** | `parse(format(c)) == c` | Shape 提供 codec，编解码互逆 | 证明目标模板**能被读回来**，转换不是单程票 |
| ~~全链路~~ | `src → tgt → src` | — | **不要求**。转换本来就有意丢信息 |

定律 A 是最有价值的那条：它把"我的模板是不是漏了点什么"这个文本抽取里最烦人的问题，变成一个**自动断言**。

> **定律 A 同时是最大的性能优化**：context 未被修改时，`format` 的正确答案就是原文本身，直接返回，O(1)。见 §7。

---

## 3. 数据模型

### Binding —— 值 + 出处

```go
type Binding struct {
    Value any            // 解析出的值
    Raw   []byte         // 原始文本片段；nil = 来自 mapping，无出处
    Span  [2]int32       // 在源文中的偏移
    Shape *shape.Node    // 无 Raw 时，靠它序列化
}
```

`Raw == nil` 是 format 的分水岭：
- 有 Raw → 原样吐回（定律 A）
- 无 Raw → 交给 Shape codec（定律 B）

### Context —— 结构化，不是扁平 map

旧原型用扁平 `sync.Map` + 自动生成的 `text0/regex1` 键，无法表达嵌套，Shape 也就用不上。新设计用路径寻址：

```
user.name        user.tags[0]        items[]        @        # @ = 根
```

### 关键推论：Shape 的真正职责

一旦经过 mapping，目标 context 的字段**没有 Raw 来源**了，format 不知道该怎么序列化它。
**Shape 就是为无 Raw 字段提供序列化/反序列化规则的那一层** —— 不是可有可无的校验器。

这解释了旧原型里 Shape 为什么悬空：因为 mapping 没做，就永远遇不到"没有 Raw 的字段"。

---

## 4. 模板语法

沿用旧原型的 `${tag|k=v,...}`。

| tag | 写法 | parse | format |
|---|---|---|---|
| `text` | 裸文本，`${}` 可省 | 字面量锚点，确定性 | 原样输出 |
| `re` | `${re\|name=lv,expr=\w+}` | 正则洞，**默认非贪婪** | replay raw 或 codec |
| `json` | `${json\|name=payload}` | 结构化岛，自定界 | replay raw 或 `json.Marshal` |
| `ext` | `${ext\|extension=x,...}` | 扩展点 | 扩展点 |

`greedy=true` 显式开贪婪。旧 README 里 `\d+345` 匹 `123` 的 "best-match" 语义 = 非贪婪默认值。

### 语法糖：省略 tag

`${name}` 等价于 `${re|name=name}`，且洞的定界由**后继字面量**推导。这是最常用的形式：

```
[${ts}] ${lv} ${msg} payload=${payload}
```

---

## 5. 执行架构：Element AST → 扁平 plan

这是新旧代码的接缝，也是整个设计的枢纽。

```
模板字符串
    │  syntax 扫描器（复用旧 template.go 的三态状态机）
    ▼
Element AST         ← tag/loader/extension 注册表（复用旧架构，可扩展层，不在热路径）
    │  Element.Compile(*plan.Builder)
    ▼
扁平算子 plan       ← ★ 新增，热路径只跑这个
    │  engine
    ▼
parse / format
```

**关键改造**：`Element` 接口去掉 `Parse(...) (<-chan ParseResult, chan Void)`，改为 `Compile(*plan.Builder) error`。
旧的 Element / Loader / Extension 注册表体系**原样保留**（那是好设计），只是把"每个元素自己边解析边产候选"换成"每个元素把自己编译进扁平计划"。

### 算子集

| 算子 | 触发条件 | 实现 |
|---|---|---|
| `Prefix(lit)` | 模板起始字面量 | `bytes.HasPrefix` |
| `FindByte(c)` | 洞由**单字符**定界 | `bytes.IndexByte`（memchr） |
| `FindLit(s)` | 洞由**多字符**字面量定界 | `bytes.Index`（SIMD/AVX2） |
| `Rest` | 洞吃到行尾 | 切片 |
| `Regex(p)` | 洞有真正的正则约束 | `regexp`，**仅在必要时** |
| `JSONIsland` | `${json}` | 自定界扫描，0 或 1 个候选 |

### 四个层级，自动选择模板能达到的最低层

| 层 | 条件 | 引擎 | 实测 |
|---|---|---|---|
| **T0** | 全部洞可由字面量定界 | 纯 `bytes.Index` 链 | **0.060 µs/行** |
| **T1** | 有正则约束的洞 | `regexp` 混合 | ~1.1 µs/行 |
| **T2** | 有结构化岛 | 分段 + 岛扫描 | — |
| **T3** | `${each}` / 扩展点 | 回溯解释器 + 记忆化 | — |

### 结构化岛是回溯的天然屏障

JSON 值是**自定界**的：从位置 p 开始，要么解析出唯一一个值、结束位置确定，要么失败 —— **只产生 0 或 1 个候选**。
所以回溯只发生在岛的边界上，段内部的回溯由各段引擎自己承担。这让整体在绝大多数情况下退化成线性。

---

## 6. Mapping

路径映射 + 现成表达式，**不发明语言**：

```yaml
target:
  time:      ts
  severity:  upper(level)
  host:      payload.host
  usage:     to_number(payload.pct)
  items[]:   data[*].id
```

表达式引擎选 **JMESPath**：有正式 spec（JSONPath 各家实现打架）、单值语义清晰、内置 `length/join/sort_by/to_number`、支持安全地注册自定义函数 —— 不需要 `eval`，无注入面。

左边的 key 是**写入路径**，`items[]` 表示写成列表。路径写入器与 §3 的 Path 复用同一份代码。

---

## 7. 性能设计（全部有实测背书）

### 一条法则

> Go 里的 `bytes.Index` / `IndexByte` 是 SIMD 加速的；Python 里每个"按字符/按元素"的解释级操作是 50–100 ns。
> **让每次 parse 的解释级步数逼近常数 —— 与文本长度无关、与字段数无关。**

### ⚠️ 宿主语言反转律（本项目踩到两次的同一个坑）

**同一个"手写扫描"设计，在 Python 里是负收益，在 Go 里是数量级收益。**

| 场景 | Python | Go |
|---|---|---|
| 字面量链 vs 正则 | 手写 find 循环**输** 1.3–1.5x | `bytes.Index` **赢 13x** |
| JSON 边界扫描 vs 标准库 | 手写扫描**输** 2.9x（3.73 vs 1.29 µs） | 手写扫描**赢 27x**（67 vs 1838 ns） |

原因同一个：Python 里的字符循环是解释执行，只能输给 C 实现的库；Go 里是编译代码，反而能绕开通用库的开销。
**推论：任何"要不要手写"的判断都必须先问在哪个语言里，且必须实测。**

### 实现实测（core/plan、Go 1.25.6、AMD EPYC 7763）

| 层级 | ns/行 | 吞吐 | 分配 |
|---|---|---|---|
| **T0 字面量定界** | **45.07** | 1598 MB/s | **0** |
| **T2 结构化岛** | **78.96** | 912 MB/s | **0** |
| T1 正则约束 | 587.6 | 65 MB/s | 2 |
| parse + format 往返（定律 A） | 87.77 | 820 MB/s | **0** |

正则比字面量慢 **13 倍** —— 所以 `${name}` 语法糖（无 expr）是最快写法，plan 编译器会自动选最低层级。

**岛的 Scan/Decode 分离**：parse 只需边界。只扫边界 67 ns/零分配，物化整棵值树 1838 ns/28 次分配。
纯直通字段永远不会被解码。合法性校验推迟到 `Decode`，或用 `${json|name=p,strict=true}` 在扫描期校验（约 5 倍开销）。

### 早期原型实测（决定架构方向，CPython 3.14.3 / Go 1.25.6）


| 方案 | µs/行 | vs 纯 Python |
|---|---|---|
| 纯 Python `finditer`（基线） | 0.588 | 1.00x |
| **Go `regexp`，串行** | 1.167 | **0.50x ← 慢一半** |
| Go `regexp`，4 goroutine 并行 | 0.599 | 1.29x |
| **Go plan 解释器（`bytes.Index`）** | **0.060** | **9.84x** |
| Go 手写特化匹配器（理论上限） | 0.047 | 12.64x |

**端到端 parse → map → format：**

| | µs/行 | 吞吐 |
|---|---|---|
| 纯 Python | 1.095 | 0.91 M行/秒 |
| **Go 全流水线** | **0.0905** | **10.75 M行/秒（12.09x，0.80 GB/s）** |

**同一个 `.so`，不同宿主语言：**

| 宿主 | 绑定 | µs/行 | 吞吐 |
|---|---|---|---|
| 纯 C | `dlopen` | 0.0880 | 11.36 M行/秒 |
| **Python** | `ctypes`（60 行 SDK） | **0.0949** | 10.54 M行/秒 |

> **宿主语言只影响 7.8%。** 这是多语言 SDK 可行性的直接证据。

### 三条被实测否掉的方案（决策记录，勿重蹈）

**❌ 1. Go 用 `regexp` 做核心** —— Go 的 `regexp` 是 RE2：线性时间保证，但常数因子差，**比 Python 的 `re` 慢一半**。并行也只能救回持平。
→ **必须走 plan 解释器 + `bytes.Index`。**
> 对称性很有意思：同一个"字面量链"设计，在 Python 里输给 `re` 1.3–1.5x，移进 Go 却赢 9.8x。
> **Python 里必须用正则；Go 里必须不用正则。**

**❌ 2. 模块边界切在 "Go 只做 parse"** ——

| 边界 | 实测 |
|---|---|
| Go 做 parse，宿主做 map + format | **1.09x（等于白写）** |
| Go 做 parse + map + format，宿主只拿结果 | **12.09x** |

字段逐个跨界会把优势全部吃掉（0.047 → 0.709 µs/行）。
→ **mapping / format / shape 必须全部在 Go 里。** 多语言下更狠：Java/Node 的跨界成本比 Python 还高，逐字段 API 会让它们比纯 Java/纯 JS 还慢。

**❌ 3. 在 Python 侧手写 JSON 状态机 / extent 扫描** —— 旧原型 555 行手写 JSON 状态机。Python 侧同类实验：手写 extent 扫描 3.73 µs vs C 实现的 `raw_decode` 1.29 µs。
→ **Python SDK 侧用标准库。但 Go core 侧相反 —— 见上面的「宿主语言反转律」，Go 里手写扫描快 27 倍。**

**另外砍掉的**：goroutine 并行（字面量匹配器 4 核只从 17.1x 到 19.4x，已是内存带宽瓶颈 0.8 GB/s）；splice 局部重渲染（无稳定收益，只保留 replay 的 O(1) 快路径）；cffi/SWIG/gopy（批量下 ctypes 足够）。

---

## 8. C ABI 与多语言 SDK

**ABI 就是产品。** 整个产品面只有 5 个函数，每个语言的 SDK 都是薄壳。

```c
int32_t ZtplAbiVersion(void);
int64_t ZtplCompile  (const uint8_t* cfg, int32_t cfgLen);
int32_t ZtplTransform(int64_t h, const uint8_t* in, int32_t inLen,
                      uint8_t* out, int32_t outCap);
int32_t ZtplLastError(int64_t h, uint8_t* out, int32_t outCap);
void    ZtplRelease  (int64_t h);
```

### 六条为多语言而定的铁律

1. **配置即数据** —— `Compile` 吃一段 JSON。所有语言传同样的字节，**不需要每个语言维护一套「构造模板」的 API**。
2. **不透明整数句柄** —— 绝不跨界传结构体（对齐/padding 在各语言 FFI 里是噩梦），不传指针。
3. **调用方分配缓冲 + grow-retry**（返回负数 = 建议容量）—— **不需要 `Free()`**，消掉跨分配器释放的风险。
4. **字符串一律 (ptr, len) 字节对**，UTF-8，不依赖 NUL 结尾。
5. **不用回调** —— 各语言 FFI 里最难移植的部分，还会强制重入宿主运行时。
6. **返回后绝不持有宿主内存**（cgo 硬性规则）；句柄内无全局状态，可多线程并发调用。

### 「批量」是架构必需品，不只是性能优化

| 语言 | 最省事的绑定 | 单次调用开销 | 摊到 5000 行 |
|---|---|---|---|
| Python | ctypes | ~0.33 µs | 0.00007 µs |
| Java | JNA | ~1–5 µs | 0.0002–0.001 µs |
| Node | ffi-napi | ~1–2 µs | 0.0002–0.0004 µs |
| Go | 原生 import | 0 | 0 |

逐字段 API 下，JNA/ffi-napi 比原生慢几十倍，每个语言被迫写 JNI/N-API 原生扩展。
批量 API 下，**最省事的绑定就够用** → 每个语言的 SDK 都是几十行。

### conformance 套件是多语言项目的核心资产

`conformance/cases/*.json`：`{config, input, expect_context, expect_output, laws}`。
每个语言的 SDK 跑同一套，round-trip 定律 A/B 直接编码成断言。

**语义的唯一真源是 conformance 用例，不是某个实现** —— 因此不必维护第二套完整实现。

---

## 9. 旧代码复用映射

### ✅ 高价值复用（架构原样保留）

| 旧代码 | 去向 | 说明 |
|---|---|---|
| `shape/**`（model + 5 种 node） | `core/shape/` | JSON-Schema 风格类型系统 + parser 注册表，Parse/Dump 双向。**正是 Shape 层要的东西** |
| `template/template.go` 的 `Load()` | `core/template/syntax/` | `${...}` 三态扫描器，逻辑正确 |
| `template/loader/**` | `core/template/loader/` | tag → loader 注册表 + `init()` 自注册 |
| `template/extension/**` | `core/template/extension/` | name → extension 注册表 |
| `template/model/` 的 `Tag`/`Element`/`ElementLoader` | `core/template/model/` | 接口保留 |
| `common/cache.go` 的 `Cachable[V]` | `core/common/` | 泛型惰性缓存 |
| `template/util/utils.go` | `core/template/syntax/` | 属性解析 |

### 🔧 复用但需修复

| 位置 | 问题 |
|---|---|
| `shape/node/dict_node.go` `Dump()` | 用 `:=` 遮蔽外层 `err`，错误被吞掉且可能返回 nil map。已与 array 合并为单一 `itemsParser`（两者结构完全相同，旧代码是逐字重复） |
| `template/util` `StringToMap` | `strings.Split(item,"=")` 应改 `SplitN(...,2)`，否则 `expr=a=b` 被截断 |
| `template/util` `StringToMap` | 按 `,` 无条件切分，`expr=\d{1,3}` 会被切坏。新增转义机制解决 |
| `shape/field/**`（TreeField / array_field） | **由 `core/binding` 取代**。旧代码两个 bug 一并作废：`array_field.Value()` 循环缺 `idx++`（所有元素写进 `value[0]`）、`TreeField.Key()` 在**值接收者**上缓存导致缓存永远失效。新实现须有对应测试 |

### ♻️ 必须重写

| 旧实现 | 原因 |
|---|---|
| `Element.Parse() (<-chan ParseResult, chan Void)` | 每元素每回溯起 goroutine + 2 个闭包（Sync/Undo）。改为 `Compile(*plan.Builder)` |
| `element/schema_json.go`（555 行手写 JSON 状态机） | 换标准库扫描器，见 §7 否决记录 3 |
| `context/context.go`（扁平 `sync.Map`） | 改结构化 Context + Binding |
| `element/regex_pattern.go` 的候选枚举 | 顺序与"最短优先"意图不符、重复发候选、每候选跑一次正则 O(n²) |
| 全局可变状态（如原型里的 `startsPool`） | 句柄需支持多线程并发，不得有包级可变状态 |

### ❌ 删除

- `stream/`（Future/Operator 实验），与主线无关
- `main.go` 的手工 demo，由测试与 conformance 取代

### 其它已知缺陷（新实现须避免）

- **byte/rune 混用**：旧代码 `sourceText[startIndex:]`、`tempStr[:i]` 按字节切片，测试用例里正好有 `"nn中国人"`，切在半个字符上是必然的。新实现统一按字节处理 UTF-8，**只在字面量/正则边界切分**，保证不切进多字节序列中间。

---

## 10. 仓库结构

```
zdog-template/
├── go.mod
├── docs/DESIGN.md
├── core/                      # Go 模块 —— 唯一的实现
│   ├── common/                # Void, Cachable（复用）
│   ├── shape/{model,node}/    # 复用 + 修 bug
│   ├── template/
│   │   ├── model/             # Tag, Element, ElementLoader（复用）
│   │   ├── syntax/            # ${...} 扫描器（复用）
│   │   ├── element/           # rawtext / hole / jsonisland
│   │   ├── loader/            # 注册表（复用）
│   │   └── extension/         # 注册表（复用）
│   ├── plan/                  # ★ 新：Element AST -> 扁平算子
│   ├── engine/                # ★ 新：plan 执行 + replay
│   ├── binding/               # ★ 新：Binding + Context + Path
│   └── mapping/               # 新：JMESPath 求值 + 路径写入
├── cshared/abi.go             # //export 薄封装 -> libztpl.so
├── conformance/cases/*.json   # ★ 多语言语义真源
├── sdk/python/ztpl/           # ctypes SDK
└── bench/                     # 性能门禁
```

Go 用户直接 `import core/`，不走 `.so`。

---

## 11. 路线图

| 阶段 | 内容 | 出口标准 |
|---|---|---|
| **P0** | 仓库骨架、ABI v1 冻结、conformance 格式、bench 门禁 | ✅ 已完成 |
| **P1** | shape 移植 + 语法层移植 + plan 编译器 + engine + pipeline + C ABI + Python SDK | ✅ **定律 A/B 全绿，8/8 conformance 用例 Go 与 Python 双语言通过** |
| **P2** | `${each}` 重复块（多行/列表转换的刚需） | |
| **P3** | Shape codec 接入 format，无出处字段的序列化 | 定律 B 覆盖类型化字段 |
| **P4** | mapping 表达式（JMESPath 子集）、结构化路径 `user.name` / `items[]` | 端到端日志 → JSON 报文 |
| **P5** | Java SDK（FFM API），conformance 三语言绿灯 | |
| **P6** | 歧义检测 `strict` 模式（枚举全部解）、跨算子回溯（T3） | |

### P1 已知限制（已记录，非遗漏）

- `OpRegexUntil` 只在**本算子内**回溯定界符，不会因后续算子失败而重试。完整回溯是 P6 的 T3 层。
- Context 目前按洞名平铺；结构化路径（`user.name`、`items[]`）随 P4 的 mapping 层一起做。
- mapping 目前只支持同名/重命名直通，表达式求值是 P4。
- 非 strict 的 JSON 岛只校验结构配对，不校验完整合法性（67 ns vs 360 ns 的取舍）；
  需要严格校验时写 `${json|name=p,strict=true}`。

### 性能门禁（不是事后调优，是设计约束）

由 `make bench` 守护。括号内为 P1 实测值：

| 门禁 | 阈值 | 实测 |
|---|---|---|
| T0 层 parse | ≤ 100 ns/行 | **45.07 ns/行**（1598 MB/s，0 分配） |
| 端到端 transform | ≥ 10 M行/秒 | **12.08 M行/秒**（82.80 ns/行，0 分配） |
| Python SDK 端到端 | ≥ 10 M行/秒 | **11.09 M行/秒**（90.14 ns/行） |
| SDK 相对纯 Python | ≥ 5x | **10.4x** |
| 宿主绑定层损耗 | ≤ 10% | **8.9%**（90.14 vs 82.80 ns/行） |

> 绑定层损耗的构成：ABI 的一次 Go 缓冲 → C 缓冲 memcpy（约 4.5%）+ ctypes 调用开销。
> 曾一度是 14.9%，原因是 SDK 里用了 `buf.raw[:n]` —— `.raw` 会先把**整个**缓冲物化成
> bytes 再切片，代价 O(buffer_size) 而非 O(n)。改用 `ctypes.string_at(buf, n)` 后降到 8.9%。
