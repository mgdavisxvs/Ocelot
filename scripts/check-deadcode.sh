#!/usr/bin/env bash
#
# Fails when a function is unreachable from main and is not already recorded in
# .deadcode-allow. This is a ratchet: the allowlist captures the code that was
# already unreachable when the check was introduced, so new dead code fails
# immediately while the backlog is worked down.
#
# Why call-graph reachability and not an import check: most of the dead code
# this repo accumulated lived inside package tracker, which main does import.
# Compiling and being imported is not the same as being called.
#
# Usage:
#   scripts/check-deadcode.sh           # verify
#   scripts/check-deadcode.sh --update  # rewrite the allowlist

set -euo pipefail

cd "$(dirname "$0")/.."

ALLOWLIST=".deadcode-allow"
DEADCODE="${DEADCODE:-deadcode}"

if ! command -v "$DEADCODE" >/dev/null 2>&1; then
    if [ -x "$(go env GOPATH)/bin/deadcode" ]; then
        DEADCODE="$(go env GOPATH)/bin/deadcode"
    else
        echo "deadcode not found. Install it with:"
        echo "  go install golang.org/x/tools/cmd/deadcode@latest"
        exit 2
    fi
fi

current="$(mktemp)"
trap 'rm -f "$current"' EXIT

"$DEADCODE" ./... | sed 's/.*unreachable func: //' | sort -u > "$current"

if [ "${1:-}" = "--update" ]; then
    cp "$current" "$ALLOWLIST"
    echo "Updated $ALLOWLIST ($(wc -l < "$ALLOWLIST" | tr -d ' ') entries)."
    exit 0
fi

touch "$ALLOWLIST"

added="$(comm -13 "$ALLOWLIST" "$current" || true)"
removed="$(comm -23 "$ALLOWLIST" "$current" || true)"

if [ -n "$removed" ]; then
    echo "These are now reachable and can be dropped from $ALLOWLIST:"
    echo "$removed" | sed 's/^/  - /'
    echo "  Run: scripts/check-deadcode.sh --update"
    echo
fi

if [ -n "$added" ]; then
    echo "FAIL: unreachable from main and not in $ALLOWLIST:"
    echo "$added" | sed 's/^/  - /'
    echo
    echo "Either wire it into a path reachable from main, delete it, or — if it"
    echo "is deliberately unreachable — add it to $ALLOWLIST with a reason."
    exit 1
fi

echo "OK: no new unreachable code ($(wc -l < "$ALLOWLIST" | tr -d ' ') known entries)."
