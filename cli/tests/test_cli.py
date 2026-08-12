"""CLI end-to-end tests — call main() directly to cover real arg parsing and exit codes."""

import json

import pytest

from ztpl_cli.main import main

LOG = '[T1] error disk full payload={"host":"web-1","pct":95}'
SRC = r"[${ts}] ${re|name=lv,expr=\w+} ${msg} payload=${json|name=p}"


def run(capsys, *argv) -> tuple[int, str, str]:
    code = main(list(argv))
    cap = capsys.readouterr()
    return code, cap.out, cap.err


def test_demo_runs(capsys):
    code, out, _ = run(capsys, "demo")
    assert code == 0
    assert "Z-Template" in out
    assert "ambiguous" in out     # the demo must surface what verify catches
    assert "ERROR host=web-1" in out


def test_parse(capsys):
    code, out, err = run(capsys, "parse", "-s", SRC, "--text", LOG)
    assert code == 0
    assert '"web-1"' in out
    assert "1/1" in err           # stats go to stderr, keeping stdout clean


def test_parse_ndjson_is_pipeable(capsys):
    code, out, _ = run(capsys, "parse", "-s", SRC, "--text", LOG, "--ndjson")
    assert code == 0
    rec = json.loads(out.strip())
    assert rec["lv"] == "error"
    assert rec["p"]["host"] == "web-1"


def test_convert(capsys):
    code, out, _ = run(
        capsys, "convert", "-s", SRC, "-t", "${level}|${host}",
        "-m", json.dumps({"level": "upper(lv)", "host": "p.host || 'unknown'"}),
        "--text", LOG,
    )
    assert code == 0
    assert out.strip() == "ERROR|web-1"


def test_convert_requires_target(capsys):
    code, _, err = run(capsys, "convert", "-s", SRC, "--text", LOG)
    assert code == 2
    assert "needs a target template" in err


def test_format(capsys):
    nd = json.dumps({"a": "x", "b": "y"})
    code, out, _ = run(
        capsys, "format", "-s", "[${a}] ${b}", "-t", "${b}/${a}", "--text", nd
    )
    assert code == 0
    assert out.strip() == "y/x"


def test_verify_ok(capsys):
    code, out, _ = run(capsys, "verify", "-s", SRC, "--text", LOG)
    assert code == 0
    assert "passed" in out


def test_verify_detects_ambiguity(capsys):
    code, out, _ = run(
        capsys, "verify", "-s", "[${ts}] ${lv} ${msg}", "--text", "[T1] a b c"
    )
    assert code == 1
    assert "ambiguous" in out


def test_verify_limit(capsys):
    text = "\n".join(f"[T{i}] a b c" for i in range(20))
    code, out, _ = run(
        capsys, "verify", "-s", "[${ts}] ${lv} ${msg}", "--text", text, "--limit", "3"
    )
    assert code == 1
    assert "and 17 more" in out


def test_inspect(capsys):
    code, out, _ = run(capsys, "inspect", "-s", SRC)
    assert code == 0
    assert "T2/island" in out
    assert "json island" in out


def test_inspect_json(capsys):
    code, out, _ = run(capsys, "inspect", "-s", "[${a}] ${b}", "--json")
    assert code == 0
    info = json.loads(out)
    assert info["source"]["tier"] == "T0/literal"


def test_inspect_shows_blocks(capsys):
    code, out, _ = run(
        capsys, "inspect", "-s", "${each|name=xs,sep=;}${id}:${n}${end}"
    )
    assert code == 0
    assert "block" in out and "xs" in out


def test_config_file(capsys, tmp_path):
    cfg = tmp_path / "p.json"
    cfg.write_text(json.dumps({
        "source": "[${a}] ${b}", "target": "${b}-${a}",
    }), encoding="utf-8")
    code, out, _ = run(capsys, "convert", "-c", str(cfg), "--text", "[x] y")
    assert code == 0
    assert out.strip() == "y-x"


def test_cli_args_override_config(capsys, tmp_path):
    cfg = tmp_path / "p.json"
    cfg.write_text(json.dumps({
        "source": "[${a}] ${b}", "target": "FROM-CONFIG",
    }), encoding="utf-8")
    code, out, _ = run(
        capsys, "convert", "-c", str(cfg), "-t", "${a}!", "--text", "[x] y"
    )
    assert code == 0
    assert out.strip() == "x!"


def test_missing_source(capsys):
    code, _, err = run(capsys, "parse", "--text", "x")
    assert code == 2
    assert "source template is required" in err


def test_bad_template_is_actionable(capsys):
    code, _, err = run(capsys, "parse", "-s", "${a}${b}", "--text", "x")
    assert code == 2
    assert "failed to compile" in err
    assert "ambiguous" in err


def test_bad_mapping_json(capsys):
    code, _, err = run(capsys, "parse", "-s", "[${a}]", "-m", "notjson", "--text", "[x]")
    assert code == 2
    assert "not valid JSON" in err


def test_missing_config_file(capsys):
    code, _, err = run(capsys, "parse", "-c", "/nonexistent.json", "--text", "x")
    assert code == 2
    assert "config file not found" in err


def test_no_match_exit_code(capsys):
    code, _, _ = run(capsys, "parse", "-s", "[${a}]", "--text", "nope")
    assert code == 1


def test_input_file(capsys, tmp_path):
    f = tmp_path / "in.log"
    f.write_text(LOG, encoding="utf-8")
    code, out, _ = run(capsys, "parse", "-s", SRC, "-i", str(f), "--ndjson")
    assert code == 0
    assert json.loads(out.strip())["lv"] == "error"


def test_version(capsys):
    code, out, _ = run(capsys, "--version")
    assert code == 0
    assert "ztpl-cli" in out
    assert "ABI v3" in out


def test_help_without_command(capsys):
    code, out, _ = run(capsys)
    assert code == 0
    assert "COMMAND" in out


@pytest.mark.parametrize("cmd", ["parse", "convert", "format", "verify", "inspect", "demo"])
def test_every_command_has_help(capsys, cmd):
    with pytest.raises(SystemExit) as exc:
        main([cmd, "--help"])
    assert exc.value.code == 0
