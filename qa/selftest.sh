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
# This control NAMED an anchor, and the anchor moved — transport.go was split
# and the RFC-1423 exclusion went to clientcert.go, so the mutation stopped
# applying and the control reported BLIND. That is the very drift the gate
# exists to catch, reproduced one level up in the thing checking it.
#
# So derive the anchor rather than spell it. Renaming whatever the first
# exclusion points at is exactly the real failure mode: the file moved, the
# `path:` regex quietly stopped matching, and the suppression silently became
# dead while the config still looked deliberate.
stash .golangci.yml
python3 - <<'PY'
import re, sys
path = '.golangci.yml'
text = open(path).read()
match = re.search(r'^( *- path: )(\S+)$', text, re.M)
if not match:
    sys.exit("no `- path:` exclusion found in .golangci.yml; this control "
             "cannot mutate anything and will not report a pass")
moved = match.group(2).replace(r'\.go$', r'_moved\.go$')
if moved == match.group(2):
    sys.exit(f"could not rewrite the anchor {match.group(2)!r}; has the "
             "anchoring convention changed?")
open(path, 'w').write(text[:match.start()] + match.group(1) + moved
                      + text[match.end():])
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

# --- qa/skip-audit.sh -----------------------------------------------------
# A test that newly SKIPS reports success while checking nothing, and the
# suite still prints "ok". The control makes one skip and requires the gate to
# notice.
stash internal/core/app_scratch_placement_test.go
python3 - <<'PY'
p = 'internal/core/app_scratch_placement_test.go'
s = open(p).read()
old = 'func TestScratchCollectionInsertIndex(t *testing.T) {'
new = 'func TestScratchCollectionInsertIndex(t *testing.T) {\n\tt.Skip("qa/selftest control")'
assert s.count(old) == 1
open(p, 'w').write(s.replace(old, new))
PY
expect_failure "skip-audit.sh: a test that newly skips" ./qa/skip-audit.sh
restore; RESTORE=(); STASH=$(mktemp -d)

stash qa/baseline/skipped-tests.txt
echo "TestThatDoesNotActuallySkip" >> qa/baseline/skipped-tests.txt
expect_failure "skip-audit.sh: a baseline entry that no longer skips" ./qa/skip-audit.sh
restore; RESTORE=(); STASH=$(mktemp -d)

# --- qa/layout.sh ---------------------------------------------------------
# The root holding one Go file is the outcome of a large restructure and is
# enforced by nothing the compiler does — a stray file there builds fine.
stash main.go
printf 'package main\n\nfunc selftestStray() {}\n' > selftest_stray.go
expect_failure "layout.sh: a stray .go file in the repository root" ./qa/layout.sh
rm -f selftest_stray.go
restore; RESTORE=(); STASH=$(mktemp -d)

# The same gate refuses a comment under internal/ that still calls the App's
# package "package main". 71 of them drifted that way during the restructure —
# one of them directly above `package core` — because nothing checked.
stash internal/types/terminal.go
sed -i '' 's|stays in internal/core\.|stays in package main.|' internal/types/terminal.go
expect_failure "layout.sh: a comment under internal/ reintroducing \"package main\"" ./qa/layout.sh
restore; RESTORE=(); STASH=$(mktemp -d)

# The same gate checks that the figures docs/architecture.md quotes are still
# true. It said 41 packages when there were 37, having drifted inside a single
# afternoon, because nothing measured it.
stash docs/architecture.md
sed -i '' 's/37 packages/41 packages/' docs/architecture.md
expect_failure "layout.sh: a stale package count in the architecture doc" ./qa/layout.sh
restore; RESTORE=(); STASH=$(mktemp -d)

# EVERY occurrence must be checked, not just one. The first version of this
# check used `grep -q`, which passes when ANY occurrence matches — so a stale
# copy sailed through by hiding behind a correct one. The occurrence count is
# read from the document rather than written here, and each is mutated
# separately, because a control that only ever breaks the first one would not
# have caught that either.
occurrences=$(grep -c '188 bound methods' docs/architecture.md)
for i in $(seq 1 "$occurrences"); do
  stash docs/architecture.md
  python3 - "$i" <<'PY'
import sys, pathlib
i = int(sys.argv[1])
path = pathlib.Path("docs/architecture.md")
parts = path.read_text().split("188 bound methods")
path.write_text("188 bound methods".join(parts[:i]) + "190 bound methods"
                + "188 bound methods".join(parts[i:]))
PY
  expect_failure "layout.sh: a stale method count at occurrence $i of $occurrences" ./qa/layout.sh
  restore; RESTORE=(); STASH=$(mktemp -d)
done

# An ABSENT count is a failed measurement, not a pass — the same trap the
# bound-method assertion in layout.sh guards against.
stash docs/architecture.md
sed -i '' 's/bound methods/bound thingies/g' docs/architecture.md
expect_failure "layout.sh: the architecture doc quoting no method count at all" ./qa/layout.sh
restore; RESTORE=(); STASH=$(mktemp -d)

echo ""
if [ "$failed" -ne 0 ]; then
  echo "$failed gate(s) did not fail when they should have. A gate that cannot" >&2
  echo "fail is a green tick that means nothing." >&2
  exit 1
fi
echo "all $passed gate controls still catch their break"
