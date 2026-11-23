#!/usr/bin/env sh
# Run only the executable admission/backpressure scenarios from the concurrency ledger.
set -u

usage() {
	printf '%s\n' "usage: $0 {unit|all}" >&2
}

if [ "$#" -ne 1 ]; then
	usage
	exit 64
fi

selector=$1
case "$selector" in
	unit|all) ;;
	*)
		usage
		exit 64
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
ledger="$script_dir/concurrency-admission-scenarios.json"
checker="$script_dir/scenario-ledger.py"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"
work_dir=$(mktemp -d "$TMPDIR/concurrency-admission.XXXXXX") || exit $?
overlay="$work_dir/overlay.json"
gocache="$work_dir/go-build"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

printf '%s\n' "==> validating concurrency admission ledger"
python3 "$checker" "$ledger" || exit $?

module_and_package() {
	case "$1" in
		tests/go/main/internal/bus/*)
			printf '%s\n' "main|./internal/bus"
			;;
		tests/go/presentation/admin/base/transport/*)
			printf '%s\n' "presentation/admin/base|./transport"
			;;
		tests/go/engines/loader/features/files/application/*)
			printf '%s\n' "engines/loader|./features/files/application"
			;;
		*)
			printf '%s\n' "unsupported concurrency test file: $1" >&2
			return 1
			;;
	esac
}

run_target() {
	test_file=$1
	test_symbol=$2
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
	printf '%s\n' "==> running $test_symbol in $module $package"
	log="$work_dir/$(printf '%s' "$test_symbol" | tr '/|' '__').log"
	if ! (cd "$root/$module" && GOCACHE="$gocache" go test -count=1 -v -overlay="$overlay" \
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

status=0
python3 "$checker" --test-targets unit "$ledger" > "$work_dir/targets" || exit $?
while IFS='|' read -r test_file test_symbol; do
	[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
	case "$selector" in
		unit|all) run_target "$test_file" "$test_symbol" || status=1 ;;
	esac
done < "$work_dir/targets"

exit "$status"
