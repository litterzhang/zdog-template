"""SDK 层单测：生命周期、错误处理、缓冲扩容。"""

import ctypes

import pytest

from ztpl import Template, ZtplCompileError, ZtplError, abi_version, lib_path


def test_abi_version():
    assert abi_version() == 3


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


# —— ABI v2 新增的能力面 ——

LOG = "[T1] ERROR disk full payload={\"host\":\"web-1\",\"pct\":95}"


def test_parse_returns_bindings():
    with Template("[${ts}] ${lv} ${msg} payload=${json|name=p}") as t:
        recs = t.parse_records(LOG)
        assert len(recs) == 1
        r = recs[0]
        assert r["ts"] == "T1"
        assert r["lv"] == "ERROR"
        assert r["msg"] == "disk full"
        # JSON 岛在 parse 时解码成真正的对象，而不是字符串
        assert r["p"] == {"host": "web-1", "pct": 95}


def test_parse_without_target():
    """只做 parse 时不需要目标模板。"""
    with Template("[${a}] ${b}") as t:
        assert not t.has_target
        assert t.parse_records("[x] y") == [{"a": "x", "b": "y"}]
        with pytest.raises(ZtplError, match="requires a target template"):
            t.transform(b"[x] y")
        with pytest.raises(ZtplError, match="requires a target template"):
            t.format(b"{}")


def test_parse_each_block():
    with Template("items=${each|name=xs,sep=;}${id}:${n}${end}") as t:
        recs = t.parse_records("items=a:1;b:2")
        assert recs[0]["xs"] == [{"id": "a", "n": "1"}, {"id": "b", "n": "2"}]


def test_parse_skips_non_matching():
    with Template("[${a}]") as t:
        res = t.parse(b"[x]\nnope\n[y]")
        assert res.matched == 2
        assert res.total == 3
        assert res.skipped == 1


def test_format_from_records():
    with Template("[${a}] ${b}", target="${b}/${a}") as t:
        out = t.format_records([{"a": "x", "b": "y"}, {"a": "p", "b": "q"}])
        assert out == "y/x\nq/p\n"   # 目标是 ${b}/${a}


def test_parse_format_round_trip():
    """定律 A 的 SDK 级体现：parse 出来再 format 回去应还原原文。"""
    tmpl = "[${ts}] ${lv} ${msg}"
    with Template(tmpl, target=tmpl) as t:
        src = "[T1] ERROR disk full"
        assert t.format_records(t.parse_records(src)).rstrip("\n") == src


def test_verify_ok():
    with Template("[${ts}] ${lv}") as t:
        rep = t.verify_text("[a] b\n[c] d")
        assert rep.ok
        assert rep.total == 2
        assert rep.bad == 0
        assert "passed" in str(rep)


def test_verify_detects_ambiguity():
    with Template("${a}.${b}!") as t:
        rep = t.verify_text("x.y.z!")
        assert not rep.ok
        assert rep.bad == 1
        assert any("ambiguous" in p for prob in rep.problems for p in prob["problems"])


def test_verify_detects_no_match():
    with Template("[${a}]") as t:
        rep = t.verify_text("nope")
        assert not rep.ok
        assert any("does not match" in p for prob in rep.problems for p in prob["problems"])


def test_inspect_reports_structure():
    with Template("[${ts}] ${lv} payload=${json|name=p}",
                  target="${ts}") as t:
        info = t.inspect()
        assert info["source"]["tier"] == "T2/island"
        # 末尾是岛而非 OpRest，"必须吃满输入"的终检本身就是可失败点
        assert info["source"]["backtrack"] is True
        names = [f["name"] for f in info["source"]["fields"]]
        assert names == ["ts", "lv", "p"]
        kinds = {f["name"]: f["kind"] for f in info["source"]["fields"]}
        assert kinds["p"] == "json-island"
        assert kinds["ts"] == "hole"
        assert "target" in info


def test_inspect_reports_blocks():
    with Template("${each|name=xs,sep=;}${id}:${n}${end}") as t:
        info = t.inspect()
        blocks = info["source"]["blocks"]
        assert blocks[0]["name"] == "xs"
        assert blocks[0]["sep"] == ";"
        assert [f["name"] for f in blocks[0]["fields"]] == ["id", "n"]


def test_inspect_reports_backtrack():
    with Template(r"${a}.${re|name=n,expr=\d+}") as t:
        assert t.inspect()["source"]["backtrack"] is True


def test_inspect_without_target():
    with Template("[${a}]") as t:
        assert "target" not in t.inspect()


# —— 出错的行不再静默跳过（ABI v3）——

def test_failed_is_distinct_from_skipped():
    """岛内容非法 -> failed；整行不匹配 -> skipped。两者必须分得开。"""
    with Template('[${a}] ${json|name=p}') as t:
        res = t.parse(b'[x] {"ok":1}\n[y] {"bad":}\nnot matching at all')
        assert res.ok == 1        # 第 1 行成功
        assert res.failed == 1    # 第 2 行括号配对但内容非法
        assert res.skipped == 1   # 第 3 行压根不匹配
        assert res.total == 3


def test_failed_lines_report_reasons():
    with Template('[${a}] ${json|name=p}') as t:
        res = t.parse(b'[y] {"bad":}')
        assert res.failed == 1
        assert res.errors, "出错的行必须给出原因"
        assert "not valid JSON" in res.errors[0]
        assert "line 1" in res.errors[0]


def test_format_reports_bad_input():
    with Template("[${a}] ${b}", target="${b}/${a}") as t:
        res = t.format(b'{"a":"x","b":"y"}\nthis is not json')
        assert res.ok == 1
        assert res.failed == 1
        assert any("not valid JSON" in e for e in res.errors)


def test_no_errors_when_all_good():
    with Template("[${a}]") as t:
        res = t.parse(b"[x]\n[y]")
        assert res.failed == 0
        assert res.errors == ()


# —— parse 不再把岛解码后再序列化回去 ——

def test_parse_preserves_island_verbatim():
    """岛原样嵌入：原文的数字写法与键序都不该被改写。"""
    raw = '{"z":1,"a":1.50,"big":10000000000000000000}'
    with Template("[${ts}] ${json|name=p}") as t:
        out = t.parse(f"[T1] {raw}".encode()).text()
    assert raw in out, f"岛应原样嵌入，实际: {out}"
