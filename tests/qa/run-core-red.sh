#!/usr/bin/env sh
# Run only Core integration scenarios selected from the Core scenario ledger.
set -eu

usage() {
	printf '%s\n' "usage: $0 {integration}" >&2
}

if [ "$#" -ne 1 ] || [ "$1" != "integration" ]; then
	usage
	exit 64
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
checker="$script_dir/scenario-ledger.py"
ledger="$script_dir/core-scenarios.json"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"

work_dir=$(mktemp -d "$TMPDIR/core-acceptance.XXXXXX") || exit $?
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
overlay="$work_dir/overlay.json"
log="$work_dir/core-integration.log"

python3 "$checker" "$ledger" || exit $?
python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

for name in TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL; do
	eval "value=\${$name-}"
	if [ -z "$value" ]; then
		printf '%s\n' "integration prerequisite is missing: $name" >&2
		printf '%s\n' "Provide a fresh disposable Core database; this runner never falls back to a live database." >&2
		exit 1
	fi
done

printf '%s\n' "==> running Core integration scenario: TestDisposableOwnerScopedTaskResult/CORE-001"
if ! (cd "$root/core" && GOCACHE="$work_dir/go-build" go test -count=1 -v -overlay="$overlay" \
	-run '^TestDisposableOwnerScopedTaskResult$' ./infrastructure/postgres) > "$log" 2>&1; then
	cat "$log" >&2
	exit 1
fi
cat "$log"
if ! grep -q -- "--- PASS: TestDisposableOwnerScopedTaskResult/CORE-001" "$log"; then
	printf '%s\n' "Core integration selector produced no CORE-001 pass result; refusing a zero-test success" >&2
	exit 1
fi
