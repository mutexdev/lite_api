#!/usr/bin/env bash
# A skipped test reports success while checking nothing, and `go test` prints
# "ok" either way. This gate fails when the set of tests that actually SKIP on
# this machine differs from the baseline.
#
# Written after a test in this repo skipped silently: it asserted a sidebar
# placement that only holds in a workspace containing nothing but the Scratch
# collection, took the DEFAULT workspace, which ships with a sample collection,
# and skipped on every run. It was green and it verified nothing.
#
# Platform guards are the other reason to care. Several tests skip when
# http.DefaultTransport is not an *http.Transport, or when the system
# certificate pool is unavailable. Those conditions are false here today; if a
# toolchain or CI image change makes one true, the affected test stops checking
# anything and nothing else says so.
set -euo pipefail

cd "$(dirname "$0")/.."
baseline="qa/baseline/skipped-tests.txt"

# -count=1 so a cached package still reports its skips.
actual="$(go test ./... -count=1 -v 2>/dev/null \
  | sed -n 's/^--- SKIP: \([^ ]*\).*/\1/p' \
  | sort -u || true)"

# An empty run is not a pass: if the command produced nothing at all, the
# pipeline is broken rather than the repo being skip-free.
if ! go test ./... -count=1 -v >/dev/null 2>&1; then
  echo "skip audit: the test suite does not pass, so its skip set means nothing" >&2
  exit 1
fi

if [ ! -f "$baseline" ]; then
  echo "skip audit: missing baseline $baseline" >&2
  exit 1
fi

expected="$(grep -v '^#' "$baseline" | grep -v '^[[:space:]]*$' | sort -u || true)"

if [ "$actual" != "$expected" ]; then
  echo "skip audit: the set of skipped tests changed." >&2
  echo "--- expected (${baseline}) ---" >&2
  echo "$expected" >&2
  echo "--- actual ---" >&2
  echo "$actual" >&2
  echo >&2
  echo "A test that newly skips is a test that stopped checking anything." >&2
  echo "Either fix it, or add it to the baseline with a reason." >&2
  exit 1
fi

count="$(printf '%s' "$expected" | grep -c . || true)"
echo "skip audit ok: ${count} test(s) knowingly skipped"
