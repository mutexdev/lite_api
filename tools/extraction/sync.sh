#!/bin/zsh
# US-060 step 3-6: regenerate bindings, rewrite stale main.X refs for the named
# types, fix imports, verify. Takes the moved type names as arguments.
set -e
cd /Users/mostafi/Developer/Workspace/lite_api
SP=/private/tmp/claude-501/-Users-mostafi-Developer-Workspace-lite-api/15c2ef28-f2f6-481d-9fdf-9c6b3a3b0d57/scratchpad
wails generate module >/dev/null 2>&1
cd frontend
touched=()
for t in "$@"; do
  # Refuse a type that was never actually moved. Passing one by mistake rewrites
  # a perfectly good main.X into a types.X that does not exist -- which is how I
  # broke App.svelte once already.
  if ! grep -qE "^type $t (struct|interface|=|\\[)" ../internal/types/*.go; then
    echo "REFUSED: $t is not declared in internal/types"; exit 1
  fi
  for f in $(grep -rl "main\.$t\b" src 2>/dev/null); do
    perl -pi -e "s/\bmain\.$t\b/types.$t/g" "$f"; touched+=("$f")
  done
done
[[ ${#touched[@]} -gt 0 ]] && python3 $SP/fiximports.py ${(u)touched} >/dev/null
python3 $SP/dropmain.py >/dev/null
npx svelte-check --threshold warning 2>&1 | tail -1
