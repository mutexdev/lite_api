#!/bin/zsh
# Negative-control runner.
#
# Two ways a control lies, both seen for real in this session:
#   1. the edit does not match, so nothing changed and "0 failures" means nothing
#   2. the edit does not COMPILE, so there are no "--- FAIL" lines to count and
#      a build failure reads exactly like a pass
# It refuses to report a number unless the edit landed AND the package built.
FILE=$1; BAK=$2; PKG=$3; DESC=$4; EDIT=$5
cp $BAK $FILE
perl -pi -e "$EDIT" $FILE
if diff -q $BAK $FILE >/dev/null; then
  echo "$DESC: INVALID - edit did not land"; cp $BAK $FILE; return 2>/dev/null || exit 0
fi
out=$(go test $PKG 2>&1)
if print -r -- "$out" | grep -q 'build failed\|cannot use\|undefined:\|declared and not used'; then
  echo "$DESC: INVALID - break does not compile"
else
  echo "$DESC: $(print -r -- "$out" | grep -cE '^--- FAIL') failing"
fi
cp $BAK $FILE
