#!/usr/bin/env python3
"""Regression tests for the unified test runner's reporting contract."""

from __future__ import annotations

import argparse
import importlib.util
import json
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch


MODULE_PATH = Path(__file__).with_name("run-test-suite.py")
SPEC = importlib.util.spec_from_file_location("run_test_suite", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
RUNNER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNNER)


def scenario(scenario_id: str, level: str, coverage: str, execution: str) -> dict[str, object]:
    return {
        "id": scenario_id,
        "level": level,
        "coverage_status": coverage,
        "execution_status": execution,
        "test_symbol": f"TestScenario/{scenario_id}",
        "blocker": "requires disposable resource" if execution == "blocked" else None,
        "source": "unit test fixture",
    }


class GateSelectionTests(unittest.TestCase):
    def test_opt_in_gates_are_absent_by_default(self) -> None:
        config = {
            "gates": [
                {"id": "ledger", "command": ["true"]},
                {"id": "frontend-build", "command": ["pnpm", "build"], "requires_opt_in": "with-build"},
                {"id": "website-integration", "command": ["tests/qa/run-website-red.sh", "integration"]},
            ],
            "go_checks": [],
        }
        args = argparse.Namespace(with_build=False, integration=False, only=[], skip=[])
        with patch.object(RUNNER, "workspace_modules", return_value=[]):
            gates = RUNNER.build_gate_list(config, args)
        self.assertEqual([gate["id"] for gate in gates], ["ledger"])

    def test_core_integration_gate_is_also_opt_in(self) -> None:
        config = {
            "gates": [
                {"id": "ledger", "command": ["true"]},
                {"id": "core-integration", "command": ["tests/qa/run-core-red.sh", "integration"]},
            ],
            "go_checks": [],
        }
        args = argparse.Namespace(with_build=False, integration=False, only=[], skip=[])
        with patch.object(RUNNER, "workspace_modules", return_value=[]):
            self.assertEqual([gate["id"] for gate in RUNNER.build_gate_list(config, args)], ["ledger"])
        args.integration = True
        with patch.object(RUNNER, "workspace_modules", return_value=[]):
            self.assertEqual(
                [gate["id"] for gate in RUNNER.build_gate_list(config, args)],
                ["ledger", "core-integration"],
            )

    def test_only_and_skip_prefixes_are_exact_filters(self) -> None:
        config = {
            "gates": [
                {"id": "website-unit", "command": ["true"]},
                {"id": "website-contract", "command": ["true"]},
            ],
            "go_checks": [],
        }
        args = argparse.Namespace(
            with_build=False, integration=False, only=["website-u"], skip=["website-contract"]
        )
        with patch.object(RUNNER, "workspace_modules", return_value=[]):
            gates = RUNNER.build_gate_list(config, args)
        self.assertEqual([gate["id"] for gate in gates], ["website-unit"])


class LedgerAggregationTests(unittest.TestCase):
    def test_all_declared_ledgers_are_aggregated(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            first = Path(tmp) / "website.json"
            second = Path(tmp) / "core.json"
            first.write_text(json.dumps({"scenarios": [scenario("WEB-001", "unit", "covered", "pass")]}))
            second.write_text(json.dumps({"scenarios": [scenario("CORE-001", "integration", "covered", "pass")]}))
            with patch.object(RUNNER, "LEDGER_PATHS", (first, second)):
                records = RUNNER.read_scenarios()
        self.assertEqual([record["id"] for record in records], ["WEB-001", "CORE-001"])


class ScenarioResultParsingTests(unittest.TestCase):
    def test_indented_go_subtest_results_are_observed(self) -> None:
        log = (
            "=== RUN   TestWebsiteScenarioSnapshotCAS\n"
            "=== RUN   TestWebsiteScenarioSnapshotCAS/WEB-004\n"
            "    --- PASS: TestWebsiteScenarioSnapshotCAS/WEB-004 (0.00s)\n"
            "PASS\n"
        )
        with patch.object(RUNNER, "LOG_DIR", Path("output")):
            gate = {
                "id": "website-unit",
                "status": "pass",
                "log": "test-logs/website-unit.log",
            }
            with patch.object(RUNNER, "ROOT", Path("/repo")), patch(
                "pathlib.Path.is_file", return_value=True
            ), patch("pathlib.Path.read_text", return_value=log):
                observed = RUNNER.observed_scenario_statuses([gate])
        self.assertEqual(observed, {"WEB-004": "pass"})


class ScenarioReportTests(unittest.TestCase):
    def test_integration_is_reported_blocked_without_explicit_gate(self) -> None:
        scenarios = [
            scenario("WEB-001", "unit", "covered", "pass"),
            scenario("WEB-002", "integration", "blocked", "blocked"),
        ]
        records, counts = RUNNER.scenario_report(scenarios, [], {"coverage_min_percent": 60})
        self.assertEqual(records[0]["status"], "pass")
        self.assertEqual(records[1]["status"], "blocked")
        self.assertEqual(counts["eligible_denominator"], 1)
        self.assertEqual(counts["coverage_percent"], 100.0)
        self.assertEqual(counts["pass"], 1)
        self.assertEqual(counts["blocked"], 1)

    def test_core_integration_passes_only_with_core_integration_gate(self) -> None:
        scenarios = [scenario("CORE-001", "integration", "covered", "pass")]
        core_gate = {"id": "core-integration", "status": "pass"}
        with patch.object(RUNNER, "observed_scenario_statuses", return_value={"CORE-001": "pass"}):
            records, _ = RUNNER.scenario_report(scenarios, [core_gate], {"coverage_min_percent": 60})
        self.assertEqual(records[0]["status"], "pass")
        with patch.object(RUNNER, "observed_scenario_statuses", return_value={"CORE-001": "pass"}):
            records, _ = RUNNER.scenario_report(
                scenarios, [{"id": "website-integration", "status": "pass"}], {"coverage_min_percent": 60}
            )
        self.assertEqual(records[0]["status"], "blocked")

    def test_unexecuted_gap_never_becomes_pass(self) -> None:
        scenarios = [scenario("WEB-003", "unit", "gap", "pass")]
        records, counts = RUNNER.scenario_report(scenarios, [], {"coverage_min_percent": 60})
        self.assertEqual(records[0]["status"], "not_run")
        self.assertEqual(counts["coverage_percent"], 0.0)
        self.assertEqual(counts["not_run"], 1)

    def test_observed_subtest_result_overrides_ledger_status(self) -> None:
        scenarios = [scenario("WEB-004", "unit", "covered", "fail")]
        with patch.object(RUNNER, "observed_scenario_statuses", return_value={"WEB-004": "pass"}):
            records, counts = RUNNER.scenario_report(scenarios, [], {"coverage_min_percent": 60})
        self.assertEqual(records[0]["status"], "pass")
        self.assertEqual(counts["pass"], 1)
        self.assertEqual(counts["fail"], 0)


if __name__ == "__main__":
    unittest.main()
