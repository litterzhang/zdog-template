# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Two version numbers are tracked separately and deliberately:

- **Config version** — the schema of the pipeline JSON. Only bumped on an
  incompatible change to that schema.
- **ABI version** — the C function-signature contract. Bumped whenever a
  function is added or changed.

They are different contracts: adding an ABI function shouldn't force every
config file to be rewritten, and vice versa.

## [Unreleased]

## [0.1.0] — 2026-08-12

First release. Config version 1, ABI version 3.

### Added

**Bidirectional templates.** One template both parses text into structured
data and formats data back into text. `${name}` holes are delimited by the
literal that follows them, which makes them fast — the fast path compiles to
a chain of SIMD literal searches.

**Two round-trip laws**, enforced by tests rather than documented as
aspirations:

- Law A — `format(parse(t)) == t`, proving a template covers its source
  completely
- Law B — `parse(format(c)) == c`, proving the target template can be read
  back

**Structured islands** (`${json|name=p}`). A regex cannot match nested or
balanced structure; a JSON value is self-delimiting, so it can be picked out
of the middle of unstructured text — and it doubles as a natural barrier to
backtracking.

**Repeat blocks** (`${each|name=xs,sep=';'}…${end}`), nestable, with each
level forming its own naming scope.

**Mapping expressions** — a subset of JMESPath: paths, indices, `||`
fallback, and 18 functions. Bare field names stay on a zero-copy fast path;
only real expressions pay to materialize values.

**Shape** — serialization rules for fields that have no origin in the source
text. Supports `string` / `number` / `bool` / `time` / composite types, with
`default`, `required`, and `format`. Time patterns use strftime rather than
Go's reference layout, because the SDK's users write Python and shell.

**Ambiguity detection.** `verify` reports templates that admit more than one
parse for a given input — a template bug that would otherwise surface only
when the data changes.

**Cross-operator backtracking** with failure memoization, keeping pathological
templates polynomial rather than exponential.

**Streaming** (`*_stream`) — memory bounded by chunk size, independent of
input size. A 122 MB log costs ~0 MB extra RSS.

**A 5-function C ABI**, a `ctypes` Python SDK with no runtime dependencies,
and a CLI (`ztpl parse / convert / format / verify / inspect / demo`).

**A conformance suite** (`conformance/cases/*.json`) run by both Go and
Python. The cases — not any implementation — are the source of truth for
semantics.

### Performance

Measured on an AMD EPYC 7763; see `docs/DESIGN.md §7`.

| | ns/line | allocations |
|---|---|---|
| `transform`, literal delimiters | 60 | 0 |
| with a JSON island | 87 | 0 |
| with repeat blocks | 75/item | 0 |
| Python SDK, end to end | 94 | — |
| pure Python, for comparison | 948 | — |

CI gates on **allocation counts, not wall-clock time** — allocation counts are
deterministic, while shared runners vary by 2–3× and would make any time-based
threshold flaky.

### Known limitations

26 of them, catalogued in `docs/DESIGN.md §12` and sorted into *deliberate
trade-offs*, *physical limits*, and *actual debt* — each with its blast radius
and workaround. The design document doubles as a decision log: it records the
measurements, including the ones that killed approaches that seemed obviously
right at the time.

[Unreleased]: https://github.com/litterzhang/zdog-template/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/litterzhang/zdog-template/releases/tag/v0.1.0
