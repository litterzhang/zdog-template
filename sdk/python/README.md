# ztpl —— Z-Template Python SDK

一份模板，两个方向：同一份模板既能把文本**解析**成结构化数据，也能把数据**格式化**回文本；
再加一层字段映射，"文本 A → 文本 B" 就成了声明式的。

核心引擎是 Go，本包是一层 ~200 行的 `ctypes` 薄壳，**运行期零依赖**。

## 安装

```bash
uv sync                       # 装开发依赖
make -C ../.. build           # 构建 libztpl.so
uv run pytest                 # 跑测试 + 一致性用例
```

## 用法

```python
from ztpl import Template

# 只做解析时不需要目标模板
with Template("[${ts}] ${lv} ${msg} payload=${json|name=p}") as t:
    t.parse_records("[T1] ERROR disk full payload={\"host\":\"web-1\"}")
    # [{"ts":"T1","lv":"ERROR","msg":"disk full","p":{"host":"web-1"}}]
    #                                    JSON 岛被解码成真正的对象 ↑

    t.verify_text(log)          # 校验 round-trip 定律 A 与歧义
    t.inspect()                 # 执行层级、是否回溯、字段与重复块

# 完整流水线
with Template(
    "[${ts}] ${lv} ${msg}",
    target="${level}|${text}",
    mapping={"level": "upper(lv)", "text": "msg"},
) as t:
    t.transform_text("[T1] error disk full")   # -> "ERROR|disk full\n"
```

### 四个批量操作

| 方法 | 输入 → 输出 |
|---|---|
| `transform` | 源文本 → 目标文本（完整流水线） |
| `parse` | 源文本 → NDJSON 绑定 |
| `format` | NDJSON 绑定 → 目标文本 |
| `verify` | 源文本 → 定律 A 与歧义报告 |

批量是**架构要求而非优化**：整条流水线都在 Go 侧完成，Python 只拿最终结果。
实测把字段逐个跨界回 Python 会让加速比从 12x 掉到 1.09x。

```python
res = t.transform(data)        # data: bytes，按 \n 分行
res.output                     # bytes
res.matched, res.total         # 匹配/总行数，不匹配的行被跳过
res.records()                  # parse/verify 的 NDJSON 输出解析成对象列表
```

## 模板语法

| 写法 | 含义 |
|---|---|
| `${name}` | 洞，由后继字面量定界（**最快**，走 SIMD 字面量搜索） |
| `${re\|name=n,expr=\d+}` | 正则洞，默认最短匹配 |
| `${json\|name=p}` | JSON 结构化岛，自定界，惰性解码 |
| `${each\|name=xs,sep=';'}...${end}` | 重复块 |

## 映射表达式（JMESPath 子集）

```python
mapping={
  "time":  "ts",                    # 裸字段名 —— 零拷贝快路径
  "level": "upper(lv)",             # 函数
  "host":  "p.host || 'unknown'",   # 路径 + 回退
}
```

裸字段名与表达式的性能差一倍以上（99 vs 245 ns/行），能用裸字段名就别写表达式。

属性值含 `,` 时用单引号：`sep=','`；或反斜杠转义：`sep=\,`。

## 性能

| | ns/行 | 吞吐 |
|---|---|---|
| 本 SDK | 90 | 11 M行/秒 |
| 纯 Python（`re.finditer` + f-string） | 940 | 1.1 M行/秒 |

宿主绑定层相对纯 C 只损耗 ~9%。

## 命令行

见 [ztpl-cli](../../cli) —— 建立在本 SDK 之上，`ztpl demo` 可快速上手。

## 设计

见 [docs/DESIGN.md](../../docs/DESIGN.md)，含全部实测数据与被否决方案的决策记录。
