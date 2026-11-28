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


class ScenarioReportTests(unittest.TestCase):
    def test_historical_pass_and_blocked_remain_in_full_denominator(self) -> None:
        scenarios = [
            scenario("WEB-001", "unit", "covered", "pass"),
            scenario("WEB-002", "integration", "blocked", "blocked"),
            scenario("WEB-003", "contract", "covered", "pass"),
        ]
        with patch.object(RUNNER, "observed_scenario_statuses", return_value={"TestScenario/WEB-003": "pass"}):
            records, counts = RUNNER.scenario_report(scenarios, [])
        self.assertEqual([item["status"] for item in records], ["not_run", "blocked", "pass"])
        self.assertEqual(counts["denominator"], 3)
        self.assertEqual(counts["pass"], 1)
        self.assertEqual(counts["readiness_percent"], 33.33)

    def test_unrelated_test_with_same_id_cannot_prove_execution(self) -> None:
        scenarios = [scenario("WEB-004", "unit", "covered", "pass")]
        with patch.object(RUNNER, "observed_scenario_statuses", return_value={"TestOther/WEB-004": "pass"}):
            records, _ = RUNNER.scenario_report(scenarios, [])
        self.assertEqual(records[0]["status"], "not_run")

    def test_individual_results_from_failed_gate_are_preserved(self) -> None:
        log = (
            "    --- PASS: TestScenario/WEB-020 (0.00s)\n"
            "    --- FAIL: TestScenario/WEB-021 (0.00s)\n"
            "    --- SKIP: TestScenario/WEB-022 (0.00s)\n"
        )
        gate = {"id": "website-integration", "status": "fail", "log": "test-logs/website-integration.log"}
        scenarios = [scenario(f"WEB-0{index}", "integration", "covered", "pass") for index in (20, 21, 22)]
        with patch("pathlib.Path.is_file", return_value=True), patch("pathlib.Path.read_text", return_value=log):
            records, counts = RUNNER.scenario_report(scenarios, [gate])
        self.assertEqual([item["status"] for item in records], ["pass", "fail", "blocked"])
        self.assertEqual(counts["pass"], 1)
        self.assertEqual(counts["fail"], 1)

    def test_any_failure_takes_precedence_over_a_duplicate_pass(self) -> None:
        log = (
            "    --- FAIL: TestScenario/WEB-021 (0.00s)\n"
            "    --- PASS: TestScenario/WEB-021 (0.00s)\n"
        )
        gate = {"id": "website-integration", "status": "fail", "log": "test-logs/website-integration.log"}
        with patch("pathlib.Path.is_file", return_value=True), patch("pathlib.Path.read_text", return_value=log):
            observed = RUNNER.observed_scenario_statuses([gate])
        self.assertEqual(observed, {"TestScenario/WEB-021": "fail"})


if __name__ == "__main__":
    unittest.main()
