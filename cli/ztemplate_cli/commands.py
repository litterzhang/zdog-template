"""Subcommand implementations. Each returns a process exit code."""

import json
import sys

from ztemplate import Template

from . import term
from .sources import load_config, open_input, open_template

# Examples used by `ztpl demo`.
# NAIVE is deliberately ambiguous: ${lv} is unconstrained, so it can be either
# "error" or "error disk". The demo uses it to show what verify catches.
DEMO_NAIVE = "[${ts}] ${lv} ${msg} payload=${json|name=p}"
DEMO_FIXED = r"[${ts}] ${re|name=lv,expr=\w+} ${msg} payload=${json|name=p}"
DEMO_TARGET = "${time} ${level} host=${host}"
DEMO_MAPPING = {"time": "ts", "level": "upper(lv)", "host": "p.host || 'unknown'"}
DEMO_INPUT = (
    '[2026-08-12T01:00:00Z] error disk full payload={"host":"web-1","pct":95}\n'
    '[2026-08-12T01:00:05Z] warn low memory payload={"pct":80}\n'
    "this line does not match the template\n"
)

# Examples used by `ztpl demo redact`.
#
# The order number is deliberately 11 digits — the same length as the phone
# number — and the card number is deliberately 10. That is what makes the
# regex approach fail in both directions at once, which is the whole point.
REDACT_TEMPLATE = (
    "user=${user} phone=${phone} card=${card} order=${order} amt=${amt}"
)
REDACT_MAPPING = {
    "user": "user",
    "phone": "mask(phone, 4)",
    "card": "mask(card)",
    "order": "order",
    "amt": "amt",
}
REDACT_INPUT = (
    "user=alice phone=13812345678 card=6225880137 order=20260812001 amt=42.50\n"
    "user=bob phone=13900001111 card=6222021234 order=20260812002 amt=7.00\n"
)
REDACT_REGEX = r"[0-9]{11}"
# A template with card= left out. The phone hole then swallows "13812345678
# card=6225880137" and still reproduces the line exactly — which is why Law A
# holding is not by itself proof that the fields are split where you meant.
REDACT_LOOSE = "user=${user} phone=${phone} order=${order} amt=${amt}"
REDACT_ODD = "malformed line, no fields here\n"


def cmd_parse(args) -> int:
    """Source text -> structured bindings."""
    cfg = load_config(args)
    with open_template(cfg) as tpl, open_input(args) as src:
        if args.ndjson:
            # 紧凑模式直接把 NDJSON 流写出去，全程不物化
            res = tpl.parse_stream(src, sys.stdout.buffer, chunk_size=args.chunk)
        else:
            res = tpl.parse_stream(src, _PrettyJSONWriter(), chunk_size=args.chunk)
    _report(res, "parsed")
    return 0 if res.matched else 1


class _PrettyJSONWriter:
    """把流式产出的 NDJSON 逐条美化打印。

    只在换行处切分，因此不会攒下整份输出 —— 流式的意义就在这。
    """

    def __init__(self):
        self._carry = b""

    def write(self, data: bytes) -> None:
        buf = self._carry + data
        *lines, self._carry = buf.split(b"\n")
        for line in lines:
            if line.strip():
                print(term.pretty_json(json.loads(line)))


def cmd_convert(args) -> int:
    """Source text -> target text (the full pipeline)."""
    cfg = load_config(args)
    with open_template(cfg, need_target=True) as tpl, open_input(args) as src:
        res = tpl.transform_stream(src, sys.stdout.buffer, chunk_size=args.chunk)
    _report(res, "converted")
    return 0 if res.matched else 1


def cmd_format(args) -> int:
    """NDJSON bindings -> target text."""
    cfg = load_config(args)
    with open_template(cfg, need_target=True) as tpl, open_input(args) as src:
        res = tpl.format_stream(src, sys.stdout.buffer, chunk_size=args.chunk)
    _report(res, "rendered")
    return 0 if res.rendered else 1


def cmd_verify(args) -> int:
    """Check round-trip law A and ambiguity, line by line."""
    cfg = load_config(args)
    with open_template(cfg) as tpl, open_input(args) as src:
        rep = tpl.verify_stream(src, limit=args.limit, chunk_size=args.chunk)

    if rep.ok:
        print(term.green(f"✓ all {rep.total} line(s) passed"))
        print(term.dim("  law A:     the template covers the source fully, format(parse(t)) == t"))
        print(term.dim("  unambiguous: every line has exactly one parse"))
        return 0

    print(term.red(f"✗ {rep.bad}/{rep.total} line(s) have problems\n"))
    for item in rep.problems[: args.limit]:
        print(f"  {term.bold('line ' + str(item['line']))}: {item.get('input', '')}")
        for p in item.get("problems", []):
            print(f"    {term.yellow('•')} {p}")
        print()
    # 用真实失败计数而不是 len(problems) —— 流式模式下后者已被 limit 截断
    shown = min(len(rep.problems), args.limit)
    if rep.bad > shown:
        print(term.dim(f"  …and {rep.bad - shown} more; use --limit to show more"))
    return 1


def cmd_inspect(args) -> int:
    """Show what the template compiled to."""
    cfg = load_config(args)
    with open_template(cfg) as tpl:
        info = tpl.inspect()

    if args.json:
        print(term.pretty_json(info))
        return 0

    for role in ("source", "target"):
        if role not in info:
            continue
        d = info[role]
        label = "source template" if role == "source" else "target template"
        print(term.bold(label))
        print(term.table([
            ("template", d["template"]),
            ("tier", _tier_desc(d["tier"])),
            ("backtracking", _bool_desc(d["backtrack"])),
        ]))
        if d.get("fields"):
            print(f"\n  {term.cyan('fields')}")
            for f in d["fields"]:
                kind = (term.magenta("json island") if f["kind"] == "json-island"
                        else term.dim("hole"))
                print(f"    {f['name']:<16} {kind}")
        _print_blocks(d.get("blocks"), 1)
        print()
    return 0


def cmd_demo(args) -> int:
    """Run a self-contained example — no input needed."""
    if getattr(args, "scenario", "tour") == "redact":
        return _demo_redact()
    return _demo_tour()


def _demo_tour() -> int:
    data = DEMO_INPUT.encode()

    print(term.bold("Z-Template quick tour\n"))
    print(term.cyan("input"))
    for line in DEMO_INPUT.rstrip("\n").split("\n"):
        print(f"  {term.dim(line)}")

    # (1) Start with a template that looks fine
    print(f"\n{term.bold('(1) A template that looks fine')}")
    print(f"  {DEMO_NAIVE}")
    with Template(DEMO_NAIVE) as t:
        rep = t.verify(data)
        print(f"\n  {term.cyan('verify')} disagrees:")
        if rep.bad:
            for item in rep.problems[:2]:
                for p in item.get("problems", []):
                    print(f"    {term.yellow('•')} line {item['line']} — {p}")
    print(term.dim('\n  ${lv} is unconstrained, so it can be "error" or "error disk" —'))
    print(term.dim("  the engine has to pick one, and other data may make it pick the"))
    print(term.dim("  other. That is a bug in the template, not in the input."))

    # (2) Constrain it
    print(f"\n{term.bold('(2) Constrain it')}")
    print(f"  {DEMO_FIXED}")
    with Template(DEMO_FIXED) as t:
        rep = t.verify(data)
        good = rep.total - rep.bad
        print(f"\n  {term.green('✓')} {good}/{rep.total} line(s) pass"
              f"{term.dim('  (line 3 was never meant to match)') if rep.bad else ''}")

        print(f"\n  {term.cyan('parse')} — the JSON island is decoded into a real object")
        for rec in t.parse(data).records():
            print("    " + json.dumps(rec, ensure_ascii=False))

    # (3) Full pipeline
    print(f"\n{term.bold('(3) Full conversion')}")
    print(f"  {term.cyan('mapping')}  {json.dumps(DEMO_MAPPING, ensure_ascii=False)}")
    print(f"  {term.cyan('target')}   {DEMO_TARGET}")
    with Template(DEMO_FIXED, target=DEMO_TARGET, mapping=DEMO_MAPPING) as t:
        res = t.transform(data)
        print()
        for line in res.text().rstrip("\n").split("\n"):
            print(f"    {term.green(line)}")
        print(term.dim(
            f"\n  {res.matched}/{res.total} line(s) matched, {res.skipped} skipped"))

    print(f"\n{term.dim('next:')}")
    print(term.dim("  ztpl demo redact         redact a log without breaking it"))
    print(term.dim("  ztpl inspect -s '...'    see what a template compiles to"))
    print(term.dim("  ztpl verify  -s '...'    check your own template for ambiguity"))
    return 0


def _demo_redact() -> int:
    """Why field-scoped redaction beats pattern-scoped redaction.

    This is the one job that cannot be done correctly without knowing where
    the fields are — which is exactly what a bidirectional template knows.
    """
    import re

    print(term.bold("Redacting a log without breaking it\n"))
    print(term.cyan("input"))
    for line in REDACT_INPUT.rstrip("\n").split("\n"):
        print(f"  {term.dim(line)}")

    # (1) The regex approach, and its two simultaneous failure modes.
    print(f"\n{term.bold('(1) The usual approach: match the pattern')}")
    print(f"  {term.dim(f'sed -E s/{REDACT_REGEX}/***********/g')}")
    print()
    for line in REDACT_INPUT.rstrip("\n").split("\n"):
        print(f"    {re.sub(REDACT_REGEX, '*' * 11, line)}")
    print()
    print(f"  {term.red('✗')} order= was destroyed — an order id is also 11 digits")
    print(f"  {term.red('✗')} card= was missed — this one happens to be 10")
    print(term.dim("\n  One pattern, over-reaching and under-reaching at the same"))
    print(term.dim("  time, and neither is reported. The corruption surfaces later,"))
    print(term.dim("  in whatever consumes order=, far from the code that caused it."))

    # (2) Name the fields instead.
    print(f"\n{term.bold('(2) Name the fields instead')}")
    print(f"  {term.cyan('template')} {REDACT_TEMPLATE}")
    print(f"  {term.cyan('mapping')}  {json.dumps(REDACT_MAPPING, ensure_ascii=False)}")
    print()
    with Template(REDACT_TEMPLATE, target=REDACT_TEMPLATE,
                  mapping=REDACT_MAPPING) as t:
        redacted = t.transform_text(REDACT_INPUT)
    for line in redacted.rstrip("\n").split("\n"):
        print(f"    {term.green(line)}")
    print(term.dim("\n  mask(phone, 4) keeps the last 4 digits; mask(card) keeps none."))

    # (3) The template is the thing being trusted, so be precise about what
    #     verify does and does not prove. Law A holding does NOT mean the
    #     fields are split where you meant — a greedy hole can swallow its
    #     neighbour and still reproduce the line byte for byte.
    print(f"\n{term.bold('(3) But why trust the template?')}")
    with Template(REDACT_TEMPLATE) as t:
        rep = t.verify(REDACT_INPUT.encode())
        print(f"  {term.cyan('verify')}  {rep.total - rep.bad}/{rep.total} lines, "
              f"no ambiguity")
    print(term.dim("\n  verify catches ambiguity — templates where the engine's choice"))
    print(term.dim("  of split can flip as the data changes."))
    print(term.dim("\n  It does not prove the fields are delimited where you meant."))
    print(term.dim("  Drop card= from the template and the phone hole simply eats it,"))
    print(term.dim("  reproducing the line exactly, so Law A still holds:"))
    with Template(REDACT_LOOSE) as t:
        eaten = t.parse_records(REDACT_INPUT)[0]["phone"]
    print(f"    {term.yellow('phone')} = {eaten!r}")
    print(term.dim("\n  Masking that over-redacts rather than leaks — the safe"))
    print(term.dim("  direction — but it is still wrong. ztpl inspect shows the split."))

    # (4) The failure mode that costs data rather than privacy.
    print(f"\n{term.bold('(4) The one number you have to check')}")
    with Template(REDACT_TEMPLATE, target=REDACT_TEMPLATE,
                  mapping=REDACT_MAPPING) as t:
        res = t.transform((REDACT_INPUT + REDACT_ODD).encode())
    print(f"  {term.dim(REDACT_ODD.rstrip())}   {term.dim('<- extra input line')}")
    print(f"\n  matched {res.matched}/{res.total}, "
          f"{term.yellow('skipped ' + str(res.skipped))}")
    print(term.dim("\n  A line the template does not match is dropped, not passed"))
    print(term.dim("  through — so nothing leaks. But the line is gone. For a"))
    print(term.dim("  redaction run skipped must be 0, or you are losing data"))
    print(term.dim("  quietly, which is the failure this whole exercise is about."))

    # (5) The actual guarantee, verified rather than asserted.
    print(f"\n{term.bold('(5) The guarantee, checked by machine')}")
    with Template(REDACT_TEMPLATE) as t:
        before = t.parse_records(REDACT_INPUT)
        after = t.parse_records(redacted)
    kept = [k for k in before[0] if before[0][k] == after[0][k]]
    changed = [k for k in before[0] if before[0][k] != after[0][k]]
    for row_before, row_after in zip(before, after):
        assert [k for k in row_before if row_before[k] != row_after[k]] == changed
    print(f"  {term.green('unchanged')}  {', '.join(kept)}"
          f"{term.dim('   (byte for byte)')}")
    print(f"  {term.yellow('redacted')}   {', '.join(changed)}")
    print(term.dim("\n  Re-parsing the output and diffing field by field turns"))
    print(term.dim("  \"I did not break anything else\" into something a test can"))
    print(term.dim("  assert. That is the part sed cannot give you."))

    # (5) The escape hatch, with its price tag.
    print(f"\n{term.bold('(5) When the built-ins are not enough')}")
    print(term.dim("  Arbitrary logic goes in your own language — parse out,"))
    print(term.dim("  edit, format back:\n"))
    for line in (
        "with Template(tpl, target=tpl) as t:",
        "    rows = t.parse_records(text)",
        "    for r in rows:",
        "        r['phone'] = your_own_hash(r['phone'])   # any Python",
        "    out = t.format_records(rows)",
    ):
        print(f"    {term.cyan(line)}")
    print(term.dim("\n  Costs roughly 80x the single-pass path (~7 us/line vs ~90 ns)"))
    print(term.dim("  because the intermediate is materialized. Still ~130k lines/s,"))
    print(term.dim("  and the round-trip guarantee above holds either way."))

    print(f"\n{term.dim('next:')}")
    print(term.dim("  ztpl demo tour           the general walkthrough"))
    print(term.dim("  ztpl verify -s '...'     check your own template first"))
    return 0


# —— helpers ——


def _print_blocks(blocks, depth: int) -> None:
    if not blocks:
        return
    pad = "  " * depth
    for b in blocks:
        print(f"\n{pad}{term.cyan('block')} {term.bold(b['name'])} "
              f"{term.dim('separator=' + repr(b['sep']))}")
        for f in b.get("fields", []):
            print(f"{pad}    {f['name']:<16} {term.dim(f['kind'])}")
        _print_blocks(b.get("blocks"), depth + 1)


_TIER_DESC = {
    "T0/literal": "literal delimiters — fastest path (SIMD search)",
    "T1/regex": "has regex constraints — roughly 12x slower than literals",
    "T2/island": "has JSON islands",
    "T3/each": "has repeat blocks",
}


def _tier_desc(tier: str) -> str:
    return f"{tier}  {term.dim(_TIER_DESC.get(tier, ''))}"


def _bool_desc(v: bool) -> str:
    if v:
        return (f"{term.yellow('yes')}  "
                f"{term.dim('(only after the flat engine fails; matching lines pay nothing)')}")
    return term.green("no")


def _report(res, verb: str) -> None:
    """Summarize to stderr, keeping stdout clean for pipes.

    Skipped and failed are reported separately: "skipped" means the line did
    not match the template, "failed" means it should have worked but didn't.
    Conflating them leaves you guessing whether the data or the template is
    at fault.
    """
    if res.total == 0:
        term.warn("input is empty")
        return
    msg = f"{verb} {res.ok}/{res.total} line(s)"
    if res.skipped:
        msg += f", {res.skipped} skipped (no match)"
    if res.failed:
        msg += f", {res.failed} failed"
    term.note(msg)

    for e in res.errors:
        print(f"  {term.red('failed')}: {e}", file=sys.stderr)
    if res.failed > len(res.errors):
        term.note(f"  …and {res.failed - len(res.errors)} more failure(s)")
