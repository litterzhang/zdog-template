"""各子命令的实现。每个函数返回进程退出码。"""

import json
import sys

from ztpl import Template

from . import term
from .sources import load_config, open_template, read_input

# 供 `ztpl demo` 使用的示例。
# NAIVE 是刻意留了歧义的写法：${lv} 无约束，既可以是 "error" 也可以是
# "error disk" —— demo 用它来展示 verify 能抓到什么。
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
    """源文本 -> 结构化绑定。"""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg) as tpl:
        res = tpl.parse(data)

    if args.ndjson:
        sys.stdout.buffer.write(res.output)
    else:
        for rec in res.records():
            print(term.pretty_json(rec))

    _report(res.matched, res.total, "解析")
    return 0 if res.matched else 1


def cmd_convert(args) -> int:
    """源文本 -> 目标文本（完整流水线）。"""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg, need_target=True) as tpl:
        res = tpl.transform(data)
    sys.stdout.buffer.write(res.output)
    _report(res.matched, res.total, "转换")
    return 0 if res.matched else 1


def cmd_format(args) -> int:
    """NDJSON 绑定 -> 目标文本。"""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg, need_target=True) as tpl:
        res = tpl.format(data)
    sys.stdout.buffer.write(res.output)
    _report(res.rendered, res.total, "渲染")
    return 0 if res.rendered else 1


def cmd_verify(args) -> int:
    """逐行校验 round-trip 定律 A 与歧义。"""
    cfg = load_config(args)
    data = read_input(args)
    with open_template(cfg) as tpl:
        rep = tpl.verify(data)

    if rep.ok:
        print(term.green(f"✓ {rep.total} 行全部通过"))
        print(term.dim("  定律 A: 模板完整覆盖源文，format(parse(t)) == t"))
        print(term.dim("  无歧义: 每行只有唯一解"))
        return 0

    print(term.red(f"✗ {rep.bad}/{rep.total} 行有问题\n"))
    for item in rep.problems[: args.limit]:
        print(f"  {term.bold('第 ' + str(item['line']) + ' 行')}: {item.get('input', '')}")
        for p in item.get("problems", []):
            print(f"    {term.yellow('•')} {p}")
        print()
    if len(rep.problems) > args.limit:
        print(term.dim(f"  …还有 {len(rep.problems) - args.limit} 行，用 --limit 调整"))
    return 1


def cmd_inspect(args) -> int:
    """展示模板结构：执行层级、是否回溯、字段与重复块。"""
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
        label = "源模板" if role == "source" else "目标模板"
        print(term.bold(f"{label}"))
        print(term.table([
            ("模板", d["template"]),
            ("执行层级", _tier_desc(d["tier"])),
            ("需要回溯", _bool_desc(d["backtrack"])),
        ]))
        if d.get("fields"):
            print(f"\n  {term.cyan('字段')}")
            for f in d["fields"]:
                kind = term.magenta("json 岛") if f["kind"] == "json-island" else term.dim("洞")
                print(f"    {f['name']:<16} {kind}")
        _print_blocks(d.get("blocks"), 1)
        print()
    return 0


def cmd_demo(args) -> int:
    """跑一个自带的完整例子，不需要任何输入。"""
    data = DEMO_INPUT.encode()

    print(term.bold("Z-Template 快速体验\n"))
    print(term.cyan("输入"))
    for line in DEMO_INPUT.rstrip("\n").split("\n"):
        print(f"  {term.dim(line)}")

    # ① 先用一个"看起来没问题"的模板
    print(f"\n{term.bold('① 一个看起来没问题的模板')}")
    print(f"  {DEMO_NAIVE}")
    with Template(DEMO_NAIVE) as t:
        rep = t.verify(data)
        print(f"\n  {term.cyan('verify')} 说：")
        if rep.bad:
            for item in rep.problems[:2]:
                for p in item.get("problems", []):
                    print(f"    {term.yellow('•')} 第 {item['line']} 行 —— {p}")
    print(term.dim("\n  ${lv} 没有约束，既可以是 \"error\" 也可以是 \"error disk\" ——"))
    print(term.dim("  引擎只能挑一个，换份数据就可能挑到另一个。这是模板的 bug。"))

    # ② 加上约束
    print(f"\n{term.bold('② 给它加个约束')}")
    print(f"  {DEMO_FIXED}")
    with Template(DEMO_FIXED) as t:
        rep = t.verify(data)
        good = rep.total - rep.bad
        print(f"\n  {term.green('✓')} {good}/{rep.total} 行通过"
              f"{term.dim('（第 3 行本就不该匹配）') if rep.bad else ''}")

        print(f"\n  {term.cyan('parse')} —— JSON 岛已解码成真正的对象")
        for rec in t.parse(data).records():
            print("    " + json.dumps(rec, ensure_ascii=False))

    # ③ 完整流水线
    print(f"\n{term.bold('③ 完整转换')}")
    print(f"  {term.cyan('映射')}   {json.dumps(DEMO_MAPPING, ensure_ascii=False)}")
    print(f"  {term.cyan('目标')}   {DEMO_TARGET}")
    with Template(DEMO_FIXED, target=DEMO_TARGET, mapping=DEMO_MAPPING) as t:
        res = t.transform(data)
        print()
        for line in res.text().rstrip("\n").split("\n"):
            print(f"    {term.green(line)}")
        print(term.dim(f"\n  {res.matched}/{res.total} 行匹配，跳过 {res.skipped} 行"))

    print(f"\n{term.dim('下一步：')}")
    print(term.dim("  ztpl inspect -s '...'    看模板编译成了什么"))
    print(term.dim("  ztpl verify  -s '...'    校验自己的模板有没有歧义"))
    return 0


# —— 辅助 ——


def _print_blocks(blocks, depth: int) -> None:
    if not blocks:
        return
    pad = "  " * depth
    for b in blocks:
        print(f"\n{pad}{term.cyan('重复块')} {term.bold(b['name'])} "
              f"{term.dim('分隔符=' + repr(b['sep']))}")
        for f in b.get("fields", []):
            print(f"{pad}    {f['name']:<16} {term.dim(f['kind'])}")
        _print_blocks(b.get("blocks"), depth + 1)


_TIER_DESC = {
    "T0/literal": "字面量定界 —— 最快路径（SIMD 搜索）",
    "T1/regex": "含正则约束 —— 比字面量慢约 12 倍",
    "T2/island": "含 JSON 结构化岛",
    "T3/each": "含重复块",
}


def _tier_desc(tier: str) -> str:
    return f"{tier}  {term.dim(_TIER_DESC.get(tier, ''))}"


def _bool_desc(v: bool) -> str:
    if v:
        return f"{term.yellow('是')}  {term.dim('（仅在扁平引擎失败后启用，匹配的行不多付代价）')}"
    return term.green("否")


def _report(ok: int, total: int, verb: str) -> None:
    if total == 0:
        term.warn("输入为空")
        return
    msg = f"{verb} {ok}/{total} 行"
    if ok < total:
        msg += f"，跳过 {total - ok} 行"
    term.note(msg)
