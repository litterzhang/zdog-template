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
| `each` | `${each\|name=xs,sep=;}...${end}` | 重复块，按 sep 切分迭代 | 每次迭代渲染一遍，用 sep 连接 |

`greedy=true` 显式开贪婪。旧 README 里 `\d+345` 匹 `123` 的 "best-match" 语义 = 非贪婪默认值。

### 属性值的引号与转义

属性以 `,` 分隔、`=` 赋值，因此值里出现这些字符时需要消歧：

- **单引号包裹**：`sep=','`、`sep=', '` —— 逗号分隔符是最常见的写法，这是推荐形式
- **反斜杠转义**：`sep=\,`、`expr=\d{1\,3}` —— 等价，适合嵌在正则里
- 反斜杠只在其后紧跟 `} , $ \ =` 时才吞掉自身，所以 `\d` `\s` 这类正则元字符不受影响

> 分工要点：`Scan` 只用转义定位结束的 `}`，**元素体原样交给 `ParseAttrs` 反转义**。
> 两边都做会导致双重反转义，`expr=a\,b` 被切成两个属性。

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
| **T0** | 全部洞可由字面量定界 | 纯 `bytes.Index` 链 | **47.79 ns/行** |
| **T1** | 有正则约束的洞 | `regexp` 混合 | 576.5 ns/行 |
| **T2** | 有结构化岛 | 分段 + 岛扫描 | **87.33 ns/行** |
| **T3** | 有重复块 `${each}` | 子计划 + 分隔符切分 | **65.14 ns/迭代** |

### 重复块：把同一套思路抬高一层

`${each|name=xs,sep=S}body${end}` 的边界确定方式与洞完全同构：

1. **块的范围**由块之后的后继字面量划定（一次 `bytes.Index`）
2. **块内**按 `sep` 切分成若干次迭代
3. 每次迭代必须被 body 的**子计划完整消费**

切分不是无脑 `Split`：分隔符可能出现在某次迭代的内容里（如 `[a;b];[c]`），
因此对每个候选边界都尝试子计划解析，失败则延伸到下一个分隔符 ——
与 `OpRegexUntil` 的 best-match 是同一个思路：**边界由"能否被后续消费"裁决**。

每个 `${each}` 层是**独立命名空间**，内外同名不冲突。块可嵌套。

### 跨算子回溯：先扁平，失败才回溯

扁平引擎在每个定界点取**首次匹配** —— 零分配、无函数调用。但它会漏掉这类情况：

| 模板 | 输入 | 扁平引擎 | 正解 |
|---|---|---|---|
| `${a}.${re\|expr=\d+}` | `x.y.42` | 首个 `.` → 剩 `y.42` 匹不上 `\d+` ✗ | `a="x.y"` |
| `${a}/${json}` | `a/b/{"x":1}` | 首个 `/` → 剩 `b/{...}` 不是 JSON ✗ | `a="a/b"` |
| `${a}!` | `x!y!` | 首个 `!` → 剩 `y!` 没吃完 ✗ | `a="x!y"` |

策略是 **先跑扁平引擎，失败了才回溯**。扁平引擎成功时，回溯引擎的第一条路径
与它完全相同，所以**匹配的输入一行代价都不多付**。

回溯的启用与否由**编译期分析**决定（`needsBacktrack`），其中有一条严谨的收紧：

> **`Find` 类算子之后跟 `Find` 不需要回溯。**
> 候选按位置**递增**枚举，所有备选都让剩余输入更短。若 `Find` 因"剩下的输入里
> 没有定界符"而失败，更短的输入同样没有 —— 推后不可能把失败变成成功。

据此 `${a} ${b} ${c}` 这类纯字面量链被排除在回溯之外，保住扁平快路径。
但**末尾若不是 `OpRest`，"必须吃满输入"这个终检本身就是可失败点**（上表第三行）。

### 歧义检测

同一个搜索机制换个 visitor 就能枚举全部解：

```go
e.VerifyUnambiguous(src)      // 多于一个解 -> ErrAmbiguous
e.CountParses(src, limit)
```

歧义是**模板设计的 bug** 而非运行期偶然：`${a}.${b}!` 对 `x.y.z!` 既可解成
`a="x"` 也可解成 `a="x.y"`，引擎只能挑一个。这类问题在开发期暴露，
好过上线后靠数据暴露。

### 结构化岛是回溯的天然屏障

JSON 值是**自定界**的：从位置 p 开始，要么解析出唯一一个值、结束位置确定，要么失败 —— **只产生 0 或 1 个候选**。
所以回溯只发生在岛的边界上，段内部的回溯由各段引擎自己承担。这让整体在绝大多数情况下退化成线性。

---

## 6. Mapping

**JMESPath 的一个子集**，不发明语言：

```json
"mapping": {
  "time":  "ts",                      // 裸字段 —— 零拷贝快路径
  "level": "upper(lv)",               // 函数
  "host":  "p.host || 'unknown'",     // 路径 + 回退
  "first": "p.tags[0]",               // 下标
  "n":     "length(p.tags)"
}
```

| 支持 | 不支持（刻意留白） |
|---|---|
| 字段、属性路径 `a.b`、下标 `a[0]`（含负下标） | 投影 `a[*].b` |
| 字面量 `'s'` `42` `true` `null` | 过滤 `[?x=='y']` |
| `\|\|` 回退（JMESPath 假值语义，**数字 0 不是假值**） | 多选哈希/列表 |
| 函数（见下） | 管道 `\|` |

重复结构由模板的 `${each}` 展开，所以投影在这里没有必要。

**函数**：`length` `to_string` `to_number` `type` `not_null` `join` `keys` `values`
`starts_with` `ends_with` `contains` `reverse` `sort`（JMESPath 内置）
\+ `upper` `lower` `trim` `replace` `split`（按 JMESPath 的自定义函数机制扩展 ——
规范本身没有大小写函数，但文本转换里太常用）。

### ⚡ 性能分叉：裸字段名不走表达式

这是本层最重要的设计约束：

| 映射形式 | 路径 | 实测 |
|---|---|---|
| `"time": "ts"` | **零拷贝**，直接引用源文子切片 | **93.65 ns/行，0 分配** |
| `"level": "upper(lv)"` | 物化值 → 求值 → 序列化进 arena | 236.5 ns/行 |
| `"host": "p.host"` | 同上，且要**解码 JSON 岛** | 1310 ns/行 |

编译期就用 `IsBareField` 把两类分开。表达式引用的字段也在编译期校验存在性，
免得每一行才在运行期发现字段名拼错。求值环境是惰性的 —— **没被任何表达式
引用的岛永远不会被解码**。

> 已知优化点：表达式路径每行约 3 次分配来自 `any` 装箱。改用带类型标签的
> Value 结构体可以消除，属 P6 范畴。当前 236 ns/行 仍是纯 Python 的 4 倍快。

---

## 6b. Shape codec —— 无出处字段的序列化规则

```json
"shape": {
  "usage": { "type": "number", "format": "%.2f" },
  "host":  { "type": "string", "default": "N/A" },
  "id":    { "type": "number", "required": true }
}
```

Encode 与 Decode 必须**互逆** —— 这就是定律 B 在 codec 层的表述，有专门的测试守护。

职责：
- `default` —— 值为 null 时的填充
- `required` —— 值为 null 且无 default 时报错
- `format` —— `%.2f` / `%-10s` 之类；**在编译期**校验与类型匹配，不是每行才报
- 类型强制与校验（number/string/bool 之间的合法转换）

> **声明了 shape 的字段会被移出零拷贝快路径。** 按类型格式化本身就意味着
> 输出不再等于源文字节，这是刻意的显式取舍。

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
| **T0 字面量定界** | **47.79** | 1507 MB/s | **0** |
| **T2 结构化岛** | **87.33** | 824 MB/s | **0** |
| **T3 重复块**（5 迭代/行） | **325.7**（65.14/迭代） | — | **0** |
| T1 正则约束 | 576.5 | 66 MB/s | 2 |
| parse + format 往返（定律 A） | 89.31 | 806 MB/s | **0** |

> P2 引入 `Result`/`Data` 这层间接后，T0 从 45.07 → 47.79 ns（+6%），
> 端到端 82.80 → 89.59 ns（+8.2%），零分配保持。这是支持重复块的合理代价。

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
| **P2** | `${each}` 重复块 + 属性引号语法 | ✅ **嵌套块、组到组递归路由、定律 A/B 全绿，零分配** |
| **P4** | mapping 表达式（JMESPath 子集）、结构化路径 | ✅ **快路径零拷贝保持不变，表达式惰性求值** |
| **P3** | Shape codec 接入 format，无出处字段的序列化 | ✅ **Encode/Decode 互逆有测试守护** |
| **P5** | Python SDK 用 uv + pyproject 正式打包 | ✅ 运行期零依赖，28 项 pytest 绿 |
| **P6** | 跨算子回溯、歧义检测 | ✅ **快路径 49.12 ns/行不变，回溯只在失败后启用** |

---

## 12. 已知限制

每条都注明**影响范围**与**规避方式**。这些是有意识的取舍，不是遗漏。

### 12.1 解析引擎

| 限制 | 影响 | 规避 |
|---|---|---|
| **回溯无记忆化** | 相邻选择点多 + 输入长时理论上指数级。实际模板极少触发（岛与字面量都是强约束） | 给洞加 `expr` 约束把选择点变确定；必要时加 packrat |
| **重复块内部的迭代切分不重试** | 块的**终止位置**参与回溯，但块内某次迭代的切分一旦选定就不因块后算子失败而重选 | 让迭代体带明确的定界结构（如 `[${v}]`） |
| **无记忆化的失败代价落在不匹配的行上** | 批量日志里被跳过的行会走完整回溯搜索 | 用更严格的模板让扁平引擎就能快速否决 |
| **`.` 不跨行、按字节处理** | 模板以行为单位；多行记录需先自行切分 | 批量 API 已按 `\n` 分行 |

### 12.2 模板语法

| 限制 | 影响 | 规避 |
|---|---|---|
| **两个相邻无约束洞被拒绝** | `${a}${b}` 编译期报错 | 加字面量分隔符或 `expr` 约束（这是特性：避免运行期猜） |
| **`${each}` 必须有非空 `sep`** | 无分隔符时迭代边界无从确定 | 逗号写 `sep=','`，反斜杠写 `sep=\,` |
| **`${each}` 后必须跟字面量或结束模板** | 否则块的范围不确定 | 加一个字面量终止符 |
| **属性值里的 `,` `}` 需转义或加引号** | `,` 同时是属性分隔符 | 用 `sep=','`；反斜杠只在 `} , $ \ =` 前生效，正则元字符不受影响 |

### 12.3 Mapping 表达式

| 限制 | 影响 | 规避 |
|---|---|---|
| **不支持投影 `a[*].b`、过滤 `[?…]`、管道 `\|`** | JMESPath 的这部分不在子集内 | 重复结构用 `${each}` 展开 —— 这正是不需要投影的原因 |
| **表达式路径每行约 3 次分配** | 来自 `any` 装箱；244 ns/行 vs 快路径 99 ns/行 | 能用裸字段名就别写表达式；改用带类型标签的 Value 结构体可消除（未做） |
| **岛内取值必须解码整个岛** | `p.host` 会解码整份 JSON（~1.3 µs/行） | 只有被表达式引用的岛才解码；纯直通字段零成本 |

### 12.4 Shape

| 限制 | 影响 | 规避 |
|---|---|---|
| **按字段名生效，无路径限定** | `{"id": …}` 会作用于所有层级同名字段（含 `${each}` 内部） | 给不同层级的字段起不同名字 |
| **声明 shape 即离开零拷贝快路径** | 按类型格式化意味着输出不再等于源文字节 | 这是刻意的显式取舍；不需要格式化就别声明 |
| **`format` 只支持 number/string** | 复合类型退回 JSON | — |

### 12.5 JSON 入口层（ABI v2 的 parse / format / verify）

这层服务于 SDK 与 CLI，**不在热路径上**，因此有意选了简单实现。但代价要记清楚。

| 限制 | 影响 | 规避 |
|---|---|---|
| ~~`parse` 急切解码所有岛~~ | ✅ **已修**：岛原文原样嵌入，5047 → 796 ns/行（6.3 倍） | — |
| ~~错误行被静默跳过~~ | ✅ **已修**：`stats[3]` 给出 failed 计数，原因经 `LastError` 回传 | — |
| **`parse` 仍比 `transform` 慢 4.6 倍** | 796 vs 174 ns/行。大头是 `json.Valid`（~450 ns）：非 strict 的岛在 Scan 只查了括号配对，直接拼进输出会毁掉整行 NDJSON | 只要最终文本就用 `transform`；`parse` 面向调试与中小批量 |
| **`format` 整条路径是 map 驱动的** | **1997 ns/行（2 字段）→ 4335 ns（8 字段），比 `transform` 的 58 ns 慢 33 倍**。每行都要 `json.NewDecoder` + 解码进 `map[string]any` + 按名做哈希查找写进 context；成本随字段数线性增长（~390 ns/字段）。`NewContext` 只是其中一小块固定开销 | 逐行喂 JSON 本来就不是高吞吐场景；真要快就用 `transform`（源文本直接进、目标文本直接出，全程零拷贝） |
| **只回报前 10 条错误原因** | 计数不受限，但原因最多 `MaxReportedErrors` 条 | 逐行诊断用 `verify` |

### 12.6 SDK 与 CLI

| 限制 | 影响 | 规避 |
|---|---|---|
| **CLI 全量读入内存** | `stdin.read()` / `read_bytes()`，GB 级输入会吃满内存 | 先 `split` 分片，或用 SDK 自己分块喂 |
| **SDK 输出缓冲只增不减** | 处理过一次大输入后，`Template` 实例一直占着那块缓冲 | 大批量用完 `close()` 重建 |
| **重复块只能同名/重命名映射** | 组不支持表达式，只有 `{"目标块名": "源块名"}` | 块内字段可以用表达式；块本身的重排需在模板层做 |

### 12.7 分发与平台

| 限制 | 影响 | 规避 |
|---|---|---|
| **仅构建 Linux x86_64** | macOS 开发机需自行 `make build` | CI 加 macos runner 原生构建 darwin/arm64 |
| **cgo 交叉编译成本高** | 每个平台都要原生构建 | 用 GitHub Actions matrix |
| **Go runtime 常驻宿主进程** | 额外几 MB RSS + GC 线程 | 批量 API 已把这个固定成本摊到极低 |
