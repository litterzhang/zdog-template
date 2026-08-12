"""Template —— SDK 的唯一公开类型。"""

import ctypes
import json
from dataclasses import dataclass

from ._lib import (
    ABI_VERSION,
    E_HANDLE,
    E_SHORT_BUFFER,
    _STATS_LEN,
    ZtplCompileError,
    ZtplError,
    abi_version,
    lib_path,
    load,
)

__all__ = ["Template", "Result", "ZtplError", "ZtplCompileError", "abi_version", "lib_path"]


@dataclass(frozen=True)
class Result:
    """一次批量转换的结果。"""

    output: bytes
    matched: int
    total: int

    @property
    def skipped(self) -> int:
        """未匹配而被跳过的行数。"""
        return self.total - self.matched

    def text(self, encoding: str = "utf-8") -> str:
        return self.output.decode(encoding)


class Template:
    """一条已编译的转换流水线：源模板 -> 字段映射 -> 目标模板。

    一次编译、长期持有。句柄在 Go 侧无全局可变状态，可多线程并发调用；
    但同一个 Template 实例的输出缓冲不是线程安全的，多线程请各持一个实例。

    mapping 的值可以是裸字段名（零拷贝快路径），也可以是 JMESPath 子集表达式
    （`upper(lv)`、`p.host || 'unknown'`）。shape 为表达式产物提供序列化规则。

    示例::

        with Template(source="[${ts}] ${lv} ${msg}",
                      target="${level}|${text}",
                      mapping={"level": "upper(lv)", "text": "msg"}) as t:
            print(t.transform_text("[T1] error disk full"))
    """

    __slots__ = ("_lib", "_h", "_buf", "_stats", "_closed")

    def __init__(self, source: str, target: str, mapping: dict | None = None,
                 shape: dict | None = None, *, buffer_size: int = 1 << 16):
        if not isinstance(source, str) or not isinstance(target, str):
            raise TypeError("source 与 target 必须是 str")
        self._closed = True  # 先置位，__del__ 在构造失败时也能安全运行
        self._lib = load()
        cfg_obj = {
            "version": ABI_VERSION,
            "source": source,
            "target": target,
            "mapping": mapping or {},
        }
        if shape:
            # shape 为无 Raw 出处的字段（表达式产物）提供序列化规则。
            cfg_obj["shape"] = shape
        cfg = json.dumps(cfg_obj, ensure_ascii=False).encode("utf-8")

        h = self._lib.ZtplCompile(cfg, len(cfg))
        if h <= 0:
            msg = self._last_error(h) or f"编译失败 (code={h})"
            self._lib.ZtplRelease(h)
            raise ZtplCompileError(msg)

        self._h = h
        self._closed = False
        self._buf = ctypes.create_string_buffer(max(buffer_size, 1024))
        self._stats = (ctypes.c_int32 * _STATS_LEN)()

    # —— 生命周期 ——

    def close(self) -> None:
        """释放句柄。重复调用是安全的。"""
        if not self._closed:
            self._lib.ZtplRelease(self._h)
            self._closed = True

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False

    def __del__(self):
        try:
            self.close()
        except Exception:
            pass

    # —— 转换 ——

    def transform(self, data: bytes) -> Result:
        """批量转换。输入按 \\n 分行，不匹配的行被跳过。

        这是热路径上唯一的跨界调用 —— 整条流水线在 Go 侧完成，
        Python 只拿最终结果。
        """
        if self._closed:
            raise ZtplError("Template 已关闭")
        if isinstance(data, str):
            raise TypeError("transform 接受 bytes；字符串请用 transform_text")

        n = self._call(data)
        if n == E_SHORT_BUFFER:
            needed = self._stats[2]
            self._buf = ctypes.create_string_buffer(needed + 1024)
            n = self._call(data)
        if n < 0:
            raise ZtplError(self._last_error(self._h) or f"transform 失败 (code={n})")

        # 用 string_at 而非 self._buf.raw[:n]：.raw 会先把**整个**缓冲
        # 物化成 bytes 再切片，代价是 O(buffer_size) 而非 O(n)。
        # 缓冲通常远大于实际输出，这个差别在热路径上很可观。
        return Result(output=ctypes.string_at(self._buf, n),
                      matched=self._stats[0], total=self._stats[1])

    def transform_text(self, text: str, encoding: str = "utf-8") -> str:
        """transform 的字符串便利版本。"""
        return self.transform(text.encode(encoding)).text(encoding)

    # —— 内部 ——

    def _call(self, data: bytes) -> int:
        # 注意：必须先调用再取 .raw —— `self._buf.raw[:self._call(...)]` 会先
        # 求值 .raw（拷贝调用前的空缓冲），是个很隐蔽的坑。
        return self._lib.ZtplTransform(
            self._h, data, len(data), self._buf, len(self._buf), self._stats
        )

    def _last_error(self, h: int) -> str:
        buf = ctypes.create_string_buffer(2048)
        n = self._lib.ZtplLastError(h, buf, len(buf))
        if n == E_HANDLE or n == 0:
            return ""
        if n < 0:  # 缓冲不足，n 为负的所需长度
            buf = ctypes.create_string_buffer(-n)
            n = self._lib.ZtplLastError(h, buf, len(buf))
            if n <= 0:
                return ""
        return buf.raw[:n].decode("utf-8", "replace")

    def __repr__(self):
        state = "closed" if self._closed else f"handle={self._h}"
        return f"<ztpl.Template {state}>"
