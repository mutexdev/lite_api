#!/usr/bin/env python3
"""Replays a catalogue of deliberate breaks and requires the tests to catch each.

Coverage says which lines RAN. This says which behaviours are actually
CHECKED, which is a different question and the one that matters. In this repo
they came apart badly: two helpers at 100% line coverage had the single thing
they decide inverted without one test noticing, and a function at 60% had its
whole insertion index replaced by "always append" while the suite stayed green.

Each catalogue entry names a break that MUST fail the tests. A break that no
longer fails is reported as BLIND — either a test was weakened, or the code
moved and the entry is stale. Both need a person.

Four ways a control lies, all refused rather than counted as a pass:

  1. The pattern does not match, so nothing changed and the green run means
     nothing.
  2. The pattern matches MORE THAN ONCE, so the edit may have landed somewhere
     other than intended. Two controls in this repo reported a clean result
     after editing a different function than the one under test.
  3. The edit does not compile, so there are no failures to see and a build
     error reads exactly like a pass.
  4. The edit makes a test HANG. The run then dies on a timeout and exits
     non-zero, and non-zero used to be the whole definition of caught — so a
     stall was reported as a success while nothing asserted anything.

The tree is restored on every exit path, including an interrupt.
"""

import argparse
import pathlib
import shutil
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
CATALOGUE = ROOT / "qa" / "baseline" / "mutations.txt"


class Mutation:
    def __init__(self, desc, path, scope, old, new):
        self.desc = desc
        self.path = path
        self.scope = scope
        self.old = old
        self.new = new


def parse(text):
    """Entries are separated by a line of '==='. Fields precede '--- old'."""
    mutations = []
    for block in text.split("\n===\n"):
        block = block.strip("\n")
        if not block.strip():
            continue
        lines = block.split("\n")
        # A block is a comment ONLY if EVERY non-blank line is one. Skipping a
        # block because its FIRST line was a comment silently dropped the first
        # entry, which sits directly under the file header — a catalogue that
        # loses entries quietly is worse than no catalogue.
        content = [line for line in lines if line.strip()]
        if all(line.lstrip().startswith("#") for line in content):
            continue
        fields, index = {}, 0
        while index < len(lines) and lines[index] != "--- old":
            line = lines[index]
            if line.strip() and not line.lstrip().startswith("#"):
                key, _, value = line.partition(":")
                fields[key.strip()] = value.strip()
            index += 1
        if index >= len(lines):
            raise SystemExit(f"catalogue entry has no '--- old' section:\n{block}")
        index += 1
        old = []
        while index < len(lines) and lines[index] != "--- new":
            old.append(lines[index])
            index += 1
        if index >= len(lines):
            raise SystemExit(f"catalogue entry has no '--- new' section:\n{block}")
        index += 1
        new = lines[index:]
        for required in ("desc", "file", "scope"):
            if required not in fields:
                raise SystemExit(f"catalogue entry is missing '{required}':\n{block}")
        mutations.append(
            Mutation(
                fields["desc"],
                fields["file"],
                fields["scope"],
                "\n".join(old),
                "\n".join(new),
            )
        )
    return mutations


# A mutated test run is bounded, and hitting the bound is its OWN outcome.
#
# Without this a break that makes a test hang is counted as caught: the run
# eventually dies on Go's default 10-minute timeout, exits non-zero, and
# "non-zero" was the entire definition of caught. That is a ten-minute stall
# reported as a success, and nothing asserted anything — the same class of lie
# as NO COMPILE, where a build error also reads exactly like a pass.
TEST_TIMEOUT = "120s"  # passed to `go test`, so it bounds execution not building
SUBPROCESS_TIMEOUT = 600  # a backstop for the go tool itself wedging


def run(command, timeout=None):
    """Runs a command, returning (returncode, combined output).

    A returncode of None means the subprocess timeout was reached. Callers must
    treat that as its own result rather than folding it into "failed", which is
    the distinction this whole script exists to make.
    """
    try:
        done = subprocess.run(
            command, cwd=ROOT, shell=True, capture_output=True, text=True,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired:
        return None, ""
    return done.returncode, (done.stdout or "") + (done.stderr or "")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--only", help="run entries whose description contains this")
    parser.add_argument("--list", action="store_true", help="print the catalogue")
    args = parser.parse_args()

    if not CATALOGUE.exists():
        print(f"mutations: missing catalogue {CATALOGUE}", file=sys.stderr)
        return 1
    mutations = parse(CATALOGUE.read_text())
    # The catalogue's own entry count, checked against what parsed. A separator
    # typo would otherwise merge two entries and quietly drop one.
    declared = CATALOGUE.read_text().count("\n--- old\n")
    if declared != len(mutations):
        print(
            f"mutations: the catalogue declares {declared} entries but {len(mutations)} parsed",
            file=sys.stderr,
        )
        return 1
    if args.only:
        mutations = [m for m in mutations if args.only.lower() in m.desc.lower()]
    if not mutations:
        print("mutations: no entries selected", file=sys.stderr)
        return 1
    if args.list:
        for mutation in mutations:
            print(f"{mutation.path:36} {mutation.desc}")
        return 0

    # A catalogue is only meaningful against a passing tree.
    if run("go build ./...")[0] != 0:
        print("mutations: the tree does not build, so no result means anything", file=sys.stderr)
        return 1

    stash = pathlib.Path(tempfile.mkdtemp())
    caught, problems = 0, []
    try:
        for mutation in mutations:
            target = ROOT / mutation.path
            if not target.exists():
                problems.append(f"MISSING FILE  {mutation.desc} ({mutation.path})")
                continue
            backup = stash / mutation.path.replace("/", "_")
            shutil.copy2(target, backup)
            try:
                source = target.read_text()
                occurrences = source.count(mutation.old)
                if occurrences != 1:
                    label = "NOT FOUND" if occurrences == 0 else f"AMBIGUOUS({occurrences})"
                    problems.append(f"{label:14}{mutation.desc}")
                    continue
                target.write_text(source.replace(mutation.old, mutation.new))
                if run("go build ./...")[0] != 0:
                    problems.append(f"NO COMPILE    {mutation.desc}")
                    continue
                code, output = run(
                    f"go test -count=1 -timeout {TEST_TIMEOUT} {mutation.scope}",
                    timeout=SUBPROCESS_TIMEOUT,
                )
                if code is None or "test timed out" in output:
                    problems.append(f"TIMED OUT     {mutation.desc}")
                elif code == 0:
                    problems.append(f"BLIND         {mutation.desc}")
                else:
                    caught += 1
                    print(f"catches  {mutation.desc}")
            finally:
                shutil.copy2(backup, target)
    finally:
        shutil.rmtree(stash, ignore_errors=True)

    print()
    if problems:
        for problem in problems:
            print(problem, file=sys.stderr)
        print(
            f"\n{len(problems)} of {len(mutations)} controls did not catch their break.\n"
            "A break nothing notices is a behaviour nothing checks.",
            file=sys.stderr,
        )
        return 1
    print(f"all {caught} mutation controls still catch their break")
    return 0


if __name__ == "__main__":
    sys.exit(main())
