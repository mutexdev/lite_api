#!/usr/bin/env bash
# Fails when a package's tests disappear.
#
# `go test ./...` reports "[no test files]" for a package with none and EXITS 0.
# So deleting or moving a package's tests leaves CI green, and the loss shows up
# only as a coverage number nobody is watching.
#
# Worse, `-cover` HIDES it: with that flag the same package prints
# "coverage: 0.0% of statements" instead, so `grep -c "no test files"` returns
# zero. I claimed in several summaries that every package in this module had
# tests, on the strength of exactly that measurement. Four do not.
#
# This compares the real list against qa/baseline/untested-packages.txt in both
# directions: a package that loses its tests fails, and one that gains them and
# is not removed from the list fails too, so the file cannot rot into a
# blanket exemption.
set -uo pipefail
cd "$(dirname "$0")/.."

BASELINE=qa/baseline/untested-packages.txt
current=$(mktemp); expected=$(mktemp)
trap 'rm -f "$current" "$expected"' EXIT

# Deliberately WITHOUT -cover, which would mask the very thing being looked for.
go test -count=1 ./... 2>&1 \
  | grep '\[no test files\]' \
  | awk '{print $2}' \
  | sed 's|github.com/mutexdev/lite_api/||; s|github.com/mutexdev/lite_api|.|' \
  | sort > "$current" || true

grep -vE '^\s*(#|$)' "$BASELINE" | sort > "$expected"

# An empty result is an instrument failure, not "every package has tests": if
# `go test` could not run at all, the grep finds nothing and this would silently
# agree with an empty baseline.
if ! go build ./... >/dev/null 2>&1; then
  echo "the module does not build; test presence cannot be measured." >&2
  exit 1
fi

if diff -u "$expected" "$current"; then
  echo "test presence ok: $(wc -l < "$expected" | tr -d ' ') packages knowingly without their own tests"
  exit 0
fi

echo "" >&2
echo "A package listed above with '+' has no tests and is not accounted for." >&2
echo "One with '-' has gained tests; remove it from $BASELINE." >&2
exit 1
