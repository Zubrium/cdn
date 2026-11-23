#!/usr/bin/env bash
# Full repository release sweep. This script is deliberately explicit: optional
# external resources are enabled only by flags and a named private env file.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

INTEGRATION_ENV=""
WITH_E2E=0
REPORT_PREFIX="output/test-report-release"

usage() {
	cat <<'EOF'
Usage: tests/qa/run-release-checks.sh [options]

Options:
  --integration-env FILE   Source a private disposable-integration env file and
                           run the unified integration gates. Values are never printed.
  --with-e2e               Run the live website/router E2E harness. Requires the
                           harness to exist; missing prerequisites are a hard failure.
  --report-prefix PREFIX   Report path prefix (default: output/test-report-release).
  -h, --help               Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--integration-env)
			[ "$#" -ge 2 ] || { printf '%s\n' "--integration-env requires a file path" >&2; exit 2; }
			INTEGRATION_ENV=$2
			shift 2
			;;
		--with-e2e)
			WITH_E2E=1
			shift
			;;
		--report-prefix)
			[ "$#" -ge 2 ] || { printf '%s\n' "--report-prefix requires a path" >&2; exit 2; }
			REPORT_PREFIX=$2
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			printf '%s\n' "unknown argument: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

run() {
	printf '\n==> %s\n' "$*"
	"$@"
}

run_with_integration_env() {
	(
		set +u
		set -a
		. "$INTEGRATION_ENV"
		set +a
		set -u
		"$@"
	)
}

section() {
	printf '\n===== %s =====\n' "$1"
}

if [ -n "$INTEGRATION_ENV" ]; then
	[ -f "$INTEGRATION_ENV" ] || { printf '%s\n' "integration env file does not exist" >&2; exit 2; }
	# shellcheck disable=SC1090
	set +u
	. "$INTEGRATION_ENV"
	set -u
	for required in WEBSITE_TEST_DISPOSABLE WEBSITE_TEST_RUN_ID WEBSITE_TEST_POSTGRES_DSN \
		WEBSITE_TEST_POSTGRES_MIGRATION_DSN WEBSITE_TEST_S3_BUCKET; do
		[ -n "${!required:-}" ] || { printf '%s\n' "missing required integration variable: $required" >&2; exit 2; }
	done
	[ "$WEBSITE_TEST_DISPOSABLE" = 1 ] || { printf '%s\n' "WEBSITE_TEST_DISPOSABLE must equal 1" >&2; exit 2; }
	if [ "${WEBSITE_TEST_S3_MOCK:-}" != 1 ]; then
		for required in WEBSITE_TEST_S3_ENDPOINT WEBSITE_TEST_S3_ACCESS_KEY WEBSITE_TEST_S3_SECRET_KEY; do
			[ -n "${!required:-}" ] || { printf '%s\n' "missing required integration variable: $required" >&2; exit 2; }
		done
	fi
fi

section 'preflight'
run git diff --check
run git rev-parse --abbrev-ref HEAD
run git rev-parse HEAD

section 'architecture and Go sweep'
run make lint-core-boundary
run make vet
run make test
run make test-full
run make test-router
run make test-dev-fixtures
run make build
run make build-full
run make build-router
run make build-dev-fixtures

section 'product binary smoke build'
binary_dir=$(mktemp -d)
trap 'rm -rf "$binary_dir"' EXIT
run go build -buildvcs=false -tags=full -o "$binary_dir/zubrium" ./main/cmd/zubrium
[ -x "$binary_dir/zubrium" ] || { printf '%s\n' 'product binary was not produced' >&2; exit 1; }

section 'frontend sweep'
run pnpm --dir frontend typecheck
run pnpm --dir frontend lint
run pnpm --dir frontend test
run pnpm --dir frontend build

section 'ledgers and runner infrastructure'
run env PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests/qa/test_run_test_suite.py tests/qa/test_scenario_ledger.py
run python3 tests/qa/scenario-ledger.py tests/qa/website-scenarios.json
run python3 tests/qa/scenario-ledger.py tests/qa/core-scenarios.json
run tests/qa/run-website-red.sh contract

section 'unified QA gates'
run python3 tests/qa/run-test-suite.py ci --with-build --report "${REPORT_PREFIX}.json"
if [ -n "$INTEGRATION_ENV" ]; then
	printf '\n==> python3 tests/qa/run-test-suite.py ci --with-build --integration --report %s\n' "${REPORT_PREFIX}-integration.json"
	run_with_integration_env python3 tests/qa/run-test-suite.py ci --with-build --integration --report "${REPORT_PREFIX}-integration.json"
fi

if [ "$WITH_E2E" -eq 1 ]; then
	section 'live website/router E2E'
	e2e_runner=tests/qa/run-website-router-e2e.sh
	if [ ! -x "$e2e_runner" ]; then
		printf '%s\n' "$e2e_runner is missing; live E2E is a release prerequisite, not an optional skip" >&2
		exit 2
	fi
	run "$e2e_runner"
fi

printf '\nrelease checks completed successfully\n'
