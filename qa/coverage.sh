#!/usr/bin/env bash
# Reports Go statement coverage reproducibly.
#
# Two figures, because they measure different things and the repo has quoted
# them interchangeably before:
#
#   internal   the internal/ packages only — the extracted, unit-tested code
#   all        internal/ plus the root package main, which is bound to Wails
#              and exercised mostly through integration-shaped tests
#
# Both go through tools/coverage/mergeprofile.go. Without it the number is
# meaningless: with -coverpkg every test binary writes a full profile, so one
# source block appears once PER BINARY (10,485 blocks became 283,457 lines
# across 28 binaries here) and `go tool cover -func` counts each occurrence
# separately in the denominator. The reported percentage then FALLS as test
# binaries are added.
#
# -count=1 is required for an independent reason: a cached package result
# contributes no profile data at all.
set -euo pipefail
cd "$(dirname "$0")/.."

measure() {
  local scope="$1" label="$2" raw merged
  raw=$(mktemp) merged=$(mktemp)
  # The test output is discarded but its STATUS is not. A failing suite still
  # writes a profile — a partial one — and reporting a percentage from it is
  # reporting a number for code that did not pass. Same shape as the lint
  # exclusion check measuring with no linter installed.
  if ! go test -count=1 -coverpkg="$scope" -coverprofile="$raw" ./... >/dev/null 2>&1; then
    echo "go test failed for scope $scope; coverage from a failing suite is not a" >&2
    echo "figure worth quoting. Fix the tests first." >&2
    rm -f "$raw" "$merged"
    exit 1
  fi
  if [ ! -s "$raw" ]; then
    echo "no coverage profile was written for scope $scope." >&2
    rm -f "$raw" "$merged"
    exit 1
  fi
  go run tools/coverage/mergeprofile.go "$raw" > "$merged"
  printf '%-10s %s   (%s blocks)\n' \
    "$label" \
    "$(go tool cover -func="$merged" | tail -1 | grep -oE '[0-9.]+%')" \
    "$(($(wc -l < "$merged") - 1))"
  rm -f "$raw" "$merged"
}

# One scope, not two. These used to be `./internal/...` and `./...`, which
# measured genuinely different things while the application lived in package
# main at the repository root. Now that root holds only main.go — 22 lines of
# Wails wiring with nothing a test can reach — the two figures differ by
# rounding, and reporting both invites quoting whichever is higher.
measure ./internal/... internal
