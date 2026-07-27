#!/usr/bin/env bash
# Checks that every .golangci.yml exclusion still suppresses something.
#
# WHY THIS EXISTS. An exclusion is anchored to an exact file path. When code
# MOVES, the anchor stops matching — and that failure is silent: the exclusion
# does not error, it just stops applying, and the findings it was hiding start
# appearing somewhere nobody is looking. This repo has already been through
# that once: an earlier extraction left five exclusions anchored to ^app\.go$
# after the code moved to internal/, and golangci-lint went to 45 live issues
# before anyone noticed.
#
# The opposite failure is worse and even quieter: an exclusion that survives a
# move but now matches MORE than it was written for suppresses genuine new
# findings, and nothing at all indicates it.
#
# This script catches the first directly. For each exclusion it removes that
# block, re-runs the linter, and reports how many findings appear. A count of
# ZERO means the exclusion is suppressing nothing — either the code it covered
# is gone, or the anchor no longer matches.
#
# It does not catch the second; only reading the `text:` pattern does. Both
# reasons for each exclusion are written in .golangci.yml beside it.
set -uo pipefail
cd "$(dirname "$0")/.."

CONFIG=.golangci.yml
BACKUP=$(mktemp)
cp "$CONFIG" "$BACKUP"
restore() { cp "$BACKUP" "$CONFIG"; rm -f "$BACKUP"; }
trap restore EXIT

# `while read` rather than mapfile: macOS ships bash 3.2, which has neither
# mapfile nor readarray, and this script has to run on a developer's machine as
# well as in CI.
paths=$(grep -oE '^ *- path: \S+' "$CONFIG" | sed 's/^ *- path: //')
if [ -z "$paths" ]; then
  echo "no exclusions found in $CONFIG — has the format changed?" >&2
  exit 1
fi

# A MISSING LINTER MUST NOT READ AS "NOTHING IS SUPPRESSED". Without this
# check, `golangci-lint run | grep -c` returns 0 when the binary is absent, the
# baseline passes, every removal also counts 0, and the script confidently
# reports every exclusion DEAD. I put this step in the wrong CI job first —
# one that never installs golangci-lint — and that is exactly what it would
# have produced.
if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint is not on PATH; this check cannot measure anything." >&2
  exit 1
fi

# THE MEASUREMENT IS A DELTA, so the baseline has to be zero or every number
# below is inflated by whatever is already failing — and a genuinely dead
# exclusion then still reads as live, because the findings appearing are
# somebody else's. I found that by controlling this script: misanchoring one
# exclusion made lint dirty, every other count rose by exactly its findings, and
# the broken anchor still reported "live".
# Counted from golangci-lint's JSON, not from a regex over its human-readable
# output. Two instrument errors in this session came from grepping a tool's
# prose for a string: -cover changed "[no test files]" into a coverage line, and
# a cached package contributed no coverage data at all. A format meant for
# machines does not move under a flag.
#
# `|| true` on the LINTER, not on the parse. golangci-lint exits non-zero when
# it finds issues — which is the entire case being measured here — and pipefail
# propagates that, so without it every successful measurement looked like a
# failure. I first blamed SIGPIPE from `head` and fixed the wrong thing; the
# giveaway was that the count AND the failure marker were both printed.
#
# The parse keeps its own error path, so a genuinely unreadable format is still
# reported rather than counted as zero.
count_issues() {
  local raw
  raw=$(golangci-lint run --timeout 5m --output.json.path stdout 2>/dev/null || true)
  printf '%s' "$raw" | python3 -c 'import sys,json
print(len(json.loads(sys.stdin.readline()).get("Issues") or []))' 2>/dev/null \
    || echo "PARSE_FAILED"
}

baseline=$(count_issues)
if [ "$baseline" = "PARSE_FAILED" ] || [ -z "$baseline" ]; then
  echo "could not read golangci-lint's JSON output; the check cannot measure" >&2
  echo "anything and will not guess. Has the --output flag changed?" >&2
  exit 1
fi
if [ "$baseline" -ne 0 ]; then
  echo "golangci-lint reports $baseline findings before anything is removed." >&2
  echo "This check measures what each exclusion suppresses, which is only" >&2
  echo "meaningful against a clean baseline. Fix the findings first." >&2
  exit 1
fi

status=0
for path in $paths; do
  python3 - "$path" "$BACKUP" "$CONFIG" <<'PY'
import sys
target, source, dest = sys.argv[1], sys.argv[2], sys.argv[3]
out, skipping = [], False
for line in open(source).read().split('\n'):
    if line.strip().startswith('- path:') and line.strip().endswith(target):
        skipping = True
        continue
    if skipping:
        if line.strip().startswith('- path:') or line.strip() == '':
            skipping = False
        else:
            continue
    out.append(line)
open(dest, 'w').write('\n'.join(out))
PY
  count=$(count_issues)
  if [ "$count" = "PARSE_FAILED" ]; then
    echo "golangci-lint's JSON became unreadable while testing $path." >&2
    exit 1
  fi
  if [ "$count" -eq 0 ]; then
    printf 'DEAD    %-42s suppresses nothing\n' "$path" >&2
    status=1
  else
    printf 'live    %-42s %s findings\n' "$path" "$count"
  fi
  cp "$BACKUP" "$CONFIG"
done

if [ $status -ne 0 ]; then
  echo "" >&2
  echo "An exclusion that suppresses nothing is either stale after a code move" >&2
  echo "or covering something already fixed. Re-anchor it or delete it." >&2
fi
exit $status
