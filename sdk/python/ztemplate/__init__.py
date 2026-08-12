"""zdog-template —— Z-Template 的 Python SDK。

它是一层薄壳：加载 libztpl.so、校验 ABI 版本、编译配置拿句柄、批量 transform。
整条流水线（parse -> mapping -> format）都在 Go 侧完成 —— 这不是性能优化而是
架构约束：实测把字段逐个跨界回 Python 会让加速比从 12x 掉到 1.09x（等于白写）。
详见 docs/DESIGN.md §7 否决记录 2。
"""

from importlib.metadata import PackageNotFoundError, version

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
    "DIST_NAME",
    "__version__",
]

DIST_NAME = "zdog-template"

try:
    # 从包元数据读，而不是硬编码。发版要改的地方越少越好 —— 这里写死过一次
    # 0.1.0，改包名时没跟着动，__version__ 和 pyproject 就已经能对不上了。
    __version__ = version(DIST_NAME)
except PackageNotFoundError:  # 源码树里直接跑，没装过
    __version__ = "0.0.0+dev"
