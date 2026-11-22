#!/usr/bin/env bash
# Prepare a run-specific, fully isolated copy of this checkout for browser QA.
# The source tree is only read: git optional locks are disabled so status cannot
# refresh its index, and ditto writes exclusively to the target.
set -euo pipefail
export GIT_OPTIONAL_LOCKS=0

fail() {
	printf 'prepare-checkout: %s\n' "$*" >&2
	exit 1
}

usage() {
	printf 'usage: %s [target-dir]\n' "${0##*/}" >&2
}

if [ "$#" -gt 1 ]; then
	usage
	exit 64
fi

command -v ditto >/dev/null 2>&1 || fail 'ditto is required (macOS)'
command -v git >/dev/null 2>&1 || fail 'git is required'

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
src=$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)

top=$(git --no-optional-locks -C "$src" rev-parse --show-toplevel 2>/dev/null) || fail "not a git checkout: $src"
top=$(CDPATH= cd -- "$top" && pwd -P)
[ "$top" = "$src" ] || fail 'the script must live at the checkout root'
[ ! -f "$src/.git" ] || fail 'linked worktrees are not supported; prepare a full checkout'

created_parent=''
if [ "$#" -eq 1 ]; then
	raw_target=$1
	[ -n "$raw_target" ] || fail 'target directory is empty'
	parent=$(dirname -- "$raw_target")
	parent_abs=$(CDPATH= cd -- "$parent" 2>/dev/null && pwd -P) || fail "target parent does not exist: $parent"
	base=$(basename -- "$raw_target")
	case "$base" in
		''|.|..) fail "invalid target name: $base" ;;
	esac
	target="$parent_abs/$base"
else
	tmp=${TMPDIR:-/tmp}
	[ -d "$tmp" ] || fail "TMPDIR is not a directory: $tmp"
	created_parent=$(mktemp -d "$tmp/website-engine-qa.XXXXXX") || fail 'cannot create the run directory'
	target="$created_parent/checkout"
fi

if [ -e "$target" ] || [ -L "$target" ]; then
	fail "target already exists: $target"
fi
case "$target/" in
	"$src"/*) fail 'target must be outside the source checkout' ;;
esac

cleanup_on_failure() {
	rc=$?
	trap - EXIT HUP INT TERM
	if [ "$rc" -ne 0 ] && [ -n "$created_parent" ] && [ -d "$created_parent" ]; then
		rm -rf "$created_parent"
	fi
	exit "$rc"
}
trap cleanup_on_failure EXIT HUP INT TERM

head_of() { git --no-optional-locks -C "$1" rev-parse --verify HEAD 2>/dev/null; }
status_of() { git --no-optional-locks -C "$1" status --porcelain=v1 2>/dev/null; }

before_head=$(head_of "$src") || fail 'cannot read the source HEAD'
before_status=$(status_of "$src") || fail 'cannot read the source git status'

ditto "$src" "$target" || fail 'ditto failed'
chmod 700 "$target" || fail 'cannot restrict permissions on the copy'

after_head=$(head_of "$src") || fail 'cannot re-read the source HEAD'
after_status=$(status_of "$src") || fail 'cannot re-read the source git status'
[ "$after_head" = "$before_head" ] || fail 'source HEAD changed during the copy'
[ "$after_status" = "$before_status" ] || fail 'source working tree changed during the copy'

copy_head=$(head_of "$target") || fail 'the copy has no usable git metadata'
copy_status=$(status_of "$target") || fail 'cannot read the copied git status'
[ "$copy_head" = "$before_head" ] || fail 'copied HEAD differs from the source'
[ "$copy_status" = "$before_status" ] || fail 'copied working tree differs from the source'

status_count=$(printf '%s\n' "$before_status" | grep -c . || true)
printf 'prepare-checkout: HEAD %.12s, %s status entries; source unchanged, copy matches\n' \
	"$before_head" "$status_count" >&2
printf '%s\n' "$target"
