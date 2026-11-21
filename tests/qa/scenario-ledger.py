#!/usr/bin/env python3
"""Validate scenario ledgers and report their coverage/execution counts."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections.abc import Iterable
from pathlib import Path
from typing import Any

LEVELS = frozenset({"unit", "contract", "integration", "e2e"})
COVERAGE_STATUSES = frozenset({"covered", "blocked", "gap"})
EXECUTION_STATUSES = frozenset({"not_run", "pass", "fail", "blocked"})
REQUIRED_FIELDS = frozenset(
    {
        "id",
        "source",
        "level",
        "preconditions",
        "input",
        "action",
        "expected_result",
        "expected_state",
        "test_file",
        "test_symbol",
        "coverage_status",
        "execution_status",
        "blocker",
    }
)
ID_PATTERN = re.compile(r"^[A-Z][A-Z0-9]*-[0-9]{3}$")
SYMBOL_PATTERN = re.compile(r"^(Test[A-Za-z0-9_]+)(?:/([A-Z][A-Z0-9]*-[0-9]{3}))?$")


class LedgerError(ValueError):
    """A ledger violates its stable scenario contract."""


def project_root() -> Path:
    return Path(__file__).resolve().parents[2]


def read_scenarios(path: Path) -> list[dict[str, Any]]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except OSError as error:
        raise LedgerError(f"cannot read ledger {path}: {error}") from error
    except json.JSONDecodeError as error:
        raise LedgerError(f"invalid JSON in {path}: {error}") from error

    if isinstance(payload, list):
        scenarios = payload
    elif isinstance(payload, dict) and isinstance(payload.get("scenarios"), list):
        scenarios = payload["scenarios"]
    else:
        raise LedgerError("ledger must be an array or an object containing a scenarios array")

    if not scenarios:
        raise LedgerError("ledger must contain at least one scenario")
    if not all(isinstance(scenario, dict) for scenario in scenarios):
        raise LedgerError("every scenario must be a JSON object")
    return scenarios


def require_nonempty_string(record: dict[str, Any], field: str, scenario_id: str) -> str:
    value = record.get(field)
    if not isinstance(value, str) or not value.strip():
        raise LedgerError(f"{scenario_id}: {field} must be a non-empty string")
    return value


def source_has_symbol_and_id(
    source: str, symbol: str, scenario_id: str, *, require_exact_subtest: bool = False
) -> bool:
    match = SYMBOL_PATTERN.fullmatch(symbol)
    if match is None:
        return False
    function_name, symbol_id = match.groups()
    function = re.compile(rf"(?m)^func\s+{re.escape(function_name)}\s*\(")
    function_match = function.search(source)
    if function_match is None:
        return False
    expected_id = symbol_id or scenario_id
    if require_exact_subtest:
        return source_has_exact_subtest(source, function_match.end(), expected_id)
    return re.search(rf"[\"'`]({re.escape(expected_id)})[\"'`]", source) is not None


def source_has_exact_subtest(source: str, function_header_end: int, scenario_id: str) -> bool:
    opening = next_go_code_character(source, function_header_end, "{")
    if opening is None:
        return False
    testing_parameters = go_testing_t_parameter_names(source[function_header_end:opening])
    if not testing_parameters:
        return False
    body = go_function_body(source, opening)
    if body is None:
        return False
    return go_body_has_exact_subtest(go_tokens(body), testing_parameters, scenario_id)


def go_body_has_exact_subtest(
    tokens: list[tuple[str, str]], testing_parameters: set[str], scenario_id: str
) -> bool:
    shadowed_scopes: list[set[str]] = [set()]
    index = 0
    while index < len(tokens):
        token = tokens[index]
        if token == ("identifier", "func"):
            index = skip_go_function_literal(tokens, index)
            continue
        if token == ("punctuation", "{"):
            shadowed_scopes.append(set())
            index += 1
            continue
        if token == ("punctuation", "}"):
            if len(shadowed_scopes) > 1:
                shadowed_scopes.pop()
            index += 1
            continue
        if token == ("identifier", "var") and index + 1 < len(tokens):
            name = tokens[index + 1]
            if name[0] == "identifier":
                shadowed_scopes[-1].add(name[1])
        if (
            token[0] == "identifier"
            and index + 2 < len(tokens)
            and tokens[index + 1] == ("punctuation", ":")
            and tokens[index + 2] == ("punctuation", "=")
        ):
            shadowed_scopes[-1].add(token[1])
        if (
            token[0] == "identifier"
            and token[1] in testing_parameters
            and not any(token[1] in scope for scope in shadowed_scopes)
            and index + 4 < len(tokens)
            and tokens[index + 1] == ("punctuation", ".")
            and tokens[index + 2] == ("identifier", "Run")
            and tokens[index + 3] == ("punctuation", "(")
            and tokens[index + 4] == ("string", scenario_id)
        ):
            return True
        index += 1
    return False


def skip_go_function_literal(tokens: list[tuple[str, str]], index: int) -> int:
    while index < len(tokens) and tokens[index] != ("punctuation", "{"):
        index += 1
    if index == len(tokens):
        return index
    depth = 0
    while index < len(tokens):
        if tokens[index] == ("punctuation", "{"):
            depth += 1
        elif tokens[index] == ("punctuation", "}"):
            depth -= 1
            if depth == 0:
                return index + 1
        index += 1
    return index


def go_testing_t_parameter_names(function_header: str) -> set[str]:
    tokens = go_tokens(function_header)
    names: set[str] = set()
    for index in range(1, len(tokens) - 3):
        if (
            tokens[index - 1][0] == "identifier"
            and tokens[index] == ("punctuation", "*")
            and tokens[index + 1] == ("identifier", "testing")
            and tokens[index + 2] == ("punctuation", ".")
            and tokens[index + 3] == ("identifier", "T")
        ):
            names.add(tokens[index - 1][1])
    return names


def go_function_body(source: str, opening: int) -> str | None:
    depth = 0
    index = opening
    while index < len(source):
        character = source[index]
        if character in {'"', "'", "`"}:
            index = consume_go_string(source, index)
            continue
        if source.startswith("//", index):
            newline = source.find("\n", index + 2)
            index = len(source) if newline == -1 else newline + 1
            continue
        if source.startswith("/*", index):
            closing_comment = source.find("*/", index + 2)
            if closing_comment == -1:
                return None
            index = closing_comment + 2
            continue
        if character == "{":
            depth += 1
        elif character == "}":
            depth -= 1
            if depth == 0:
                return source[opening + 1 : index]
        index += 1
    return None


def next_go_code_character(source: str, index: int, expected: str) -> int | None:
    while index < len(source):
        if source[index] in {'"', "'", "`"}:
            index = consume_go_string(source, index)
            continue
        if source.startswith("//", index):
            newline = source.find("\n", index + 2)
            index = len(source) if newline == -1 else newline + 1
            continue
        if source.startswith("/*", index):
            closing_comment = source.find("*/", index + 2)
            if closing_comment == -1:
                return None
            index = closing_comment + 2
            continue
        if source[index] == expected:
            return index
        index += 1
    return None


def consume_go_string(source: str, start: int) -> int:
    quote = source[start]
    index = start + 1
    if quote == "`":
        closing = source.find("`", index)
        return len(source) if closing == -1 else closing + 1
    while index < len(source):
        if source[index] == "\\":
            index += 2
            continue
        if source[index] == quote:
            return index + 1
        index += 1
    return len(source)


def go_tokens(source: str) -> list[tuple[str, str]]:
    tokens: list[tuple[str, str]] = []
    index = 0
    while index < len(source):
        character = source[index]
        if character.isspace():
            index += 1
            continue
        if source.startswith("//", index):
            newline = source.find("\n", index + 2)
            index = len(source) if newline == -1 else newline + 1
            continue
        if source.startswith("/*", index):
            closing_comment = source.find("*/", index + 2)
            if closing_comment == -1:
                return tokens
            index = closing_comment + 2
            continue
        if character in {'"', "'", "`"}:
            end = consume_go_string(source, index)
            if end > len(source) or end == len(source) and source[-1:] != character:
                return tokens
            tokens.append(("string", source[index + 1 : end - 1]))
            index = end
            continue
        if character.isalpha() or character == "_":
            end = index + 1
            while end < len(source) and (source[end].isalnum() or source[end] == "_"):
                end += 1
            tokens.append(("identifier", source[index:end]))
            index = end
            continue
        tokens.append(("punctuation", character))
        index += 1
    return tokens


def validate_scenarios(scenarios: Iterable[dict[str, Any]], root: Path) -> None:
    seen_ids: set[str] = set()
    for index, scenario in enumerate(scenarios, start=1):
        scenario_id = scenario.get("id")
        display_id = scenario_id if isinstance(scenario_id, str) else f"record {index}"
        missing = sorted(REQUIRED_FIELDS.difference(scenario))
        if missing:
            raise LedgerError(f"{display_id}: missing required fields: {', '.join(missing)}")
        if not isinstance(scenario_id, str) or not ID_PATTERN.fullmatch(scenario_id):
            raise LedgerError(f"{display_id}: id must match {ID_PATTERN.pattern}")
        if scenario_id in seen_ids:
            raise LedgerError(f"{scenario_id}: duplicate scenario id")
        seen_ids.add(scenario_id)

        for field in ("source", "input", "action", "expected_result", "expected_state", "test_file", "test_symbol"):
            require_nonempty_string(scenario, field, scenario_id)

        preconditions = scenario["preconditions"]
        if not isinstance(preconditions, list) or not preconditions or not all(
            isinstance(item, str) and item.strip() for item in preconditions
        ):
            raise LedgerError(f"{scenario_id}: preconditions must be a non-empty array of non-empty strings")

        level = scenario["level"]
        if level not in LEVELS:
            raise LedgerError(f"{scenario_id}: unsupported level {level!r}")
        coverage_status = scenario["coverage_status"]
        if coverage_status not in COVERAGE_STATUSES:
            raise LedgerError(f"{scenario_id}: unsupported coverage_status {coverage_status!r}")
        execution_status = scenario["execution_status"]
        if execution_status not in EXECUTION_STATUSES:
            raise LedgerError(f"{scenario_id}: unsupported execution_status {execution_status!r}")

        blocker = scenario["blocker"]
        is_blocked = coverage_status == "blocked" or execution_status == "blocked"
        if is_blocked and (not isinstance(blocker, str) or not blocker.strip()):
            raise LedgerError(f"{scenario_id}: blocked scenario requires an exact non-empty blocker")
        if not is_blocked and blocker is not None:
            raise LedgerError(f"{scenario_id}: non-blocked scenario must use blocker: null")
        if coverage_status == "covered" and scenario["test_symbol"].startswith("TestWebsiteScenarioLedger/"):
            raise LedgerError(f"{scenario_id}: ledger validator is not a substantive covered test")

        symbol_match = SYMBOL_PATTERN.fullmatch(scenario["test_symbol"])
        if symbol_match is None or symbol_match.group(2) != scenario_id:
            raise LedgerError(f"{scenario_id}: test_symbol must name its exact {scenario_id} subtest")

        relative_test_file = Path(scenario["test_file"])
        if relative_test_file.is_absolute() or ".." in relative_test_file.parts:
            raise LedgerError(f"{scenario_id}: test_file must be a repository-relative path")
        test_path = root / relative_test_file
        if not test_path.is_file():
            raise LedgerError(f"{scenario_id}: test_file does not exist: {relative_test_file}")
        try:
            source = test_path.read_text(encoding="utf-8")
        except OSError as error:
            raise LedgerError(f"{scenario_id}: cannot read {relative_test_file}: {error}") from error
        if not source_has_symbol_and_id(
            source,
            scenario["test_symbol"],
            scenario_id,
            require_exact_subtest=coverage_status == "covered",
        ):
            raise LedgerError(
                f"{scenario_id}: {relative_test_file} does not contain test symbol/ID {scenario['test_symbol']!r}"
            )


def print_report(scenarios: list[dict[str, Any]]) -> None:
    denominator = len(scenarios)
    covered = sum(scenario["coverage_status"] == "covered" for scenario in scenarios)
    gaps = sum(scenario["coverage_status"] == "gap" for scenario in scenarios)
    blockers = sum(scenario["coverage_status"] == "blocked" for scenario in scenarios)
    passed = sum(scenario["execution_status"] == "pass" for scenario in scenarios)
    failed = sum(scenario["execution_status"] == "fail" for scenario in scenarios)
    not_run = sum(scenario["execution_status"] == "not_run" for scenario in scenarios)
    execution_blocked = sum(scenario["execution_status"] == "blocked" for scenario in scenarios)
    coverage_percent = (covered / denominator * 100) if denominator else 0.0

    print(f"denominator: {denominator}")
    print(f"covered: {covered}")
    print(f"coverage_percent: {coverage_percent:.2f}")
    print(f"gaps: {gaps}")
    print(f"blockers: {blockers}")
    print(f"execution_pass: {passed}")
    print(f"execution_fail: {failed}")
    print(f"execution_not_run: {not_run}")
    print(f"execution_blocked: {execution_blocked}")


def selected_symbols(scenarios: list[dict[str, Any]], level: str) -> list[str]:
    symbols: set[str] = set()
    for scenario in scenarios:
        if scenario["level"] != level or scenario["coverage_status"] != "covered":
            continue
        match = SYMBOL_PATTERN.fullmatch(scenario["test_symbol"])
        assert match is not None  # validate_scenarios ran first.
        symbols.add(match.group(1))
    return sorted(symbols)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--test-symbols",
        choices=sorted(LEVELS),
        metavar="LEVEL",
        help="print unique top-level Go test symbols for covered scenarios at the selected level after validation",
    )
    parser.add_argument("ledger", type=Path, help="path to a scenario ledger JSON file")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        scenarios = read_scenarios(args.ledger)
        validate_scenarios(scenarios, project_root())
    except LedgerError as error:
        print(f"scenario-ledger: error: {error}", file=sys.stderr)
        return 1

    if args.test_symbols:
        print("\n".join(selected_symbols(scenarios, args.test_symbols)))
    else:
        print_report(scenarios)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
