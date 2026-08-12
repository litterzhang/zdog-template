"""ztpl command line — a quick way to try Z-Template.

Built entirely on the zdog-template Python SDK: it never touches ctypes or
the .so. Anything the CLI cannot do means the SDK is missing something —
that is a deliberate constraint on the SDK's surface.
"""

from importlib.metadata import PackageNotFoundError, version

__all__ = ["main", "DIST_NAME", "__version__"]

DIST_NAME = "zdog-template-cli"

try:
    # 从包元数据读，而不是硬编码 —— 否则改包名/版本时这里必然忘记同步，
    # 而 `--version` 恰恰是最容易被忽略的角落。
    __version__ = version(DIST_NAME)
except PackageNotFoundError:  # 源码树里直接跑，没装过
    __version__ = "0.0.0+dev"

from .main import main
