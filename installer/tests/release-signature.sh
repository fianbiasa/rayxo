#!/usr/bin/env bash
#
# Unit test for gpg_verify_checksum() in setup.sh (the core of the release
# signature check verify_release_signature() performs before trusting a
# checksum): a valid detached signature from the pinned key is accepted, and a
# tampered file, a wrong key, a wrong fingerprint, or a missing signature are
# all refused. Skips when gpg is not installed (e.g. a bare dev box).
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source setup.sh for its functions; its guarded main() does not run.
# shellcheck source=/dev/null
source "$here/../setup.sh"
set +e

command -v gpg >/dev/null 2>&1 || { echo "SKIP: gpg tidak terpasang"; exit 0; }

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# A throwaway signing keyring (separate from the one gpg_verify_checksum makes).
gen_key() {
    local home="$1" name="$2"
    mkdir -p "$home"
    chmod 700 "$home"
    GNUPGHOME="$home" gpg --batch --quiet --passphrase '' --pinentry-mode loopback \
        --quick-generate-key "$name" ed25519 sign 0 2>/dev/null
}

signer="$work/signer"
gen_key "$signer" "RAYXO Test Signer" || fail "could not generate a test key (gpg too old?)"
pub="$(GNUPGHOME="$signer" gpg --batch --armor --export)"
fpr="$(GNUPGHOME="$signer" gpg --batch --with-colons --fingerprint | awk -F: '$1 == "fpr" { print $10; exit }')"

other="$work/other"
gen_key "$other" "RAYXO Other Key"
pub_other="$(GNUPGHOME="$other" gpg --batch --armor --export)"

checksum="$work/release.tar.gz.sha256"
write_checksum() { printf '%s  release.tar.gz\n' "$(printf data | sha256sum | awk '{print $1}')" > "$checksum"; }
write_checksum
sig="$checksum.asc"
GNUPGHOME="$signer" gpg --batch --yes --pinentry-mode loopback --passphrase '' \
    --armor --detach-sign --output "$sig" "$checksum" || fail "could not sign the checksum"

# 1. Valid signature, correct key, correct fingerprint -> accepted.
gpg_verify_checksum "$pub" "$fpr" "$checksum" "$sig" || fail "valid signature was rejected"

# 2. Valid signature, no fingerprint pin -> accepted.
gpg_verify_checksum "$pub" "" "$checksum" "$sig" || fail "valid signature (no fingerprint) was rejected"

# 3. Tampered checksum file -> refused.
printf 'tampered\n' >> "$checksum"
gpg_verify_checksum "$pub" "$fpr" "$checksum" "$sig" && fail "a tampered file was accepted"
write_checksum

# 4. Wrong fingerprint pin (right key) -> refused.
gpg_verify_checksum "$pub" "0000000000000000000000000000000000000000" "$checksum" "$sig" &&
    fail "a wrong fingerprint was accepted"

# 5. A different key than the one that signed -> refused.
gpg_verify_checksum "$pub_other" "" "$checksum" "$sig" && fail "a wrong signing key was accepted"

# 6. Missing signature file -> refused.
gpg_verify_checksum "$pub" "$fpr" "$checksum" "$work/absent.asc" && fail "a missing signature was accepted"

echo "PASS: release-signature.sh"
