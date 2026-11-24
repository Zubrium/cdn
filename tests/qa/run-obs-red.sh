#!/usr/bin/env sh
# Run the executable OBS-001..OBS-010 scenarios selected from the observability ledger.
set -eu

usage() {
	printf '%s\n' "usage: $0" >&2
}

if [ "$#" -ne 0 ]; then
	usage
	exit 64
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
checker="$script_dir/scenario-ledger.py"
ledger="$script_dir/obs-scenarios.json"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"

work_dir=$(mktemp -d "$TMPDIR/obs-acceptance.XXXXXX") || exit $?
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
overlay="$work_dir/overlay.json"
log="$work_dir/obs-scenarios.log"

python3 "$checker" "$ledger" || exit $?
python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

printf '%s\n' "==> running observability scenarios: TestObsScenario in pkg"
if ! (cd "$root/pkg" && GOCACHE="${GOCACHE:-$work_dir/go-build}" go test -count=1 -v -overlay="$overlay" \
	-run '^TestObsScenario$' ./...) > "$log" 2>&1; then
	cat "$log" >&2
	exit 1
fi
cat "$log"
for id in OBS-001 OBS-002 OBS-003 OBS-004 OBS-005 OBS-007 OBS-009 OBS-010; do
	if ! grep -q -- "--- PASS: TestObsScenario/$id" "$log"; then
		printf '%s\n' "observability selector produced no $id pass result; refusing a zero-test success" >&2
		exit 1
	fi
done
if [ -n "${OBS_TEST_OTEL_ENDPOINT:-}" ] && [ -n "${OBS_TEST_OQ_SEARCH_URL:-}" ]; then
	# OpenObserve v1.1.2 requires OBS_TEST_OQ_AUTH (base64 email:password).
	integration_result='PASS'
else
	integration_result='SKIP'
fi
for id in OBS-006 OBS-008; do
	if [ "$id" = 'OBS-008' ] && [ -z "${OBS_TEST_OTEL_FAILURE_ENDPOINT:-}" ]; then
		integration_result='SKIP'
	fi
	if ! grep -q -- "--- $integration_result: TestObsScenario/$id" "$log"; then
		printf '%s\n' "integration scenario $id must report $integration_result for the configured environment" >&2
		exit 1
	fi
done
