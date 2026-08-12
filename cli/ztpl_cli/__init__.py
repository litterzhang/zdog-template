"""ztpl 命令行 —— 快速体验 Z-Template。

完全建立在 ztpl Python SDK 之上：不碰 ctypes、不碰 .so。
CLI 做不到的事就说明 SDK 缺东西 —— 这是对 SDK 能力面的一道约束。
"""

__all__ = ["main"]
__version__ = "0.1.0"

from .main import main
