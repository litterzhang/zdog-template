"""模板与输入的来源解析：命令行参数、配置文件、stdin。"""

import json
import sys
from pathlib import Path

from ztpl import Template, ZtplError


class UsageError(ZtplError):
    """用法错误 —— 与库层错误区分开，退出码不同。"""


def load_config(args) -> dict:
    """把命令行参数与 -c 配置文件合并成一份配置。

    命令行参数优先于配置文件，方便在配置基础上临时改一处试。
    """
    cfg: dict = {}
    if getattr(args, "config", None):
        path: Path = args.config
        try:
            cfg = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            raise UsageError(f"配置文件不存在: {path}") from None
        except json.JSONDecodeError as exc:
            raise UsageError(f"配置文件不是合法 JSON ({path}): {exc}") from None
        if not isinstance(cfg, dict):
            raise UsageError(f"配置文件顶层必须是对象: {path}")

    if getattr(args, "source", None):
        cfg["source"] = args.source
    if getattr(args, "target", None):
        cfg["target"] = args.target
    if getattr(args, "mapping", None):
        cfg["mapping"] = _json_arg(args.mapping, "--mapping")
    if getattr(args, "shape", None):
        cfg["shape"] = _json_arg(args.shape, "--shape")

    if not cfg.get("source"):
        raise UsageError("需要源模板：用 -s/--source 或在 -c 配置文件里给 source")
    return cfg


def _json_arg(raw: str, flag: str) -> dict:
    try:
        v = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise UsageError(f"{flag} 不是合法 JSON: {exc}") from None
    if not isinstance(v, dict):
        raise UsageError(f"{flag} 需要一个 JSON 对象")
    return v


def open_template(cfg: dict, *, need_target: bool = False) -> Template:
    if need_target and not cfg.get("target"):
        raise UsageError("该命令需要目标模板：用 -t/--target 或在配置里给 target")
    return Template(
        source=cfg["source"],
        target=cfg.get("target"),
        mapping=cfg.get("mapping"),
        shape=cfg.get("shape"),
    )


def read_input(args) -> bytes:
    """从 --input 文件或 stdin 读入。"""
    if getattr(args, "text", None):
        return args.text.encode("utf-8")
    if getattr(args, "input", None):
        path: Path = args.input
        try:
            return path.read_bytes()
        except FileNotFoundError:
            raise UsageError(f"输入文件不存在: {path}") from None
    if sys.stdin.isatty():
        raise UsageError(
            "没有输入。用 --text '…'、-i 文件，或从管道喂给它：\n"
            "  cat app.log | ztpl parse -s '[${ts}] ${lv} ${msg}'"
        )
    return sys.stdin.buffer.read()
