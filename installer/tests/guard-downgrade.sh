#!/usr/bin/env bash
#
# Unit test for release_older_than() in setup.sh, the version comparison that
# guard_downgrade uses to refuse a downgrade below the release that introduced
# the "rayxod cron-scope" PAM hook.
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

# older: $1 must be older than $2.
older() {
    release_older_than "$1" "$2" || fail "$1 should be older than $2"
}

# not_older: $1 must not be older than $2 (newer or equal).
not_older() {
    release_older_than "$1" "$2" && fail "$1 should not be older than $2"
}

older 0.7.1 0.7.2
older 0.6.0 0.7.2
older 0.7.1-dev 0.7.2      # the -suffix on the base 0.7.1 is still older
not_older 0.7.2 0.7.2      # equal is not older
not_older 0.7.2-dev 0.7.2  # the -suffix is ignored, so this is equal
not_older 0.7.10 0.7.2     # 0.7.10 is newer than 0.7.2, not an alphabetic compare
not_older 0.8.0 0.7.2
not_older 1.0.0 0.7.2

printf 'PASS: release_older_than orders releases by version\n'
