"""SDK 层单测：生命周期、错误处理、缓冲扩容。"""

import ctypes

import pytest

from ztpl import Template, ZtplCompileError, ZtplError, abi_version, lib_path


def test_abi_version():
    assert abi_version() == 1


def test_lib_path_exists():
    assert lib_path().is_file()


def test_basic_transform():
    with Template(source="[${a}] ${b}", target="${b}/${a}") as t:
        assert t.transform_text("[x] y") == "y/x\n"


def test_mapping_reorder():
    with Template(
        source="[${ts}] ${lv} ${msg}",
        target="${level}|${text}",
        mapping={"level": "lv", "text": "msg"},
    ) as t:
        assert t.transform_text("[T1] ERROR boom") == "ERROR|boom\n"


def test_batch_skips_non_matching():
    with Template(source="[${a}] ${b}", target="${a}-${b}") as t:
        res = t.transform(b"[1] x\nnope\n[2] y")
        assert res.output == b"1-x\n2-y\n"
        assert (res.matched, res.total) == (2, 3)
        assert res.skipped == 1


def test_each_block():
    with Template(
        source="items=${each|name=xs,sep=;}${id}:${n}${end}",
        target="${each|name=xs,sep=','}${id}x${n}${end}",
    ) as t:
        assert t.transform_text("items=a:1;b:2") == "ax1,bx2\n"


def test_unicode_roundtrip():
    with Template(source="前 ${a} 后", target="[${a}]") as t:
        assert t.transform_text("前 中国人 后") == "[中国人]\n"


def test_compile_error_is_actionable():
    with pytest.raises(ZtplCompileError) as exc:
        Template(source="${a}${b}", target="${a}")
    assert "ambiguous" in str(exc.value)


def test_unknown_target_field():
    with pytest.raises(ZtplCompileError) as exc:
        Template(source="[${a}]", target="${nope}")
    msg = str(exc.value)
    assert "unknown source field" in msg
    assert "available" in msg  # 必须告诉用户有哪些字段可用


def test_transform_rejects_str():
    with Template(source="[${a}]", target="${a}") as t:
        with pytest.raises(TypeError):
            t.transform("[x]")


def test_closed_template_raises():
    t = Template(source="[${a}]", target="${a}")
    t.close()
    t.close()  # 幂等
    with pytest.raises(ZtplError):
        t.transform(b"[x]")


def test_buffer_grows_automatically():
    """初始缓冲故意开得极小，必须靠 grow-retry 协议自动扩容。"""
    with Template(source="[${a}]", target="${a}", buffer_size=1024) as t:
        big = "\n".join(f"[{'x' * 200}]" for _ in range(500)).encode()
        res = t.transform(big)
        assert res.matched == 500
        assert len(res.output) > 100_000


def test_output_is_exact_length():
    """回归：早期用 buf.raw[:n] 会先物化整个缓冲再切片，
    代价 O(buffer_size) 而非 O(n)。"""
    with Template(source="[${a}]", target="${a}", buffer_size=1 << 20) as t:
        res = t.transform(b"[hi]")
        assert res.output == b"hi\n"
        assert len(res.output) == 3


def test_context_manager_releases():
    t = Template(source="[${a}]", target="${a}")
    with t:
        pass
    with pytest.raises(ZtplError):
        t.transform(b"[x]")


def test_repr():
    t = Template(source="[${a}]", target="${a}")
    assert "handle=" in repr(t)
    t.close()
    assert "closed" in repr(t)


def test_concurrent_templates_are_independent():
    a = Template(source="[${x}]", target="A${x}")
    b = Template(source="[${x}]", target="B${x}")
    with a, b:
        assert a.transform_text("[1]") == "A1\n"
        assert b.transform_text("[2]") == "B2\n"
        assert a.transform_text("[3]") == "A3\n"


def test_ctypes_string_at_is_used(monkeypatch):
    """确保 transform 走的是 string_at 而不是 .raw 切片。"""
    calls = []
    orig = ctypes.string_at

    def spy(*args, **kwargs):
        calls.append(args)
        return orig(*args, **kwargs)

    monkeypatch.setattr(ctypes, "string_at", spy)
    with Template(source="[${a}]", target="${a}") as t:
        t.transform(b"[x]")
    assert calls, "transform 应当用 ctypes.string_at 精确拷贝 n 字节"
