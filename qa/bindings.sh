#!/usr/bin/env bash
# Guards the Wails binding surface — the contract between package main and the
# frontend.
#
# THREE CHECKS, because each catches something the others cannot:
#
#  1. The Go signature list matches qa/baseline/bindings.txt.
#     A COUNT alone does not catch a changed parameter or return type, and it
#     does not catch one method being unexported while another is added. Both
#     compile. The first breaks every call site at runtime with an argument
#     mismatch; the second removes a feature silently.
#
#  2. The Go method NAMES match what frontend/wailsjs/go/main/App.d.ts declares.
#     The generated bindings are committed, so a rename in Go that is not
#     followed by `wails generate module` leaves the frontend calling a method
#     that no longer exists — and nothing in either build says so, because the
#     .d.ts still type-checks against itself.
#
#  3. App.d.ts and App.js declare the same names, since the types and the
#     runtime shims are generated separately.
#
# When a change to the surface is INTENDED: run `wails generate module`, then
# regenerate the baseline with `qa/bindings.sh --update`, and review that diff
# as part of the change rather than as noise.
set -euo pipefail
cd "$(dirname "$0")/.."

BASELINE=qa/baseline/bindings.txt
current=$(mktemp); go_names=$(mktemp); ts_names=$(mktemp); js_names=$(mktemp)
trap 'rm -f "$current" "$go_names" "$ts_names" "$js_names"' EXIT

# `|| true` so a no-match does not kill the script under `set -e`: grep exits 1
# when it matches nothing, and pipefail propagates that. Without it the script
# dies here with NO OUTPUT AT ALL, and the check below — the one that explains
# what went wrong — never runs. I found that by controlling the check and
# discovering it was unreachable.
go doc -all . 2>/dev/null | grep -E '^func \(a \*App\) [A-Z]' | sed 's/^func (a \*App) //' | sort > "$current" || true

# AN EMPTY RESULT IS AN INSTRUMENT FAILURE, NOT A FINDING. `go doc` prints
# nothing and exits 0 when it cannot load the package — run from the wrong
# directory, or with the module in a state it will not parse. A Wails app with
# zero bound methods does not exist, so treat it as a broken measurement.
#
# This matters most on the --update path: without it, one bad run overwrites the
# baseline with an empty file, reports "baseline updated: 0 bound methods", and
# the gate is dead from then on with nothing to notice.
if [ ! -s "$current" ]; then
  echo "go doc returned no bound methods. That is a failed measurement, not a" >&2
  echo "surface with nothing in it — check that this is the module root and" >&2
  echo "that the package loads." >&2
  exit 1
fi

if [ "${1:-}" = "--update" ]; then
  cp "$current" "$BASELINE"
  echo "baseline updated: $(wc -l < "$BASELINE" | tr -d ' ') bound methods"
  exit 0
fi

status=0

if ! diff -u "$BASELINE" "$current"; then
  echo "FAIL: the bound method signatures changed. If intended, see the header of this script." >&2
  status=1
fi

awk '{print $1}' "$current" | sed 's/(.*//' | sort > "$go_names"
grep -oE '^export function [A-Za-z0-9_]+' frontend/wailsjs/go/main/App.d.ts | awk '{print $3}' | sort > "$ts_names"
grep -oE '^export function [A-Za-z0-9_]+' frontend/wailsjs/go/main/App.js  | awk '{print $3}' | sort > "$js_names"

if ! diff -u "$go_names" "$ts_names" > /dev/null; then
  echo "FAIL: Go and App.d.ts disagree. Run 'wails generate module'." >&2
  diff -u "$go_names" "$ts_names" >&2 || true
  status=1
fi

if ! diff -u "$ts_names" "$js_names" > /dev/null; then
  echo "FAIL: App.d.ts and App.js disagree. Run 'wails generate module'." >&2
  diff -u "$ts_names" "$js_names" >&2 || true
  status=1
fi

[ $status -eq 0 ] && echo "bindings ok: $(wc -l < "$current" | tr -d ' ') methods, Go/TS/JS in agreement"
exit $status
