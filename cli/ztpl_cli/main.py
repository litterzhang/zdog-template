"""ztpl 命令行入口。"""

import argparse
import sys

from ztpl import ZtplCompileError, ZtplError

from . import commands, term
from .sources import UsageError

EPILOG = """\
示例:
  # 解析成结构化数据
  ztpl parse -s '[${ts}] ${lv} ${msg}' --text '[T1] ERROR disk full'

  # 完整转换（源模板 -> 字段映射 -> 目标模板）
  cat app.log | ztpl convert \\
      -s '[${ts}] ${lv} ${msg}' \\
      -t '${level}|${text}' \\
      -m '{"level":"upper(lv)","text":"msg"}'

  # 校验模板是否完整覆盖源文、是否有歧义
  ztpl verify -s '[${ts}] ${lv}' -i app.log

  # 看模板编译成了什么
  ztpl inspect -s '[${ts}] ${lv} payload=${json|name=p}'

  # 不带任何参数跑一个完整例子
  ztpl demo

模板语法:
  ${name}                       洞，由后继字面量定界（最快）
  ${re|name=n,expr=\\d+}         正则洞，默认最短匹配
  ${json|name=p}                JSON 结构化岛，自定界
  ${each|name=xs,sep=';'}…${end} 重复块

映射表达式（JMESPath 子集）:
  ts                            裸字段名 —— 零拷贝快路径
  upper(lv)                     函数
  p.host || 'unknown'           路径 + 回退
"""


def _add_template_args(p: argparse.ArgumentParser, *, target: bool = True) -> None:
    g = p.add_argument_group("模板")
    g.add_argument("-s", "--source", help="源模板")
    if target:
        g.add_argument("-t", "--target", help="目标模板")
    g.add_argument("-m", "--mapping", metavar="JSON",
                   help='字段映射，如 \'{"level":"upper(lv)"}\'')
    g.add_argument("--shape", metavar="JSON",
                   help='字段序列化规则，如 \'{"pct":{"type":"number","format":"%%.2f"}}\'')
    g.add_argument("-c", "--config", type=_path, metavar="FILE",
                   help="配置文件（JSON）；命令行参数优先")


def _add_input_args(p: argparse.ArgumentParser) -> None:
    g = p.add_argument_group("输入")
    g.add_argument("-i", "--input", type=_path, metavar="FILE", help="输入文件，默认读 stdin")
    g.add_argument("--text", metavar="STR", help="直接给一段文本（方便快速试）")


def _path(s: str):
    from pathlib import Path

    return Path(s)


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="ztpl",
        description="Z-Template —— 一份模板，两个方向：解析与格式化。",
        epilog=EPILOG,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    p.add_argument("-V", "--version", action="store_true", help="显示版本与 ABI 信息")
    sub = p.add_subparsers(dest="command", metavar="命令")

    sp = sub.add_parser("parse", help="源文本 -> 结构化绑定",
                        description="按源模板解析，输出每行的字段绑定。JSON 岛会被解码。")
    _add_template_args(sp, target=False)
    _add_input_args(sp)
    sp.add_argument("--ndjson", action="store_true", help="输出紧凑 NDJSON（便于管道）")
    sp.set_defaults(func=commands.cmd_parse)

    sc = sub.add_parser("convert", help="源文本 -> 目标文本",
                        description="完整流水线：解析、字段映射、按目标模板渲染。")
    _add_template_args(sc)
    _add_input_args(sc)
    sc.set_defaults(func=commands.cmd_convert)

    sf = sub.add_parser("format", help="NDJSON 绑定 -> 目标文本",
                        description="读 NDJSON（每行一个对象），按目标模板渲染。")
    _add_template_args(sf)
    _add_input_args(sf)
    sf.set_defaults(func=commands.cmd_format)

    sv = sub.add_parser("verify", help="校验 round-trip 定律与歧义",
                        description="逐行校验：模板是否完整覆盖源文（定律 A）、是否只有唯一解。")
    _add_template_args(sv, target=False)
    _add_input_args(sv)
    sv.add_argument("--limit", type=int, default=10, help="最多展示多少条问题（默认 10）")
    sv.set_defaults(func=commands.cmd_verify)

    si = sub.add_parser("inspect", help="展示模板编译结果",
                        description="展示执行层级、是否需要回溯、字段与重复块结构。")
    _add_template_args(si)
    si.add_argument("--json", action="store_true", help="输出原始 JSON")
    si.set_defaults(func=commands.cmd_inspect)

    sd = sub.add_parser("demo", help="跑一个自带的完整例子",
                        description="不需要任何输入，演示 parse / verify / convert。")
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
        term.err(f"模板编译失败\n  {exc}")
        return 2
    except ZtplError as exc:
        term.err(str(exc))
        return 3
    except BrokenPipeError:
        return 0  # 下游 head 之类提前退出，属正常
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    sys.exit(main())
