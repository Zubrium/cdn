#!/usr/bin/env sh
# Run only Content acceptance scenarios selected from the dedicated scenario
# ledger. Blocked e2e scenarios are never reported as a pass, and a zero-test
# result is treated as a failure.
set -u

usage() {
	printf '%s\n' "usage: $0 {go|e2e|all}" >&2
}

selector=all
if [ "$#" -gt 1 ]; then
	usage
	exit 64
fi
if [ "$#" -eq 1 ]; then
	selector=$1
fi
case "$selector" in
	go | e2e | all) ;;
	*)
		usage
		exit 64
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
ledger="$script_dir/content-scenarios.json"
checker="$script_dir/scenario-ledger.py"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"
work_dir=$(mktemp -d "$TMPDIR/content-acceptance.XXXXXX") || exit $?
overlay="$work_dir/overlay.json"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

printf '%s\n' "==> validating Content scenario ledger"
python3 "$checker" "$ledger" || exit $?
python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

module_for_test_file() {
	case "$1" in
		tests/go/engines/content/*)
			printf '%s\n' "engines/content"
			;;
		*)
			printf '%s\n' "unsupported centralized Content test file: $1" >&2
			return 1
			;;
	esac
}

run_targets_for_level() {
	level=$1
	targets="$work_dir/$level-targets"
	python3 "$checker" --test-targets "$level" "$ledger" > "$targets" || return $?
	if [ ! -s "$targets" ]; then
		printf '%s\n' "no substantive $level scenarios are registered; refusing a zero-test success" >&2
		return 1
	fi
	while IFS='|' read -r test_file test_symbol; do
		[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
		case "$test_symbol" in
			*/*) ;;
			*)
				printf '%s\n' "$test_symbol must include its exact scenario subtest" >&2
				return 1
				;;
		esac
		module=$(module_for_test_file "$test_file") || return $?
		top_level=${test_symbol%%/*}
		printf '%s\n' "==> running $test_symbol in $module"
		log="$work_dir/$(printf '%s' "$test_symbol" | tr '/|' '__').log"
		if ! (cd "$root/$module" && go test -count=1 -v -overlay="$overlay" -run "^${top_level}$" ./...) > "$log" 2>&1; then
			cat "$log" >&2
			return 1
		fi
		if ! grep -q -- "--- PASS: $test_symbol" "$log"; then
			cat "$log" >&2
			printf '%s\n' "$test_symbol did not produce a PASS subtest; refusing a zero-test success" >&2
			return 1
		fi
		cat "$log"
	done < "$targets"
}

blocked_scenario_text() {
	python3 - "$ledger" <<'PY'
import json
import sys

ledger = json.load(open(sys.argv[1], encoding="utf-8"))
for scenario in ledger["scenarios"]:
    if scenario.get("coverage_status") == "blocked" or scenario.get("execution_status") == "blocked":
        print(f"{scenario['id']}: {scenario['blocker']}")
PY
}

run_e2e() {
	ready=1
	for name in CONTENT_TEST_DISPOSABLE CONTENT_TEST_ADMIN_BASE_URL \
		CONTENT_TEST_PUBLIC_BASE_URL CONTENT_TEST_TIMEOUT; do
		eval "value=\${$name-}"
		if [ -z "$value" ]; then
			ready=0
		fi
	done
	if [ "$ready" -ne 1 ] || [ "${CONTENT_TEST_DISPOSABLE:-}" != "1" ]; then
		printf '%s\n' "==> e2e scenarios are BLOCKED"
		blocked_scenario_text
		return 0
	fi
	run_targets_for_level e2e
}

status=0
case "$selector" in
	go)
		run_targets_for_level unit || status=1
		run_targets_for_level contract || status=1
		;;
	e2e)
		run_e2e || status=1
		;;
	all)
		run_targets_for_level unit || status=1
		run_targets_for_level contract || status=1
		run_e2e || status=1
		;;
esac

exit "$status"
