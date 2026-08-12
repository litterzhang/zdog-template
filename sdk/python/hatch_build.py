"""Hatchling 构建钩子：把 wheel 标成平台相关、但与解释器版本无关。

两件事：

1. 包里带 `libztpl.so` / `.dylib` / `.dll`，**不是**纯 Python 包。默认标签
   `py3-none-any` 会让 PyPI 认为它在哪都能装 —— macOS 用户装到 Linux 的库，
   import 就炸。

2. 但它也**不绑定解释器版本**：库是用 `ctypes` 加载的，不碰 CPython 的
   C-API/ABI。所以标签该是 `py3-none-<平台>`，而不是 hatchling 默认推断出的
   `cp313-cp313-<平台>` —— 后者会让同一个 wheel 在 3.12 上装不了。

`ZTPL_WHEEL_TAG` 可以覆盖平台部分。发布 Linux 包时必须用它指定
manylinux 标签（PyPI 直接拒绝 `linux_x86_64`），例如：

    ZTPL_WHEEL_TAG=py3-none-manylinux_2_28_x86_64 uv build --wheel
"""

import os
import sysconfig

from hatchling.builders.hooks.plugin.interface import BuildHookInterface


def _platform_tag() -> str:
    """当前平台的 wheel 标签片段，如 linux_x86_64 / macosx_11_0_arm64。"""
    return sysconfig.get_platform().replace("-", "_").replace(".", "_")


class CustomBuildHook(BuildHookInterface):
    def initialize(self, version, build_data):
        build_data["pure_python"] = False
        build_data["tag"] = os.environ.get(
            "ZTPL_WHEEL_TAG", f"py3-none-{_platform_tag()}"
        )
