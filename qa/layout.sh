#!/usr/bin/env bash
# The repository root holds exactly one Go file, and the bound struct lives in
# exactly one package.
#
# Both are the outcome of a restructure that moved ~70,000 lines, and neither is
# enforced by anything the compiler does. A new file added to the root builds
# perfectly well; so does a second bound struct. The layout would drift back one
# file at a time with every gate still green.
#
# See docs/architecture.md for WHY the root looks like this — in particular that
# main.go is pinned there by //go:embed, and that the App does NOT have to be in
# package main, which is the belief that kept 142 files in the root.
set -euo pipefail

cd "$(dirname "$0")/.."
status=0

# --- 1. the root holds exactly main.go ------------------------------------
root_go=$(find . -maxdepth 1 -name '*.go' -type f | sed 's|^\./||' | sort)
if [ "$root_go" != "main.go" ]; then
  echo "the repository root should hold exactly main.go, and holds:" >&2
  echo "$root_go" | sed 's/^/  /' >&2
  echo >&2
  echo "Anything that is not the embedded-asset declaration belongs under" >&2
  echo "internal/. See docs/architecture.md." >&2
  status=1
fi

# --- 2. main.go is pinned by go:embed, and nothing else claims to be ------
# An embed path cannot escape its declaring file's directory, which is the only
# reason a Go file remains in the root at all. If that directive ever leaves
# main.go, the root file has no reason to exist and this gate is measuring
# nothing.
if ! grep -q '^//go:embed' main.go; then
  echo "main.go no longer declares //go:embed." >&2
  echo "That directive is the only thing pinning a Go file to the root; if it" >&2
  echo "has moved, this gate and the layout it defends need revisiting." >&2
  status=1
fi

# --- 3. exactly one package declares bound methods ------------------------
# Wails binds every exported method of the struct passed to Bind. A second
# struct with exported methods bound from another package would split the
# frontend's API across two generated directories without any build failing.
bound_pkgs=$(grep -rl '^func (a \*App) [A-Z]' internal --include='*.go' 2>/dev/null \
  | grep -v '_test\.go$' \
  | xargs -n1 dirname 2>/dev/null | sort -u || true)
bound_count=$(printf '%s' "$bound_pkgs" | grep -c . || true)
if [ "$bound_count" != "1" ] || [ "$bound_pkgs" != "internal/core" ]; then
  echo "bound methods should live in internal/core alone, and are declared in:" >&2
  echo "${bound_pkgs:-  (none found — the detection has broken, which is not a pass)}" | sed 's/^/  /' >&2
  status=1
fi

# AN EMPTY RESULT IS AN INSTRUMENT FAILURE, NOT A FINDING. If the pattern stops
# matching, the check above would report "no packages" and could read as
# success; it is treated as a failure above, and the count is asserted here.
methods=$(grep -rc '^func (a \*App) [A-Z]' internal/core/*.go 2>/dev/null | awk -F: '{s+=$2} END{print s+0}')
if [ "$methods" -lt 100 ]; then
  echo "only $methods bound methods found in internal/core." >&2
  echo "That is a failed measurement rather than a finding: the surface is 188." >&2
  status=1
fi

# --- 4. internal package short names are unique ---------------------------
# Wails addresses bound types by SHORT package name (reflect.Type.String()), so
# two packages named alike anywhere in the tree collide silently in the
# generated TypeScript rather than failing to build.
dupes=$(find internal -mindepth 1 -type d -not -path '*/.*' \
  | xargs -n1 basename 2>/dev/null | sort | uniq -d || true)
if [ -n "$dupes" ]; then
  echo "these internal package short names are not unique:" >&2
  echo "$dupes" | sed 's/^/  /' >&2
  echo "Wails resolves bound types by short name; duplicates collide silently." >&2
  status=1
fi

# --- 5. nothing under internal/ still calls the App's package "package main" -
# The App left package main during the restructure and 71 comments across 42
# files did not follow it, including one directly above `package core` telling the
# reader the aliases exist "so package main compiles unchanged". Comments are
# the only record of WHY most of this code is shaped as it is, and a wrong one
# is worse than none — it sends the next reader to a 24-line main.go looking for
# something that is not there.
#
# Three references are legitimate and listed explicitly rather than pattern-
# matched, so a NEW one has to be justified here to pass.
allowed_main_refs="internal/core/run.go
internal/auth/awsv4/awsv4.go
internal/codegen/generators.go"
stale_main=$(grep -rln 'package main' --include='*.go' internal/ 2>/dev/null \
  | grep -vxF "$allowed_main_refs" || true)
if [ -n "$stale_main" ]; then
  echo "these files under internal/ still refer to \"package main\":" >&2
  echo "$stale_main" | sed 's/^/  /' >&2
  echo >&2
  echo "The App lives in internal/core. If a reference is genuinely about the" >&2
  echo "real main package, add it to allowed_main_refs in this script." >&2
  status=1
fi

# --- 6. the figures docs/architecture.md quotes are still true -------------
# The document names a package count and a bound-method count. Both went stale
# inside a single afternoon — it said 41 packages when there were 37 — because
# nothing measured them. A number nobody checks is the same failure mode as a
# lint exclusion anchored to a path that moved: it does not error, it just stops
# being true, and it is believed for exactly as long as that goes unnoticed.
# Counted as GO PACKAGES, not as directories.
#
# This used to be a plain directory count, which is not the same thing and was
# quietly wrong in both directions. internal/auth and internal/store hold only
# subpackages and contain no .go files of their own, so they inflated the total;
# and a directory of vendored assets (internal/scripting/thirdparty/lodash)
# counted as a package while being nothing of the kind.
#
# The distinction matters because of what the number is USED for: the document
# tells the reader to check a new package name against the existing ones,
# because Wails addresses bound types by short package name and two alike
# collide silently in the generated TypeScript. A directory with no Go in it can
# never take part in that collision, so counting it makes the advice wrong.
packages=$(find internal -mindepth 1 -type d -not -path '*/.*' \
             -exec sh -c 'ls "$1"/*.go >/dev/null 2>&1' _ {} \; -print \
           | wc -l | tr -d ' ')
for quoted in $(grep -oE '\b[0-9]+ packages\b|existing [0-9]+\.' docs/architecture.md \
                 | grep -oE '[0-9]+' || true); do
  if [ "$quoted" != "$packages" ]; then
    echo "docs/architecture.md quotes $quoted packages; there are $packages." >&2
    status=1
  fi
done
# EVERY occurrence must agree, not merely one of them. `grep -q` was the first
# attempt and it was blind: the count appears twice, so a stale copy passed by
# hiding behind a correct one. The negative control below is what caught it.
quoted_methods=$(grep -oE '\b[0-9]+ bound methods\b' docs/architecture.md | grep -oE '^[0-9]+' || true)
if [ -z "$quoted_methods" ]; then
  echo "docs/architecture.md no longer quotes a bound-method count at all." >&2
  echo "That is a failed measurement rather than a pass." >&2
  status=1
fi
for quoted in $quoted_methods; do
  if [ "$quoted" != "$methods" ]; then
    echo "docs/architecture.md quotes $quoted bound methods; there are $methods." >&2
    status=1
  fi
done

if [ "$status" -eq 0 ]; then
  echo "layout ok: root holds main.go alone, $methods bound methods in internal/core, $packages packages, docs agree"
fi
exit "$status"
