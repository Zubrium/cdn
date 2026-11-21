#!/usr/bin/env sh
# Run only Website acceptance scenarios selected from the scenario ledger.
set -u

usage() {
	printf '%s\n' "usage: $0 {unit|contract|integration|all}" >&2
}

if [ "$#" -ne 1 ]; then
	usage
	exit 64
fi

selector=$1
case "$selector" in
	unit|contract|integration|all) ;;
	*)
		usage
		exit 64
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
ledger="$script_dir/website-scenarios.json"
checker="$script_dir/scenario-ledger.py"
: "${TMPDIR:?TMPDIR must be set for temporary acceptance overlays}"
work_dir=$(mktemp -d "$TMPDIR/website-acceptance.XXXXXX") || exit $?
overlay="$work_dir/overlay.json"
gocache="$work_dir/go-build"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

run_ledger_validator() {
	printf '%s\n' "==> validating Website scenario ledger"
	python3 "$checker" "$ledger" || return $?

	printf '%s\n' "==> running TestWebsiteScenarioLedger"
	(
		cd "$root/engines/website" || exit 1
		GOCACHE="$gocache" go test -tags=website_acceptance -overlay="$overlay" -run '^TestWebsiteScenarioLedger$' ./application
	)
}

selected_test_pattern() {
	level=$1
	python3 "$checker" --test-symbols "$level" "$ledger" |
	while IFS= read -r symbol; do
		[ "$symbol" = "TestWebsiteScenarioLedger" ] && continue
		[ -n "$symbol" ] && printf '%s\n' "$symbol"
	done |
	sort -u |
	paste -sd '|' - |
	sed 's/^/^(/; s/$/)$/'
}

run_go_level() {
	level=$1
	pattern=$(selected_test_pattern "$level") || return $?
	if [ "$pattern" = "^()$" ] || [ -z "$pattern" ]; then
		printf '%s\n' "no substantive $level Go scenarios are registered; refusing a zero-test success" >&2
		return 1
	fi

	# Exact top-level names come from test_symbol, so a level selector cannot run
	# every TestWebsiteScenario* function. The ledger validator runs separately.
	printf '%s\n' "==> running $level Go scenarios: $pattern"
	(
		cd "$root/engines/website" || exit 1
		GOCACHE="$gocache" go test -tags=website_acceptance -overlay="$overlay" -run "$pattern" ./...
	)
}

run_ui_contract_selector() {
	ui_test_file=${WEBSITE_UI_TEST_FILE:-../../tests/frontend/admin/src/pages/site/website-acceptance.test.tsx}
	if [ ! -f "$root/frontend/admin/$ui_test_file" ]; then
		printf '%s\n' "Website contract UI selector is unavailable: $ui_test_file" >&2
		printf '%s\n' "Add the dedicated Vitest file or set WEBSITE_UI_TEST_FILE to its repository test selector." >&2
		return 1
	fi

	printf '%s\n' "==> running Website contract UI scenario: $ui_test_file"
	(
		cd "$root/frontend" || exit 1
		pnpm --filter @website-engine/admin test -- "$ui_test_file"
	)
}

require_integration_prerequisites() {
	missing=""
	for name in WEBSITE_TEST_DISPOSABLE WEBSITE_TEST_RUN_ID WEBSITE_TEST_POSTGRES_DSN WEBSITE_TEST_S3_ENDPOINT WEBSITE_TEST_S3_BUCKET WEBSITE_TEST_S3_ACCESS_KEY WEBSITE_TEST_S3_SECRET_KEY; do
		eval "value=\${$name-}"
		if [ -z "$value" ]; then
			missing="$missing $name"
		fi
	done
	if [ -n "$missing" ]; then
		printf '%s\n' "integration prerequisites are missing:$missing" >&2
		printf '%s\n' "Set only run-specific disposable PostgreSQL/S3 inputs; this runner never falls back to a live API, database, bucket, or credentials." >&2
		return 1
	fi
	if [ "$WEBSITE_TEST_DISPOSABLE" != "1" ]; then
		printf '%s\n' "WEBSITE_TEST_DISPOSABLE must equal 1 for integration acceptance" >&2
		return 1
	fi
	case "$WEBSITE_TEST_RUN_ID" in
		*[!A-Za-z0-9._-]*|'')
			printf '%s\n' "WEBSITE_TEST_RUN_ID must be a non-empty run-specific token using A-Za-z0-9._-" >&2
			return 1
			;;
	esac
}

run_integration() {
	printf '%s\n' "==> checking disposable integration prerequisites"
	require_integration_prerequisites || return $?
	run_go_level integration
}

python3 "$root/tests/go-overlay.py" > "$overlay" || exit $?

status=0
run_ledger_validator || status=1
case "$selector" in
	unit)
		run_go_level unit || status=1
		;;
	contract)
		run_go_level contract || status=1
		run_ui_contract_selector || status=1
		;;
	integration)
		run_integration || status=1
		;;
	all)
		run_go_level unit || status=1
		run_go_level contract || status=1
		run_ui_contract_selector || status=1
		run_integration || status=1
		;;
esac

exit "$status"
