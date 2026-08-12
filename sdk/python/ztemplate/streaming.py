"""按行边界分块的流式处理。

批量 ABI 一次吃一个缓冲区，所以流式的本质是**分块**。唯一的难点是
块边界必须落在行边界上 —— 把一行切成两半，两半都不匹配模板。

内存因此有界：峰值 ≈ chunk_size + 最长一行，与输入总大小无关。
"""

import re
from dataclasses import dataclass

DEFAULT_CHUNK = 1 << 20  # 1 MiB
# 单行超过这个长度就报错。正常文本里不会有这么长的"行"，
# 多半是二进制文件或分隔符搞错了 —— 与其把内存吃光，不如早点说清楚。
DEFAULT_MAX_LINE = 64 << 20

_LINE_PREFIX = re.compile(r"^line (\d+): ")


@dataclass
class StreamTotals:
    """跨块累加的统计。"""

    ok: int = 0
    total: int = 0
    failed: int = 0
    errors: list = None

    def __post_init__(self):
        if self.errors is None:
            self.errors = []

    def absorb(self, res, line_offset: int, error_cap: int) -> None:
        self.ok += res.ok
        self.total += res.total
        self.failed += res.failed
        room = error_cap - len(self.errors)
        if room > 0:
            self.errors.extend(
                shift_line_number(e, line_offset) for e in res.errors[:room]
            )


def shift_line_number(msg: str, offset: int) -> str:
    """把块内行号换算成全局行号。

    Go 侧把诊断格式化成 ``line N: …``，N 是**本块内**的序号。分块之后
    这个号对不上整个输入，所以在这里加上偏移。

    这里依赖了 Go 侧的错误前缀格式 —— 是刻意的耦合，换来的是不必为了
    行号再往 ABI 里塞一个参数。诊断字符串不在数据通路上，值得。
    """
    if not offset:
        return msg
    return _LINE_PREFIX.sub(lambda m: f"line {int(m.group(1)) + offset}: ", msg, count=1)


def iter_line_aligned(src, chunk_size: int = DEFAULT_CHUNK,
                      max_line: int = DEFAULT_MAX_LINE):
    """把二进制流切成**以行边界对齐**的块。

    产出的每一块都不含末尾换行，且保证是若干完整行。
    """
    carry = b""
    while True:
        chunk = src.read(chunk_size)
        if not chunk:
            if carry:
                yield carry
            return

        buf = carry + chunk if carry else chunk
        cut = buf.rfind(b"\n")
        if cut < 0:
            # 整块里一个换行都没有 —— 继续攒，但别无限攒下去
            if len(buf) > max_line:
                raise ValueError(
                    f"single line exceeds {max_line} bytes without a newline; "
                    "is this really line-oriented text?"
                )
            carry = buf
            continue

        yield buf[:cut]
        carry = buf[cut + 1:]
