#!/usr/bin/env sh
# Run the full Core Go verification set with centralized tests mounted through
# the standard overlay. Credentials and DSNs are read from QA_ENV_FILE and are
# never printed or stored by this script.
set -eu

if [ "$#" -ne 1 ]; then
	printf '%s\n' "usage: QA_ENV_FILE=/path/to/private/env $0 run" >&2
	exit 64
fi
if [ "$1" != "run" ]; then
	printf '%s\n' "unknown command: $1" >&2
	exit 64
fi
: "${QA_ENV_FILE:?set QA_ENV_FILE to a private environment file}"

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
. "$QA_ENV_FILE"

overlay=$(mktemp "${TMPDIR:-/tmp}/core-overlay.XXXXXX")
trap 'rm -f "$overlay"' EXIT HUP INT TERM
python3 "$root/tests/go-overlay.py" > "$overlay"

cd "$root/core"
go test -count=1 -overlay="$overlay" ./...
go vet -overlay="$overlay" ./...
go build ./...
