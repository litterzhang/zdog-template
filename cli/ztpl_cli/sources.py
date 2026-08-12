"""Resolving templates and input from flags, config files, and stdin."""

import json
import sys
from pathlib import Path

from ztpl import Template, ZtplError


class UsageError(ZtplError):
    """A usage problem — kept distinct from library errors so the exit code differs."""


def load_config(args) -> dict:
    """Merge command-line flags with a -c config file.

    Flags win over the file, so you can keep a config around and override one
    thing on the fly.
    """
    cfg: dict = {}
    if getattr(args, "config", None):
        path: Path = args.config
        try:
            cfg = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            raise UsageError(f"config file not found: {path}") from None
        except json.JSONDecodeError as exc:
            raise UsageError(f"config file is not valid JSON ({path}): {exc}") from None
        if not isinstance(cfg, dict):
            raise UsageError(f"config file must contain a JSON object: {path}")

    if getattr(args, "source", None):
        cfg["source"] = args.source
    if getattr(args, "target", None):
        cfg["target"] = args.target
    if getattr(args, "mapping", None):
        cfg["mapping"] = _json_arg(args.mapping, "--mapping")
    if getattr(args, "shape", None):
        cfg["shape"] = _json_arg(args.shape, "--shape")

    if not cfg.get("source"):
        raise UsageError(
            "a source template is required: pass -s/--source or set "
            '"source" in the -c config file'
        )
    return cfg


def _json_arg(raw: str, flag: str) -> dict:
    try:
        v = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise UsageError(f"{flag} is not valid JSON: {exc}") from None
    if not isinstance(v, dict):
        raise UsageError(f"{flag} must be a JSON object")
    return v


def open_template(cfg: dict, *, need_target: bool = False) -> Template:
    if need_target and not cfg.get("target"):
        raise UsageError(
            "this command needs a target template: pass -t/--target or set "
            '"target" in the config'
        )
    return Template(
        source=cfg["source"],
        target=cfg.get("target"),
        mapping=cfg.get("mapping"),
        shape=cfg.get("shape"),
    )


def read_input(args) -> bytes:
    """Read from --text, --input, or stdin."""
    if getattr(args, "text", None):
        return args.text.encode("utf-8")
    if getattr(args, "input", None):
        path: Path = args.input
        try:
            return path.read_bytes()
        except FileNotFoundError:
            raise UsageError(f"input file not found: {path}") from None
    if sys.stdin.isatty():
        raise UsageError(
            "no input. Use --text '…', -i FILE, or pipe it in:\n"
            "  cat app.log | ztpl parse -s '[${ts}] ${lv} ${msg}'"
        )
    return sys.stdin.buffer.read()
