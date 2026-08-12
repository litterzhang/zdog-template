#!/usr/bin/env python3
"""用 Python SDK 跑与 Go 完全相同的 conformance 用例。

这是多语言 SDK 的核心机制：语义的唯一真源是 conformance/cases/*.json，
不是任何一个实现。两侧跑同一套用例通过，才算语义一致。
"""

import json
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO / "sdk" / "python"))

from ztpl import Template, ZtplCompileError, abi_version  # noqa: E402

CASES = REPO / "conformance" / "cases"


def run_case(path: Path) -> tuple[bool, str]:
    case = json.loads(path.read_text(encoding="utf-8"))
    cfg = case["config"]
    name = case["name"]

    try:
        tpl = Template(
            source=cfg["source"],
            target=cfg["target"],
            mapping=cfg.get("mapping"),
        )
    except ZtplCompileError as exc:
        want = case.get("compile_error")
        if not want:
            return False, f"意外的编译失败: {exc}"
        if want not in str(exc):
            return False, f"错误信息 {str(exc)!r} 不含 {want!r}"
        return True, "编译失败符合预期"

    with tpl:
        if case.get("compile_error"):
            return False, f"期望编译失败（含 {case['compile_error']!r}），却成功了"

        res = tpl.transform(case["input"].encode("utf-8"))
        got = res.output.decode("utf-8")
        if got != case["output"]:
            return False, f"输出不符\n      want: {case['output']!r}\n      got:  {got!r}"
        if "matched" in case and res.matched != case["matched"]:
            return False, f"matched = {res.matched}, want {case['matched']}"
        if "total" in case and res.total != case["total"]:
            return False, f"total = {res.total}, want {case['total']}"
    return True, f"{res.matched}/{res.total} 行"


def main() -> int:
    print(f"ABI version: {abi_version()}")
    paths = sorted(CASES.glob("*.json"))
    if not paths:
        print(f"没有找到用例: {CASES}", file=sys.stderr)
        return 2

    failures = 0
    for p in paths:
        ok, detail = run_case(p)
        mark = "PASS" if ok else "FAIL"
        print(f"  [{mark}] {p.stem:<34} {detail}")
        if not ok:
            failures += 1

    print(f"\n{len(paths) - failures}/{len(paths)} 用例通过")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
