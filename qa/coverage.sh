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
  go test -count=1 -coverpkg="$scope" -coverprofile="$raw" ./... >/dev/null 2>&1
  go run tools/coverage/mergeprofile.go "$raw" > "$merged"
  printf '%-10s %s   (%s blocks)\n' \
    "$label" \
    "$(go tool cover -func="$merged" | tail -1 | grep -oE '[0-9.]+%')" \
    "$(($(wc -l < "$merged") - 1))"
  rm -f "$raw" "$merged"
}

measure ./internal/... internal
measure ./...          all
