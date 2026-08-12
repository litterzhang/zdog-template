"""ztpl command line — a quick way to try Z-Template.

Built entirely on the ztpl Python SDK: it never touches ctypes or the .so.
Anything the CLI cannot do means the SDK is missing something — that is a
deliberate constraint on the SDK's surface.
"""

__all__ = ["main"]
__version__ = "0.1.0"

from .main import main
