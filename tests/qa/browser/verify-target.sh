#!/usr/bin/env bash
# Preflight for write-capable browser QA. Reads only; never prints command lines,
# configuration values, headers, environment variables, or response bodies.
set -euo pipefail

usage() {
	printf 'usage: %s <checkout> <admin-port> <public-port> [expected-build-identity]\n' "${0##*/}" >&2
}

if [ "$#" -lt 3 ] || [ "$#" -gt 4 ]; then
	usage
	exit 64
fi

expected_arg=''
if [ "$#" -eq 4 ]; then
	expected_arg=$4
fi

for tool in python3 lsof ps; do
	command -v "$tool" >/dev/null 2>&1 || {
		printf 'verify-target: %s is required\n' "$tool" >&2
		exit 1
	}
done

exec python3 - "$1" "$2" "$3" "$expected_arg" <<'PY'
import http.client
import os
import shlex
import subprocess
import sys

checkout_arg, admin_arg, public_arg, expected_arg = sys.argv[1:5]


def fail(message):
    print("verify-target: FAIL: " + message, file=sys.stderr)
    raise SystemExit(1)


def ok(message):
    print("verify-target: OK: " + message)


def parse_port(raw, label):
    if not raw.isdigit() or len(raw) > 5:
        fail(label + " port must be a decimal number in 1..65535")
    port = int(raw)
    if port < 1 or port > 65535:
        fail(label + " port must be a decimal number in 1..65535")
    return port


admin_port = parse_port(admin_arg, "admin")
public_port = parse_port(public_arg, "public")
if admin_port == public_port:
    fail("admin and public ports must differ")

working_ports = (8080, 5173, 55432)
if admin_port in working_ports or public_port in working_ports:
    fail("QA ports must not use the working ports 8080, 5173, or 55432")

checkout = os.path.realpath(checkout_arg)
if not os.path.isdir(checkout):
    fail("checkout is not a directory")

binary = os.path.realpath(os.path.join(checkout, "bin", "zubrium"))
if not os.path.isfile(binary) or not os.access(binary, os.X_OK):
    fail("checkout has no executable bin/zubrium")

config_setting = os.environ.get("QA_BROWSER_CONFIG", "main/qa-browser.yaml")
config_user_path = config_setting if os.path.isabs(config_setting) else os.path.join(checkout, config_setting)
config_path = os.path.realpath(config_user_path)
if not os.path.isfile(config_path):
    fail("checkout config is missing; set QA_BROWSER_CONFIG if it has another name")
try:
    if os.path.commonpath([config_path, checkout]) != checkout:
        fail("checkout config must be inside the checkout")
except ValueError:
    fail("checkout config path is invalid")

expected_identity = expected_arg or os.environ.get("EXPECTED_BUILD_IDENTITY", "")
if not expected_identity:
    fail("expected build identity is required (4th argument or EXPECTED_BUILD_IDENTITY)")
if any(ord(character) < 0x20 or ord(character) == 0x7f for character in expected_identity):
    fail("expected build identity contains control characters")


def run(command):
    return subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)


def listeners(port):
    result = run(["lsof", "-nP", "-t", "-iTCP:%d" % port, "-sTCP:LISTEN"])
    if result.returncode not in (0, 1):
        fail("cannot inspect listeners on port %d" % port)
    pids = []
    for field in result.stdout.split():
        if not field.isdigit():
            fail("unexpected listener output on port %d" % port)
        pid = int(field)
        if pid not in pids:
            pids.append(pid)
    return pids


busy = []
for port in working_ports:
    pids = listeners(port)
    if pids:
        busy.append((port, pids))
if busy:
    detail = ", ".join(
        "port %d (pid %s)" % (port, ",".join(str(pid) for pid in pids))
        for port, pids in busy
    )
    fail("working ports are occupied: " + detail)
ok("working ports 8080, 5173, and 55432 have no listeners")


def process_command(pid):
    result = run(["ps", "-ww", "-p", str(pid), "-o", "command="])
    if result.returncode != 0:
        fail("cannot read the command line of pid %d" % pid)
    return result.stdout.strip()


def process_cwd(pid):
    result = run(["lsof", "-a", "-p", str(pid), "-d", "cwd", "-Fn"])
    for line in result.stdout.splitlines():
        if line.startswith("n") and len(line) > 1:
            return line[1:]
    return "/"


def command_tokens(command):
    try:
        return shlex.split(command)
    except ValueError:
        return command.split()


def resolved(token, cwd):
    path = token if os.path.isabs(token) else os.path.join(cwd, token)
    return os.path.realpath(path)


def config_arguments(tokens):
    return [tokens[index + 1] for index, token in enumerate(tokens[:-1]) if token == "--config"]


def check_listener(port):
    pids = listeners(port)
    if not pids:
        fail("no listener on port %d" % port)
    if len(pids) > 1:
        fail("more than one listener on port %d" % port)
    pid = pids[0]
    tokens = command_tokens(process_command(pid))
    cwd = process_cwd(pid)
    if not any(resolved(token, cwd) == binary for token in tokens):
        fail("listener on port %d does not use the checkout bin/zubrium" % port)
    configured = config_arguments(tokens)
    if not configured or not any(resolved(token, cwd) == config_path for token in configured):
        fail("listener on port %d does not use the checkout config" % port)
    ok("port %d (pid %d) uses the checkout binary and config" % (port, pid))


check_listener(admin_port)
check_listener(public_port)


IDENTITY_HEADER = "x-zubrium-build-identity"


def fetch(port, path):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        connection.request("GET", path, headers={"Cache-Control": "no-store"})
        response = connection.getresponse()
        status = response.status
        values = [
            value
            for name, value in response.getheaders()
            if name.lower() == IDENTITY_HEADER
        ]
        response.read()
        return status, values
    except Exception:
        fail("GET %s on port %d failed" % (path, port))
    finally:
        connection.close()


def require_identity(listener, values):
    if len(values) != 1:
        fail("%s /healthz did not expose exactly one build identity header" % listener)
    if values[0] != expected_identity:
        fail("%s /healthz build identity does not match the expected value" % listener)
    ok("%s /healthz build identity matches the expected value" % listener)


admin_status, admin_values = fetch(admin_port, "/healthz")
if admin_status != 204:
    fail("GET /healthz on the admin port returned %d instead of 204" % admin_status)
ok("GET /healthz on the admin port returned 204")
require_identity("admin", admin_values)

admin_status, _ = fetch(admin_port, "/readyz")
if admin_status != 204:
    fail("GET /readyz on the admin port returned %d instead of 204" % admin_status)
ok("GET /readyz on the admin port returned 204")

public_status, public_values = fetch(public_port, "/healthz")
if public_status == 204 or public_values:
    if public_status != 204:
        fail("GET /healthz on the public port returned %d instead of 204" % public_status)
    require_identity("public", public_values)
else:
    ok("public listener does not expose /healthz; build identity check is not applicable")

ok("target verified; write QA may proceed against this checkout")
PY
