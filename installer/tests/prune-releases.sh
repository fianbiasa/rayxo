#!/usr/bin/env bash
#
# Unit test for prune_releases() in setup.sh. Builds a throwaway releases/
# tree and checks that an update keeps the active release and one previous,
# and removes older trees and .replaced-* leftovers an install left behind.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source setup.sh for its functions; its guarded main() does not run.
# shellcheck source=/dev/null
source "$here/../setup.sh"
set +e

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/releases"/{0.8.5,0.8.6,0.8.7,0.8.8} "$tmp/releases/0.8.4.replaced-123"
# Oldest to newest mtime, so 0.8.8 is the most recent other release.
touch -t 202601010001 "$tmp/releases/0.8.5"
touch -t 202601010002 "$tmp/releases/0.8.6"
touch -t 202601010003 "$tmp/releases/0.8.7"
touch -t 202601010004 "$tmp/releases/0.8.8"
# current points at an older release on purpose: it must be kept whatever its age.
ln -sfn releases/0.8.6 "$tmp/current"

prune_releases "$tmp"

has() { [[ -e "$tmp/releases/$1" ]]; }

has 0.8.6 || fail "the active release (0.8.6) was removed"
has 0.8.8 || fail "the most recent other release (0.8.8) was removed"
has 0.8.7 && fail "an older release (0.8.7) was kept"
has 0.8.5 && fail "an older release (0.8.5) was kept"
has 0.8.4.replaced-123 && fail "a .replaced- leftover was kept"

count="$(find "$tmp/releases" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
[[ "$count" == "2" ]] || fail "expected 2 release directories, found $count"

printf 'PASS: prune_releases keeps the active release and one previous\n'
