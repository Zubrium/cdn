#!/usr/bin/env sh
# Run only the executable Core cache and cache/concurrency scenarios from the
# dedicated ledger. Exact PASS lines are required; zero-test success is refused.
set -u

usage() {
	printf '%s\n' "usage: $0 {unit|race|all}" >&2
}

if [ "$#" -ne 1 ]; then
	usage
	exit 64
fi

selector=$1
case "$selector" in
	unit | race | all) ;;
	*)
		usage
		exit 64
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
ledger="$script_dir/core-cache-scenarios.json"
checker="$script_dir/scenario-ledger.py"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"
work_dir=$(mktemp -d "$TMPDIR/core-cache-acceptance.XXXXXX") || exit $?
overlay="$work_dir/overlay.json"
gotmp="$work_dir/gotmp"
mkdir -p "$gotmp" || exit $?
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

printf '%s\n' "==> validating Core cache scenario ledger"
python3 "$checker" "$ledger" || exit $?

run_target() {
	test_file=$1
	test_symbol=$2
	test_level=$3
	case "$test_file" in
		tests/go/core/infrastructure/cache/*) ;;
		*)
			printf '%s\n' "unsupported Core cache test file: $test_file" >&2
			return 1
			;;
	esac
	case "$test_symbol" in
		TestCoreCacheScenarios/*) ;;
		*)
			printf '%s\n' "Core cache symbol must name an exact scenario subtest: $test_symbol" >&2
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
	top_level=${test_symbol%%/*}
	subtest=${test_symbol#*/}
	if [ "$test_level" = "race" ]; then
		race_arg="-race"
	else
		race_arg=""
	fi
	printf '%s\n' "==> running $test_symbol in core ./infrastructure/cache (race=${race_arg:-off})"
	log="$work_dir/$(printf '%s' "$test_symbol" | tr '/' '_').log"
	if ! (cd "$root/core" && GOTMPDIR="$gotmp" go test $race_arg -count=1 -v -overlay="$overlay" \
		-run "^${top_level}\$/^${subtest}\$" ./infrastructure/cache) > "$log" 2>&1; then
		cat "$log" >&2
		return 1
	fi
	cat "$log"
	if ! grep -qF -- "--- PASS: $test_symbol" "$log"; then
		printf '%s\n' "selector $test_symbol did not produce a PASS result; refusing a zero-test success" >&2
		return 1
	fi
}

status=0
python3 "$checker" --test-targets unit "$ledger" > "$work_dir/targets" || exit $?
if [ ! -s "$work_dir/targets" ]; then
	printf '%s\n' "ledger produced no executable targets; refusing a zero-test success" >&2
	exit 1
fi
while IFS='|' read -r test_file test_symbol; do
	[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
	run_target "$test_file" "$test_symbol" unit || status=1
done < "$work_dir/targets"
case "$selector" in
	all | race)
		while IFS='|' read -r test_file test_symbol; do
			[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
			run_target "$test_file" "$test_symbol" race || status=1
		done < "$work_dir/targets"
		;;
esac

exit "$status"
