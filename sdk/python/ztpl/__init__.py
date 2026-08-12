"""ztpl —— Z-Template 的 Python SDK。

它是一层薄壳：加载 libztpl.so、校验 ABI 版本、编译配置拿句柄、批量 transform。
整条流水线（parse -> mapping -> format）都在 Go 侧完成 —— 这不是性能优化而是
架构约束：实测把字段逐个跨界回 Python 会让加速比从 12x 掉到 1.09x（等于白写）。
详见 docs/DESIGN.md §7 否决记录 2。
"""

from .template import (
    Result,
    Template,
    VerifyReport,
    ZtplCompileError,
    ZtplError,
    abi_version,
    lib_path,
)

__all__ = [
    "Template",
    "Result",
    "VerifyReport",
    "ZtplError",
    "ZtplCompileError",
    "abi_version",
    "lib_path",
]
__version__ = "0.1.0"
