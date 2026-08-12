"""ztpl 命令行入口。"""

import argparse
import sys

from ztpl import ZtplCompileError, ZtplError

from . import commands, term
from .sources import UsageError

EPILOG = """\
examples:
  # parse into structured records
  ztpl parse -s '[${ts}] ${lv} ${msg}' --text '[T1] ERROR disk full'

  # full conversion (source template -> field mapping -> target template)
  cat app.log | ztpl convert \\
      -s '[${ts}] ${lv} ${msg}' \\
      -t '${level}|${text}' \\
      -m '{"level":"upper(lv)","text":"msg"}'

  # check that the template covers the source fully and has no ambiguity
  ztpl verify -s '[${ts}] ${lv}' -i app.log

  # see what the template compiles to
  ztpl inspect -s '[${ts}] ${lv} payload=${json|name=p}'

  # run a self-contained example, no arguments needed
  ztpl demo

template syntax:
  ${name}                        hole, delimited by the literal that follows (fastest)
  ${re|name=n,expr=\\d+}          regex hole, shortest match by default
  ${json|name=p}                 JSON island, self-delimiting
  ${each|name=xs,sep=';'}…${end}  repeat block

mapping expressions (a JMESPath subset):
  ts                             bare field name — zero-copy fast path
  upper(lv)                      function call
  p.host || 'unknown'            path with a fallback
"""


def _add_template_args(p: argparse.ArgumentParser, *, target: bool = True) -> None:
    g = p.add_argument_group("template")
    g.add_argument("-s", "--source", help="source template")
    if target:
        g.add_argument("-t", "--target", help="target template")
    g.add_argument("-m", "--mapping", metavar="JSON",
                   help='field mapping, e.g. \'{"level":"upper(lv)"}\'')
    g.add_argument("--shape", metavar="JSON",
                   help='serialization rules, e.g. \'{"pct":{"type":"number","format":"%%.2f"}}\'')
    g.add_argument("-c", "--config", type=_path, metavar="FILE",
                   help="config file (JSON); command-line flags take precedence")


def _add_input_args(p: argparse.ArgumentParser) -> None:
    g = p.add_argument_group("input")
    g.add_argument("-i", "--input", type=_path, metavar="FILE", help="input file; reads stdin by default")
    g.add_argument("--text", metavar="STR", help="inline text (handy for quick tries)")


def _path(s: str):
    from pathlib import Path

    return Path(s)


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="ztpl",
        description="Z-Template — one template, both directions: parse and format.",
        epilog=EPILOG,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    p.add_argument("-V", "--version", action="store_true", help="show version and ABI info")
    sub = p.add_subparsers(dest="command", metavar="COMMAND")

    sp = sub.add_parser("parse", help="source text -> structured bindings",
                        description="Parse with the source template and print each "
                                    "line's field bindings. JSON islands are decoded.")
    _add_template_args(sp, target=False)
    _add_input_args(sp)
    sp.add_argument("--ndjson", action="store_true", help="emit compact NDJSON (pipe-friendly)")
    sp.set_defaults(func=commands.cmd_parse)

    sc = sub.add_parser("convert", help="source text -> target text",
                        description="The full pipeline: parse, map fields, render "
                                    "with the target template.")
    _add_template_args(sc)
    _add_input_args(sc)
    sc.set_defaults(func=commands.cmd_convert)

    sf = sub.add_parser("format", help="NDJSON bindings -> target text",
                        description="Read NDJSON (one object per line) and render it "
                                    "with the target template.")
    _add_template_args(sf)
    _add_input_args(sf)
    sf.set_defaults(func=commands.cmd_format)

    sv = sub.add_parser("verify", help="check round-trip laws and ambiguity",
                        description="Per line: does the template cover the source fully "
                                    "(law A), and is there exactly one parse?")
    _add_template_args(sv, target=False)
    _add_input_args(sv)
    sv.add_argument("--limit", type=int, default=10, help="max problems to show (default 10)")
    sv.set_defaults(func=commands.cmd_verify)

    si = sub.add_parser("inspect", help="show what a template compiles to",
                        description="Show the execution tier, whether backtracking is "
                                    "needed, and the field / block structure.")
    _add_template_args(si)
    si.add_argument("--json", action="store_true", help="emit raw JSON")
    si.set_defaults(func=commands.cmd_inspect)

    sd = sub.add_parser("demo", help="run a self-contained example",
                        description="No input needed; walks through parse / verify / convert.")
    sd.set_defaults(func=commands.cmd_demo)

    return p


def main(argv=None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.version:
        from ztpl import abi_version

        from . import __version__

        print(f"ztpl-cli {__version__}")
        try:
            print(f"libztpl ABI v{abi_version()}")
        except ZtplError as exc:
            term.warn(str(exc))
            return 3
        return 0

    if not getattr(args, "func", None):
        parser.print_help()
        return 0

    try:
        return args.func(args)
    except UsageError as exc:
        term.err(str(exc))
        return 2
    except ZtplCompileError as exc:
        term.err(f"template failed to compile\n  {exc}")
        return 2
    except ZtplError as exc:
        term.err(str(exc))
        return 3
    except BrokenPipeError:
        return 0  # downstream `head` etc. exited early; that is normal
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    sys.exit(main())
