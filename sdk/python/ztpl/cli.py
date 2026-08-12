"""ztpl 命令行：把 stdin 按模板转换后写到 stdout。"""

import argparse
import json
import sys
from pathlib import Path

from .template import Template, ZtplError


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="ztpl",
        description="Z-Template：按源模板解析、按目标模板重新格式化。",
        epilog="示例：\n"
        "  ztpl -s '[${ts}] ${lv} ${msg}' -t '${lv}|${msg}' < app.log\n"
        "  ztpl -c pipeline.json < app.log",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    p.add_argument("-s", "--source", help="源模板")
    p.add_argument("-t", "--target", help="目标模板")
    p.add_argument("-m", "--mapping", help="字段映射，JSON 对象：{目标字段: 源字段}")
    p.add_argument("-c", "--config", type=Path, help="流水线配置文件（JSON）")
    p.add_argument("-i", "--input", type=Path, help="输入文件，默认读 stdin")
    p.add_argument("--stats", action="store_true", help="把匹配统计写到 stderr")
    return p


def _load_config(args) -> dict:
    if args.config:
        cfg = json.loads(args.config.read_text(encoding="utf-8"))
        missing = [k for k in ("source", "target") if k not in cfg]
        if missing:
            raise ZtplError(f"配置缺少字段: {', '.join(missing)}")
        return cfg
    if not args.source or not args.target:
        raise ZtplError("需要同时提供 --source 与 --target，或改用 --config")
    return {
        "source": args.source,
        "target": args.target,
        "mapping": json.loads(args.mapping) if args.mapping else {},
    }


def main(argv=None) -> int:
    args = _build_parser().parse_args(argv)
    try:
        cfg = _load_config(args)
        data = args.input.read_bytes() if args.input else sys.stdin.buffer.read()
        with Template(
            source=cfg["source"], target=cfg["target"],
            mapping=cfg.get("mapping"), shape=cfg.get("shape"),
        ) as tpl:
            res = tpl.transform(data)
    except ZtplError as exc:
        print(f"ztpl: {exc}", file=sys.stderr)
        return 1
    except (OSError, json.JSONDecodeError) as exc:
        print(f"ztpl: {exc}", file=sys.stderr)
        return 2

    sys.stdout.buffer.write(res.output)
    if args.stats:
        print(f"ztpl: {res.matched}/{res.total} 行匹配，跳过 {res.skipped} 行",
              file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
