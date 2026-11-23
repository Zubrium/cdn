#!/usr/bin/env sh
# Start a run-scoped disposable PostgreSQL + MinIO stack for integration QA.
# Credentials are generated into a 0600 file under TMPDIR and are never printed.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file="$script_dir/compose.yaml"
bucket_helper="$script_dir/ensure-bucket.py"
tmp_root=${TMPDIR:-/tmp}
wait_timeout=${WEBSITE_QA_WAIT_TIMEOUT:-120}

usage() {
	printf '%s\n' "usage: $0" >&2
}

fail() {
	printf '%s\n' "up: $*" >&2
	exit 1
}

[ "$#" -eq 0 ] || { usage; exit 64; }
command -v docker >/dev/null 2>&1 || fail "docker is not available"
command -v python3 >/dev/null 2>&1 || fail "python3 is not available"
[ -f "$compose_file" ] || fail "compose file not found: $compose_file"
[ -f "$bucket_helper" ] || fail "bucket helper not found: $bucket_helper"
[ -x "$script_dir/atlas-wrapper.py" ] || fail "pinned Atlas wrapper is missing or not executable"
[ -d "$tmp_root" ] || fail "TMPDIR does not exist: $tmp_root"
case "$wait_timeout" in
	''|*[!0-9]*) fail "WEBSITE_QA_WAIT_TIMEOUT must be a positive integer" ;;
esac
[ "$wait_timeout" -gt 0 ] || fail "WEBSITE_QA_WAIT_TIMEOUT must be a positive integer"

random_hex() {
	python3 -c 'import secrets, sys; print(secrets.token_hex(int(sys.argv[1])))' "$1"
}

normalize_run_id() {
	printf '%s' "$1" |
		tr '[:upper:]' '[:lower:]' |
		tr -c 'a-z0-9_-' '-' |
		sed 's/^-*//; s/-*$//'
}

read_env_value() {
	sed -n "s/^$1=//p" "$2" | sed -n '1p'
}

require_env_keys() {
	file=$1
	shift
	for key in "$@"; do
		[ -n "$(read_env_value "$key" "$file")" ] || fail "private env file is missing $key"
	done
}

all_env_keys="WEBSITE_TEST_RUN_ID COMPOSE_PROJECT_NAME WEBSITE_TEST_DISPOSABLE \
QA_PG_PORT QA_S3_API_PORT QA_S3_CONSOLE_PORT \
CORE_QA_POSTGRES_PASSWORD CORE_QA_RUNTIME_PASSWORD CORE_QA_MIGRATION_PASSWORD CORE_QA_ELEVATED_PASSWORD \
CORE_QA_OWNER_RESULT_DATABASE TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL \
QA_S3_ACCESS_KEY QA_S3_SECRET_KEY WEBSITE_TEST_S3_ACCESS_KEY WEBSITE_TEST_S3_SECRET_KEY \
WEBSITE_TEST_S3_ENDPOINT WEBSITE_TEST_S3_BUCKET WEBSITE_TEST_S3_REGION \
WEBSITE_TEST_POSTGRES_DSN WEBSITE_TEST_POSTGRES_MIGRATION_DSN \
TEST_ATLAS_BINARY \
CORE_QA_POSTGRES_DSN CORE_QA_RUNTIME_DATABASE_URL CORE_QA_MIGRATION_DATABASE_URL \
TEST_CORE_PLATFORM_DATABASE_URL TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL TEST_CORE_PLATFORM_ADMIN_DATABASE_URL \
TEST_CORE_PLATFORM_S3_ENDPOINT TEST_CORE_PLATFORM_S3_BUCKET TEST_CORE_PLATFORM_S3_ACCESS_KEY TEST_CORE_PLATFORM_S3_SECRET_KEY \
TEST_CORE_S3_ENDPOINT TEST_CORE_S3_BUCKET TEST_CORE_S3_ACCESS_KEY TEST_CORE_S3_SECRET_KEY"

raw_run_id=${WEBSITE_TEST_RUN_ID:-}
if [ -z "$raw_run_id" ]; then
	raw_run_id="qa-$(date -u +%Y%m%dT%H%M%SZ)-$(random_hex 4)"
fi
run_id=$(normalize_run_id "$raw_run_id")
[ -n "$run_id" ] || fail "WEBSITE_TEST_RUN_ID has no usable characters"
project_name="website-qa-$run_id"
env_file="$tmp_root/website-qa-${run_id}.env"
[ ! -L "$env_file" ] || fail "refusing a symlinked private env file: $env_file"

if [ -e "$env_file" ]; then
	[ -f "$env_file" ] || fail "private env file is not a regular file: $env_file"
	chmod 600 "$env_file" || fail "cannot restrict permissions on the private env file"
	recorded_run_id=$(read_env_value WEBSITE_TEST_RUN_ID "$env_file")
	[ "$recorded_run_id" = "$run_id" ] || fail "existing private env file belongs to another run"
	require_env_keys "$env_file" $all_env_keys
else
	pg_port=${QA_PG_PORT:-55433}
	s3_api_port=${QA_S3_API_PORT:-59000}
	s3_console_port=${QA_S3_CONSOLE_PORT:-59001}
	for port in "$pg_port" "$s3_api_port" "$s3_console_port"; do
		case "$port" in
			''|*[!0-9]*) fail "QA ports must be integers" ;;
		esac
		[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail "QA ports must be in 1..65535"
	done
	# Reserved working ports must never be occupied by a disposable QA stack;
	# tests/qa/browser/verify-target.sh enforces the same invariant.
	for port in "$pg_port" "$s3_api_port" "$s3_console_port"; do
		case "$port" in
			8080|5173|55432) fail "QA port $port collides with a reserved working port" ;;
		esac
	done

	pg_password=$(random_hex 24)
	runtime_password=$(random_hex 24)
	migration_password=$(random_hex 24)
	elevated_password=$(random_hex 24)
	s3_access_key="QA$(random_hex 12)"
	s3_secret_key=$(random_hex 24)
	bucket="zubrium-core-qa-$run_id"
	owner_result_db="zubrium_core_qa_owner_result_$run_id"
	[ "${#owner_result_db}" -le 63 ] || fail "run ID is too long for the disposable owner-result database name"
	repo_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd -P) || fail "cannot resolve repository root"

	umask 077
	tmp_env=$(mktemp "$tmp_root/website-qa-${run_id}.env.XXXXXX") || fail "cannot create a temporary env file"
	trap 'rm -f "$tmp_env"' EXIT HUP INT TERM
	cat > "$tmp_env" <<EOF
WEBSITE_TEST_RUN_ID=$run_id
COMPOSE_PROJECT_NAME=$project_name
WEBSITE_TEST_DISPOSABLE=1
QA_PG_PORT=$pg_port
QA_S3_API_PORT=$s3_api_port
QA_S3_CONSOLE_PORT=$s3_console_port
CORE_QA_POSTGRES_PASSWORD=$pg_password
CORE_QA_RUNTIME_PASSWORD=$runtime_password
CORE_QA_MIGRATION_PASSWORD=$migration_password
CORE_QA_ELEVATED_PASSWORD=$elevated_password
CORE_QA_OWNER_RESULT_DATABASE=$owner_result_db
TEST_CORE_OWNER_TASK_RESULT_DATABASE_URL=postgres://core_qa_migrator:$migration_password@127.0.0.1:$pg_port/$owner_result_db?sslmode=disable
QA_S3_ACCESS_KEY=$s3_access_key
QA_S3_SECRET_KEY=$s3_secret_key
WEBSITE_TEST_S3_ACCESS_KEY=$s3_access_key
WEBSITE_TEST_S3_SECRET_KEY=$s3_secret_key
WEBSITE_TEST_S3_ENDPOINT=http://127.0.0.1:$s3_api_port
WEBSITE_TEST_S3_BUCKET=$bucket
WEBSITE_TEST_S3_REGION=us-east-1
WEBSITE_TEST_POSTGRES_DSN=postgres://core_qa_runtime:$runtime_password@127.0.0.1:$pg_port/zubrium_core_qa_platform?sslmode=disable
WEBSITE_TEST_POSTGRES_MIGRATION_DSN=postgres://core_qa_migrator:$migration_password@127.0.0.1:$pg_port/zubrium_core_qa_platform?sslmode=disable
TEST_ATLAS_BINARY="$repo_root/tests/qa/docker/atlas-wrapper.py"
CORE_QA_POSTGRES_DSN=postgres://core_qa_admin:$pg_password@127.0.0.1:$pg_port/postgres?sslmode=disable
CORE_QA_RUNTIME_DATABASE_URL=postgres://core_qa_runtime:$runtime_password@127.0.0.1:$pg_port/zubrium_core_qa_platform?sslmode=disable
CORE_QA_MIGRATION_DATABASE_URL=postgres://core_qa_migrator:$migration_password@127.0.0.1:$pg_port/zubrium_core_qa_platform?sslmode=disable
TEST_CORE_PLATFORM_DATABASE_URL=postgres://core_qa_runtime:$runtime_password@127.0.0.1:$pg_port/zubrium_core_qa_platform?sslmode=disable
TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL=postgres://core_qa_migrator:$migration_password@127.0.0.1:$pg_port/zubrium_core_qa_platform?sslmode=disable
TEST_CORE_PLATFORM_ADMIN_DATABASE_URL=postgres://core_qa_elevated:$elevated_password@127.0.0.1:$pg_port/zubrium_core_qa_bootstrap?sslmode=disable
TEST_CORE_PLATFORM_S3_ENDPOINT=http://127.0.0.1:$s3_api_port
TEST_CORE_PLATFORM_S3_BUCKET=$bucket
TEST_CORE_PLATFORM_S3_ACCESS_KEY=$s3_access_key
TEST_CORE_PLATFORM_S3_SECRET_KEY=$s3_secret_key
TEST_CORE_S3_ENDPOINT=http://127.0.0.1:$s3_api_port
TEST_CORE_S3_BUCKET=$bucket
TEST_CORE_S3_ACCESS_KEY=$s3_access_key
TEST_CORE_S3_SECRET_KEY=$s3_secret_key
EOF

	if ln "$tmp_env" "$env_file" 2>/dev/null; then
		:
	else
		[ -e "$env_file" ] || fail "cannot create the private env file"
		[ ! -L "$env_file" ] || fail "refusing a symlinked private env file: $env_file"
		recorded_run_id=$(read_env_value WEBSITE_TEST_RUN_ID "$env_file")
		[ "$recorded_run_id" = "$run_id" ] || fail "existing private env file belongs to another run"
		require_env_keys "$env_file" $all_env_keys
	fi
	rm -f "$tmp_env"
	trap - EXIT HUP INT TERM
	chmod 600 "$env_file" || fail "cannot restrict permissions on the private env file"
fi

cleanup_on_failure() {
	status=$?
	trap - EXIT HUP INT TERM
	if [ "$status" -ne 0 ]; then
		docker compose --env-file "$env_file" -f "$compose_file" \
			--project-name "$project_name" down --volumes --remove-orphans >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup_on_failure EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

printf '%s\n' "==> starting disposable QA stack (run: $run_id)"
[ "$raw_run_id" = "$run_id" ] || printf '%s\n' "==> normalized run ID to: $run_id"
docker compose --env-file "$env_file" -f "$compose_file" \
	--project-name "$project_name" up -d --wait --wait-timeout "$wait_timeout"

printf '%s\n' "==> preparing disposable MinIO bucket"
s3_endpoint=$(read_env_value WEBSITE_TEST_S3_ENDPOINT "$env_file")
s3_bucket=$(read_env_value WEBSITE_TEST_S3_BUCKET "$env_file")
s3_access_key=$(read_env_value WEBSITE_TEST_S3_ACCESS_KEY "$env_file")
s3_secret_key=$(read_env_value WEBSITE_TEST_S3_SECRET_KEY "$env_file")
WEBSITE_TEST_S3_ENDPOINT="$s3_endpoint" \
WEBSITE_TEST_S3_BUCKET="$s3_bucket" \
WEBSITE_TEST_S3_ACCESS_KEY="$s3_access_key" \
WEBSITE_TEST_S3_SECRET_KEY="$s3_secret_key" \
	python3 "$bucket_helper"

trap - EXIT HUP INT TERM
printf '%s\n' "==> disposable QA stack ready (run: $run_id)"
printf '%s\n' "private env file: $env_file"
printf '%s\n' "load it with: set -a; . \"$env_file\"; set +a"
printf '%s\n' "teardown: \"$script_dir/down.sh\" $run_id"
