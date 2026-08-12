"""与 libztpl.so 的 ctypes 绑定。

为什么用 ctypes 而不是 cffi：实测批量调用下 FFI 边界成本摊到每行是
0.0003 µs，完全不是瓶颈；纯 C 与 Python 跑同一个 .so 只差 7.8%。
ctypes 是标准库自带、零依赖，因此没有理由引入额外的编译期依赖。
"""

import ctypes
import os
import sys
from pathlib import Path

# 两个版本号是**不同**的契约，必须分开：
#   ABI_VERSION    —— C 函数签名的契约，加函数就要升
#   CONFIG_VERSION —— 配置 JSON 的 schema，只有不兼容变更才升
# （target 由必填变可选属于向后兼容，因此 CONFIG_VERSION 保持 1）
ABI_VERSION = 2
CONFIG_VERSION = 1

# 返回码，与 cshared/abi.go 保持一致
E_SHORT_BUFFER = -1
E_HANDLE = -2
E_CONFIG = -3
E_ARG = -4

_STATS_LEN = 3  # [matched, total, needed]

_SO_NAMES = {
    "linux": "libztpl.so",
    "darwin": "libztpl.dylib",
    "win32": "ztpl.dll",
}


class ZtplError(Exception):
    """Base error for the ztpl SDK."""


class ZtplCompileError(ZtplError):
    """The template, mapping, or shape configuration failed to compile."""


def _candidates():
    """按优先级产出 .so 的候选路径。"""
    if env := os.environ.get("ZTPL_LIB"):
        yield Path(env)
    name = _SO_NAMES.get(sys.platform, "libztpl.so")
    here = Path(__file__).resolve().parent
    yield here / name                      # 打包进 wheel 时
    yield here.parents[2] / "cshared" / name   # 仓库内开发时


def lib_path() -> Path:
    """定位 libztpl.so，找不到时给出可操作的报错。"""
    tried = []
    for p in _candidates():
        if p.is_file():
            return p
        tried.append(str(p))
    raise ZtplError(
        "libztpl shared library not found. Build it first:\n"
        "  go build -buildmode=c-shared -o cshared/libztpl.so ./cshared/\n"
        "or point ZTPL_LIB at an existing one.\nTried:\n  " + "\n  ".join(tried)
    )


# 四个批量入口的签名完全相同 —— 这是刻意的：宿主 SDK 因此能共用一套包装。
_BATCH_SIG = (
    ctypes.c_int32,
    [ctypes.c_int64, ctypes.c_char_p, ctypes.c_int32,
     ctypes.c_char_p, ctypes.c_int32, ctypes.POINTER(ctypes.c_int32)],
)

_SIGNATURES = {
    "ZtplAbiVersion": (ctypes.c_int32, []),
    "ZtplCompile": (ctypes.c_int64, [ctypes.c_char_p, ctypes.c_int32]),
    "ZtplTransform": _BATCH_SIG,
    "ZtplParse": _BATCH_SIG,
    "ZtplFormat": _BATCH_SIG,
    "ZtplVerify": _BATCH_SIG,
    "ZtplInspect": (ctypes.c_int32, [ctypes.c_int64, ctypes.c_char_p, ctypes.c_int32]),
    "ZtplLastError": (ctypes.c_int32, [ctypes.c_int64, ctypes.c_char_p, ctypes.c_int32]),
    "ZtplRelease": (None, [ctypes.c_int64]),
}

_lib = None


def load():
    """加载并缓存共享库，校验 ABI 版本。"""
    global _lib
    if _lib is not None:
        return _lib
    path = lib_path()
    try:
        lib = ctypes.CDLL(str(path))
    except OSError as exc:
        raise ZtplError(f"failed to load {path}: {exc}") from exc
    for name, (restype, argtypes) in _SIGNATURES.items():
        try:
            fn = getattr(lib, name)
        except AttributeError as exc:
            raise ZtplError(
                f"{path} is missing symbol {name} — likely a version mismatch"
            ) from exc
        fn.restype, fn.argtypes = restype, argtypes
    got = lib.ZtplAbiVersion()
    if got != ABI_VERSION:
        raise ZtplError(f"ABI version mismatch: library={got}, SDK={ABI_VERSION}")
    _lib = lib
    return lib


def abi_version() -> int:
    """返回共享库报告的 ABI 版本。"""
    return load().ZtplAbiVersion()
