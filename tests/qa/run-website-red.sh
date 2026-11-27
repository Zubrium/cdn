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
		GOCACHE="$gocache" go test -v -tags=website_acceptance -overlay="$overlay" -run '^TestWebsiteScenarioLedger$' ./application
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
		GOCACHE="$gocache" go test -v -tags=website_acceptance -overlay="$overlay" -run "$pattern" ./...
	)
}

run_sites_http_contract_selector() {
	printf '%s\n' "==> running Website Admin HTTP contract scenarios"
	(
		cd "$root/presentation/admin/sites" || exit 1
		GOCACHE="$gocache" go test -v -tags=website_acceptance -overlay="$overlay" \
			-run '^TestWebsiteAdminHTTPAcceptanceWEB017$' ./...
	)
}

run_ui_contract_selector() {
	ui_test_file=${WEBSITE_UI_TEST_FILE:-../../tests/frontend/admin/src/pages/site/website-acceptance.test.tsx}
	if [ ! -f "$root/frontend/admin/$ui_test_file" ]; then
		printf '%s\n' "Website contract UI selector is unavailable: $ui_test_file" >&2
		printf '%s\n' "Add the dedicated Vitest file or set WEBSITE_UI_TEST_FILE to its repository test selector." >&2
		return 1
	fi

	absolute_ui_test_file="$root/frontend/admin/$ui_test_file"
	printf '%s\n' "==> running Website contract UI scenario: $ui_test_file"
	(
		cd "$root/frontend/admin" || exit 1
		# Call the repository's Vitest wrapper directly: pnpm's --filter does not
		# forward positional file filters to the custom test script.
		node "$root/tests/run-admin-tests.mjs" "$absolute_ui_test_file"
	)
}

require_integration_prerequisites() {
	missing=""
	for name in WEBSITE_TEST_DISPOSABLE WEBSITE_TEST_RUN_ID WEBSITE_TEST_POSTGRES_DSN WEBSITE_TEST_POSTGRES_MIGRATION_DSN WEBSITE_TEST_S3_BUCKET; do
		eval "value=\${$name-}"
		if [ -z "$value" ]; then
			missing="$missing $name"
		fi
	done
	if [ "${WEBSITE_TEST_S3_MOCK:-}" != "1" ]; then
		for name in WEBSITE_TEST_S3_ENDPOINT WEBSITE_TEST_S3_ACCESS_KEY WEBSITE_TEST_S3_SECRET_KEY; do
			eval "value=\${$name-}"
			if [ -z "$value" ]; then
				missing="$missing $name"
			fi
		done
	fi
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

module_for_test_file() {
	case "$1" in
		tests/go/main/*)
			printf '%s\n' "main"
			;;
		tests/go/core/*)
			printf '%s\n' "core"
			;;
		tests/go/engines/*)
			remainder=${1#tests/go/engines/}
			printf '%s\n' "engines/${remainder%%/*}"
			;;
		tests/go/presentation/*)
			remainder=${1#tests/go/presentation/}
			printf '%s\n' "presentation/${remainder%%/*}"
			;;
		tests/go/workers/*)
			remainder=${1#tests/go/workers/}
			printf '%s\n' "workers/${remainder%%/*}"
			;;
		*)
			printf '%s\n' "unsupported centralized test file: $1" >&2
			return 1
			;;
	esac
}

run_integration_targets() {
	targets="$work_dir/integration-targets"
	python3 "$checker" --test-targets integration "$ledger" > "$targets" || return $?
	if [ ! -s "$targets" ]; then
		printf '%s\n' "no substantive integration Go scenarios are registered; refusing a zero-test success" >&2
		return 1
	fi

	failed=0
	while IFS='|' read -r test_file test_symbol; do
		[ -n "$test_file" ] && [ -n "$test_symbol" ] || continue
		top_level=${test_symbol%%/*}
		module=$(module_for_test_file "$test_file") || return $?
		case "$test_symbol" in
			*/*) ;;
			*)
				printf '%s\n' "integration symbol $test_symbol must include its exact scenario subtest" >&2
				return 1
				;;
		esac
		case "$top_level" in
			TestWebsitePublicationFanoutAcceptance|TestWebsitePageRetirementAcceptance)
				tags=website_acceptance,website_orchestration_acceptance
				;;
			*) tags=website_acceptance ;;
		esac
		printf '%s\n' "==> running integration scenario $test_symbol in $module"
		log="$work_dir/$(printf '%s' "$test_symbol" | tr '/|' '__').log"
		if ! (cd "$root/$module" && GOCACHE="$gocache" go test -count=1 -v -tags="$tags" \
			-overlay="$overlay" -run "^${top_level}$" ./...) > "$log" 2>&1; then
			cat "$log" >&2
			failed=1
			continue
		fi
		cat "$log"
		if ! grep -q -- "--- PASS: $test_symbol" "$log"; then
			printf '%s\n' "integration selector $test_symbol did not produce a PASS result; refusing a zero-test success" >&2
			failed=1
		fi
	done < "$targets"
	return "$failed"
}

run_integration() {
	printf '%s\n' "==> checking disposable integration prerequisites"
	require_integration_prerequisites || return $?
	run_integration_targets || return $?
	# WEB-019 is only complete when the exact executable scenario subtest passes
	# against the disposable stack; a ledger-only pass is not sufficient.
	if ! grep -qF -- "--- PASS: TestWebsiteMediaDerivativeAcceptance/WEB-019" "$work_dir"/*.log; then
		printf '%s\n' "WEB-019 integration did not produce the required PASS subtest line; refusing a zero-test success" >&2
		return 1
	fi
	if ! grep -qF -- "--- PASS: TestWebsitePrerenderAcceptance/WEB-020" "$work_dir"/*.log; then
		printf '%s\n' "WEB-020 integration did not produce the required PASS subtest line; refusing a zero-test success" >&2
		return 1
	fi
	if ! grep -qF -- "--- PASS: TestWebsitePublicationFanoutAcceptance/WEB-021" "$work_dir"/*.log; then
		printf '%s\n' "WEB-021 integration did not produce the required PASS subtest line; refusing a zero-test success" >&2
		return 1
	fi
	if ! grep -qF -- "--- PASS: TestWebsitePageRetirementAcceptance/WEB-022" "$work_dir"/*.log; then
		printf '%s\n' "WEB-022 integration did not produce the required PASS subtest line; refusing a zero-test success" >&2
		return 1
	fi
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
		run_sites_http_contract_selector || status=1
		run_ui_contract_selector || status=1
		;;
	integration)
		run_integration || status=1
		;;
	all)
		run_go_level unit || status=1
		run_go_level contract || status=1
		run_sites_http_contract_selector || status=1
		run_ui_contract_selector || status=1
		run_integration || status=1
		;;
esac

exit "$status"
