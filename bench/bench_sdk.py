#!/usr/bin/env python3
"""性能门禁：Python SDK vs 纯 Python 实现。

门禁（见 docs/DESIGN.md §11）：
  1. 端到端 ≥ 10 M行/秒（Go 侧）
  2. Python SDK 相对纯 Python 加速 ≥ 5x
  3. 宿主绑定层损耗 ≤ 10%（由 Go 基准与本脚本对比得出）
"""

import re
import sys
import timeit
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / "sdk" / "python"))

from ztpl import Template  # noqa: E402

LINE = '[2026-08-12T01:00:00Z] ERROR disk full payload={"host":"web-1","pct":95}'
NLINES = 5000
BUF_TEXT = "\n".join([LINE] * NLINES)
BUF = BUF_TEXT.encode()

# 纯 Python 参考实现：这是 Python 能做到的最快写法
# （单条编译正则 + finditer + f-string，见 DESIGN.md §7）
PY_RE = re.compile(
    r"\[(?P<ts>[^\]\n]*)\] (?P<lv>[^ \n]*) (?P<msg>.*?) payload=(?P<p>.*)"
)


def pure_python():
    return "\n".join(
        f"{m['lv']}|{m['ts']}|{m['msg']}" for m in PY_RE.finditer(BUF_TEXT)
    )


def bench(label, fn, reps):
    fn()
    total = timeit.timeit(fn, number=reps)
    ns_per_line = total / reps / NLINES * 1e9
    mlines = 1000 / ns_per_line
    print(f"  {label:<38} {ns_per_line:8.2f} ns/行   {mlines:6.2f} M行/秒")
    return ns_per_line


def main() -> int:
    tpl = Template(
        source="[${ts}] ${lv} ${msg} payload=${payload}",
        target="${level}|${time}|${text}",
        mapping={"level": "lv", "time": "ts", "text": "msg"},
        buffer_size=len(BUF),
    )

    # 正确性先于性能：两侧结果必须逐行一致
    got = tpl.transform(BUF).text().rstrip("\n").split("\n")
    want = pure_python().split("\n")
    if got != want:
        print(f"结果不一致！\n  got[0]={got[0]!r}\n  want[0]={want[0]!r}", file=sys.stderr)
        return 2
    print(f"正确性: {len(got)} 行与纯 Python 参考实现逐行一致  ✓\n")

    print(f"=== 批量 {NLINES} 行, 每行均摊 ===")
    py = bench("[纯 Python] finditer + f-string", pure_python, 200)
    sdk = bench("[Python SDK -> Go .so] transform", lambda: tpl.transform(BUF), 200)
    tpl.close()

    speedup = py / sdk
    print(f"\n  Go core 相对纯 Python 加速: {speedup:.1f}x")

    gates = [
        ("端到端 ≥ 10 M行/秒", 1000 / sdk >= 10.0, f"{1000 / sdk:.2f} M行/秒"),
        ("SDK 相对纯 Python ≥ 5x", speedup >= 5.0, f"{speedup:.1f}x"),
    ]
    print("\n=== 门禁 ===")
    failed = 0
    for name, ok, actual in gates:
        print(f"  [{'PASS' if ok else 'FAIL'}] {name:<28} 实测 {actual}")
        failed += not ok
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
