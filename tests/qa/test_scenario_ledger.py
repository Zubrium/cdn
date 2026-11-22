#!/usr/bin/env python3
"""Regression tests for exact Go acceptance-subtest validation."""

from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


MODULE_PATH = Path(__file__).with_name("scenario-ledger.py")
SPEC = importlib.util.spec_from_file_location("scenario_ledger", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
SCENARIO_LEDGER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SCENARIO_LEDGER)


class ExactSubtestReceiverTests(unittest.TestCase):
    def assert_exact_subtest(self, source: str, expected: bool) -> None:
        self.assertEqual(
            SCENARIO_LEDGER.source_has_symbol_and_id(
                source,
                "TestScenario/WEB-001",
                "WEB-001",
                require_exact_subtest=True,
            ),
            expected,
        )

    def test_accepts_named_testing_t_receiver(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { t.Run("WEB-001", func(t *testing.T) {}) }\n',
            True,
        )

    def test_accepts_same_scope_short_declaration_reuse(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { t, ok := t, true; _ = ok; t.Run("WEB-001", func(t *testing.T) {}) }\n',
            True,
        )

    def test_rejects_control_statement_short_declaration_shadowing(self) -> None:
        for control in (
            'if t, ok := fake, true; ok { t.Run("WEB-001", func(t *testing.T) {}) }',
            'for t, ok := fake, true; ok; ok = false { t.Run("WEB-001", func(t *testing.T) {}) }',
            'switch t, ok := fake, true; ok { default: t.Run("WEB-001", func(t *testing.T) {}) }',
        ):
            with self.subTest(control=control.split()[0]):
                self.assert_exact_subtest(f'func TestScenario(t *testing.T) {{ {control} }}\n', False)

    def test_rejects_shadow_after_function_literal_in_control_header(self) -> None:
        for control in (
            'if t, ok := fake, func() bool { return true }(); ok { t.Run("WEB-001", func(t *testing.T) {}) }',
            'for t, ok := fake, func() bool { return true }(); ok; ok = false { t.Run("WEB-001", func(t *testing.T) {}) }',
            'switch t, ok := fake, func() bool { return true }(); ok { default: t.Run("WEB-001", func(t *testing.T) {}) }',
        ):
            with self.subTest(control=control.split()[0]):
                self.assert_exact_subtest(f'func TestScenario(t *testing.T) {{ {control} }}\n', False)

    def test_rejects_shadow_after_composite_literal_in_control_header(self) -> None:
        for control in (
            'if t, ok := fake, map[bool]bool{true: true}[true]; ok { t.Run("WEB-001", func(t *testing.T) {}) }',
            'for t, ok := fake, map[bool]bool{true: true}[true]; ok; ok = false { t.Run("WEB-001", func(t *testing.T) {}) }',
            'switch t, ok := fake, map[bool]bool{true: true}[true]; ok { default: t.Run("WEB-001", func(t *testing.T) {}) }',
        ):
            with self.subTest(control=control.split()[0]):
                self.assert_exact_subtest(f'func TestScenario(t *testing.T) {{ {control} }}\n', False)

    def test_range_map_literal_body_preserves_shadowing(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { for t := range map[fakeTestT]bool{fake: true} { t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            False,
        )
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { for t := range map[fakeTestT]bool{fake: true} { _ = t }; t.Run("WEB-001", func(t *testing.T) {}) }\n',
            True,
        )

    def test_literal_header_shadow_ends_after_control_statement(self) -> None:
        for expression in ('func() bool { return true }()', 'map[bool]bool{true: true}[true]'):
            with self.subTest(expression=expression):
                self.assert_exact_subtest(
                    f'func TestScenario(t *testing.T) {{ if t, ok := fake, {expression}; ok {{ _ = t }}; t.Run("WEB-001", func(t *testing.T) {{}}) }}\n',
                    True,
                )

    def test_named_composite_literal_header_and_range_body(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { if t, ok := fake, Flag{enabled: true}; ok.enabled { t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            False,
        )
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { for t := range values { t.Run("WEB-001", func(t *testing.T) {}) }; _ = t }\n',
            False,
        )
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { for t := range values { _ = t }; t.Run("WEB-001", func(t *testing.T) {}) }\n',
            True,
        )

    def test_control_statement_shadow_does_not_escape_scope(self) -> None:
        for control in (
            'if t, ok := fake, true; ok { _ = t }',
            'for t, ok := fake, true; ok; ok = false { _ = t }',
            'switch t, ok := fake, true; ok { default: _ = t }',
        ):
            with self.subTest(control=control.split()[0]):
                self.assert_exact_subtest(
                    f'func TestScenario(t *testing.T) {{ {control}; t.Run("WEB-001", func(t *testing.T) {{}}) }}\n',
                    True,
                )

    def test_control_statement_without_receiver_declaration_keeps_parameter(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { if ok := true; ok { t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            True,
        )

    def test_rejects_control_statement_shadow_in_else(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { if t, ok := fake, true; ok { _ = t } else { t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            False,
        )

    def test_rejects_control_statement_shadow_in_else_if(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { if t, ok := fake, true; ok { _ = t } else if ok { t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            False,
        )

    def test_rejects_fake_receiver(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { fake.Run("WEB-001", func(t *testing.T) {}) }\n',
            False,
        )

    def test_rejects_parenthesized_var_shadowing(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { { var (t fakeTestT); t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            False,
        )

    def test_rejects_multi_name_short_declaration_shadowing(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { { t, ok := fake, true; _ = ok; t.Run("WEB-001", func(t *testing.T) {}) } }\n',
            False,
        )

    def test_rejects_subtest_in_nested_function_literal(self) -> None:
        self.assert_exact_subtest(
            'func TestScenario(t *testing.T) { invoke(func() { t.Run("WEB-001", func(t *testing.T) {}) }) }\n',
            False,
        )


class TestTargetSelectionTests(unittest.TestCase):
    def test_targets_pair_test_file_with_exact_symbol(self) -> None:
        scenarios = [
            {
                "level": "integration",
                "coverage_status": "covered",
                "test_file": "tests/go/main/cmd/zubrium/example_test.go",
                "test_symbol": "TestExample/WEB-018",
            },
            {
                "level": "integration",
                "coverage_status": "blocked",
                "test_file": "tests/go/engines/website/example_test.go",
                "test_symbol": "TestBlocked/WEB-012",
            },
        ]
        self.assertEqual(
            SCENARIO_LEDGER.selected_test_targets(scenarios, "integration"),
            ["tests/go/main/cmd/zubrium/example_test.go|TestExample/WEB-018"],
        )


if __name__ == "__main__":
    unittest.main()
