# ztpl-cli

Z-Template 的命令行 —— 用来快速体验双向模板。

完全建立在 [`ztpl` Python SDK](../sdk/python) 之上：不碰 ctypes、不碰 `.so`。
CLI 做不到的事就说明 SDK 缺东西 —— 这是对 SDK 能力面的一道约束。

## 安装

```bash
make -C .. build      # 先构建 libztpl.so
uv sync
uv run ztpl demo      # 跑一个完整例子
```

## 命令

| 命令 | 作用 |
|---|---|
| `ztpl parse` | 源文本 → 结构化绑定（JSON 岛会被解码） |
| `ztpl convert` | 源文本 → 目标文本（完整流水线） |
| `ztpl format` | NDJSON 绑定 → 目标文本 |
| `ztpl verify` | 校验 round-trip 定律 A 与歧义 |
| `ztpl inspect` | 展示模板编译成了什么 |
| `ztpl demo` | 自带的完整例子，不需要输入 |

## 例子

```bash
ztpl parse -s '[${ts}] ${lv} ${msg}' --text '[T1] ERROR disk full'

cat app.log | ztpl convert \
    -s '[${ts}] ${lv} ${msg}' \
    -t '${level}|${text}' \
    -m '{"level":"upper(lv)","text":"msg"}'

ztpl verify -s '[${ts}] ${lv}' -i app.log
ztpl inspect -s '[${ts}] ${lv} payload=${json|name=p}'
```

## 退出码

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 没有任何一行匹配 / 校验发现问题 |
| 2 | 用法或模板编译错误 |
| 3 | 运行期错误（如找不到共享库） |

错误信息一律走 stderr，stdout 只放结果 —— 可以放心接管道。
非 TTY 时自动关闭颜色。
