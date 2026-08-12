"""终端输出辅助：颜色、表格、JSON 高亮。

无第三方依赖 —— CLI 是给人快速上手用的，不该先让人装一堆东西。
非 TTY（管道、重定向）时自动关闭颜色，保证输出可被下游程序消费。
"""

import json
import os
import sys

_ENABLED = (
    sys.stdout.isatty()
    and os.environ.get("TERM") not in (None, "dumb")
    and not os.environ.get("NO_COLOR")
)


def _c(code: str):
    def wrap(s: str) -> str:
        return f"\033[{code}m{s}\033[0m" if _ENABLED else s

    return wrap


bold = _c("1")
dim = _c("2")
red = _c("31")
green = _c("32")
yellow = _c("33")
blue = _c("34")
magenta = _c("35")
cyan = _c("36")


def err(msg: str) -> None:
    """错误信息一律走 stderr，不污染可被管道消费的 stdout。"""
    print(f"{red('错误')}: {msg}", file=sys.stderr)


def warn(msg: str) -> None:
    print(f"{yellow('警告')}: {msg}", file=sys.stderr)


def note(msg: str) -> None:
    print(dim(msg), file=sys.stderr)


def kv(key: str, value, indent: int = 0) -> str:
    return f"{' ' * indent}{cyan(key)}: {value}"


def pretty_json(obj, indent: int = 2) -> str:
    """带轻量高亮的 JSON。"""
    text = json.dumps(obj, ensure_ascii=False, indent=indent)
    if not _ENABLED:
        return text
    out = []
    for line in text.split("\n"):
        stripped = line.lstrip()
        if ":" in stripped and stripped.startswith('"'):
            pad = line[: len(line) - len(stripped)]
            k, _, v = stripped.partition(":")
            out.append(f"{pad}{cyan(k)}:{v}")
        else:
            out.append(line)
    return "\n".join(out)


def table(rows: list[tuple[str, str]], indent: int = 2) -> str:
    """两列对齐表格。按显示宽度对齐（中文算两格）。"""
    if not rows:
        return ""
    width = max(_display_width(k) for k, _ in rows)
    return "\n".join(
        f"{' ' * indent}{cyan(k)}{' ' * (width - _display_width(k))}  {v}"
        for k, v in rows
    )


def _display_width(s: str) -> int:
    # CJK 与全角标点占两格；不追求 Unicode 完备，够对齐即可
    return sum(2 if ord(ch) > 0x2E80 else 1 for ch in s)
