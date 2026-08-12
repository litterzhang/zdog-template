"""Terminal helpers: colors, tables, JSON highlighting.

No third-party dependencies — the CLI is meant for quick hands-on use and
shouldn't make you install things first. Colors turn themselves off when
stdout isn't a TTY, so the output stays pipe-friendly.
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
    """Errors always go to stderr so stdout stays consumable by a pipe."""
    print(f"{red('error')}: {msg}", file=sys.stderr)


def warn(msg: str) -> None:
    print(f"{yellow('warning')}: {msg}", file=sys.stderr)


def note(msg: str) -> None:
    print(dim(msg), file=sys.stderr)


def pretty_json(obj, indent: int = 2) -> str:
    """JSON with light key highlighting."""
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
    """Two-column aligned table. Aligns by display width (CJK counts as 2)."""
    if not rows:
        return ""
    width = max(_display_width(k) for k, _ in rows)
    return "\n".join(
        f"{' ' * indent}{cyan(k)}{' ' * (width - _display_width(k))}  {v}"
        for k, v in rows
    )


def _display_width(s: str) -> int:
    # CJK and full-width punctuation take two cells; not aiming for full
    # Unicode correctness, just enough to line columns up.
    return sum(2 if ord(ch) > 0x2E80 else 1 for ch in s)
