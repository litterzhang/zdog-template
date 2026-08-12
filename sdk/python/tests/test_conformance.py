"""跨语言一致性用例 —— 与 Go 侧跑的是同一份 conformance/cases/*.json。

这是多语言 SDK 的核心机制：语义的唯一真源是用例，不是任何一个实现。
"""

import pytest

from ztpl import Template, ZtplCompileError

from conftest import load_cases

CASES = load_cases()


@pytest.mark.parametrize("name,case", CASES, ids=[n for n, _ in CASES])
def test_conformance(name, case):
    cfg = case["config"]

    if case.get("compile_error"):
        with pytest.raises(ZtplCompileError) as exc:
            Template(
                source=cfg["source"], target=cfg["target"],
                mapping=cfg.get("mapping"), shape=cfg.get("shape"),
            ).close()
        assert case["compile_error"] in str(exc.value), (
            f"错误信息 {str(exc.value)!r} 不含期望子串 {case['compile_error']!r}"
        )
        return

    with Template(
        source=cfg["source"], target=cfg["target"],
        mapping=cfg.get("mapping"), shape=cfg.get("shape"),
    ) as tpl:
        res = tpl.transform(case["input"].encode("utf-8"))
        assert res.output.decode("utf-8") == case["output"]
        if "matched" in case:
            assert res.matched == case["matched"]
        if "total" in case:
            assert res.total == case["total"]


def test_every_case_is_documented():
    """用例是跨语言的语义真源；没有说明的用例在别的语言里出错时无从判断
    是实现错还是用例错。"""
    for name, case in CASES:
        assert len(case.get("description", "")) >= 10, f"用例 {name} 缺少充分说明"


def test_cases_are_not_empty():
    assert len(CASES) > 0, "没有加载到任何 conformance 用例"
