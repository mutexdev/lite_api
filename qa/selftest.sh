#!/usr/bin/env bash
# Checks that the other qa gates can still FAIL.
#
# Every gate here was verified once, by hand, by breaking something and watching
# it complain. That verification lived in a terminal and is gone. A gate that
# has quietly lost its ability to fail is worse than no gate: it is a green tick
# that means nothing, and this session found five of those before writing any of
# these scripts.
#
# So each control is now repeatable. Every one mutates the tree, runs the gate,
# asserts a NON-ZERO exit, and restores. The restore is a trap, so an
# interrupted run does not leave the repo broken.
#
# Deliberately not run in CI by default: it is slow, it mutates the working
# tree, and its value is before a release or after touching a gate. Run it then.
#
# WHAT THIS CANNOT DO, stated so nobody assumes otherwise. It cannot verify
# ITSELF. I broke expect_failure so that it always reported success, then also
# broke a gate so it could not fail, and this script cheerfully printed "all 5
# gate controls still catch their break" and exited 0.
#
# That is the regress every checker has: something must be trusted at the
# bottom. The mitigation is that expect_failure is six lines long and its two
# branches are visibly opposite — small enough to read and be sure of, which is
# the only reason it is allowed to be the thing taken on faith.
set -uo pipefail
cd "$(dirname "$0")/.."

STASH=$(mktemp -d)
RESTORE=()

restore() {
  for entry in "${RESTORE[@]:-}"; do
    [ -z "$entry" ] && continue
    src="${entry%%:*}"; dst="${entry#*:}"
    if [ -e "$src" ]; then
      rm -rf "$dst"
      mv "$src" "$dst"
    fi
  done
  rm -rf "$STASH"
}
trap restore EXIT INT TERM

# stash <path> — move a path aside, remembering how to put it back.
stash() {
  local path="$1" key
  key=$(echo "$path" | tr '/' '_')
  cp -R "$path" "$STASH/$key"
  RESTORE+=("$STASH/$key:$path")
}

passed=0
failed=0

# expect_failure <description> <gate command...>
expect_failure() {
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then
    printf 'BLIND   %s\n' "$desc" >&2
    failed=$((failed + 1))
  else
    printf 'catches %s\n' "$desc"
    passed=$((passed + 1))
  fi
}

# --- qa/bindings.sh -------------------------------------------------------
stash qa/baseline/bindings.txt
head -20 qa/baseline/bindings.txt > /tmp/qa_selftest_partial && mv /tmp/qa_selftest_partial qa/baseline/bindings.txt
expect_failure "bindings.sh: a method missing from the baseline" ./qa/bindings.sh
restore; RESTORE=(); STASH=$(mktemp -d)

# --- qa/test-presence.sh --------------------------------------------------
stash internal/urlbuild
rm -f internal/urlbuild/*_test.go
expect_failure "test-presence.sh: a package losing its tests" ./qa/test-presence.sh
restore; RESTORE=(); STASH=$(mktemp -d)

# --- qa/lint-exclusions.sh ------------------------------------------------
stash .golangci.yml
python3 - <<'PY'
p = '.golangci.yml'
s = open(p).read()
old = r'      - path: ^internal/transport/transport\.go$'
new = r'      - path: ^internal/transport/cache\.go$'
assert s.count(old) == 1
open(p, 'w').write(s.replace(old, new))
PY
expect_failure "lint-exclusions.sh: an exclusion anchored at the wrong file" ./qa/lint-exclusions.sh
restore; RESTORE=(); STASH=$(mktemp -d)

# --- frontend verify-inputs ------------------------------------------------
stash frontend/test
rm -rf frontend/test
expect_failure "verify-inputs: the test directory gone" \
  bash -c 'cd frontend && node scripts/verify-inputs.mjs'
restore; RESTORE=(); STASH=$(mktemp -d)

stash frontend/src/lib
find frontend/src/lib -name '*.ts' -delete
expect_failure "verify-inputs: src/lib emptied of modules" \
  bash -c 'cd frontend && node scripts/verify-inputs.mjs'
restore; RESTORE=(); STASH=$(mktemp -d)

echo ""
if [ "$failed" -ne 0 ]; then
  echo "$failed gate(s) did not fail when they should have. A gate that cannot" >&2
  echo "fail is a green tick that means nothing." >&2
  exit 1
fi
echo "all $passed gate controls still catch their break"
