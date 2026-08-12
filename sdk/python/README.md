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

with Template(
    source="[${ts}] ${lv} ${msg}",
    target="${level}|${text}",
    mapping={"level": "lv", "text": "msg"},
) as t:
    print(t.transform_text("[T1] ERROR disk full\n[T2] WARN low memory"))
    # ERROR|disk full
    # WARN|low memory
```

批量是**架构要求而非优化**：整条流水线（parse → mapping → format）都在 Go 侧完成，
Python 只拿最终结果。实测把字段逐个跨界回 Python 会让加速比从 12x 掉到 1.09x。

```python
res = t.transform(data)        # data: bytes，按 \n 分行
res.output                     # bytes
res.matched, res.total         # 匹配/总行数，不匹配的行被跳过
```

## 模板语法

| 写法 | 含义 |
|---|---|
| `${name}` | 洞，由后继字面量定界（**最快**，走 SIMD 字面量搜索） |
| `${re\|name=n,expr=\d+}` | 正则洞，默认最短匹配 |
| `${json\|name=p}` | JSON 结构化岛，自定界，惰性解码 |
| `${each\|name=xs,sep=';'}...${end}` | 重复块 |

属性值含 `,` 时用单引号：`sep=','`；或反斜杠转义：`sep=\,`。

## 性能

| | ns/行 | 吞吐 |
|---|---|---|
| 本 SDK | 90 | 11 M行/秒 |
| 纯 Python（`re.finditer` + f-string） | 940 | 1.1 M行/秒 |

宿主绑定层相对纯 C 只损耗 ~9%。

## 设计

见 [docs/DESIGN.md](../../docs/DESIGN.md)，含全部实测数据与被否决方案的决策记录。
