# Z-Template

*[中文](README.md) · English*

**One template, both directions.** The same template that *parses* text into structured data also *formats* data back into text. Add a field-mapping layer and "text A → text B" becomes declarative.

```
source text ──parse(source tmpl)──> data ──mapping──> data ──format(target tmpl)──> target text
     └────────────────────────────── transform ──────────────────────────────────────┘
```

A Go core, a thin `ctypes` Python SDK, and a CLI.

---

## 60 seconds

```bash
git clone https://github.com/litterzhang/zdog-template && cd zdog-template
make build                                # builds libztpl.so (needs Go 1.25+ and a C compiler)
uv tool install --from ./cli zdog-template-cli     # installs the `ztpl` command
ztpl demo                                 # runs a self-contained example
```

`ztpl demo` walks through a real discovery: it starts with a template that *looks* fine, `verify` reports that it's ambiguous, and then a constraint makes it work.

## What it's for

Transforming logs, wire formats, and other semi-structured text. Turning a log line into CSV:

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

## Why not an existing tool

| | parse | format | structured islands | mapping |
|---|:---:|:---:|:---:|:---:|
| Python `parse` | ✅ | ✅ | ❌ | ❌ |
| Grok / logstash | ✅ | ❌ | ❌ | ❌ |
| Jinja / Handlebars | ❌ | ✅ | ❌ | ❌ |
| **Z-Template** | ✅ | ✅ | ✅ | ✅ |

**Structured islands** are the real differentiator. A regex cannot match nested or balanced structure — that isn't a regular language — but `${json|name=p}` picks a complete JSON value out of the middle of unstructured text. Because such a value is *self-delimiting*, it also acts as a natural barrier to backtracking.

## Two round-trip laws

These are the skeleton of the design, not a bolted-on checker:

| Law | Statement | What it buys you |
|---|---|---|
| **A** | `format(parse(t)) == t` | Proves the template **covers the source completely** — no characters silently dropped |
| **B** | `parse(format(c)) == c` | Proves the target template **can be read back** — the transform isn't a one-way trip |

Law A turns the most annoying question in text extraction — *"is my template quietly missing something?"* — into an automatic assertion:

```bash
$ ztpl verify -s '[${ts}] ${lv} ${msg}' --text '[T1] error disk full'
✗ 1/1 line(s) have problems
  line 1: [T1] error disk full
    • ambiguous: at least 2 parses
```

`${lv}` is unconstrained, so it can be `error` or `error disk`. The engine has to pick one, and different data may make it pick the other. **That's a bug in the template, and it should surface during development rather than in production.**

## Performance

| | ns/line | allocations |
|---|---|---|
| `transform` (literal delimiters) | **60** | **0** |
| with a JSON island | 87 | 0 |
| with repeat blocks (5 items/line) | 75/item | 0 |
| Python SDK, end to end | 94 | — |
| for comparison: pure Python (`re.finditer` + f-string) | 948 | — |

**10× pure Python, and the fast path allocates nothing.** The host language accounts for only ~8% (plain C and Python drive the same `.so`).

The trick isn't "generating faster code" — it's **moving repeated decisions from once-per-line to once-per-template**. A template compiles to a flat sequence of operators; the hot path has no interface dispatch, no closures, no allocation.

## Python SDK

```python
from ztemplate import Template

# No target template needed when you only parse
with Template("[${ts}] ${lv} payload=${json|name=p}") as t:
    t.parse_records('[T1] ERROR payload={"host":"web-1"}')
    # [{'ts':'T1','lv':'ERROR','p':{'host':'web-1'}}]   ← the island decodes to a real object

    t.verify_text(log)     # check the round-trip laws and ambiguity
    t.inspect()            # execution tier, whether backtracking is needed, field structure

# Full pipeline
with Template("[${ts}] ${lv}", target="${level}|${time}",
              mapping={"level": "upper(lv)", "time": "ts"}) as t:
    t.transform_text("[T1] error")          # 'ERROR|T1'

    # Streaming: memory is independent of input size (122 MB log costs +0 MB RSS)
    with open("big.log", "rb") as src, open("out.txt", "wb") as dst:
        t.transform_stream(src, dst)
```

**Invariant: `parse | format ≡ transform`.** You can split the pipeline in the middle to inspect it and the result is guaranteed identical. The price is an explicit 29× — the intermediate has to be materialized as text and parsed back — rather than a vague risk that the answer might differ.

## Template syntax

| Syntax | Meaning |
|---|---|
| `${name}` | A hole, delimited by the literal that follows (**fastest** — SIMD literal search) |
| `${re\|name=n,expr=\d+}` | Regex hole, shortest match by default |
| `${json\|name=p}` | JSON island — self-delimiting, decoded lazily |
| `${each\|name=xs,sep=';'}…${end}` | Repeat block, nestable |

**Mapping expressions** are a subset of JMESPath: `ts` (bare field — zero-copy fast path), `upper(lv)`, `p.host \|\| 'unknown'`, `p.tags[0]`, `length(p.tags)`.

**Shape** supplies serialization rules for fields that have no origin in the source text (i.e. expression results):

```json
"shape": {
  "usage": { "type": "number", "format": "%.2f" },
  "day":   { "type": "time",   "format": "%Y/%m/%d" },
  "epoch": { "type": "time",   "format": "unix" }
}
```

Time patterns use strftime rather than Go's reference layout — the core is Go, but the SDK's users write Python and shell.

## Commands

| Command | Purpose |
|---|---|
| `ztpl parse` | source text → structured bindings |
| `ztpl convert` | source text → target text (full pipeline) |
| `ztpl format` | NDJSON bindings → target text |
| `ztpl verify` | check the round-trip laws and ambiguity |
| `ztpl inspect` | see what a template compiles to |
| `ztpl demo` | self-contained example |

Diagnostics go to stderr, results to stdout, and colors switch off when not a TTY — safe to put in a pipe.

## Development

```bash
make ci          # exactly what CI runs
make bench       # performance benchmarks
make demo        # run the CLI example
make help        # all targets
```

Cross-language consistency rests on `conformance/cases/*.json`: **the cases are the single source of truth for semantics, not any one implementation.** Go and Python each run the same suite — it has already caught two divergences between the SDK and the core.

## Status

Usable, but young. Every known limitation is recorded in [`docs/DESIGN.md §12`](docs/DESIGN.md) — 26 of them, sorted into *deliberate trade-offs*, *physical limits*, and *actual debt*, each with its blast radius and workaround.

The design document doubles as a decision log. It contains all the measurements, including **the approaches the measurements killed** — several of which were my first instinct.

> The design doc is written in Chinese. The measurements, tables, and code identifiers
> in it are language-neutral; if you'd like an English translation, open an issue.

## Origins

Grew out of a [2022 design note](https://blog.942295.xyz/2022/11/22/z-template-设计/) and an unfinished Go prototype. This version rewrote the execution engine while keeping what was good in the prototype: the shape type system, the `${...}` scanner, and the tag/loader registry architecture.

## License

[MIT](LICENSE)
