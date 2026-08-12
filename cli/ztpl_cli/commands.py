"""Subcommand implementations. Each returns a process exit code."""

import json
import sys

from ztpl import Template

from . import term
from .sources import load_config, open_template, read_input

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


def cmd_parse(args) -> int:
    """Source text -> structured bindings."""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg) as tpl:
        res = tpl.parse(data)

    if args.ndjson:
        sys.stdout.buffer.write(res.output)
    else:
        for rec in res.records():
            print(term.pretty_json(rec))

    _report(res.matched, res.total, "parsed")
    return 0 if res.matched else 1


def cmd_convert(args) -> int:
    """Source text -> target text (the full pipeline)."""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg, need_target=True) as tpl:
        res = tpl.transform(data)
    sys.stdout.buffer.write(res.output)
    _report(res.matched, res.total, "converted")
    return 0 if res.matched else 1


def cmd_format(args) -> int:
    """NDJSON bindings -> target text."""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg, need_target=True) as tpl:
        res = tpl.format(data)
    sys.stdout.buffer.write(res.output)
    _report(res.rendered, res.total, "rendered")
    return 0 if res.rendered else 1


def cmd_verify(args) -> int:
    """Check round-trip law A and ambiguity, line by line."""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg) as tpl:
        rep = tpl.verify(data)

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
    if len(rep.problems) > args.limit:
        print(term.dim(
            f"  …and {len(rep.problems) - args.limit} more; use --limit to show more"))
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
    print(term.dim("  ztpl inspect -s '...'    see what a template compiles to"))
    print(term.dim("  ztpl verify  -s '...'    check your own template for ambiguity"))
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


def _report(ok: int, total: int, verb: str) -> None:
    if total == 0:
        term.warn("input is empty")
        return
    msg = f"{verb} {ok}/{total} line(s)"
    if ok < total:
        msg += f", {total - ok} skipped"
    term.note(msg)
