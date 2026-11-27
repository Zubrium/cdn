#!/usr/bin/env bash
# Live Website/Router publication and retirement E2E runner.
# It builds and serves a private checkout copy, verifies the target from that
# copy, runs real Playwright scenarios, and removes only resources it created.
set -euo pipefail
export GIT_OPTIONAL_LOCKS=0

usage() {
	printf 'usage: %s\n' "${0##*/}" >&2
}

fail() {
	printf 'run-website-router-e2e: %s\n' "$*" >&2
	exit 1
}

[ "$#" -eq 0 ] || { usage; exit 64; }

for tool in python3 docker git make go node npm pnpm lsof shasum; do
	command -v "$tool" >/dev/null 2>&1 || fail "required tool is missing: $tool"
done
docker_bin=$(command -v docker)
case "$(uname -m)" in
	arm64|aarch64) sandbox_platform=linux/arm64 ;;
	x86_64|amd64) sandbox_platform=linux/amd64 ;;
	*) fail "unsupported host architecture for the pinned prerender image" ;;
esac


script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
[ -f "$root/go.work" ] || fail "repository root not found: $root"

tmp_root=${TMPDIR:-/tmp}
[ -d "$tmp_root" ] || fail "TMPDIR does not exist: $tmp_root"
tmp_root=$(CDPATH= cd -- "$tmp_root" && pwd -P) || fail "cannot resolve TMPDIR"

# sanitized DOCKER_CONFIG for public image pulls in headless shells
docker_config_dir=$(mktemp -d "$tmp_root/website-e2e-docker-config.XXXXXX") || fail "cannot create Docker config dir"
chmod 700 "$docker_config_dir"
python3 - "$docker_config_dir/config.json" <<'PY' || fail "cannot create sanitized Docker config"
import json, os, pathlib, sys
target = pathlib.Path(sys.argv[1])
source = pathlib.Path(os.environ.get("DOCKER_CONFIG", pathlib.Path.home() / ".docker")) / "config.json"
cfg = {}
if source.is_file():
    try:
        cfg = json.loads(source.read_text(encoding="utf-8"))
    except Exception:
        cfg = {}
for key in ("credsStore", "credHelpers"):
    cfg.pop(key, None)
cfg["auths"] = {}
target.write_text(json.dumps(cfg), encoding="utf-8")
PY
for link in cli-plugins contexts; do
	src="$HOME/.docker/$link"
	[ -e "$src" ] && ln -s "$src" "$docker_config_dir/$link"
done
export DOCKER_CONFIG="$docker_config_dir"

run_id="e2e-$(date -u +%Y%m%dT%H%M%SZ)-$(python3 -c 'import secrets;print(secrets.token_hex(3))')"
evidence_dir="$root/output/e2e/$run_id"
mkdir -p "$evidence_dir"
chmod 700 "$evidence_dir"
run_dir=$(mktemp -d "$tmp_root/website-router-e2e.XXXXXX") || fail "cannot create run directory"
chmod 700 "$run_dir"
state_file="$run_dir/state.json"
env_file=''
docker_started=0
server_pid=''
proxy_pid=''
docker_config_dir=''

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if [ "${KEEP_E2E_ON_FAIL:-0}" = "1" ] && [ "$status" -ne 0 ]; then
		printf '==> keeping E2E run %s for debugging (run_dir=%s)
' "$run_id" "$run_dir" >&2
		exit "$status"
	fi
	if [ -n "$server_pid" ]; then
		kill "$server_pid" >/dev/null 2>&1 || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if [ -n "$proxy_pid" ]; then
		kill "$proxy_pid" >/dev/null 2>&1 || true
		wait "$proxy_pid" 2>/dev/null || true
	fi
	if [ "$docker_started" -eq 1 ]; then
		"$root/tests/qa/docker/down.sh" "$run_id" >/dev/null 2>&1 || true
	fi
	if [ -n "$env_file" ] && [ -f "$env_file" ]; then
		chmod 600 "$env_file" 2>/dev/null || true
		rm -f "$env_file" || true
	fi
	if [ -n "$docker_config_dir" ] && [ -d "$docker_config_dir" ]; then
		rm -rf "$docker_config_dir" || true
	fi
	rm -rf "$run_dir" || true
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

pick_port() {
	python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

admin_port=$(pick_port)
public_port=$(pick_port)
pg_port=$(pick_port)
s3_port=$(pick_port)
s3_console_port=$(pick_port)
for port in "$admin_port" "$public_port" "$pg_port" "$s3_port" "$s3_console_port"; do
	case "$port" in
		''|*[!0-9]*) fail "invalid allocated port: $port" ;;
	esac
done
[ "$admin_port" -ne "$public_port" ] || fail "admin and public ports collide"

printf '==> E2E run %s (evidence: %s)\n' "$run_id" "${evidence_dir#"$root"/}"

printf '==> starting disposable PostgreSQL and S3\n'
WEBSITE_TEST_RUN_ID="$run_id" \
QA_PG_PORT="$pg_port" \
QA_S3_API_PORT="$s3_port" \
QA_S3_CONSOLE_PORT="$s3_console_port" \
	"$root/tests/qa/docker/up.sh" >"$evidence_dir/docker-up.log" 2>&1 || {
		cat "$evidence_dir/docker-up.log" >&2
		fail "disposable stack failed to start"
	}
docker_started=1
env_file="$tmp_root/website-qa-${run_id}.env"
[ -f "$env_file" ] || fail "private environment file was not created"
chmod 600 "$env_file"

set -a
# shellcheck disable=SC1090
. "$env_file"
set +a

proxy_port=$(pick_port)
case "$proxy_port" in ''|*[!0-9]*) fail "invalid allocated port: $proxy_port" ;; esac
for allocated_port in "$admin_port" "$public_port" "$pg_port" "$s3_port" "$s3_console_port"; do
	[ "$proxy_port" -ne "$allocated_port" ] || fail "S3 proxy port $proxy_port collides with $allocated_port"
done

printf '==> starting S3 private-boundary proxy\n'
E2E_S3_UPSTREAM="$WEBSITE_TEST_S3_ENDPOINT" \
E2E_S3_BUCKET="$WEBSITE_TEST_S3_BUCKET" \
E2E_S3_PROXY_PORT="$proxy_port" \
E2E_S3_PROXY_LOG="$evidence_dir/s3-proxy.log" \
	python3 "$root/tests/e2e/s3-boundary-proxy.py" \
	>"$evidence_dir/s3-proxy.log" 2>&1 &
proxy_pid=$!
python3 - "$proxy_port" <<'PY' || fail "S3 boundary proxy did not become ready"
import http.client
import socket
import sys
import time

port = int(sys.argv[1])
deadline = time.monotonic() + 30
while time.monotonic() < deadline:
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=1):
            raise SystemExit(0)
    except OSError:
        time.sleep(0.2)
raise SystemExit(1)
PY
config_s3_endpoint="http://127.0.0.1:$proxy_port"

credential="e2e-${run_id}-$(python3 -c 'import secrets;print(secrets.token_hex(24))')"
export ZUBRIUM_E2E_SERVICE_CREDENTIAL="$credential"
export E2E_BOOTSTRAP_CREDENTIAL="$credential"
unset credential

printf '==> preparing separate checkout\n'
"$root/tests/qa/browser/prepare-checkout.sh" "$run_dir/checkout" \
	>"$evidence_dir/prepare-checkout.log" 2>&1 || {
		cat "$evidence_dir/prepare-checkout.log" >&2
		fail "separate checkout preparation failed"
	}
checkout="$run_dir/checkout"
[ -x "$checkout/bin/zubrium" ] || [ ! -e "$checkout/bin" ] || true

printf '==> building Admin assets in the copy\n'
(
	cd "$checkout/frontend"
	pnpm install --frozen-lockfile
	pnpm --filter @website-engine/admin build
) >"$evidence_dir/frontend-build.log" 2>&1 || {
	tail -n 40 "$evidence_dir/frontend-build.log" >&2
	fail "Admin asset build failed in the copy"
}

printf '==> building full product binary in the copy\n'
(
	cd "$checkout"
	make -C main generate-profiles
	make build-full
	mkdir -p bin
	go build -tags=full -o "$checkout/bin/zubrium" ./main/cmd/zubrium
) >"$evidence_dir/product-build.log" 2>&1 || {
	tail -n 40 "$evidence_dir/product-build.log" >&2
	fail "product build failed in the copy"
}

printf '==> installing standalone Playwright harness\n'
(
	cd "$checkout/tests/e2e"
	npm ci --no-audit --no-fund
) >"$evidence_dir/npm-ci.log" 2>&1 || {
	tail -n 40 "$evidence_dir/npm-ci.log" >&2
	fail "Playwright harness installation failed"
}

build_identity=$(python3 - "$checkout" <<'PY'
import hashlib
import pathlib
import subprocess
import sys

checkout = pathlib.Path(sys.argv[1])
head = subprocess.run(
    ["git", "rev-parse", "HEAD"], cwd=checkout, capture_output=True, text=True, check=True
).stdout.strip()
files = [
    checkout / "bin/zubrium",
    checkout / "pkg/static/dist/index.html",
    checkout / "main/internal/composition/profile_full_generated.go",
    checkout / "tests/e2e/package-lock.json",
    checkout / "tests/qa/e2e-scenarios.json",
]
digest = hashlib.sha256()
digest.update(head.encode())
for path in files:
    if not path.is_file():
        raise SystemExit("missing build artifact: " + str(path.relative_to(checkout)))
    digest.update(str(path.relative_to(checkout)).encode())
    digest.update(path.read_bytes())
print("e2e-" + digest.hexdigest()[:40])
PY
) || fail "cannot compute build identity from the copied artifacts"
export ZUBRIUM_BUILD_IDENTITY="$build_identity"

mkdir -p "$run_dir/staging/feature-import" "$run_dir/staging/prerender" "$run_dir/staging/router" "$checkout/pkg/static/dist"
chmod 700 "$run_dir/staging" "$run_dir/staging/feature-import" "$run_dir/staging/prerender" "$run_dir/staging/router"

atlas_wrapper="$run_dir/atlas-wrapper.py"
cat >"$atlas_wrapper" <<'PY'
#!/usr/bin/env python3
"""Run the pinned Atlas CLI image against the staged host migration files."""
import os
import pathlib
import subprocess
import sys
from urllib.parse import urlparse

image = "arigaio/atlas:1.3.0"
args = sys.argv[1:]
if args == ["version"]:
    command = ["docker", "run", "--rm"]
    notifier = os.environ.get("ATLAS_NO_UPDATE_NOTIFIER")
    if notifier is not None:
        command.extend(["-e", "ATLAS_NO_UPDATE_NOTIFIER=" + notifier])
    command.extend([image, "version"])
    result = subprocess.run(command, capture_output=True, text=True)
    for line in result.stdout.splitlines():
        if line.startswith("atlas ") and " version " in line:
            print(line)
            break
    if result.returncode != 0:
        sys.stderr.write(result.stderr)
    raise SystemExit(result.returncode)
try:
    config_index = args.index("--config")
    config_arg = args[config_index + 1]
except (ValueError, IndexError):
    raise SystemExit("atlas wrapper requires --config")
stage = pathlib.Path(urlparse(config_arg).path).parent
if not stage.is_dir():
    raise SystemExit("staged Atlas directory does not exist")

rewritten = []
for arg in args:
    parsed = urlparse(arg)
    if parsed.scheme == "file":
        rewritten.append("file:///atlas-stage/" + pathlib.Path(parsed.path).name)
    else:
        rewritten.append(arg)

command = [
    "docker", "run", "--rm",
    "--add-host=host.docker.internal:host-gateway",
    "-v", f"{stage}:/atlas-stage:ro",
]
for key in ("PATH", "HOME", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR",
            "ATLAS_NO_UPDATE_NOTIFIER", "ZUBRIUM_CORE_DATABASE_URL",
            "ZUBRIUM_CORE_MIGRATION_DATABASE_URL", "ZUBRIUM_CORE_MIGRATION_DIR"):
    value = os.environ.get(key)
    if value is None:
        continue
    if key.endswith("_DATABASE_URL"):
        value = value.replace("@127.0.0.1:", "@host.docker.internal:")
    if key.endswith("_DIR") and value.startswith("file://"):
        value = "file:///atlas-stage/" + pathlib.Path(urlparse(value).path).name
    command.extend(["-e", f"{key}={value}"])
command.append(image)
command.extend(rewritten)
raise SystemExit(subprocess.call(command))
PY
chmod 700 "$atlas_wrapper"

write_config() {
	target=$1
	file_set_id=$2
	bindings_json=$3
	retirement_json=$4
	python3 - "$checkout" "$target" "$admin_port" "$public_port" "$atlas_wrapper" \
		"$run_dir/staging/feature-import" "$run_dir/staging/prerender" "$run_dir/staging/router" \
		"$file_set_id" "$bindings_json" "$docker_bin" "$sandbox_platform" "$evidence_dir/observability" \
		"$config_s3_endpoint" "$WEBSITE_TEST_S3_BUCKET" "$retirement_json" <<'PY'
import json
import pathlib
import re
import sys

(
    checkout, target, admin_port, public_port, atlas,
    feature_stage, prerender_stage, router_stage, file_set_id, bindings_json,
    docker_bin, sandbox_platform, observability_dir, s3_endpoint, s3_bucket, retirement_json,
) = sys.argv[1:17]

source = (pathlib.Path(checkout) / "main/config.example.yaml").read_text(encoding="utf-8")
text = source


def replace_once(pattern, value, label):
    global text
    text, count = re.subn(pattern, lambda match: match.group(1) + value, text, count=1, flags=re.M)
    if count != 1:
        raise SystemExit("cannot patch " + label)


def yaml(value):
    return json.dumps(str(value))


replace_once(r"^(\s*listen_address:\s*).*$", "127.0.0.1:" + admin_port, "http.listen_address")
replace_once(r"^(\s*public_listen_address:\s*).*$", "127.0.0.1:" + public_port, "router.public_listen_address")
if "  - http://127.0.0.1:8080" not in text:
    raise SystemExit("cannot patch admin allowed_origins")
text = text.replace("  - http://127.0.0.1:8080", "  - http://127.0.0.1:" + admin_port, 1)
replace_once(r"^(\s*database_url_env:\s*).*$", "CORE_QA_RUNTIME_DATABASE_URL", "core database env")
replace_once(r"^(\s*migration_database_url_env:\s*).*$", "CORE_QA_MIGRATION_DATABASE_URL", "core migration env")
replace_once(r"^(\s*s3_endpoint:\s*).*$", yaml(s3_endpoint), "s3 endpoint")
replace_once(r"^(\s*s3_bucket:\s*).*$", yaml(s3_bucket), "s3 bucket")
replace_once(r"^(\s*s3_access_key_env:\s*).*$", "WEBSITE_TEST_S3_ACCESS_KEY", "s3 access env")
replace_once(r"^(\s*s3_secret_key_env:\s*).*$", "WEBSITE_TEST_S3_SECRET_KEY", "s3 secret env")
replace_once(r"^(\s*atlas_binary:\s*).*$", yaml(atlas), "atlas binary")
observability_block = (
    "observability:\n"
    "  mode: file\n"
    "  min_level: info\n"
    "  file:\n"
    "    directory: " + yaml(observability_dir) + "\n"
    "    max_file_bytes: 16777216\n"
    "    max_files: 4"
)
text, count = re.subn(
    r"observability:\n  mode: noop\n  min_level: info",
    lambda _: observability_block,
    text,
    count=1,
)
if count != 1:
    raise SystemExit("cannot patch E2E file observability")
text = text.replace("ZUBRIUM_ROUTER_SERVICE_CREDENTIAL", "ZUBRIUM_E2E_SERVICE_CREDENTIAL")
text = text.replace("ZUBRIUM_FORMS_FILE_SERVICE_CREDENTIAL", "ZUBRIUM_E2E_SERVICE_CREDENTIAL")
text = text.replace("ZUBRIUM_CONTENT_SERVICE_CREDENTIAL", "ZUBRIUM_E2E_SERVICE_CREDENTIAL")
text = text.replace("core:\n", "core:\n  bootstrap_secret_env: ZUBRIUM_E2E_SERVICE_CREDENTIAL\n", 1)
text = text.replace("  upload_staging_directory: /var/lib/zubrium/router-staging",
                    "  upload_staging_directory: " + yaml(router_stage), 1)

feature_block = "\n".join([
    "feature_import:",
    "  target_file_set_id: " + yaml(file_set_id),
    "  output_groups:",
    "    - features",
    "  staging_directory: " + yaml(feature_stage),
    "  service_credential_env: ZUBRIUM_E2E_SERVICE_CREDENTIAL",
    "  max_archive_bytes: 1048576",
    "  max_entries: 16",
    "  max_entry_bytes: 65536",
    "  max_expanded_bytes: 262144",
    "  max_compression_ratio: 100",
    "",
])
prerender_block = "\n".join([
    "prerender:",
    "  sandbox_image: zenika/alpine-chrome@sha256:1727e672844c93a15b5f32991febcf67d2d4f23ac4dbc2dec2f310c582aa36a2",
    "  sandbox_platform: " + yaml(sandbox_platform),
    "  docker_binary: " + yaml(docker_bin),
    "  staging_directory: " + yaml(prerender_stage),
    '  cpus: "1"',
    "  memory_limit_bytes: 268435456",
    "  pids_limit: 64",
    "  max_document_bytes: 1048576",
    "  max_plan_bytes: 1048576",
    "  max_artifacts: 4",
    "  max_artifact_bytes: 2097152",
    "  max_total_bytes: 8388608",
    "  timeout: 90s",
    "",
])
bindings = json.loads(bindings_json)
publication_block = ""
if bindings:
    lines = ["publication:", "  bindings:"]
    for binding in bindings:
        lines.extend([
            "    - route_id: " + yaml(binding["route_id"]),
            "      page_snapshot_id: " + yaml(binding["page_snapshot_id"]),
            "      locale: " + yaml(binding["locale"]),
        ])
    lines.extend([
        "  website_credential_env: ZUBRIUM_E2E_SERVICE_CREDENTIAL",
        "  build_timeout: 10m",
        "  poll_interval: 1s",
        "",
    ])
    publication_block = "\n".join(lines)

retirement_bindings = json.loads(retirement_json)
retirement_block = ""
if retirement_bindings:
    lines = ["retirement:", "  website_credential_env: ZUBRIUM_E2E_SERVICE_CREDENTIAL", "  bindings:"]
    for binding in retirement_bindings:
        lines.append("    - page_id: " + yaml(binding["page_id"]))
        lines.append("      route_ids:")
        for route_id in binding["route_ids"]:
            lines.append("        - " + yaml(route_id))
    lines.append("")
    retirement_block = "\n".join(lines)

if "\nhttp:\n" not in text:
    raise SystemExit("cannot locate http block")
text = text.replace("\nhttp:\n", "\n" + feature_block + prerender_block + publication_block + retirement_block + "http:\n", 1)
pathlib.Path(target).write_text(text, encoding="utf-8")
PY
}

wait_ready() {
	python3 - "$admin_port" <<'PY'
import http.client
import sys
import time

port = int(sys.argv[1])
deadline = time.monotonic() + 180
while time.monotonic() < deadline:
    connection = None
    try:
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
        connection.request("GET", "/readyz", headers={"Cache-Control": "no-store"})
        response = connection.getresponse()
        response.read()
        if response.status == 204:
            raise SystemExit(0)
    except Exception:
        pass
    finally:
        if connection is not None:
            connection.close()
    time.sleep(1)
raise SystemExit(1)
PY
}

wait_port_free() {
	python3 - "$1" <<'PY'
import subprocess
import sys
import time

port = int(sys.argv[1])
deadline = time.monotonic() + 60
while time.monotonic() < deadline:
    result = subprocess.run(["lsof", "-nP", "-t", "-iTCP:%d" % port, "-sTCP:LISTEN"],
                            capture_output=True, text=True)
    if result.returncode == 1 and not result.stdout.strip():
        raise SystemExit(0)
    time.sleep(0.5)
raise SystemExit(1)
PY
}

start_server() {
	config=$1
	phase=$2
	(
		cd "$checkout"
		exec "$checkout/bin/zubrium" --suppress-bootstrap-secret --config "$config"
	) >"$evidence_dir/server-$phase.log" 2>&1 &
	server_pid=$!
	wait_ready || {
		tail -n 40 "$evidence_dir/server-$phase.log" >&2
		if grep -q 'invalid engine data operation' "$evidence_dir/server-$phase.log"; then
			printf 'run-website-router-e2e: product composition did not register the worker/publication module descriptor; this is not an E2E scenario failure\n' >&2
		fi
		fail "server did not become ready in phase $phase"
	}
}

stop_server() {
	[ -n "$server_pid" ] || return 0
	kill "$server_pid" >/dev/null 2>&1 || true
	for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
		kill -0 "$server_pid" >/dev/null 2>&1 || break
		sleep 1
	done
	kill -9 "$server_pid" >/dev/null 2>&1 || true
	wait "$server_pid" 2>/dev/null || true
	server_pid=''
}

placeholder_file_set="00000000-0000-4000-8000-000000000001"
write_config "$run_dir/config-bootstrap.yaml" "$placeholder_file_set" '[]' '[]' || fail "cannot write bootstrap config"
cp "$run_dir/config-bootstrap.yaml" "$evidence_dir/config-bootstrap.yaml"

printf '==> starting phase 1 server (locales and FileSet bootstrap)\n'
start_server "$run_dir/config-bootstrap.yaml" bootstrap
(
	cd "$checkout/tests/e2e"
	E2E_ADMIN_URL="http://127.0.0.1:$admin_port" \
	E2E_PUBLIC_URL="http://127.0.0.1:$public_port" \
	E2E_BOOTSTRAP_CREDENTIAL="$ZUBRIUM_E2E_SERVICE_CREDENTIAL" \
	E2E_RUN_ID="$run_id" \
	E2E_STATE_FILE="$state_file" \
		node setup.mjs prepare
) >"$evidence_dir/setup-prepare.log" 2>&1 || {
	tail -n 40 "$evidence_dir/setup-prepare.log" >&2
	fail "E2E bootstrap fixtures failed"
}

file_set_id=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["fileSetID"])' "$state_file")
write_config "$run_dir/config-fixtures.yaml" "$file_set_id" '[]' '[]' || fail "cannot write fixtures config"
cp "$run_dir/config-fixtures.yaml" "$evidence_dir/config-fixtures.yaml"
stop_server
wait_port_free "$admin_port" || fail "admin port was not released before restart"
wait_port_free "$public_port" || fail "public port was not released before restart"

printf '==> starting phase 2 server (feature, snapshot, builds, routes)\n'
start_server "$run_dir/config-fixtures.yaml" fixtures
(
	cd "$checkout/tests/e2e"
	E2E_ADMIN_URL="http://127.0.0.1:$admin_port" \
	E2E_PUBLIC_URL="http://127.0.0.1:$public_port" \
	E2E_BOOTSTRAP_CREDENTIAL="$ZUBRIUM_E2E_SERVICE_CREDENTIAL" \
	E2E_RUN_ID="$run_id" \
	E2E_STATE_FILE="$state_file" \
		node setup.mjs fixtures
) >"$evidence_dir/setup-fixtures.log" 2>&1 || {
	tail -n 60 "$evidence_dir/setup-fixtures.log" >&2
	fail "E2E content fixtures failed"
}

bindings=$(python3 - "$state_file" <<'PY'
import json
import sys

state = json.load(open(sys.argv[1], encoding="utf-8"))
print(json.dumps([
    {"route_id": route["id"], "page_snapshot_id": state["snapshotID"], "locale": locale}
    for route in state["routes"] for locale in state["locales"]
]))
PY
) || fail "cannot build publication bindings"
retirement_bindings=$(python3 - "$state_file" <<'PY'
import json
import sys

state = json.load(open(sys.argv[1], encoding="utf-8"))
print(json.dumps([{"page_id": state["pageID"], "route_ids": [route["id"] for route in state["routes"]]}]))
PY
) || fail "cannot build retirement bindings"
final_config="$checkout/main/qa-e2e.yaml"
write_config "$final_config" "$file_set_id" "$bindings" "$retirement_bindings" || fail "cannot write final config"
cp "$final_config" "$evidence_dir/config-final.yaml"
stop_server
wait_port_free "$admin_port" || fail "admin port was not released before final restart"
wait_port_free "$public_port" || fail "public port was not released before final restart"

printf '==> starting final server with trusted publication bindings\n'
start_server "$final_config" final

printf '==> settling baseline publications before Playwright tests\n'
(
	cd "$checkout/tests/e2e"
	E2E_ADMIN_URL="http://127.0.0.1:$admin_port" \
	E2E_PUBLIC_URL="http://127.0.0.1:$public_port" \
	E2E_BOOTSTRAP_CREDENTIAL="$ZUBRIUM_E2E_SERVICE_CREDENTIAL" \
	E2E_RUN_ID="$run_id" \
	E2E_STATE_FILE="$state_file" \
		node setup.mjs baseline
) >"$evidence_dir/setup-baseline.log" 2>&1 || {
	tail -n 60 "$evidence_dir/setup-baseline.log" >&2
	fail "E2E baseline settlement failed"
}

printf '==> verifying target build identity and listener ownership\n'
QA_BROWSER_CONFIG="$final_config" \
EXPECTED_BUILD_IDENTITY="$build_identity" \
	"$root/tests/qa/browser/verify-target.sh" "$checkout" "$admin_port" "$public_port" \
	>"$evidence_dir/verify-target.log" 2>&1 || {
		cat "$evidence_dir/verify-target.log" >&2
		fail "target verification failed"
	}

printf '==> running Playwright scenarios through Go ledger shim\n'
set +e
(
	cd "$checkout/tests/e2e"
	E2E_ADMIN_URL="http://127.0.0.1:$admin_port" \
	E2E_PUBLIC_URL="http://127.0.0.1:$public_port" \
	E2E_EXPECTED_BUILD_IDENTITY="$build_identity" \
	E2E_BOOTSTRAP_CREDENTIAL="$ZUBRIUM_E2E_SERVICE_CREDENTIAL" \
	E2E_RUN_ID="$run_id" \
	E2E_STATE_FILE="$state_file" \
		GOWORK=off go test -v -count=1 ./...
) >"$evidence_dir/playwright.log" 2>&1
test_status=$?
set -e

for scenario in E2E-PUB-001 E2E-RET-001 E2E-LOC-001; do
	# Go's t.Logf indents the Playwright reporter's output; require the exact
	# PASS line apart from that harness indentation.
	if ! grep -Eq "^[[:space:]]*PASS $scenario[[:space:]]*$" "$evidence_dir/playwright.log"; then
		printf 'run-website-router-e2e: missing exact PASS line for %s\n' "$scenario" >&2
		printf '==> relevant E2E output\n' >&2
		grep -E '^(PASS|FAIL|--- (PASS|FAIL)|[[:space:]]+--- (PASS|FAIL))' "$evidence_dir/playwright.log" >&2 || true
		fail "live E2E assertion failed ($scenario)"
	fi
done
[ "$test_status" -eq 0 ] || fail "Go/Playwright runner exited with status $test_status"

printf '==> E2E suite passed (run %s)\n' "$run_id"
