#!/usr/bin/env python3
"""Unified Website Engine test runner with machine-readable evidence.

The runner only executes declared gates, never falls back to a live API/database,
and keeps blocked infrastructure checks distinct from failed assertions.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
CONFIG_PATH = ROOT / "tests/qa/gates.json"
LEDGER_PATHS = (
    ROOT / "tests/qa/website-scenarios.json",
    ROOT / "tests/qa/core-scenarios.json",
    ROOT / "tests/qa/e2e-scenarios.json",
)
DEFAULT_REPORT = ROOT / "output/test-report.json"
LOG_DIR = ROOT / "output/test-logs"
SCENARIO_RESULT = re.compile(
    r"^\s*--- (PASS|FAIL|SKIP): (Test[A-Za-z0-9_]+)/([A-Z][A-Z0-9]*-[0-9]{3})",
    re.MULTILINE,
)
ERROR_HINT = re.compile(
    r"(--- FAIL:|^\s*FAIL\b|panic:|\berror:|Error:|✖|ELIFECYCLE|No files matching|"
    r"cannot |failed with exit code|exit status| assertions? failed|missing prerequisite)",
    re.IGNORECASE,
)


def load_json(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise SystemExit(f"cannot load {path.relative_to(ROOT)}: {error}") from error
    if not isinstance(payload, dict):
        raise SystemExit(f"{path.relative_to(ROOT)} must contain a JSON object")
    return payload


def git_state() -> dict[str, Any]:
    def git(*args: str) -> str:
        result = subprocess.run(["git", *args], cwd=ROOT, capture_output=True, text=True, check=False)
        return result.stdout.strip() if result.returncode == 0 else ""

    head = git("rev-parse", "HEAD")
    dirty = bool(git("status", "--porcelain"))
    return {"head": head, "dirty": dirty, "branch": git("rev-parse", "--abbrev-ref", "HEAD")}


def workspace_modules() -> list[dict[str, str]]:
    result = subprocess.run(
        ["go", "list", "-m", "-f", "{{.Dir}}|{{.Path}}"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise SystemExit(f"cannot enumerate go.work modules: {result.stderr.strip()}")
    modules: list[dict[str, str]] = []
    for line in result.stdout.splitlines():
        if not line.strip():
            continue
        directory, module_path = line.split("|", 1)
        modules.append({
            "id": module_path,
            "path": module_path,
            "dir": str(Path(directory).relative_to(ROOT)),
        })
    return modules


def build_gate_list(config: dict[str, Any], args: argparse.Namespace) -> list[dict[str, Any]]:
    gates: list[dict[str, Any]] = []
    for gate in config.get("gates", []):
        gates.append(dict(gate))
    for module in workspace_modules():
        for check in config.get("go_checks", []):
            gates.append({
                "id": f"{check['id']}/{module['path']}",
                "label": f"{check['id']} in {module['dir']}",
                "command": check["command"],
                "cwd": module["dir"],
            })

    selected: list[dict[str, Any]] = []
    for gate in gates:
        gate_id = str(gate["id"])
        if gate.get("requires_opt_in") == "with-build" and not args.with_build:
            continue
        if gate_id.endswith("-integration") and not args.integration:
            continue
        if args.only and not any(gate_id.startswith(prefix) for prefix in args.only):
            continue
        if any(gate_id.startswith(prefix) for prefix in args.skip):
            continue
        selected.append(gate)
    return selected


def run_gate(gate: dict[str, Any]) -> dict[str, Any]:
    gate_id = str(gate["id"])
    missing = [name for name in gate.get("requires_env", []) if not os.environ.get(name)]
    record: dict[str, Any] = {
        "id": gate_id,
        "label": gate.get("label", gate_id),
        "command": gate["command"],
        "cwd": gate.get("cwd", "."),
        "status": "blocked" if missing else "fail",
        "exit_code": None,
        "duration_ms": 0,
        "error_lines": [],
    }
    if missing:
        record["error_lines"] = [f"missing prerequisites: {', '.join(missing)}"]
        return record

    log_path = LOG_DIR / f"{re.sub(r'[^A-Za-z0-9._-]+', '_', gate_id)}.log"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    cwd = ROOT / record["cwd"]
    started = time.monotonic()
    try:
        result = subprocess.run(
            record["command"],
            cwd=cwd,
            capture_output=True,
            text=True,
            check=False,
            timeout=int(gate.get("timeout_seconds", 900)),
        )
        output = result.stdout + ("\n" + result.stderr if result.stderr else "")
        record["exit_code"] = result.returncode
        record["status"] = "pass" if result.returncode == 0 else "fail"
    except subprocess.TimeoutExpired as error:
        output = f"timeout after {gate.get('timeout_seconds', 900)}s\n"
        if error.stdout:
            output += str(error.stdout)
        if error.stderr:
            output += str(error.stderr)
        record["status"] = "fail"
        record["exit_code"] = None
    except OSError as error:
        output = f"cannot execute gate: {error}\n"
        record["status"] = "fail"
    record["duration_ms"] = round((time.monotonic() - started) * 1000)
    record["log"] = str(log_path.relative_to(ROOT))
    log_path.write_text(output, encoding="utf-8")
    record["error_lines"] = [line.strip() for line in output.splitlines() if ERROR_HINT.search(line)][:12]
    if record["status"] == "fail" and not record["error_lines"]:
        record["error_lines"] = [output.strip().splitlines()[-1] if output.strip() else "no output"]
    return record


def read_scenarios() -> list[dict[str, Any]]:
    scenarios: list[dict[str, Any]] = []
    seen: set[str] = set()
    for path in LEDGER_PATHS:
        payload = load_json(path)
        entries = payload.get("scenarios")
        if not isinstance(entries, list):
            raise SystemExit(f"{path.relative_to(ROOT)} must contain a scenarios array")
        for scenario in entries:
            scenario_id = str(scenario.get("id", ""))
            if scenario_id in seen:
                raise SystemExit(f"duplicate scenario id across ledgers: {scenario_id}")
            seen.add(scenario_id)
            scenarios.append(scenario)
    return scenarios


def observed_scenario_statuses(gates: list[dict[str, Any]]) -> dict[str, str]:
    observed: dict[str, str] = {}
    for gate in gates:
        gate_id = str(gate["id"])
        if gate["status"] != "pass" or not gate_id.startswith(("website-", "core-")):
            continue
        log_path = ROOT / gate.get("log", "")
        if not log_path.is_file():
            continue
        for match in SCENARIO_RESULT.finditer(log_path.read_text(encoding="utf-8")):
            action, _, scenario_id = match.groups()
            observed[scenario_id] = {"PASS": "pass", "FAIL": "fail", "SKIP": "blocked"}[action]
    return observed


def scenario_report(
    scenarios: list[dict[str, Any]], gates: list[dict[str, Any]], config: dict[str, Any]
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    observed = observed_scenario_statuses(gates)
    integration_gates = {
        str(gate["id"]) for gate in gates if gate["status"] == "pass" and str(gate["id"]).endswith("-integration")
    }
    records: list[dict[str, Any]] = []
    for scenario in scenarios:
        scenario_id = str(scenario["id"])
        ledger_status = str(scenario["execution_status"])
        status = observed.get(scenario_id, ledger_status)
        if status == "pass" and scenario["coverage_status"] != "covered":
            status = "not_run"
        if scenario["level"] == "integration":
            family = "core" if scenario_id.startswith("CORE-") else "website"
            if f"{family}-integration" not in integration_gates:
                status = "blocked"
        records.append({
            "id": scenario_id,
            "level": scenario["level"],
            "coverage_status": scenario["coverage_status"],
            "ledger_execution_status": ledger_status,
            "status": status,
            "test_symbol": scenario["test_symbol"],
            "blocker": scenario.get("blocker"),
            "source": scenario["source"],
        })

    denominator = len(records)
    # Integration scenarios without a disposable fixture are reported as blocked,
    # not as failures. They are excluded from the executable coverage gate while
    # remaining visible in the report and diagnose output.
    eligible = [
        item
        for item in records
        if item["status"] != "blocked" and item["coverage_status"] != "blocked"
    ]
    covered = sum(item["coverage_status"] == "covered" for item in eligible)
    counts = {
        "denominator": denominator,
        "eligible_denominator": len(eligible),
        "covered": covered,
        "coverage_percent": round(covered / len(eligible) * 100, 2) if eligible else 0.0,
        "pass": sum(item["status"] == "pass" for item in records),
        "fail": sum(item["status"] == "fail" for item in records),
        "not_run": sum(item["status"] == "not_run" for item in records),
        "blocked": sum(item["status"] == "blocked" for item in records),
        "gap": sum(item["coverage_status"] == "gap" for item in records),
    }
    return records, counts


def write_report(report: dict[str, Any], path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def print_summary(report: dict[str, Any]) -> None:
    summary = report["summary"]
    print(f"report: {report['report_path']}")
    print(f"git: {report['git']['head'] or 'unknown'} (dirty={report['git']['dirty']})")
    print(
        "gates: "
        f"{summary['gates_pass']} pass, {summary['gates_fail']} fail, "
        f"{summary['gates_blocked']} blocked"
    )
    scenarios = summary["scenarios"]
    print(
        "scenarios: "
        f"{scenarios['pass']} pass, {scenarios['fail']} fail, "
        f"{scenarios['not_run']} not_run, {scenarios['blocked']} blocked, "
        f"coverage {scenarios['covered']}/{scenarios['eligible_denominator']} "
        f"executable = {scenarios['coverage_percent']:.2f}%"
    )


def print_diagnose(report: dict[str, Any], gate_filter: list[str]) -> None:
    for gate in report["gates"]:
        if gate_filter and not any(gate["id"].startswith(prefix) for prefix in gate_filter):
            continue
        if gate["status"] == "pass":
            continue
        print(f"[{gate['status'].upper()}] {gate['id']} ({gate['command'][0]} in {gate['cwd']})")
        for line in gate.get("error_lines", []):
            print(f"  {line}")
        if gate.get("log"):
            print(f"  log: {gate['log']}")
    for scenario in report["scenarios"]:
        if gate_filter:
            continue
        if scenario["status"] in {"fail", "blocked", "not_run"}:
            detail = scenario["blocker"] or scenario["test_symbol"]
            print(f"[{scenario['status'].upper()}] {scenario['id']} {detail}")
    for diagnostic in report.get("diagnostics", []):
        print(f"[GATE] {diagnostic}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("run", "ci", "status", "diagnose"))
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--config", type=Path, default=CONFIG_PATH)
    parser.add_argument("--only", action="append", default=[], metavar="PREFIX")
    parser.add_argument("--skip", action="append", default=[], metavar="PREFIX")
    parser.add_argument("--gate", action="append", default=[], metavar="PREFIX", help="diagnose only these gates")
    parser.add_argument("--integration", action="store_true", help="include explicitly gated disposable integration checks")
    parser.add_argument("--with-build", action="store_true", help="include the Admin build gate that regenerates dist")
    parser.add_argument("--strict-scenarios", action="store_true", help="require every ledger scenario to be pass")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.command in {"status", "diagnose"}:
        if not args.report.is_file():
            raise SystemExit(f"report does not exist: {args.report}")
        report = load_json(args.report)
        if args.command == "status":
            print_summary(report)
        else:
            print_diagnose(report, args.gate)
        return 0

    config = load_json(args.config)
    scenarios = read_scenarios()
    gate_list = build_gate_list(config, args)
    gate_records = [run_gate(gate) for gate in gate_list]
    scenario_records, scenario_counts = scenario_report(scenarios, gate_records, config)

    failures: list[str] = []
    for gate in gate_records:
        if gate["status"] != "pass":
            failures.append(f"gate {gate['id']}: {gate['status']}")
    coverage_min = float(config.get("coverage_min_percent", 0))
    if scenario_counts["coverage_percent"] < coverage_min:
        failures.append(
            f"coverage {scenario_counts['coverage_percent']:.2f}% below {coverage_min:.2f}%"
        )
    if scenario_counts["fail"]:
        failures.append(f"{scenario_counts['fail']} ledger scenario(s) failed")
    if args.strict_scenarios and scenario_counts["pass"] != scenario_counts["denominator"]:
        failures.append("strict scenario mode requires every ledger scenario to pass")

    report_path = args.report if args.report.is_absolute() else ROOT / args.report
    report = {
        "schema_version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "report_path": str(report_path.relative_to(ROOT)),
        "git": git_state(),
        "options": {
            "integration": args.integration,
            "with_build": args.with_build,
            "strict_scenarios": args.strict_scenarios,
            "only": args.only,
            "skip": args.skip,
        },
        "gates": gate_records,
        "scenarios": scenario_records,
        "summary": {
            "gates_total": len(gate_records),
            "gates_pass": sum(gate["status"] == "pass" for gate in gate_records),
            "gates_fail": sum(gate["status"] == "fail" for gate in gate_records),
            "gates_blocked": sum(gate["status"] == "blocked" for gate in gate_records),
            "scenarios": scenario_counts,
            "coverage_min_percent": coverage_min,
        },
        "diagnostics": failures,
    }
    write_report(report, report_path)
    print_summary(report)
    if failures:
        print_diagnose(report, [])
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
