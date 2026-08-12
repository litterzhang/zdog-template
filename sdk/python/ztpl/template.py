"""Template —— SDK 的唯一公开类型。"""

import ctypes
import json
from dataclasses import dataclass, field

from .streaming import DEFAULT_CHUNK, DEFAULT_MAX_LINE, StreamTotals, iter_line_aligned
from ._lib import (
    CONFIG_VERSION,
    E_HANDLE,
    E_SHORT_BUFFER,
    _STATS_LEN,
    ZtplCompileError,
    ZtplError,
    abi_version,
    lib_path,
    load,
)

__all__ = [
    "Template", "Result", "VerifyReport",
    "ZtplError", "ZtplCompileError", "abi_version", "lib_path",
]


@dataclass(frozen=True)
class Result:
    """一次批量操作的结果。

    ``failed`` 与 ``skipped`` 是**两回事**：前者是"这行本该处理却出错了"
    （岛不是合法 JSON、渲染失败……），后者是"这行本来就不匹配模板"。
    ``errors`` 给出前若干条出错原因。
    """

    output: bytes
    ok: int
    total: int
    failed: int = 0
    errors: tuple = ()

    @property
    def matched(self) -> int:
        return self.ok

    @property
    def rendered(self) -> int:
        return self.ok

    @property
    def skipped(self) -> int:
        """既没成功也没出错 —— 就是不匹配模板。"""
        return self.total - self.ok - self.failed

    def text(self, encoding: str = "utf-8") -> str:
        return self.output.decode(encoding)

    def records(self) -> list:
        """把 NDJSON 输出解析成对象列表（供 parse / verify 使用）。"""
        return [json.loads(line) for line in self.output.splitlines() if line.strip()]


@dataclass(frozen=True)
class VerifyReport:
    """一次校验的汇总。"""

    total: int
    bad: int
    problems: list = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return self.bad == 0

    def __str__(self) -> str:
        if self.ok:
            return f"all {self.total} line(s) passed (law A + unambiguous)"
        return f"{self.bad}/{self.total} line(s) have problems"


class Template:
    """一条已编译的模板流水线。

    一次编译、长期持有。句柄在 Go 侧无全局可变状态，可多线程并发调用；
    但同一个实例的输出缓冲不是线程安全的，多线程请各持一个实例。

    .. note::
       输出缓冲按需增长且**不回收**。处理过一次超大输入后，实例会一直占着
       那块内存 —— 用 :meth:`transform_stream` 之类的流式接口即可避免
       （缓冲只按 chunk 大小增长），或者用完 ``close()`` 重建。

    ``target`` 可省略 —— 只做 :meth:`parse` / :meth:`verify` / :meth:`inspect`
    时不需要目标模板。

    ``mapping`` 的值可以是裸字段名（零拷贝快路径），也可以是 JMESPath 子集
    表达式（``upper(lv)``、``p.host || 'unknown'``）。``shape`` 为表达式产物
    提供序列化规则。

    示例::

        with Template("[${ts}] ${lv} ${msg}") as t:
            print(t.parse_records("[T1] ERROR disk full"))

        with Template("[${ts}] ${lv} ${msg}",
                      target="${level}|${text}",
                      mapping={"level": "upper(lv)", "text": "msg"}) as t:
            print(t.transform_text("[T1] error disk full"))
    """

    __slots__ = ("_lib", "_h", "_buf", "_stats", "_closed", "_has_target")

    def __init__(self, source: str, target: str | None = None,
                 mapping: dict | None = None, shape: dict | None = None,
                 *, buffer_size: int = 1 << 16):
        if not isinstance(source, str):
            raise TypeError("source must be a str")
        if target is not None and not isinstance(target, str):
            raise TypeError("target must be a str or None")
        self._closed = True  # 先置位，__del__ 在构造失败时也能安全运行
        self._lib = load()
        self._has_target = bool(target)

        cfg_obj: dict = {"version": CONFIG_VERSION, "source": source}
        if target:
            cfg_obj["target"] = target
        if mapping:
            cfg_obj["mapping"] = mapping
        if shape:
            cfg_obj["shape"] = shape
        cfg = json.dumps(cfg_obj, ensure_ascii=False).encode("utf-8")

        h = self._lib.ZtplCompile(cfg, len(cfg))
        if h <= 0:
            msg = self._last_error(h) or f"compilation failed (code={h})"
            self._lib.ZtplRelease(h)
            raise ZtplCompileError(msg)

        self._h = h
        self._closed = False
        self._buf = ctypes.create_string_buffer(max(buffer_size, 1024))
        self._stats = (ctypes.c_int32 * _STATS_LEN)()

    # —— 生命周期 ——

    @property
    def has_target(self) -> bool:
        return self._has_target

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

    # —— 四个批量操作 ——

    def transform(self, data: bytes) -> Result:
        """源文本 -> 目标文本。整条流水线在 Go 侧完成，是热路径上唯一的跨界调用。"""
        self._need_target("transform")
        return self._batch(self._lib.ZtplTransform, data)

    def parse(self, data: bytes) -> Result:
        """源文本 -> NDJSON 绑定（每行一个对象）。JSON 岛在此处解码。"""
        return self._batch(self._lib.ZtplParse, data)

    def format(self, data: bytes) -> Result:
        """NDJSON 绑定 -> 目标文本。"""
        self._need_target("format")
        return self._batch(self._lib.ZtplFormat, data)

    def verify(self, data: bytes) -> VerifyReport:
        """逐行校验 round-trip 定律 A 与歧义。"""
        res = self._batch(self._lib.ZtplVerify, data)
        problems = [r for r in res.records() if not r.get("ok", True)]
        return VerifyReport(total=res.total, bad=res.failed, problems=problems)

    # —— 便利包装 ——

    def transform_text(self, text: str, encoding: str = "utf-8") -> str:
        return self.transform(text.encode(encoding)).text(encoding)

    def parse_records(self, text: str, encoding: str = "utf-8") -> list:
        """解析并直接返回对象列表。"""
        return self.parse(text.encode(encoding)).records()

    def format_records(self, records: list, encoding: str = "utf-8") -> str:
        """把对象列表渲染成目标文本。"""
        nd = "\n".join(json.dumps(r, ensure_ascii=False) for r in records)
        return self.format(nd.encode(encoding)).text(encoding)

    def verify_text(self, text: str, encoding: str = "utf-8") -> VerifyReport:
        return self.verify(text.encode(encoding))

    # —— 流式：内存有界，与输入总大小无关 ——

    def transform_stream(self, src, dst, **kw) -> Result:
        """源文本 -> 目标文本，边读边写。"""
        self._need_target("transform")
        return self._stream(self._lib.ZtplTransform, src, dst, **kw)

    def parse_stream(self, src, dst, **kw) -> Result:
        """源文本 -> NDJSON 绑定，边读边写。"""
        return self._stream(self._lib.ZtplParse, src, dst, **kw)

    def format_stream(self, src, dst, **kw) -> Result:
        """NDJSON 绑定 -> 目标文本，边读边写。"""
        self._need_target("format")
        return self._stream(self._lib.ZtplFormat, src, dst, **kw)

    def verify_stream(self, src, *, limit: int = 100, **kw) -> VerifyReport:
        """逐行校验，只保留前 limit 条问题。"""
        problems: list = []
        totals = StreamTotals()

        def collect(res, offset):
            for rec in res.records():
                if rec.get("ok", True) or len(problems) >= limit:
                    continue
                rec["line"] = rec.get("line", 0) + offset
                problems.append(rec)

        res = self._stream(self._lib.ZtplVerify, src, None, _on_chunk=collect,
                           _totals=totals, **kw)
        return VerifyReport(total=res.total, bad=res.failed, problems=problems)

    def _stream(self, fn, src, dst, *, chunk_size: int = DEFAULT_CHUNK,
                max_line: int = DEFAULT_MAX_LINE, error_cap: int = 100,
                _on_chunk=None, _totals: StreamTotals = None) -> Result:
        """按行边界分块处理。

        内存峰值 ≈ chunk_size + 最长一行，**与输入总大小无关**。
        分块之后每块的行号是块内序号，这里换算成全局行号。
        """
        if self._closed:
            raise ZtplError("Template is closed")
        totals = _totals or StreamTotals()

        for chunk in iter_line_aligned(src, chunk_size, max_line):
            offset = totals.total
            res = self._batch(fn, chunk)
            totals.absorb(res, offset, error_cap)
            if _on_chunk is not None:
                _on_chunk(res, offset)
            if dst is not None and res.output:
                dst.write(res.output)

        return Result(output=b"", ok=totals.ok, total=totals.total,
                      failed=totals.failed, errors=tuple(totals.errors))

    # —— 内省 ——

    def inspect(self) -> dict:
        """返回模板结构：执行层级、是否需要回溯、字段与重复块。"""
        if self._closed:
            raise ZtplError("Template is closed")
        buf = ctypes.create_string_buffer(8192)
        n = self._lib.ZtplInspect(self._h, buf, len(buf))
        if n < 0 and n > E_HANDLE:  # 负的所需长度
            buf = ctypes.create_string_buffer(-n)
            n = self._lib.ZtplInspect(self._h, buf, len(buf))
        if n < 0:
            raise ZtplError(self._last_error(self._h) or f"inspect failed (code={n})")
        return json.loads(ctypes.string_at(buf, n))

    # —— 内部 ——

    def _need_target(self, op: str) -> None:
        if not self._has_target:
            raise ZtplError(f"{op}() requires a target template; pass target= when constructing Template")

    def _batch(self, fn, data: bytes) -> Result:
        if self._closed:
            raise ZtplError("Template is closed")
        if isinstance(data, str):
            raise TypeError(
                "batch methods take bytes; use the *_text / *_records helpers for str"
            )

        n = self._call(fn, data)
        if n == E_SHORT_BUFFER:
            self._buf = ctypes.create_string_buffer(self._stats[2] + 1024)
            n = self._call(fn, data)
        if n < 0:
            raise ZtplError(self._last_error(self._h) or f"call failed (code={n})")

        failed = self._stats[3]
        errors: tuple = ()
        if failed:
            # 出错的行不再无声无息 —— 原因挂在句柄上，这里取回来。
            msg = self._last_error(self._h)
            errors = tuple(msg.split("\n")) if msg else ()

        # 用 string_at 而非 self._buf.raw[:n]：.raw 会先把**整个**缓冲
        # 物化成 bytes 再切片，代价是 O(buffer_size) 而非 O(n)。
        return Result(output=ctypes.string_at(self._buf, n),
                      ok=self._stats[0], total=self._stats[1],
                      failed=failed, errors=errors)

    def _call(self, fn, data: bytes) -> int:
        return fn(self._h, data, len(data), self._buf, len(self._buf), self._stats)

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
        return f"<ztpl.Template {state} target={self._has_target}>"
