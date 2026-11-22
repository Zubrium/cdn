#!/usr/bin/env sh
# Remove only the containers, network and tmpfs data of one disposable QA run.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file="$script_dir/compose.yaml"
tmp_root=${TMPDIR:-/tmp}

usage() {
	printf '%s\n' "usage: $0 [RUN_ID]" >&2
	printf '%s\n' "RUN_ID defaults to WEBSITE_TEST_RUN_ID; no wildcards or global cleanup are accepted." >&2
}

fail() {
	printf '%s\n' "down: $*" >&2
	exit 1
}

[ "$#" -le 1 ] || { usage; exit 64; }
command -v docker >/dev/null 2>&1 || fail "docker is not available"
[ -f "$compose_file" ] || fail "compose file not found: $compose_file"
[ -d "$tmp_root" ] || fail "TMPDIR does not exist: $tmp_root"

normalize_run_id() {
	printf '%s' "$1" |
		tr '[:upper:]' '[:lower:]' |
		tr -c 'a-z0-9_-' '-' |
		sed 's/^-*//; s/-*$//'
}

read_env_value() {
	sed -n "s/^$1=//p" "$2" | sed -n '1p'
}

raw_run_id=${1:-${WEBSITE_TEST_RUN_ID:-}}
[ -n "$raw_run_id" ] || { usage; exit 64; }
run_id=$(normalize_run_id "$raw_run_id")
[ -n "$run_id" ] || fail "RUN_ID has no usable characters"
project_name="website-qa-$run_id"
env_file="$tmp_root/website-qa-${run_id}.env"

if [ -L "$env_file" ]; then
	fail "refusing a symlinked private env file: $env_file"
fi

if [ -f "$env_file" ]; then
	chmod 600 "$env_file" || fail "cannot restrict permissions on the private env file"
	recorded_run_id=$(read_env_value WEBSITE_TEST_RUN_ID "$env_file")
	[ "$recorded_run_id" = "$run_id" ] || fail "private env file belongs to another run"
	set -- --env-file "$env_file"
	using_private_env=1
else
	# Compose needs non-secret interpolation values even when only tearing down.
	umask 077
	placeholder_env=$(mktemp "$tmp_root/website-qa-${run_id}.down.XXXXXX") || fail "cannot create a temporary env file"
	trap 'rm -f "$placeholder_env"' EXIT HUP INT TERM
	cat > "$placeholder_env" <<EOF
WEBSITE_TEST_RUN_ID=$run_id
COMPOSE_PROJECT_NAME=$project_name
QA_PG_PORT=55432
QA_S3_API_PORT=59000
QA_S3_CONSOLE_PORT=59001
CORE_QA_POSTGRES_PASSWORD=teardown-placeholder
CORE_QA_RUNTIME_PASSWORD=teardown-placeholder
CORE_QA_MIGRATION_PASSWORD=teardown-placeholder
CORE_QA_ELEVATED_PASSWORD=teardown-placeholder
CORE_QA_OWNER_RESULT_DATABASE=zubrium_core_qa_owner_result_$run_id
QA_S3_ACCESS_KEY=TEARDOWNPLACEHOLDER
QA_S3_SECRET_KEY=teardown-placeholder
EOF
	set -- --env-file "$placeholder_env"
	using_private_env=0
fi

printf '%s\n' "==> removing disposable QA stack (run: $run_id)"
docker compose "$@" -f "$compose_file" --project-name "$project_name" \
	down --volumes --remove-orphans

trap - EXIT HUP INT TERM
if [ "$using_private_env" -eq 1 ]; then
	rm -f "$env_file"
	printf '%s\n' "==> removed private env file for run $run_id"
else
	rm -f "$placeholder_env"
fi
printf '%s\n' "==> disposable QA stack removed (run: $run_id)"
