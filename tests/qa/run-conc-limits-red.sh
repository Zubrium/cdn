#!/usr/bin/env sh
# Run only the executable concurrency-limit scenarios from the new ledger.
set -u

usage() {
	printf '%s\n' "usage: $0 {unit|contract|e2e|all}" >&2
}

if [ "$#" -ne 1 ]; then
	usage
	exit 64
fi

selector=$1
case "$selector" in
	unit|contract|e2e|all) ;;
	*)
		usage
		exit 64
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
ledger="$script_dir/conc-limits-scenarios.json"
checker="$script_dir/scenario-ledger.py"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"
work_dir=$(mktemp -d "$TMPDIR/conc-limits.XXXXXX") || exit $?
overlay="$work_dir/overlay.json"
gotmp="$work_dir/gotmp"
mkdir -p "$gotmp" || exit $?
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

printf '%s\n' "==> validating concurrency limits ledger"
python3 "$checker" "$ledger" || exit $?

module_and_package() {
	case "$1" in
		tests/go/main/internal/config/*)
			printf '%s\n' "main|./internal/config"
			;;
		tests/go/main/internal/bus/*)
			printf '%s\n' "main|./internal/bus"
			;;
		tests/go/presentation/admin/base/transport/*)
			printf '%s\n' "presentation/admin/base|./transport"
			;;
		*)
			printf '%s\n' "unsupported concurrency test file: $1" >&2
			return 1
			;;
	esac
}

build_tags() {
	case "$1" in
		*topology_scenario_test.go)
			printf '%s\n' "router"
			;;
		*)
			printf '%s\n' ""
			;;
	esac
}

run_target() {
	test_file=$1
	test_symbol=$2
	test_level=$3
	top_level=${test_symbol%%/*}
	subtest=${test_symbol#*/}
	location=$(module_and_package "$test_file") || return 1
	module=${location%%|*}
	package=${location#*|}
	case "$test_symbol" in
		*/*) ;;
		*)
			printf '%s\n' "concurrency symbol $test_symbol must include its exact scenario subtest" >&2
			return 1
			;;
	esac
	case "$selector" in
		all) ;;
		"$test_level") ;;
		*)
			return 0
			;;
	esac
	tags=$(build_tags "$test_file")
	tag_args=""
	if [ -n "$tags" ]; then
		tag_args="-tags=$tags"
		printf '%s\n' "==> using build tags $tags for $test_file"
	fi
	printf '%s\n' "==> running $test_symbol in $module $package"
	log="$work_dir/$(printf '%s' "$test_symbol" | tr '/|' '__').log"
	if ! (cd "$root/$module" && GOTMPDIR="$gotmp" go test $tag_args -count=1 -v -overlay="$overlay" \
		-run "^${top_level}\$/^${subtest}\$" "$package") > "$log" 2>&1; then
		cat "$log" >&2
		return 1
	fi
	cat "$log"
	if ! grep -qF -- "--- PASS: $test_symbol" "$log"; then
		printf '%s\n' "selector $test_symbol did not produce a PASS result; refusing a zero-test success" >&2
		return 1
	fi
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
	printf '%s\n' "==> class-pool e2e scenarios are BLOCKED"
	blocked_scenario_text
}

status=0
case "$selector" in
	e2e)
		run_e2e || status=1
		;;
	unit|contract)
		python3 "$checker" --test-targets "$selector" "$ledger" > "$work_dir/$selector-targets" || exit $?
		while IFS='|' read -r test_file test_symbol; do
			[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
			run_target "$test_file" "$test_symbol" "$selector" || status=1
		done < "$work_dir/$selector-targets"
		;;
	all)
		python3 "$checker" --test-targets unit "$ledger" > "$work_dir/unit-targets" || exit $?
		python3 "$checker" --test-targets contract "$ledger" > "$work_dir/contract-targets" || exit $?
		while IFS='|' read -r test_file test_symbol; do
			[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
			run_target "$test_file" "$test_symbol" unit || status=1
		done < "$work_dir/unit-targets"
		while IFS='|' read -r test_file test_symbol; do
			[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
			run_target "$test_file" "$test_symbol" contract || status=1
		done < "$work_dir/contract-targets"
		run_e2e || status=1
		;;
esac

exit "$status"
