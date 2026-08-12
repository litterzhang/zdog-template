"""共享 fixture。"""

import json
import os
from pathlib import Path

import pytest


def _find_repo_root() -> Path:
    """从本文件向上找到仓库根（含 conformance/cases 的那一层）。"""
    for parent in Path(__file__).resolve().parents:
        if (parent / "conformance" / "cases").is_dir():
            return parent
    raise RuntimeError("找不到仓库根：期望某级父目录下有 conformance/cases")


REPO_ROOT = _find_repo_root()


@pytest.fixture(scope="session", autouse=True)
def _ensure_lib():
    """确保能定位到共享库；没构建就跳过整个会话并给出可操作提示。"""
    if os.environ.get("ZTPL_LIB"):
        return
    for name in ("libztpl.so", "libztpl.dylib", "ztpl.dll"):
        if (REPO_ROOT / "cshared" / name).is_file():
            return
        if (REPO_ROOT / "sdk" / "python" / "ztpl" / name).is_file():
            return
    pytest.skip(
        "未找到 libztpl 共享库，请先运行：make build",
        allow_module_level=True,
    )


@pytest.fixture(scope="session")
def repo_root() -> Path:
    return REPO_ROOT


def load_cases():
    """读取全部 conformance 用例，供参数化使用。"""
    paths = sorted((REPO_ROOT / "conformance" / "cases").glob("*.json"))
    return [(p.stem, json.loads(p.read_text(encoding="utf-8"))) for p in paths]
