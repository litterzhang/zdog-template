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

### Added

- `mask(s)` / `mask(s, keepTail)` mapping function — replaces everything but
  the last `keepTail` characters with `*`, rune-aware and length-preserving.
  When `keepTail` is at least as long as the value it masks *everything*
  rather than returning the original: a masking function that leaks the input
  on short values fails on exactly the rows nobody audits.
- `ztpl demo redact` — a worked example of field-scoped redaction. The same
  regex over-redacts an order id and misses a card number on one line, which
  is the case for knowing where the fields are rather than what they look
  like. Also shows the escape hatch (`parse` → your own code → `format`) and
  what it costs.

### Fixed

- One parser error message was still in Chinese while every other message had
  been translated.

### Documented

Two properties that were easy to over-read, both found while checking the
claims the demo was about to make:

- **Law A does not prove correct field delimitation.** `format(parse(t)) == t`
  guarantees nothing was *lost*, not that boundaries landed where intended —
  a greedy hole can swallow its neighbour and still reproduce the line
  exactly, with `verify` reporting no problems.
- **Non-matching lines are dropped, not passed through.** Safe for redaction
  in that nothing leaks, but it is silent data loss, so `skipped` has to be
  checked.

## [0.1.1] — 2026-08-12

Documentation and packaging only. No change to the core, the ABI, or the
pipeline config schema — config version 1 and ABI version 3 both stand, so
0.1.0 clients keep working untouched.

### Added

- Version classifiers in package metadata, so PyPI can filter by Python
  version and the `pyversions` badge shows the real range instead of just "3".
- Status badges on both READMEs: CI, the two PyPI versions, latest release,
  Python and Go versions, license.

### Changed

- English README is now the default (`README.md`); the Chinese one moved to
  `README.zh-CN.md`.
- Quick start uses `pip` / `uv` instead of clone-and-build, now that wheels
  are on PyPI. Building from source moved to its own section.
- `zdog-template-cli` now requires `zdog-template>=0.1.1` instead of any
  version. The two are released from one tag, so an unbounded dependency
  only meant that a future CLI feature could silently land against an SDK
  too old to support it.

### Fixed

- `ztemplate.__version__` was hard-coded and had already drifted — it read
  `0.1.0` regardless of what was installed. It now comes from package
  metadata, the way the CLI already did it.
- `docs/PUBLISHING.md` still described the pre-rename package name and a
  single `release` environment; both changed before 0.1.0 shipped and the
  doc did not follow.
- The 0.1.0 notes below described the C ABI as having 5 functions. It has 9.
  Corrected here; the published 0.1.0 release notes keep the original text.

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

**A 9-function C ABI**, a `ctypes` Python SDK with no runtime dependencies,
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

[Unreleased]: https://github.com/litterzhang/zdog-template/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/litterzhang/zdog-template/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/litterzhang/zdog-template/releases/tag/v0.1.0
