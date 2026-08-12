# Z-Template

*中文 · [English](README.en.md)*

**一份模板，两个方向。** 同一份模板既能把文本**解析**成结构化数据，也能把数据**格式化**回文本；再加一层字段映射，"文本 A → 文本 B" 就成了声明式的。

```
source text ──parse(源模板)──> 结构化数据 ──mapping──> 结构化数据 ──format(目标模板)──> target text
     └──────────────────────────── transform ────────────────────────────────────────┘
```

Go 写的核心，`ctypes` 薄壳的 Python SDK，外加一个命令行。

---

## 60 秒上手

```bash
git clone https://github.com/litterzhang/zdog-template && cd zdog-template
make build                                # 构建 libztpl.so（需要 Go 1.25+ 和 gcc）
uv tool install --from ./cli ztpl-cli     # 装 ztpl 命令
ztpl demo                                 # 跑一个自带的完整例子
```

`ztpl demo` 会演示一个真实的发现过程：先给一个"看起来没问题"的模板，`verify` 报出它有歧义，加上约束后再跑通全流程。

## 它解决什么

日志、报文、半结构化文本的**转换**。比如把一行日志变成 CSV：

```bash
ztpl convert \
  -s '[${ts}] ${re|name=lv,expr=\w+} ${msg} payload=${json|name=p}' \
  -t '${time},${level},${host}' \
  -m '{"time":"ts","level":"upper(lv)","host":"p.host || \"-\""}' \
  -i app.log
```

```
2026-08-12T01:00:00Z,ERROR,web-1
2026-08-12T01:00:05Z,WARN,-
```

## 为什么不是已有的工具

| | parse | format | 结构化岛 | mapping |
|---|:---:|:---:|:---:|:---:|
| Python `parse` 库 | ✅ | ✅ | ❌ | ❌ |
| Grok / logstash | ✅ | ❌ | ❌ | ❌ |
| Jinja / Handlebars | ❌ | ✅ | ❌ | ❌ |
| **Z-Template** | ✅ | ✅ | ✅ | ✅ |

**"结构化岛"** 是关键差异：正则匹配不了嵌套/配对结构（那不是正则语言），而 `${json|name=p}` 能在非结构化文本中间精确框出一段 JSON —— 它是**自定界**的，所以也成了回溯的天然屏障。

## 两条 round-trip 定律

这是设计的骨架，不是附赠的检查器：

| 定律 | 表述 | 作用 |
|---|---|---|
| **A** | `format(parse(t)) == t` | 证明模板**完整覆盖**了源文，没有静默丢字符 |
| **B** | `parse(format(c)) == c` | 证明目标模板**能被读回来**，转换不是单程票 |

定律 A 把"我的模板是不是漏了点什么"这个文本抽取里最烦人的问题，变成了一个自动断言：

```bash
$ ztpl verify -s '[${ts}] ${lv} ${msg}' --text '[T1] error disk full'
✗ 1/1 line(s) have problems
  line 1: [T1] error disk full
    • ambiguous: at least 2 parses
```

`${lv}` 没有约束，既可以是 `error` 也可以是 `error disk` —— 引擎只能挑一个，换份数据就可能挑到另一个。**这是模板的 bug，应该在开发期暴露，而不是上线后靠数据暴露。**

## 性能

| | ns/行 | 分配 |
|---|---|---|
| `transform`（字面量定界） | **60** | **0** |
| 含 JSON 岛 | 87 | 0 |
| 含重复块（5 迭代/行） | 75/迭代 | 0 |
| Python SDK 端到端 | 94 | — |
| 对照：纯 Python（`re.finditer` + f-string） | 948 | — |

**10 倍于纯 Python，且快路径全程零分配。** 宿主语言只影响约 8%（纯 C 与 Python 跑同一个 `.so`）。

秘密不在"生成了更快的代码"，而在**把重复的判断从每行一次挪到每模板一次** —— 模板编译成扁平算子序列，热路径上没有接口分发、没有闭包、没有分配。

## Python SDK

```python
from ztpl import Template

# 只做解析时不需要目标模板
with Template("[${ts}] ${lv} payload=${json|name=p}") as t:
    t.parse_records('[T1] ERROR payload={"host":"web-1"}')
    # [{'ts':'T1','lv':'ERROR','p':{'host':'web-1'}}]   ← JSON 岛被解码成真正的对象

    t.verify_text(log)     # 校验 round-trip 定律与歧义
    t.inspect()            # 执行层级、是否需要回溯、字段结构

# 完整流水线
with Template("[${ts}] ${lv}", target="${level}|${time}",
              mapping={"level": "upper(lv)", "time": "ts"}) as t:
    t.transform_text("[T1] error")          # 'ERROR|T1'

    # 流式：内存与输入总大小无关（122 MB 日志 +0 MB RSS）
    with open("big.log", "rb") as src, open("out.txt", "wb") as dst:
        t.transform_stream(src, dst)
```

**不变量：`parse | format ≡ transform`** —— 流水线可以从中间劈开去看一眼，结果保证一致。代价是明码标价的 29 倍（中间态必须物化成文本再解析回来），而不是"结果可能不一样"这种说不清的风险。

## 模板语法

| 写法 | 含义 |
|---|---|
| `${name}` | 洞，由后继字面量定界（**最快**，走 SIMD 搜索） |
| `${re\|name=n,expr=\d+}` | 正则洞，默认最短匹配 |
| `${json\|name=p}` | JSON 结构化岛，自定界，惰性解码 |
| `${each\|name=xs,sep=';'}…${end}` | 重复块，可嵌套 |

**映射表达式**是 JMESPath 的一个子集：`ts`（裸字段，零拷贝快路径）、`upper(lv)`、`p.host \|\| 'unknown'`、`p.tags[0]`、`length(p.tags)`。

**Shape** 给没有源文出处的字段（表达式产物）提供序列化规则：

```json
"shape": {
  "usage": { "type": "number", "format": "%.2f" },
  "day":   { "type": "time",   "format": "%Y/%m/%d" },
  "epoch": { "type": "time",   "format": "unix" }
}
```

## 命令

| 命令 | 作用 |
|---|---|
| `ztpl parse` | 源文本 → 结构化绑定 |
| `ztpl convert` | 源文本 → 目标文本（完整流水线） |
| `ztpl format` | NDJSON 绑定 → 目标文本 |
| `ztpl verify` | 校验 round-trip 定律与歧义 |
| `ztpl inspect` | 看模板编译成了什么 |
| `ztpl demo` | 自带的完整例子 |

错误信息走 stderr，stdout 只放结果，非 TTY 自动关颜色 —— 可以放心接管道。

## 开发

```bash
make ci          # 与 CI 完全相同的检查
make bench       # 性能基准
make demo        # 跑 CLI 例子
make help        # 全部目标
```

跨语言一致性靠 `conformance/cases/*.json`：**语义的唯一真源是用例，不是任何一个实现**。Go 和 Python 各跑一遍同一套用例 —— 它已经两次抓出 SDK 与 core 的分歧。

## 状态

能用，但还年轻。已知限制**全部记在 [`docs/DESIGN.md §12`](docs/DESIGN.md)**（26 条，分成"有意的设计取舍""物理限制""真正的欠债"三类，每条注明影响范围与规避方式）。

设计文档同时是决策记录：里面有全部实测数据，也有**被实测否掉的方案**——包括我一开始想错的那些。

## 缘起

源于一篇 2022 年的[设计随笔](https://blog.942295.xyz/2022/11/22/z-template-设计/)与一个未完成的 Go 原型。这一版重写了执行引擎，保留了原型里好的部分（shape 类型系统、`${...}` 扫描器、tag/loader 注册表架构）。

## 许可

[MIT](LICENSE)
